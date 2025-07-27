package web_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testJobID = "test-job-123"

// mockJobRepository implements the JobRepository interface for testing
type mockJobRepository struct {
	jobs map[string]web.Job
	mu   sync.RWMutex
}

func newMockJobRepository() *mockJobRepository {
	return &mockJobRepository{
		jobs: make(map[string]web.Job),
	}
}

func (m *mockJobRepository) Get(_ context.Context, id string) (web.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, exists := m.jobs[id]
	if !exists {
		return web.Job{}, assert.AnError
	}

	return job, nil
}

func (m *mockJobRepository) addJob(job *web.Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = *job
}

func (m *mockJobRepository) Create(_ context.Context, _ *web.Job) error {
	return nil
}

func (m *mockJobRepository) Delete(_ context.Context, _ string) error {
	return nil
}

func (m *mockJobRepository) Select(_ context.Context, _ web.SelectParams) ([]web.Job, error) {
	return nil, nil
}

func (m *mockJobRepository) Update(_ context.Context, _ *web.Job) error {
	return nil
}

func TestServerCreation(t *testing.T) {
	mockRepo := newMockJobRepository()
	mockSvc := web.NewService(mockRepo, "/tmp")
	server, err := web.New(mockSvc, ":0", "")
	require.NoError(t, err)

	assert.NotNil(t, server)
}

func TestSSEEndpointWithValidJob(t *testing.T) {
	mockRepo := newMockJobRepository()
	mockSvc := web.NewService(mockRepo, "/tmp")

	// Create and add a test job
	jobID := uuid.New()
	testJob := web.Job{
		ID:     jobID.String(),
		Name:   "test-job",
		Status: web.StatusWorking,
		Date:   time.Now(),
	}
	mockRepo.addJob(&testJob)

	// Test that service can handle job requests
	job, err := mockSvc.Get(context.Background(), jobID.String())
	require.NoError(t, err)
	assert.Equal(t, testJob.ID, job.ID)
}

func TestSSEEndpointWithInvalidJob(t *testing.T) {
	mockRepo := newMockJobRepository()
	mockSvc := web.NewService(mockRepo, "/tmp")

	// Test with non-existent job
	_, err := mockSvc.Get(context.Background(), uuid.New().String())
	assert.Error(t, err)
}
