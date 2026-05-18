package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/internal/crypto/aesutil"
	"github.com/gosom/google-maps-scraper/models"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
	"golang.org/x/sync/errgroup"
)

const (
	maxDeliveriesPerUserPerHour = 100
	maxDeliveriesPerIPPerHour   = 50
	rateLimitRetryDelay         = 1 * time.Hour
)

// WebhookDeliveryWorker polls for pending webhook deliveries and sends them.
type WebhookDeliveryWorker struct {
	deliveryRepo models.JobWebhookDeliveryRepository
	configRepo   models.WebhookConfigRepository
	jobRepo      models.JobRepository
	webhookKEK   [32]byte
	logger       *slog.Logger
	pollInterval time.Duration
}

// NewWebhookDeliveryWorker creates a new worker that processes pending webhook deliveries.
func NewWebhookDeliveryWorker(
	deliveryRepo models.JobWebhookDeliveryRepository,
	configRepo models.WebhookConfigRepository,
	jobRepo models.JobRepository,
	webhookKEK [32]byte,
	logger *slog.Logger,
) *WebhookDeliveryWorker {
	return &WebhookDeliveryWorker{
		deliveryRepo: deliveryRepo,
		configRepo:   configRepo,
		jobRepo:      jobRepo,
		webhookKEK:   webhookKEK,
		logger:       logger,
		pollInterval: 5 * time.Second,
	}
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (w *WebhookDeliveryWorker) Run(ctx context.Context) error {
	w.logger.Info("webhook_delivery_worker_started")
	defer w.logger.Info("webhook_delivery_worker_stopped")

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			w.processBatch(ctx)
			timer.Reset(w.pollInterval)
		}
	}
}

