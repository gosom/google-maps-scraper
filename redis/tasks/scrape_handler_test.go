package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateScrapeTask(t *testing.T) {
	tests := []struct {
		name        string
		payload     *ScrapePayload
		wantErr     bool
		errContains string
	}{
		{
			name: "valid payload",
			payload: &ScrapePayload{
				JobID:    "test-job",
				Keywords: []string{"test1", "test2"},
				Lang:     "en",
				Depth:    2,
				Email:    true,
			},
			wantErr: false,
		},
		{
			name: "payload with coordinates",
			payload: &ScrapePayload{
				JobID:    "test-job-coords",
				Keywords: []string{"test"},
				Lat:      "40.7128",
				Lon:      "-74.0060",
				Zoom:     15,
				Radius:   5000,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := CreateScrapeTask(tt.payload)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, TypeScrapeGMaps, task.Type())

			// Verify payload was properly serialized
			var decodedPayload ScrapePayload
			err = json.Unmarshal(task.Payload(), &decodedPayload)
			require.NoError(t, err)
			assert.Equal(t, tt.payload.JobID, decodedPayload.JobID)
			assert.Equal(t, tt.payload.Keywords, decodedPayload.Keywords)
			assert.Equal(t, tt.payload.Lang, decodedPayload.Lang)
			assert.Equal(t, tt.payload.Depth, decodedPayload.Depth)
			assert.Equal(t, tt.payload.Email, decodedPayload.Email)
			assert.Equal(t, tt.payload.Lat, decodedPayload.Lat)
			assert.Equal(t, tt.payload.Lon, decodedPayload.Lon)
			assert.Equal(t, tt.payload.Zoom, decodedPayload.Zoom)
			assert.Equal(t, tt.payload.Radius, decodedPayload.Radius)
		})
	}
}

func TestProcessScrapeTask(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		payload     *ScrapePayload
		setupHdlr   func(*Handler)
		wantErr     bool
		errContains string
	}{
		{
			name: "empty keywords",
			payload: &ScrapePayload{
				JobID:    "empty-keywords",
				Keywords: []string{},
			},
			wantErr:     true,
			errContains: "no keywords provided",
		},
		{
			name: "invalid json payload",
			payload: nil,
			wantErr:     true,
			errContains: "failed to unmarshal scrape payload",
		},
		{
			name: "valid minimal payload",
			payload: &ScrapePayload{
				JobID:    "test-minimal",
				Keywords: []string{"test"},
			},
			setupHdlr: func(h *Handler) {
				h.concurrency = 1
				h.taskTimeout = 1 * time.Second
			},
			wantErr: false,
		},
		{
			name: "with proxies",
			payload: &ScrapePayload{
				JobID:    "test-proxies",
				Keywords: []string{"test"},
				Proxies:  []string{"http://proxy1:8080", "http://proxy2:8080"},
			},
			setupHdlr: func(h *Handler) {
				h.concurrency = 1
				h.taskTimeout = 1 * time.Second
			},
			wantErr: false,
		},
		{
			name: "with handler proxies",
			payload: &ScrapePayload{
				JobID:    "test-handler-proxies",
				Keywords: []string{"test"},
			},
			setupHdlr: func(h *Handler) {
				h.concurrency = 1
				h.taskTimeout = 1 * time.Second
				h.proxies = []string{"http://proxy1:8080", "http://proxy2:8080"}
			},
			wantErr: false,
		},
		{
			name: "fast mode",
			payload: &ScrapePayload{
				JobID:    "test-fast-mode",
				Keywords: []string{"test"},
				FastMode: true,
			},
			setupHdlr: func(h *Handler) {
				h.concurrency = 1
				h.taskTimeout = 1 * time.Second
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with temp directory
			h := NewHandler(WithDataFolder(tempDir))
			if tt.setupHdlr != nil {
				tt.setupHdlr(h)
			}

			// Create task
			var task *asynq.Task
			var err error
			if tt.payload != nil {
				task, err = CreateScrapeTask(tt.payload)
				require.NoError(t, err)
			} else {
				task = asynq.NewTask(TypeScrapeGMaps, []byte(`{invalid json`))
			}

			// Process task
			err = h.processScrapeTask(context.Background(), task)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)

			// Verify output file was created
			if tt.payload != nil {
				outpath := filepath.Join(tempDir, tt.payload.JobID+".csv")
				_, err := os.Stat(outpath)
				assert.NoError(t, err, "Output file should exist")
			}
		})
	}
}

func TestSetupMate(t *testing.T) {
	tests := []struct {
		name        string
		payload     *ScrapePayload
		setupHdlr   func(*Handler)
		wantErr     bool
		errContains string
	}{
		{
			name: "default config",
			payload: &ScrapePayload{
				JobID:    "test-default",
				Keywords: []string{"test"},
			},
			setupHdlr: func(h *Handler) {
				h.concurrency = 2
			},
			wantErr: false,
		},
		{
			name: "fast mode config",
			payload: &ScrapePayload{
				JobID:    "test-fast",
				Keywords: []string{"test"},
				FastMode: true,
			},
			wantErr: false,
		},
		{
			name: "with proxies",
			payload: &ScrapePayload{
				JobID:    "test-proxies",
				Keywords: []string{"test"},
				Proxies:  []string{"http://proxy1:8080"},
			},
			wantErr: false,
		},
		{
			name: "with handler proxies",
			payload: &ScrapePayload{
				JobID:    "test-handler-proxies",
				Keywords: []string{"test"},
			},
			setupHdlr: func(h *Handler) {
				h.proxies = []string{"http://proxy1:8080"}
			},
			wantErr: false,
		},
		{
			name: "disable page reuse",
			payload: &ScrapePayload{
				JobID:    "test-no-reuse",
				Keywords: []string{"test"},
			},
			setupHdlr: func(h *Handler) {
				h.disableReuse = true
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler()
			if tt.setupHdlr != nil {
				tt.setupHdlr(h)
			}

			// Create a temporary file for testing
			tmpfile, err := os.CreateTemp("", "scrapemate-test-*.csv")
			require.NoError(t, err)
			defer os.Remove(tmpfile.Name())
			defer tmpfile.Close()

			mate, err := h.setupMate(context.Background(), tmpfile, tt.payload)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, mate)
			mate.Close()
		})
	}
} 