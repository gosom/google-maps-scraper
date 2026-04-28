package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/web/auth"
)

// TestDownloadURL_ReturnsPresignedURL exercises the success path: an
// authenticated request for a valid UUID hits the JobService stub which
// returns a fixed URL; the handler must respond 200 with a JSON body
// containing that URL and a 5-minute (300s) expiry.
func TestDownloadURL_ReturnsPresignedURL(t *testing.T) {
	jobID := uuid.Must(uuid.NewV7()).String()
	wantURL := "https://example.com/presigned?sig=abc"

	svc := &mockJobService{
		presignURLFunc: func(_ context.Context, id, userID string, ttl time.Duration) (string, error) {
			assert.Equal(t, jobID, id)
			assert.Equal(t, "user-abc", userID)
			assert.Equal(t, 5*time.Minute, ttl)
			return wantURL, nil
		},
	}
	h := &WebHandlers{Deps: Dependencies{App: svc, Auth: &auth.AuthMiddleware{}}}

	req := newRequestWithJobID(http.MethodGet, jobID)
	req = withUserID(req, "user-abc")
	w := httptest.NewRecorder()

	h.DownloadURL(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, wantURL, body["url"])
	assert.Equal(t, "300", body["expires_in"])
}

func TestDownloadURL_InvalidUUIDReturns422(t *testing.T) {
	svc := &mockJobService{}
	h := &WebHandlers{Deps: Dependencies{App: svc, Auth: &auth.AuthMiddleware{}}}

	req := newRequestWithJobID(http.MethodGet, "not-a-uuid")
	req = withUserID(req, "user-abc")
	w := httptest.NewRecorder()

	h.DownloadURL(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestDownloadURL_UnauthenticatedReturns401(t *testing.T) {
	jobID := uuid.Must(uuid.NewV7()).String()

	svc := &mockJobService{}
	h := &WebHandlers{Deps: Dependencies{App: svc, Auth: &auth.AuthMiddleware{}}}

	// No userID injected into context.
	req := newRequestWithJobID(http.MethodGet, jobID)
	w := httptest.NewRecorder()

	h.DownloadURL(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestDownloadURL_ServiceErrorReturns404 exercises the error path: the
// JobService returns an error (e.g. job-not-found, S3 not configured, file
// not available) — handler maps every such error to 404 to avoid leaking
// existence/configuration details.
func TestDownloadURL_ServiceErrorReturns404(t *testing.T) {
	jobID := uuid.Must(uuid.NewV7()).String()

	svc := &mockJobService{
		presignURLFunc: func(_ context.Context, _, _ string, _ time.Duration) (string, error) {
			return "", errors.New("job file not available")
		},
	}
	h := &WebHandlers{Deps: Dependencies{App: svc, Auth: &auth.AuthMiddleware{}}}

	req := newRequestWithJobID(http.MethodGet, jobID)
	req = withUserID(req, "user-abc")
	w := httptest.NewRecorder()

	h.DownloadURL(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestDownloadURL_NonGETReturns405 confirms only GET is accepted.
func TestDownloadURL_NonGETReturns405(t *testing.T) {
	jobID := uuid.Must(uuid.NewV7()).String()

	svc := &mockJobService{}
	h := &WebHandlers{Deps: Dependencies{App: svc, Auth: &auth.AuthMiddleware{}}}

	req := newRequestWithJobID(http.MethodPost, jobID)
	w := httptest.NewRecorder()

	h.DownloadURL(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
