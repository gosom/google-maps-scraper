package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// mockJobService is a minimal stub for ownership tests.
type mockJobService struct {
	getFunc    func(ctx context.Context, id string, userID string) (models.Job, error)
	deleteFunc func(ctx context.Context, id string, userID string) error
	cancelFunc func(ctx context.Context, id string, userID string) error
}

func (m *mockJobService) Create(_ context.Context, _ *models.Job) error { return nil }
func (m *mockJobService) All(_ context.Context, _ string) ([]models.Job, error) {
	return nil, nil
}
func (m *mockJobService) Get(ctx context.Context, id string, userID string) (models.Job, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, id, userID)
	}
	return models.Job{}, errors.New("not found")
}
func (m *mockJobService) Delete(ctx context.Context, id string, userID string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id, userID)
	}
	return nil
}
func (m *mockJobService) Cancel(ctx context.Context, id string, userID string) error {
	if m.cancelFunc != nil {
		return m.cancelFunc(ctx, id, userID)
	}
	return nil
}
func (m *mockJobService) GetCSV(_ context.Context, _ string) (string, error) { return "", nil }
func (m *mockJobService) GetCSVReader(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// withUserID injects a userID into the request context.
func withUserID(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
	return r.WithContext(ctx)
}

// newRequestWithJobID creates a request and sets the mux "id" variable.
func newRequestWithJobID(method, jobID string) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/jobs/"+jobID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": jobID})
	return req
}

func TestGetJob_OwnerCanAccess(t *testing.T) {
	ownerID := "user-abc"
	jobID := uuid.New().String()

	svc := &mockJobService{
		getFunc: func(_ context.Context, id string, userID string) (models.Job, error) {
			if id == jobID && userID == ownerID {
				return models.Job{ID: jobID, UserID: ownerID, Status: models.StatusOK}, nil
			}
			return models.Job{}, errors.New("not found")
		},
	}

	deps := Dependencies{
		App:  svc,
		Auth: &auth.AuthMiddleware{}, // non-nil signals auth is enabled
	}
	h := &APIHandlers{Deps: deps}

	req := newRequestWithJobID(http.MethodGet, jobID)
	req = withUserID(req, ownerID)
	w := httptest.NewRecorder()

	h.GetJob(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for owner, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetJob_NonOwnerGets404(t *testing.T) {
	ownerID := "user-abc"
	nonOwnerID := "user-xyz"
	jobID := uuid.New().String()

	// Simulate DB returning no rows when the non-owner queries (ownership enforced in DB)
	svc := &mockJobService{
		getFunc: func(_ context.Context, id string, userID string) (models.Job, error) {
			if id == jobID && userID == ownerID {
				return models.Job{ID: jobID, UserID: ownerID, Status: models.StatusOK}, nil
			}
			// Non-owner gets sql.ErrNoRows equivalent
			return models.Job{}, errors.New("job not found")
		},
	}

	deps := Dependencies{
		App:  svc,
		Auth: &auth.AuthMiddleware{}, // non-nil signals auth is enabled
	}
	h := &APIHandlers{Deps: deps}

	req := newRequestWithJobID(http.MethodGet, jobID)
	req = withUserID(req, nonOwnerID)
	w := httptest.NewRecorder()

	h.GetJob(w, req)

	// Must return 404, never 403, to avoid confirming the job exists
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for non-owner, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetJob_UnauthenticatedGets401(t *testing.T) {
	jobID := uuid.New().String()

	svc := &mockJobService{}
	deps := Dependencies{
		App:  svc,
		Auth: &auth.AuthMiddleware{}, // non-nil signals auth is enabled
	}
	h := &APIHandlers{Deps: deps}

	// No userID injected into context
	req := newRequestWithJobID(http.MethodGet, jobID)
	w := httptest.NewRecorder()

	h.GetJob(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for unauthenticated request, got %d: %s", w.Code, w.Body.String())
	}
}
