package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/internal/crypto/aesutil"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock: JobWebhookDeliveryRepository
// ---------------------------------------------------------------------------

type mockDeliveryRepo struct {
	createFn              func(ctx context.Context, delivery *models.JobWebhookDelivery) error
	createBatchFn         func(ctx context.Context, deliveries []*models.JobWebhookDelivery) error
	listByJobIDFn         func(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error)
	listPendingByJobIDFn  func(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error)
	listPendingGlobalFn   func(ctx context.Context, limit int) ([]*models.JobWebhookDelivery, error)
	markDeliveredFn       func(ctx context.Context, jobID, configID string) error
	markFailedFn          func(ctx context.Context, jobID, configID string) error
	setNextRetryFn        func(ctx context.Context, jobID, configID string, at time.Time) error
	countRecentByUserIDFn func(ctx context.Context, userID string, since time.Time) (int, error)
	countRecentByIPFn     func(ctx context.Context, ip string, since time.Time) (int, error)
}

func (m *mockDeliveryRepo) Create(ctx context.Context, delivery *models.JobWebhookDelivery) error {
	if m.createFn != nil {
		return m.createFn(ctx, delivery)
	}
	return nil
}

func (m *mockDeliveryRepo) CreateBatch(ctx context.Context, deliveries []*models.JobWebhookDelivery) error {
	if m.createBatchFn != nil {
		return m.createBatchFn(ctx, deliveries)
	}
	return nil
}

func (m *mockDeliveryRepo) ListByJobID(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error) {
	if m.listByJobIDFn != nil {
		return m.listByJobIDFn(ctx, jobID)
	}
	return nil, nil
}

func (m *mockDeliveryRepo) ListPendingByJobID(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error) {
	if m.listPendingByJobIDFn != nil {
		return m.listPendingByJobIDFn(ctx, jobID)
	}
	return nil, nil
}

func (m *mockDeliveryRepo) ListPendingGlobal(ctx context.Context, limit int) ([]*models.JobWebhookDelivery, error) {
	if m.listPendingGlobalFn != nil {
		return m.listPendingGlobalFn(ctx, limit)
	}
	return nil, nil
}

func (m *mockDeliveryRepo) MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error {
	if m.markDeliveredFn != nil {
		return m.markDeliveredFn(ctx, jobID, webhookConfigID)
	}
	return nil
}

func (m *mockDeliveryRepo) MarkFailed(ctx context.Context, jobID, webhookConfigID string) error {
	if m.markFailedFn != nil {
		return m.markFailedFn(ctx, jobID, webhookConfigID)
	}
	return nil
}

func (m *mockDeliveryRepo) SetNextRetry(ctx context.Context, jobID, webhookConfigID string, nextRetryAt time.Time) error {
	if m.setNextRetryFn != nil {
		return m.setNextRetryFn(ctx, jobID, webhookConfigID, nextRetryAt)
	}
	return nil
}

func (m *mockDeliveryRepo) CountRecentByUserID(ctx context.Context, userID string, since time.Time) (int, error) {
	if m.countRecentByUserIDFn != nil {
		return m.countRecentByUserIDFn(ctx, userID, since)
	}
	return 0, nil
}

func (m *mockDeliveryRepo) CountRecentByIP(ctx context.Context, resolvedIP string, since time.Time) (int, error) {
	if m.countRecentByIPFn != nil {
		return m.countRecentByIPFn(ctx, resolvedIP, since)
	}
	return 0, nil
}

// ---------------------------------------------------------------------------
// Mock: WebhookConfigRepository
// ---------------------------------------------------------------------------

type mockConfigRepo struct {
	getByIDFn                    func(ctx context.Context, id string, ownerUserID string) (*models.WebhookConfig, error)
	createFn                     func(ctx context.Context, cfg *models.WebhookConfig) error
	listByUserIDFn               func(ctx context.Context, userID string) ([]*models.WebhookConfig, error)
	listActiveByUserIDFn         func(ctx context.Context, userID string) ([]*models.WebhookConfig, error)
	updateFn                     func(ctx context.Context, cfg *models.WebhookConfig) error
	revokeFn                     func(ctx context.Context, id string, ownerUserID string) error
	listActiveWithSecretByUserID func(ctx context.Context, userID string) ([]*models.WebhookConfig, error)
}

