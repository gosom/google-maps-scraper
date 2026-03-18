package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// ---- test UUIDs ----

const (
	testWebhookID1    = "11111111-1111-1111-1111-111111111111"
	testWebhookID2    = "22222222-2222-2222-2222-222222222222"
	testWebhookID3    = "33333333-3333-3333-3333-333333333333"
	testNonExistentID = "99999999-9999-9999-9999-999999999999"
)

// ---- mock repositories ----

type mockWebhookConfigRepo struct {
	configs       []*models.WebhookConfig
	createErr     error
	getByIDFunc   func(ctx context.Context, id string) (*models.WebhookConfig, error)
	listByUserErr error
	listActiveErr error
	updateErr     error
	revokeErr     error
	created       *models.WebhookConfig // captures last Create call
}

func (m *mockWebhookConfigRepo) Create(_ context.Context, cfg *models.WebhookConfig) error {
	m.created = cfg
	return m.createErr
}

func (m *mockWebhookConfigRepo) GetByID(ctx context.Context, id string) (*models.WebhookConfig, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	for _, c := range m.configs {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, models.ErrWebhookConfigNotFound
}

func (m *mockWebhookConfigRepo) ListByUserID(_ context.Context, userID string) ([]*models.WebhookConfig, error) {
	if m.listByUserErr != nil {
		return nil, m.listByUserErr
	}
	var result []*models.WebhookConfig
	for _, c := range m.configs {
		if c.UserID == userID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *mockWebhookConfigRepo) ListActiveByUserID(_ context.Context, userID string) ([]*models.WebhookConfig, error) {
	if m.listActiveErr != nil {
		return nil, m.listActiveErr
	}
	var result []*models.WebhookConfig
	for _, c := range m.configs {
		if c.UserID == userID && c.RevokedAt == nil {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *mockWebhookConfigRepo) Update(_ context.Context, cfg *models.WebhookConfig) error {
	return m.updateErr
}

func (m *mockWebhookConfigRepo) Revoke(_ context.Context, id string, ownerUserID string) error {
	if m.revokeErr != nil {
		return m.revokeErr
	}
	for _, c := range m.configs {
		if c.ID == id && c.UserID == ownerUserID {
			return nil
		}
	}
	return models.ErrWebhookConfigNotFound
}

// ---- test helpers ----

func newWebhookHandlers(repo *mockWebhookConfigRepo) *WebhookHandlers {
	return &WebhookHandlers{
		Deps: Dependencies{
			WebhookConfigRepo: repo,
			ServerSecret:      []byte("test-server-secret-32-bytes!!!!!"),
			Logger:            slog.Default(),
			Auth:              &auth.AuthMiddleware{},
		},
	}
}

func webhookReq(method, path string, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func withWebhookID(r *http.Request, id string) *http.Request {
	return mux.SetURLVars(r, map[string]string{"id": id})
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
}

// ---- ListWebhooks tests ----

func TestListWebhooks_Unauthenticated(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	// no user ID in context
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListWebhooks_Empty(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var items []listWebhookItem
	decodeJSON(t, rec, &items)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestListWebhooks_ReturnsUserConfigs(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook 1", URL: "https://example.com/1"},
			{ID: testWebhookID2, UserID: "user-1", Name: "Hook 2", URL: "https://example.com/2"},
			{ID: testWebhookID3, UserID: "user-other", Name: "Other", URL: "https://other.com"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var items []listWebhookItem
	decodeJSON(t, rec, &items)
	if len(items) != 2 {
		t.Errorf("expected 2 items for user-1, got %d", len(items))
	}
}

func TestListWebhooks_DoesNotLeakSecretHash(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook 1", URL: "https://example.com/1", SecretHash: "supersecret123"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "supersecret123") {
		t.Error("response body contains secret_hash — should never be exposed in list")
	}
	if strings.Contains(body, "secret") {
		t.Error("response body contains 'secret' field — should not appear in list response")
	}
}

func TestListWebhooks_DBError(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		listByUserErr: errors.New("db connection lost"),
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	// Must not leak internal error
	body := rec.Body.String()
	if strings.Contains(body, "db connection lost") {
		t.Error("internal error message leaked to client")
	}
}

// ---- CreateWebhook tests ----

func TestCreateWebhook_Unauthenticated(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://example.com"})
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCreateWebhook_MissingName(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{URL: "https://example.com"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateWebhook_MissingURL(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateWebhook_NameTooLong(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	longName := strings.Repeat("a", 101)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: longName, URL: "https://example.com"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateWebhook_HTTPRejected(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "http://example.com/hook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "HTTPS") {
		t.Error("error message should mention HTTPS requirement")
	}
}

func TestCreateWebhook_SSRFLocalhost(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://localhost/hook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for localhost SSRF, got %d", rec.Code)
	}
}

func TestCreateWebhook_PerUserLimit(t *testing.T) {
	// Fill up 10 active configs
	configs := make([]*models.WebhookConfig, 10)
	for i := range configs {
		configs[i] = &models.WebhookConfig{
			ID:     fmt.Sprintf("aaaaaaaa-aaaa-aaaa-aaaa-%012d", i),
			UserID: "user-1",
			Name:   "Hook",
			URL:    "https://example.com",
		}
	}
	repo := &mockWebhookConfigRepo{configs: configs}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "one more", URL: "https://example.com/hook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for per-user limit, got %d", rec.Code)
	}
}

func TestCreateWebhook_Success(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "My Hook", URL: "https://example.com/webhook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp createWebhookResponse
	decodeJSON(t, rec, &resp)

	if resp.ID == "" {
		t.Error("response ID should not be empty")
	}
	if resp.Name != "My Hook" {
		t.Errorf("expected name 'My Hook', got %q", resp.Name)
	}
	if resp.URL != "https://example.com/webhook" {
		t.Errorf("expected URL 'https://example.com/webhook', got %q", resp.URL)
	}
	if resp.Secret == "" {
		t.Error("response secret should not be empty (shown once)")
	}
	// Secret should be 64 hex chars (32 bytes)
	if len(resp.Secret) != 64 {
		t.Errorf("expected 64-char hex secret, got %d chars", len(resp.Secret))
	}
}

func TestCreateWebhook_SecretIsHashed(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://example.com/webhook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	var resp createWebhookResponse
	decodeJSON(t, rec, &resp)

	// The stored secret_hash must NOT equal the plaintext secret
	if repo.created == nil {
		t.Fatal("expected Create to be called on repo")
	}
	if repo.created.SecretHash == resp.Secret {
		t.Error("stored secret_hash equals plaintext secret — hashing is broken")
	}
	if repo.created.SecretHash == "" {
		t.Error("stored secret_hash is empty")
	}
}

func TestCreateWebhook_ResolvedIPStored(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://example.com/webhook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if repo.created == nil {
		t.Fatal("expected Create to be called")
	}
	if repo.created.ResolvedIP == nil {
		t.Error("resolved_ip should be set after URL validation")
	}
}

func TestCreateWebhook_DBCreateError(t *testing.T) {
	repo := &mockWebhookConfigRepo{createErr: errors.New("insert failed")}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://example.com/webhook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "insert failed") {
		t.Error("internal error leaked to client")
	}
}

func TestCreateWebhook_InvalidJSON(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rec.Code)
	}
}

// ---- UpdateWebhook tests ----

func TestUpdateWebhook_Unauthenticated(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{Name: "new"})
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUpdateWebhook_NotFound(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/nonexistent", updateWebhookRequest{Name: "new"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testNonExistentID)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateWebhook_WrongOwner(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-other", Name: "Hook", URL: "https://example.com"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{Name: "hijack"})
	req = withUserID(req, "user-attacker")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	// Should return 404, not 403 — don't leak existence
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong owner (don't leak existence), got %d", rec.Code)
	}
}

func TestUpdateWebhook_NameOnly(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Old Name", URL: "https://example.com/hook"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{Name: "New Name"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateWebhook_URLChange_SSRFRejected(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com/hook"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{URL: "https://localhost/evil"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for SSRF URL in update, got %d", rec.Code)
	}
}

func TestUpdateWebhook_URLChange_HTTPRejected(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com/hook"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{URL: "http://example.com/downgrade"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for HTTP URL in update, got %d", rec.Code)
	}
}

func TestUpdateWebhook_InvalidJSON(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com/hook"},
		},
	}
	h := newWebhookHandlers(repo)
	req := httptest.NewRequest("PATCH", "/api/v1/webhooks/wh-1", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rec.Code)
	}
}

