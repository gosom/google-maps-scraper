package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/s3uploader"
)

type Service struct {
	repo        JobRepository
	dataFolder  string
	jobFileRepo models.JobFileRepository
	s3Uploader  *s3uploader.Uploader
	s3Bucket    string
}

func NewService(repo JobRepository, dataFolder string) *Service {
	return &Service{
		repo:       repo,
		dataFolder: dataFolder,
	}
}

// SetS3Config sets the S3 configuration for the service
func (s *Service) SetS3Config(jobFileRepo models.JobFileRepository, s3Uploader *s3uploader.Uploader, s3Bucket string) {
	s.jobFileRepo = jobFileRepo
	s.s3Uploader = s3Uploader
	s.s3Bucket = s3Bucket
}

func (s *Service) Create(ctx context.Context, job *Job) error {
	return s.repo.Create(ctx, job)
}

func (s *Service) All(ctx context.Context, userID string) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{UserID: userID})
}

func (s *Service) AllPaginated(ctx context.Context, params models.PaginatedJobsParams) ([]Job, int, error) {
	return s.repo.SelectPaginated(ctx, params)
}

func (s *Service) Get(ctx context.Context, id string, userID string) (Job, error) {
	return s.repo.Get(ctx, id, userID)
}

func (s *Service) Delete(ctx context.Context, id string, userID string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid file name")
	}
	log := pkglogger.FromContext(ctx)

	// First check the current job status using admin bypass (ownership enforced at Delete level)
	job, err := s.repo.Get(ctx, id, "")
	if err == nil {
		// If job is still running, cancel it first
		if job.Status == StatusWorking || job.Status == StatusPending {
			// Try to cancel the job first (admin bypass since ownership is checked at Delete)
			if cancelErr := s.repo.Cancel(ctx, id, ""); cancelErr != nil {
				// Log the error but continue with deletion
				log.Warn("cancel_before_delete_failed",
					slog.String("job_id", id),
					slog.Any("error", cancelErr),
				)
			}
		}
	}

	datapath := filepath.Join(s.dataFolder, id+".csv")

	if _, err := os.Stat(datapath); err == nil {
		if err := os.Remove(datapath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return s.repo.Delete(ctx, id, userID)
}

func (s *Service) Update(ctx context.Context, job *Job) error {
	return s.repo.Update(ctx, job)
}

func (s *Service) SelectPending(ctx context.Context) ([]Job, error) {
	// We don't filter by user ID when selecting pending jobs for processing
	return s.repo.Select(ctx, SelectParams{Status: StatusPending, Limit: 1})
}

// GetCSVReader returns an io.ReadCloser for the CSV file, either from S3 or local filesystem
// The caller is responsible for closing the returned ReadCloser
func (s *Service) GetCSVReader(ctx context.Context, id string) (io.ReadCloser, string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return nil, "", fmt.Errorf("invalid file name")
	}
	log := pkglogger.FromContext(ctx)

	log.Debug("get_csv_reader_start", slog.String("job_id", id))
	log.Debug("get_csv_reader_s3_config",
		slog.Bool("job_file_repo_configured", s.jobFileRepo != nil),
		slog.Bool("s3_uploader_configured", s.s3Uploader != nil),
		slog.Bool("s3_bucket_configured", s.s3Bucket != ""),
	)

	// Try S3 first if configured
	if s.jobFileRepo != nil && s.s3Uploader != nil && s.s3Bucket != "" {
		log.Debug("get_csv_reader_query_job_file", slog.String("job_id", id))
		jobFile, err := s.jobFileRepo.GetByJobID(ctx, id, models.JobFileTypeCSV)
		if err != nil {
			log.Debug("get_csv_reader_job_file_query_failed", slog.Any("error", err))
		} else {
			log.Debug("get_csv_reader_job_file_found",
				slog.String("bucket", jobFile.BucketName),
				slog.String("object_key", jobFile.ObjectKey),
				slog.String("status", string(jobFile.Status)),
			)

			if jobFile.Status == models.JobFileStatusAvailable {
				// File found in S3, download it
				log.Debug("get_csv_reader_s3_download_attempt",
					slog.String("bucket", jobFile.BucketName),
					slog.String("object_key", jobFile.ObjectKey),
				)
				reader, err := s.s3Uploader.Download(ctx, jobFile.BucketName, jobFile.ObjectKey)
				if err == nil {
					fileName := id + ".csv"
					log.Debug("get_csv_reader_s3_download_success", slog.String("job_id", id))
					return reader, fileName, nil
				}
				// If S3 download fails, fall through to local filesystem
				log.Warn("get_csv_reader_s3_download_failed",
					slog.String("job_id", id),
					slog.Any("error", err),
				)
			} else {
				log.Debug("get_csv_reader_job_file_not_available",
					slog.String("status", string(jobFile.Status)),
				)
			}
		}
	} else {
		log.Debug("get_csv_reader_s3_not_configured")
	}

	// Fall back to local filesystem
	base := filepath.Clean(s.dataFolder)
	datapath := filepath.Join(base, id+".csv")
	datapath = filepath.Clean(datapath)
	// Ensure the resulting path has the base as its prefix boundary (directory-safe)
	if rel, err := filepath.Rel(base, datapath); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("resolved path escapes data folder")
	}

	if _, err := os.Stat(datapath); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("csv file not found for job %s", id)
	}

	file, err := os.Open(datapath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open local csv file: %w", err)
	}

	fileName := filepath.Base(datapath)
	return file, fileName, nil
}

// GetCSV returns the local file path for the CSV file (legacy method for backward compatibility)
// Deprecated: Use GetCSVReader instead
func (s *Service) GetCSV(_ context.Context, id string) (string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid file name")
	}

	// Build and clean the path, then enforce it remains under the configured data folder
	base := filepath.Clean(s.dataFolder)
	datapath := filepath.Join(base, id+".csv")
	datapath = filepath.Clean(datapath)
	// Ensure the resulting path has the base as its prefix boundary (directory-safe)
	if rel, err := filepath.Rel(base, datapath); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("resolved path escapes data folder")
	}

	if _, err := os.Stat(datapath); os.IsNotExist(err) {
		return "", fmt.Errorf("csv file not found for job %s", id)
	}

	return datapath, nil
}

func (s *Service) Cancel(ctx context.Context, id string, userID string) error {
	log := pkglogger.FromContext(ctx)
	log.Debug("service_cancel_called", slog.String("job_id", id))

	// Get current job status before cancellation using admin bypass
	job, err := s.repo.Get(ctx, id, "")
	if err != nil {
		log.Debug("service_cancel_get_job_before_failed",
			slog.String("job_id", id),
			slog.Any("error", err),
		)
		return err
	}

	log.Debug("service_cancel_status_before",
		slog.String("job_id", id),
		slog.String("status", string(job.Status)),
	)

	// Call repository Cancel method with userID for ownership enforcement
	err = s.repo.Cancel(ctx, id, userID)
	if err != nil {
		log.Debug("service_cancel_repo_cancel_failed",
			slog.String("job_id", id),
			slog.Any("error", err),
		)
		return err
	}

	log.Debug("service_cancel_repo_cancel_completed", slog.String("job_id", id))

	// Verify the status was updated using admin bypass
	updatedJob, err := s.repo.Get(ctx, id, "")
	if err != nil {
		log.Debug("service_cancel_get_job_after_failed",
			slog.String("job_id", id),
			slog.Any("error", err),
		)
	} else {
		log.Debug("service_cancel_status_after",
			slog.String("job_id", id),
			slog.String("status", string(updatedJob.Status)),
		)
	}

	return nil
}