func (m *mockConfigRepo) GetByID(ctx context.Context, id string, ownerUserID string) (*models.WebhookConfig, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id, ownerUserID)
	}
	return nil, models.ErrWebhookConfigNotFound
}

func (m *mockConfigRepo) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	if m.createFn != nil {
		return m.createFn(ctx, cfg)
	}
	return nil
}

func (m *mockConfigRepo) ListByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	if m.listByUserIDFn != nil {
		return m.listByUserIDFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockConfigRepo) ListActiveByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	if m.listActiveByUserIDFn != nil {
		return m.listActiveByUserIDFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockConfigRepo) Update(ctx context.Context, cfg *models.WebhookConfig) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, cfg)
	}
	return nil
}

func (m *mockConfigRepo) Revoke(ctx context.Context, id string, ownerUserID string) error {
	if m.revokeFn != nil {
		return m.revokeFn(ctx, id, ownerUserID)
	}
	return nil
}

func (m *mockConfigRepo) ListActiveWithSecretByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	if m.listActiveWithSecretByUserID != nil {
		return m.listActiveWithSecretByUserID(ctx, userID)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mock: JobRepository
// ---------------------------------------------------------------------------

type mockJobRepo struct {
	getFn             func(ctx context.Context, id string, userID string) (models.Job, error)
	createFn          func(ctx context.Context, job *models.Job) error
	deleteFn          func(ctx context.Context, id string, userID string) error
	selectFn          func(ctx context.Context, params models.SelectParams) ([]models.Job, error)
	selectPaginatedFn func(ctx context.Context, params models.PaginatedJobsParams) ([]models.Job, int, error)
	updateFn          func(ctx context.Context, job *models.Job) error
	cancelFn          func(ctx context.Context, id string, userID string) error
}

func (m *mockJobRepo) Get(ctx context.Context, id string, userID string) (models.Job, error) {
	if m.getFn != nil {
		return m.getFn(ctx, id, userID)
	}
	return models.Job{}, nil
}

func (m *mockJobRepo) Create(ctx context.Context, job *models.Job) error {
	if m.createFn != nil {
		return m.createFn(ctx, job)
	}
	return nil
}

func (m *mockJobRepo) Delete(ctx context.Context, id string, userID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, id, userID)
	}
	return nil
}

func (m *mockJobRepo) Select(ctx context.Context, params models.SelectParams) ([]models.Job, error) {
	if m.selectFn != nil {
		return m.selectFn(ctx, params)
	}
	return nil, nil
}

func (m *mockJobRepo) SelectPaginated(ctx context.Context, params models.PaginatedJobsParams) ([]models.Job, int, error) {
	if m.selectPaginatedFn != nil {
		return m.selectPaginatedFn(ctx, params)
	}
	return nil, 0, nil
}

func (m *mockJobRepo) Update(ctx context.Context, job *models.Job) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, job)
	}
	return nil
}