// ---- RevokeWebhook tests ----

func TestRevokeWebhook_Unauthenticated(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("DELETE", "/api/v1/webhooks/wh-1", nil)
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRevokeWebhook_NotFound(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("DELETE", "/api/v1/webhooks/nonexistent", nil)
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testNonExistentID)
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRevokeWebhook_WrongOwner(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-owner", Name: "Hook", URL: "https://example.com"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("DELETE", "/api/v1/webhooks/wh-1", nil)
	req = withUserID(req, "user-attacker")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong owner, got %d", rec.Code)
	}
}

func TestRevokeWebhook_Success(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("DELETE", "/api/v1/webhooks/wh-1", nil)
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeWebhook_DBError(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		revokeErr: errors.New("db down"),
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("DELETE", "/api/v1/webhooks/wh-1", nil)
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "db down") {
		t.Error("internal error leaked to client")
	}
}

func TestUpdateWebhook_URLChange_Success(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://old.example.com/hook"},
		},
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{URL: "https://example.com/new-hook"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateWebhook_DBUpdateError(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com/hook"},
		},
		updateErr: errors.New("db timeout"),
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{Name: "Updated"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "db timeout") {
		t.Error("internal error leaked to client")
	}
}

func TestUpdateWebhook_AlreadyRevoked(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "Hook", URL: "https://example.com/hook"},
		},
		updateErr: models.ErrWebhookConfigNotFound,
	}
	h := newWebhookHandlers(repo)
	req := webhookReq("PATCH", "/api/v1/webhooks/wh-1", updateWebhookRequest{Name: "Updated"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, testWebhookID1)
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for already-revoked webhook, got %d", rec.Code)
	}
}