// processBatch claims and delivers a batch of pending webhook deliveries.
func (w *WebhookDeliveryWorker) processBatch(ctx context.Context) {
	deliveries, err := w.deliveryRepo.ListPendingGlobal(ctx, 50)
	if err != nil {
		w.logger.Error("webhook_list_pending_failed", slog.Any("error", err))
		return
	}
	if len(deliveries) == 0 {
		return
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for _, d := range deliveries {
		g.Go(func() error {
			w.deliverOne(gCtx, d)
			return nil // errors are logged per-delivery, never propagated
		})
	}

	_ = g.Wait()
}

// deliverOne handles the full lifecycle of a single webhook delivery attempt.
func (w *WebhookDeliveryWorker) deliverOne(ctx context.Context, delivery *models.JobWebhookDelivery) {
	log := w.logger.With(
		slog.String("job_id", delivery.JobID),
		slog.String("config_id", delivery.WebhookConfigID),
		slog.Int("attempt", delivery.Attempts),
	)

	// 1. Fetch webhook config. Trusted internal worker — ownership is verified
	// downstream by the explicit config.UserID != job.UserID cross-check below.
	config, err := w.configRepo.GetByID(ctx, delivery.WebhookConfigID, "")
	if err != nil {
		log.Error("webhook_config_fetch_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}
	// Enrich the child logger with user_id now that we have the config.
	log = log.With(slog.String("user_id", config.UserID))
	if !config.IsActive() {
		log.Warn("webhook_config_revoked")
		w.markFailed(ctx, delivery, log)
		return
	}

	// 2. Fetch job scoped to the webhook config owner.
	job, err := w.jobRepo.Get(ctx, delivery.JobID, config.UserID)
	if err != nil {
		log.Error("webhook_job_fetch_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}

	// 3. Cross-user check: webhook config must belong to the job owner.
	if config.UserID != job.UserID {
		log.Error("webhook_cross_user_mismatch",
			slog.String("config_user_id", config.UserID),
			slog.String("job_user_id", job.UserID),
		)
		w.markFailed(ctx, delivery, log)
		return
	}

	// Rate limit: per-user
	since := time.Now().UTC().Add(-1 * time.Hour)
	userCount, err := w.deliveryRepo.CountRecentByUserID(ctx, config.UserID, since)
	if err != nil {
		log.Error("webhook_rate_limit_check_failed", slog.Any("error", err))
		// On error, proceed with delivery (fail open for rate limit checks)
	} else if userCount >= maxDeliveriesPerUserPerHour {
		log.Warn("webhook_rate_limit_user_exceeded",
			slog.String("user_id", config.UserID),
			slog.Int("count", userCount),
		)
		retryAt := time.Now().UTC().Add(rateLimitRetryDelay)
		if err := w.deliveryRepo.SetNextRetry(ctx, delivery.JobID, delivery.WebhookConfigID, retryAt); err != nil {
			log.Error("webhook_rate_limit_retry_failed", slog.Any("error", err))
		}
		return
	}

	// Rate limit: per-destination-IP
	if config.ResolvedIP != nil {
		ipCount, err := w.deliveryRepo.CountRecentByIP(ctx, config.ResolvedIP.String(), since)
		if err != nil {
			log.Error("webhook_rate_limit_ip_check_failed", slog.Any("error", err))
		} else if ipCount >= maxDeliveriesPerIPPerHour {
			log.Warn("webhook_rate_limit_ip_exceeded",
				slog.String("resolved_ip", config.ResolvedIP.String()),
				slog.Int("count", ipCount),
			)
			retryAt := time.Now().UTC().Add(rateLimitRetryDelay)
			if err := w.deliveryRepo.SetNextRetry(ctx, delivery.JobID, delivery.WebhookConfigID, retryAt); err != nil {
				log.Error("webhook_rate_limit_retry_failed", slog.Any("error", err))
			}
			return
		}
	}

	// 4. Decrypt signing secret (stored as hex-encoded AES-GCM ciphertext).
	encryptedBytes, err := hex.DecodeString(config.EncryptedSecret)
	if err != nil {
		log.Error("webhook_secret_hex_decode_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}
	signingSecret, err := aesutil.Decrypt(w.webhookKEK, encryptedBytes)
	if err != nil {
		log.Error("webhook_secret_decrypt_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}

	// 5. Build webhook event payload.
	event := models.WebhookEvent{
		EventType:   eventTypeFromStatus(job.Status),
		JobID:       job.ID,
		JobName:     job.Name,
		Status:      job.Status,
		ResultCount: job.ResultCount,
		CreatedAt:   job.Date,
		EndedAt: func() time.Time {
			if job.UpdatedAt != nil {
				return *job.UpdatedAt
			}
			return time.Now().UTC()
		}(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		log.Error("webhook_event_marshal_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}

	// 6. Generate delivery ID and timestamp.
	deliveryUUID, err := uuid.NewV7()
	if err != nil {
		log.Error("webhook_delivery_uuid_failed", slog.Any("error", err))
		w.handleRetry(ctx, delivery, 0, log)
		return
	}
	deliveryID := deliveryUUID.String()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// 7. Compute HMAC-SHA256 signature (signs timestamp.body for replay protection).
	mac := hmac.New(sha256.New, signingSecret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	signature := fmt.Sprintf("t=%s,sha256=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))

	// 8. Ensure resolved IP is available (DNS rebinding prevention).
	if config.ResolvedIP == nil {
		log.Error("webhook_resolved_ip_missing")
		w.markFailed(ctx, delivery, log)
		return
	}

	// 9. Extract hostname from URL for TLS SNI.
	parsedURL, err := url.Parse(config.URL)
	if err != nil {
		log.Error("webhook_url_parse_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}

	// 10. Build HTTP request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		log.Error("webhook_request_build_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BrezelScraper-Webhook/1.0")
	req.Header.Set("X-Webhook-Signature", signature)
	req.Header.Set("X-Webhook-ID", deliveryID)

	// 11. Create IP-pinned HTTP client.
	client := webutils.NewIPPinnedClient(config.ResolvedIP.String(), parsedURL.Hostname())

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(sendCtx)

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		log.Error("webhook_delivery_request_failed",
			slog.Any("error", err),
			slog.Duration("latency", latency),
		)
		w.handleRetry(ctx, delivery, 0, log)
		return
	}

	// 12. Read and discard response body (max 1KB) to allow connection reuse.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	resp.Body.Close()

	// 13. Evaluate response.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if markErr := w.deliveryRepo.MarkDelivered(ctx, delivery.JobID, delivery.WebhookConfigID); markErr != nil {
			log.Error("webhook_mark_delivered_failed", slog.Any("error", markErr))
		}
		log.Info("webhook_delivered",
			slog.Int("status_code", resp.StatusCode),
			slog.Duration("latency", latency),
		)
	} else {
		log.Warn("webhook_delivery_non_2xx",
			slog.Int("status_code", resp.StatusCode),
			slog.Duration("latency", latency),
		)
		w.handleRetry(ctx, delivery, resp.StatusCode, log)
	}
}

// handleRetry either marks the delivery as failed (if retries exhausted) or
// schedules the next retry with exponential backoff.
func (w *WebhookDeliveryWorker) handleRetry(ctx context.Context, delivery *models.JobWebhookDelivery, statusCode int, log *slog.Logger) {
	if delivery.Attempts >= delivery.MaxAttempts {
		log.Warn("webhook_delivery_retries_exhausted",
			slog.Int("status_code", statusCode),
		)
		w.markFailed(ctx, delivery, log)
		return
	}

	// Exponential backoff: 2^attempt seconds with jitter [0.5, 1.5).
	// math/rand is fine here — jitter is not security-sensitive.
	backoff := time.Duration(math.Pow(2, float64(delivery.Attempts))) * time.Second
	jitter := 0.5 + rand.Float64()
	backoff = time.Duration(float64(backoff) * jitter)

	// Cap at 1 hour.
	const maxBackoff = time.Hour
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	nextRetryAt := time.Now().UTC().Add(backoff)

	if err := w.deliveryRepo.SetNextRetry(ctx, delivery.JobID, delivery.WebhookConfigID, nextRetryAt); err != nil {
		log.Error("webhook_set_next_retry_failed", slog.Any("error", err))
		return
	}

	log.Info("webhook_delivery_retry_scheduled",
		slog.Int("status_code", statusCode),
		slog.Time("next_retry_at", nextRetryAt),
	)
}

// markFailed marks a delivery as permanently failed and logs any error.
func (w *WebhookDeliveryWorker) markFailed(ctx context.Context, delivery *models.JobWebhookDelivery, log *slog.Logger) {
	if err := w.deliveryRepo.MarkFailed(ctx, delivery.JobID, delivery.WebhookConfigID); err != nil {
		log.Error("webhook_mark_failed_error", slog.Any("error", err))
	}
}

// eventTypeFromStatus maps a job status to a webhook event type string.
func eventTypeFromStatus(status string) string {
	switch status {
	case models.StatusCompleted:
		return models.EventTypeJobCompleted
	case models.StatusFailed:
		return models.EventTypeJobFailed
	case models.StatusCancelled:
		return models.EventTypeJobCancelled
	default:
		return "job." + status
	}
}