func (m *mockJobRepo) Cancel(ctx context.Context, id string, userID string) error {
	if m.cancelFn != nil {
		return m.cancelFn(ctx, id, userID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testKEK returns a deterministic AES-256 key derived from a test secret.
func testKEK() [32]byte {
	return aesutil.DeriveKey([]byte("test-server-secret"), "webhook-signing-key-encryption")
}

// testSigningSecret is the plaintext signing secret used in tests (64 hex chars).
const testSigningSecret = "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

// encryptSecret encrypts the test signing secret with the test KEK and returns
// the hex-encoded ciphertext suitable for WebhookConfig.EncryptedSecret.
func encryptSecret(t *testing.T) string {
	t.Helper()
	encrypted, err := aesutil.Encrypt(testKEK(), []byte(testSigningSecret))
	require.NoError(t, err)
	return hex.EncodeToString(encrypted)
}

// newTestWorker creates a WebhookDeliveryWorker wired to the given mock repos.
func newTestWorker(dr *mockDeliveryRepo, cr *mockConfigRepo, jr *mockJobRepo) *WebhookDeliveryWorker {
	return NewWebhookDeliveryWorker(
		dr, cr, jr,
		testKEK(),
		slog.Default(),
	)
}

// resolvedIP127 returns a *net.IP pointing to 127.0.0.1.
func resolvedIP127() *net.IP {
	ip := net.ParseIP("127.0.0.1")
	return &ip
}

const (
	testUserID   = "user_test_123"
	testJobID    = "job_test_456"
	testConfigID = "cfg_test_789"
)

// baseDelivery returns a delivery with sensible defaults for tests.
func baseDelivery() *models.JobWebhookDelivery {
	return &models.JobWebhookDelivery{
		JobID:           testJobID,
		WebhookConfigID: testConfigID,
		Attempts:        1,
		MaxAttempts:     5,
		Status:          models.DeliveryStatusDelivering,
	}
}

// baseConfig builds a WebhookConfig pointing at the given URL.
func baseConfig(t *testing.T, url string) *models.WebhookConfig {
	t.Helper()
	return &models.WebhookConfig{
		ID:              testConfigID,
		UserID:          testUserID,
		URL:             url,
		EncryptedSecret: encryptSecret(t),
		ResolvedIP:      resolvedIP127(),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

// baseJob returns a completed Job.
func baseJob() models.Job {
	now := time.Now().UTC()
	return models.Job{
		ID:          testJobID,
		UserID:      testUserID,
		Name:        "test scrape",
		Status:      models.StatusCompleted,
		ResultCount: 42,
		Date:        now.Add(-10 * time.Minute),
		UpdatedAt:   &now,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDeliverOne_Success(t *testing.T) {
	var (
		receivedReq     *http.Request
		receivedBody    []byte
		markDelivered   bool
		markFailedCalls int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedReq = r
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		markDeliveredFn: func(_ context.Context, jobID, configID string) error {
			markDelivered = true
			assert.Equal(t, testJobID, jobID)
			assert.Equal(t, testConfigID, configID)
			return nil
		},
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalls++
			return nil
		},
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}

	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, id string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}

	jr := &mockJobRepo{
		getFn: func(_ context.Context, id string, _ string) (models.Job, error) {
			return baseJob(), nil
		},
	}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	// Assert MarkDelivered called, MarkFailed not called.
	assert.True(t, markDelivered, "MarkDelivered should have been called")
	assert.Equal(t, 0, markFailedCalls, "MarkFailed should not have been called")

	// Assert request headers.
	require.NotNil(t, receivedReq, "test server should have received a request")
	assert.Equal(t, "application/json", receivedReq.Header.Get("Content-Type"))
	assert.Equal(t, "BrezelScraper-Webhook/1.0", receivedReq.Header.Get("User-Agent"))
	assert.NotEmpty(t, receivedReq.Header.Get("X-Webhook-Signature"))
	assert.NotEmpty(t, receivedReq.Header.Get("X-Webhook-ID"))
	// Timestamp is now embedded in the signature header (t=<ts>,sha256=<hex>).
	assert.Contains(t, receivedReq.Header.Get("X-Webhook-Signature"), "t=")

	// Assert the body is a valid WebhookEvent.
	var event models.WebhookEvent
	require.NoError(t, json.Unmarshal(receivedBody, &event))
	assert.Equal(t, models.EventTypeJobCompleted, event.EventType)
	assert.Equal(t, testJobID, event.JobID)
	assert.Equal(t, 42, event.ResultCount)
}

func TestDeliverOne_SignatureCorrect(t *testing.T) {
	var (
		capturedBody []byte
		capturedSig  string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Webhook-Signature")
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	require.NotEmpty(t, capturedSig, "signature header must be present")
	require.NotEmpty(t, capturedBody, "body must be present")

	// Parse t=<timestamp>,sha256=<hex> format.
	require.True(t, strings.HasPrefix(capturedSig, "t="), "signature must start with t=")
	parts := strings.SplitN(capturedSig, ",", 2)
	require.Len(t, parts, 2, "signature must have t= and sha256= parts")
	timestamp := strings.TrimPrefix(parts[0], "t=")
	require.NotEmpty(t, timestamp, "timestamp must not be empty")
	hexSig := strings.TrimPrefix(parts[1], "sha256=")

	// Independently compute HMAC-SHA256 over timestamp.body.
	mac := hmac.New(sha256.New, []byte(testSigningSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(capturedBody)
	expectedHex := hex.EncodeToString(mac.Sum(nil))

	expectedSig := fmt.Sprintf("t=%s,sha256=%s", timestamp, expectedHex)
	assert.Equal(t, expectedSig, capturedSig, "HMAC signature must match independently computed value")
	assert.Equal(t, expectedHex, hexSig, "hex portion must match")
}

func TestDeliverOne_Non2xx_Retries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var (
		setNextRetryCalled bool
		retryTime          time.Time
		markFailedCalled   bool
	)

	dr := &mockDeliveryRepo{
		setNextRetryFn: func(_ context.Context, _, _ string, at time.Time) error {
			setNextRetryCalled = true
			retryTime = at
			return nil
		},
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalled = true
			return nil
		},
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	delivery := baseDelivery()
	delivery.Attempts = 1
	delivery.MaxAttempts = 5

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), delivery)

	assert.True(t, setNextRetryCalled, "SetNextRetry should have been called for non-2xx with attempts remaining")
	assert.True(t, retryTime.After(time.Now().UTC()), "retry time should be in the future")
	assert.False(t, markFailedCalled, "MarkFailed should not be called when attempts remain")
}

func TestDeliverOne_MaxAttemptsExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var (
		setNextRetryCalled bool
		markFailedCalled   bool
	)

	dr := &mockDeliveryRepo{
		setNextRetryFn: func(_ context.Context, _, _ string, _ time.Time) error {
			setNextRetryCalled = true
			return nil
		},
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalled = true
			return nil
		},
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	delivery := baseDelivery()
	delivery.Attempts = 5
	delivery.MaxAttempts = 5

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), delivery)

	assert.True(t, markFailedCalled, "MarkFailed should be called when max attempts exhausted")
	assert.False(t, setNextRetryCalled, "SetNextRetry should not be called when max attempts exhausted")
}

func TestDeliverOne_RevokedConfig(t *testing.T) {
	var (
		requestReceived  bool
		markFailedCalled bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalled = true
			return nil
		},
	}

	revokedAt := time.Now().UTC().Add(-1 * time.Hour)
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			cfg := baseConfig(t, srv.URL)
			cfg.RevokedAt = &revokedAt
			return cfg, nil
		},
	}
	jr := &mockJobRepo{}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	assert.True(t, markFailedCalled, "MarkFailed should be called for revoked config")
	assert.False(t, requestReceived, "no HTTP request should be sent for revoked config")
}