func TestUpdateWebhook_MissingID(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("PATCH", "/api/v1/webhooks/", updateWebhookRequest{Name: "test"})
	req = withUserID(req, "user-1")
	// no mux vars set — empty ID
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing ID, got %d", rec.Code)
	}
}

func TestRevokeWebhook_MissingID(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("DELETE", "/api/v1/webhooks/", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing ID, got %d", rec.Code)
	}
}

// ---- JSON response format tests ----

func TestErrorResponseFormat(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	var apiErr models.APIError
	decodeJSON(t, rec, &apiErr)

	if apiErr.Code == 0 {
		t.Error("error response missing 'code' field")
	}
	if apiErr.Message == "" {
		t.Error("error response missing 'message' field")
	}
}

func TestCreateWebhook_ResponseContentType(t *testing.T) {
	repo := &mockWebhookConfigRepo{}
	h := newWebhookHandlers(repo)
	req := webhookReq("POST", "/api/v1/webhooks", createWebhookRequest{Name: "test", URL: "https://example.com/webhook"})
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json content type, got %q", ct)
	}
}

// ---- MaxBytesReader test ----

func TestCreateWebhook_OversizedBody(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	// 128KB body exceeds the 64KB MaxBytesReader limit
	bigBody := strings.Repeat("x", 128*1024)
	req := httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.CreateWebhook(rec, req)

	// Should fail with 422 (invalid JSON) since MaxBytesReader truncates
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for oversized body, got %d", rec.Code)
	}
}

// ---- Cross-user isolation test ----

func TestListWebhooks_IsolatedPerUser(t *testing.T) {
	repo := &mockWebhookConfigRepo{
		configs: []*models.WebhookConfig{
			{ID: testWebhookID1, UserID: "user-1", Name: "User1 Hook", URL: "https://example.com/1"},
			{ID: testWebhookID2, UserID: "user-2", Name: "User2 Hook", URL: "https://example.com/2"},
		},
	}
	h := newWebhookHandlers(repo)

	// user-1 should only see their own
	req := webhookReq("GET", "/api/v1/webhooks", nil)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()
	h.ListWebhooks(rec, req)

	var items []listWebhookItem
	decodeJSON(t, rec, &items)
	if len(items) != 1 {
		t.Fatalf("user-1 should see 1 config, got %d", len(items))
	}
	if items[0].ID != testWebhookID1 {
		t.Errorf("user-1 should see wh-1, got %s", items[0].ID)
	}

	// user-2 should only see their own
	req2 := webhookReq("GET", "/api/v1/webhooks", nil)
	req2 = withUserID(req2, "user-2")
	rec2 := httptest.NewRecorder()
	h.ListWebhooks(rec2, req2)

	var items2 []listWebhookItem
	decodeJSON(t, rec2, &items2)
	if len(items2) != 1 {
		t.Fatalf("user-2 should see 1 config, got %d", len(items2))
	}
	if items2[0].ID != testWebhookID2 {
		t.Errorf("user-2 should see wh-2, got %s", items2[0].ID)
	}
}

// ---- UUID validation tests ----

func TestUpdateWebhook_NonUUIDIDRejected(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("PATCH", "/api/v1/webhooks/not-a-uuid", updateWebhookRequest{Name: "test"})
	req = withUserID(req, "user-1")
	req = withWebhookID(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.UpdateWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-UUID webhook ID, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid webhook id") {
		t.Errorf("expected 'invalid webhook id' error, got: %s", body)
	}
}

func TestRevokeWebhook_NonUUIDIDRejected(t *testing.T) {
	h := newWebhookHandlers(&mockWebhookConfigRepo{})
	req := webhookReq("DELETE", "/api/v1/webhooks/not-a-uuid", nil)
	req = withUserID(req, "user-1")
	req = withWebhookID(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.RevokeWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-UUID webhook ID, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid webhook id") {
		t.Errorf("expected 'invalid webhook id' error, got: %s", body)
	}
}
