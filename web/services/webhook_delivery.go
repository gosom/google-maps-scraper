package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/crypto/aesutil"
	"golang.org/x/sync/errgroup"
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

	timer := time.NewTimer(w.pollInterval)
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

	// 1. Fetch webhook config.
	config, err := w.configRepo.GetByID(ctx, delivery.WebhookConfigID)
	if err != nil {
		log.Error("webhook_config_fetch_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}
	if !config.IsActive() {
		log.Warn("webhook_config_revoked")
		w.markFailed(ctx, delivery, log)
		return
	}

	// 2. Fetch job (empty userID = admin/internal fetch).
	job, err := w.jobRepo.Get(ctx, delivery.JobID, "")
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
		CompletedAt: time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		log.Error("webhook_event_marshal_failed", slog.Any("error", err))
		w.markFailed(ctx, delivery, log)
		return
	}

	// 6. Compute HMAC-SHA256 signature.
	mac := hmac.New(sha256.New, signingSecret)
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// 7. Generate delivery ID and timestamp.
	deliveryID := uuid.Must(uuid.NewV7()).String()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

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
	req.Header.Set("X-Webhook-Timestamp", timestamp)

	// 11. Create IP-pinned HTTP client (inline to avoid import cycle with handlers).
	client := newIPPinnedClient(config.ResolvedIP.String(), parsedURL.Hostname())

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

	// Exponential backoff: 5^attempt seconds with jitter [0.5, 1.5).
	backoff := time.Duration(math.Pow(5, float64(delivery.Attempts))) * time.Second
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

// newIPPinnedClient returns an *http.Client that forces all connections to the
// given resolvedIP while preserving the original Host header for TLS/SNI.
// This prevents DNS rebinding attacks by ensuring the HTTP client connects only
// to the IP that was validated at registration time.
// Redirects are blocked to prevent SSRF via 3xx to internal IPs.
//
// This mirrors handlers.NewWebhookHTTPClient but lives here to avoid an import
// cycle between web/services and web/handlers.
func newIPPinnedClient(resolvedIP string, originalHost string) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				port = "443"
			}
			pinnedAddr := net.JoinHostPort(resolvedIP, port)
			return dialer.DialContext(ctx, network, pinnedAddr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			ServerName: originalHost,
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