func TestDeliverOne_ConfigNotFound(t *testing.T) {
	var markFailedCalled bool

	dr := &mockDeliveryRepo{
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalled = true
			return nil
		},
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return nil, models.ErrWebhookConfigNotFound
		},
	}
	jr := &mockJobRepo{}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	assert.True(t, markFailedCalled, "MarkFailed should be called when config is not found")
}

func TestDeliverOne_CrossUserMismatch(t *testing.T) {
	var (
		requestReceived  bool
		markFailedCalled bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		markFailedFn: func(_ context.Context, _, _ string) error {
			markFailedCalled = true
			return nil
		},
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			cfg := baseConfig(t, srv.URL)
			cfg.UserID = "user_attacker_999" // Different user than the job owner
			return cfg, nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) {
			return baseJob(), nil // job.UserID = testUserID
		},
	}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	assert.True(t, markFailedCalled, "MarkFailed should be called for cross-user mismatch")
	assert.False(t, requestReceived, "no HTTP request should be sent for cross-user mismatch")
}

func TestDeliverOne_RateLimitUserExceeded(t *testing.T) {
	var (
		requestReceived    bool
		setNextRetryCalled bool
		retryTime          time.Time
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) {
			return 100, nil // At the limit
		},
		countRecentByIPFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		setNextRetryFn: func(_ context.Context, _, _ string, at time.Time) error {
			setNextRetryCalled = true
			retryTime = at
			return nil
		},
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	assert.True(t, setNextRetryCalled, "SetNextRetry should be called when user rate limit exceeded")
	assert.False(t, requestReceived, "no HTTP request should be sent when rate limited")

	// The retry delay should be approximately 1 hour from now.
	expectedMin := time.Now().UTC().Add(55 * time.Minute)
	expectedMax := time.Now().UTC().Add(65 * time.Minute)
	assert.True(t, retryTime.After(expectedMin) && retryTime.Before(expectedMax),
		"retry time should be ~1 hour from now, got %v", retryTime)
}

