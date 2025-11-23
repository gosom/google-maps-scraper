package models

import (
	"context"
	"time"
)

// JobFile represents a file (CSV, JSON, etc.) stored in S3 for a job
type JobFile struct {
	ID             string
	JobID          string
	UserID         string
	FileType       string // csv, json, xlsx, log, archive
	BucketName     string
	ObjectKey      string
	VersionID      *string // S3 version ID if bucket versioning is enabled
	ETag           string  // S3 ETag for integrity verification
	SizeBytes      int64
	MimeType       string
	Status         string // uploading, available, failed, archived, deleted
	ErrorMessage   *string
	CreatedAt      time.Time
	UploadedAt     *time.Time
	LastAccessedAt *time.Time
}

// JobFileStatus constants
const (
	JobFileStatusUploading = "uploading"
	JobFileStatusAvailable = "available"
	JobFileStatusFailed    = "failed"
	JobFileStatusArchived  = "archived"
	JobFileStatusDeleted   = "deleted"
)

// JobFileType constants
const (
	JobFileTypeCSV     = "csv"
	JobFileTypeJSON    = "json"
	JobFileTypeXLSX    = "xlsx"
	JobFileTypeLog     = "log"
	JobFileTypeArchive = "archive"
)

// JobFileRepository defines the interface for job file storage
type JobFileRepository interface {
	Create(ctx context.Context, jobFile *JobFile) error
	Get(ctx context.Context, id string) (*JobFile, error)
	GetByJobID(ctx context.Context, jobID string, fileType string) (*JobFile, error)
	Update(ctx context.Context, jobFile *JobFile) error
	Delete(ctx context.Context, id string) error
	ListByJobID(ctx context.Context, jobID string) ([]*JobFile, error)
	ListByUserID(ctx context.Context, userID string) ([]*JobFile, error)
}
