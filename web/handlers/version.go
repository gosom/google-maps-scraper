package handlers

import (
	"encoding/json"
	"net/http"
	"os"
)

// VersionResponse contains build metadata and runtime information.
// Fields are carefully selected to balance debugging utility with security.
// Full git_commit and go_version are excluded to prevent targeted exploits.
type VersionResponse struct {
	Version        string `json:"version"`
	BuildDate      string `json:"build_date"`
	GitCommitShort string `json:"git_commit_short"`
	Environment    string `json:"environment"`
}

// VersionHandler handles version information requests.
type VersionHandler struct{}

// NewVersionHandler creates a new version handler instance.
func NewVersionHandler() *VersionHandler {
	return &VersionHandler{}
}

// GetVersion returns build metadata as JSON.
// This endpoint does not require authentication.
// Exposes: version, build_date, git_commit_short (7 chars), environment.
// Excludes: full git_commit (source targeting), go_version (CVE exploits).
func (h *VersionHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	gitCommit := getEnvOrDefault("GIT_COMMIT", "")
	shortCommit := gitCommit
	if len(gitCommit) > 7 {
		shortCommit = gitCommit[:7]
	}

	response := VersionResponse{
		Version:        getEnvOrDefault("VERSION", ""),
		BuildDate:      getEnvOrDefault("BUILD_DATE", ""),
		GitCommitShort: shortCommit,
		Environment:    getEnvOrDefault("ENVIRONMENT", "development"),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// getEnvOrDefault retrieves environment variable or returns default value.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