func TestDeliverOne_RateLimitIPExceeded(t *testing.T) {
	var (
		requestReceived    bool
		setNextRetryCalled bool
		retryTime          time.Time
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dr := &mockDeliveryRepo{
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) {
			return 0, nil // User rate limit OK
		},
		countRecentByIPFn: func(_ context.Context, _ string, _ time.Time) (int, error) {
			return 50, nil // At the IP limit
		},
		setNextRetryFn: func(_ context.Context, _, _ string, at time.Time) error {
			setNextRetryCalled = true
			retryTime = at
			return nil
		},
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	w := newTestWorker(dr, cr, jr)
	w.deliverOne(context.Background(), baseDelivery())

	assert.True(t, setNextRetryCalled, "SetNextRetry should be called when IP rate limit exceeded")
	assert.False(t, requestReceived, "no HTTP request should be sent when IP rate limited")

	expectedMin := time.Now().UTC().Add(55 * time.Minute)
	expectedMax := time.Now().UTC().Add(65 * time.Minute)
	assert.True(t, retryTime.After(expectedMin) && retryTime.Before(expectedMax),
		"retry time should be ~1 hour from now, got %v", retryTime)
}

func TestProcessBatch_ConcurrencyLimit(t *testing.T) {
	const numDeliveries = 20

	var (
		currentConcurrent atomic.Int32
		maxConcurrent     atomic.Int32
		wg                sync.WaitGroup
	)

	// Create a slow HTTP server so goroutines overlap.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cur := currentConcurrent.Add(1)
		// Atomically update max if current is higher.
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		currentConcurrent.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	deliveries := make([]*models.JobWebhookDelivery, numDeliveries)
	for i := range deliveries {
		deliveries[i] = &models.JobWebhookDelivery{
			JobID:           testJobID,
			WebhookConfigID: testConfigID,
			Attempts:        1,
			MaxAttempts:     5,
			Status:          models.DeliveryStatusDelivering,
		}
	}

	dr := &mockDeliveryRepo{
		listPendingGlobalFn: func(_ context.Context, limit int) ([]*models.JobWebhookDelivery, error) {
			return deliveries, nil
		},
		countRecentByUserIDFn: func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
		countRecentByIPFn:     func(_ context.Context, _ string, _ time.Time) (int, error) { return 0, nil },
	}
	cr := &mockConfigRepo{
		getByIDFn: func(_ context.Context, _ string, _ string) (*models.WebhookConfig, error) {
			return baseConfig(t, srv.URL), nil
		},
	}
	jr := &mockJobRepo{
		getFn: func(_ context.Context, _ string, _ string) (models.Job, error) { return baseJob(), nil },
	}

	w := newTestWorker(dr, cr, jr)

	wg.Add(1)
	go func() {
		defer wg.Done()
		w.processBatch(context.Background())
	}()
	wg.Wait()

	observed := maxConcurrent.Load()
	assert.LessOrEqual(t, observed, int32(10),
		"max concurrent goroutines should not exceed 10, observed %d", observed)
	assert.Greater(t, observed, int32(1),
		"should have had some concurrency (observed %d); ensure deliveries overlap", observed)
}

func TestRun_ContextCancellation(t *testing.T) {
	dr := &mockDeliveryRepo{
		listPendingGlobalFn: func(_ context.Context, _ int) ([]*models.JobWebhookDelivery, error) {
			return nil, nil // no deliveries
		},
	}
	cr := &mockConfigRepo{}
	jr := &mockJobRepo{}

	w := newTestWorker(dr, cr, jr)
	// Use a very short poll interval so we don't wait long.
	w.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	// Let it run at least one poll cycle.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "Run should return context.Canceled")
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1 second after context cancellation")
	}
}
