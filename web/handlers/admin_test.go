package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	"github.com/gosom/google-maps-scraper/web/handlers"
)

// mockAdminJobService implements handlers.JobService for testing.
type mockAdminJobService struct {
	createCalled bool
	lastJob      *models.Job
}

func (m *mockAdminJobService) Create(_ context.Context, job *models.Job) error {
	m.createCalled = true
	m.lastJob = job
	return nil
}
func (m *mockAdminJobService) All(_ context.Context, _ string) ([]models.Job, error) {
	return nil, nil
}
func (m *mockAdminJobService) Get(_ context.Context, _ string, _ string) (models.Job, error) {
	return models.Job{}, nil
}
func (m *mockAdminJobService) Delete(_ context.Context, _ string, _ string) error { return nil }
func (m *mockAdminJobService) Cancel(_ context.Context, _ string, _ string) error { return nil }
func (m *mockAdminJobService) GetCSV(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockAdminJobService) GetCSVReader(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func TestAdminCreateJob_SetsAdminSource(t *testing.T) {
	mock := &mockAdminJobService{}
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: mock},
	}

	body := map[string]interface{}{
		"name":     "Berlin mass scrape",
		"keywords": []string{"restaurants in Berlin"},
		"lang":     "de",
		"depth":    10,
		"max_time": 60,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin_123")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CreateJob(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if !mock.createCalled {
		t.Fatal("expected App.Create to be called")
	}
	if mock.lastJob.Source != models.SourceAdmin {
		t.Errorf("expected source %q, got %q", models.SourceAdmin, mock.lastJob.Source)
	}
	if mock.lastJob.UserID != "user_admin_123" {
		t.Errorf("expected user_id %q, got %q", "user_admin_123", mock.lastJob.UserID)
	}
}

func TestAdminCreateJob_RejectsNonAdmin(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}

	body := map[string]interface{}{
		"name":     "test",
		"keywords": []string{"test"},
		"lang":     "en",
		"depth":    1,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_regular_456")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleUser)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CreateJob(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminCreateJob_RejectsAPIKeyAuth(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}

	body := map[string]interface{}{
		"name":     "test",
		"keywords": []string{"test"},
		"lang":     "en",
		"depth":    1,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin_789")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, "some-api-key-uuid")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CreateJob(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for API key auth, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminGetJobs_RejectsNonAdmin(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/jobs", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_regular")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleUser)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.GetJobs(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin GetJobs, got %d", rr.Code)
	}
}

func TestAdminGetJobs_RejectsAPIKeyAuth(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/jobs", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, "some-key")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.GetJobs(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for API key GetJobs, got %d", rr.Code)
	}
}

func TestAdminCancelJob_RejectsNonAdmin(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs/some-id/cancel", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_regular")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleUser)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CancelJob(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin CancelJob, got %d", rr.Code)
	}
}

func TestAdminCancelJob_RejectsAPIKeyAuth(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs/some-id/cancel", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, "some-key")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CancelJob(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for API key CancelJob, got %d", rr.Code)
	}
}
