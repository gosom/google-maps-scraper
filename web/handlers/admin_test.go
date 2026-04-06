package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	"github.com/gosom/google-maps-scraper/web/handlers"
)

// mockAdminJobService implements handlers.JobService for testing.
type mockAdminJobService struct {
	createCalled bool
	cancelCalled bool
	allCalled    bool
	lastJob      *models.Job
}

func (m *mockAdminJobService) Create(_ context.Context, job *models.Job) error {
	m.createCalled = true
	m.lastJob = job
	return nil
}
func (m *mockAdminJobService) All(_ context.Context, _ string) ([]models.Job, error) {
	m.allCalled = true
	return []models.Job{{ID: "job-1", Source: models.SourceAdmin}}, nil
}
func (m *mockAdminJobService) AllPaginated(_ context.Context, _ models.PaginatedJobsParams) ([]models.Job, int, error) {
	return nil, 0, nil
}
func (m *mockAdminJobService) Get(_ context.Context, _ string, _ string) (models.Job, error) {
	return models.Job{}, nil
}
func (m *mockAdminJobService) Delete(_ context.Context, _ string, _ string) error { return nil }
func (m *mockAdminJobService) Cancel(_ context.Context, _ string, _ string) error {
	m.cancelCalled = true
	return nil
}
func (m *mockAdminJobService) GetCSV(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockAdminJobService) GetCSVReader(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// ---------------------------------------------------------------------------
// Happy-path tests
// ---------------------------------------------------------------------------

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

	// Verify response body contains job ID.
	var resp models.ApiScrapeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty job ID in response")
	}
}

func TestAdminGetJobs_Success(t *testing.T) {
	mock := &mockAdminJobService{}
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: mock},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/jobs", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin_123")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.GetJobs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !mock.allCalled {
		t.Fatal("expected App.All to be called")
	}
}

func TestAdminCancelJob_Success(t *testing.T) {
	mock := &mockAdminJobService{}
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: mock},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs/job-123/cancel", nil)
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin_123")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	req = req.WithContext(ctx)

	req = mux.SetURLVars(req, map[string]string{"id": "job-123"})

	rr := httptest.NewRecorder()
	h.CancelJob(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !mock.cancelCalled {
		t.Fatal("expected App.Cancel to be called")
	}
}

// ---------------------------------------------------------------------------
// Rejection tests — table-driven for requireAdminSession coverage
// ---------------------------------------------------------------------------

func TestAdminHandlers_RejectUnauthorized(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}

	// Shared request body for CreateJob tests.
	body := map[string]interface{}{
		"name": "test", "keywords": []string{"test"}, "lang": "en", "depth": 1,
	}
	bodyBytes, _ := json.Marshal(body)

	tests := []struct {
		name       string
		handler    func(http.ResponseWriter, *http.Request)
		method     string
		path       string
		body       []byte
		userID     string
		role       string
		apiKeyID   string
		wantStatus int
	}{
		// Non-admin rejection
		{"CreateJob rejects non-admin", h.CreateJob, http.MethodPost, "/api/v1/admin/jobs", bodyBytes, "user_1", models.RoleUser, "", http.StatusForbidden},
		{"GetJobs rejects non-admin", h.GetJobs, http.MethodGet, "/api/v1/admin/jobs", nil, "user_1", models.RoleUser, "", http.StatusForbidden},
		{"CancelJob rejects non-admin", h.CancelJob, http.MethodPost, "/api/v1/admin/jobs/id/cancel", nil, "user_1", models.RoleUser, "", http.StatusForbidden},

		// API key rejection (admin role but via API key)
		{"CreateJob rejects API key", h.CreateJob, http.MethodPost, "/api/v1/admin/jobs", bodyBytes, "user_2", models.RoleAdmin, "key-uuid", http.StatusForbidden},
		{"GetJobs rejects API key", h.GetJobs, http.MethodGet, "/api/v1/admin/jobs", nil, "user_2", models.RoleAdmin, "key-uuid", http.StatusForbidden},
		{"CancelJob rejects API key", h.CancelJob, http.MethodPost, "/api/v1/admin/jobs/id/cancel", nil, "user_2", models.RoleAdmin, "key-uuid", http.StatusForbidden},

		// Missing auth
		{"CreateJob rejects no auth", h.CreateJob, http.MethodPost, "/api/v1/admin/jobs", bodyBytes, "", models.RoleAdmin, "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != nil {
				bodyReader = bytes.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)

			ctx := req.Context()
			if tt.userID != "" {
				ctx = context.WithValue(ctx, auth.UserIDKey, tt.userID)
			}
			ctx = context.WithValue(ctx, auth.UserRoleKey, tt.role)
			if tt.apiKeyID != "" {
				ctx = context.WithValue(ctx, auth.APIKeyIDKey, tt.apiKeyID)
			}
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			tt.handler(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}
