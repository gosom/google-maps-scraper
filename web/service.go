package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosom/google-maps-scraper/models"
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

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid file name")
	}

	// First check the current job status
	job, err := s.repo.Get(ctx, id)
	if err == nil {
		// If job is still running, cancel it first
		if job.Status == StatusWorking || job.Status == StatusPending {
			// Try to cancel the job first
			if cancelErr := s.repo.Cancel(ctx, id); cancelErr != nil {
				// Log the error but continue with deletion
				fmt.Printf("Warning: Failed to cancel job %s before deletion: %v\n", id, cancelErr)
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

	return s.repo.Delete(ctx, id)
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

	fmt.Printf("DEBUG GetCSVReader: Looking for CSV for job %s\n", id)
	fmt.Printf("DEBUG GetCSVReader: S3 configured? jobFileRepo=%v, s3Uploader=%v, s3Bucket=%s\n",
		s.jobFileRepo != nil, s.s3Uploader != nil, s.s3Bucket)

	// Try S3 first if configured
	if s.jobFileRepo != nil && s.s3Uploader != nil && s.s3Bucket != "" {
		fmt.Printf("DEBUG GetCSVReader: Querying job_files table for job %s\n", id)
		jobFile, err := s.jobFileRepo.GetByJobID(ctx, id, models.JobFileTypeCSV)
		if err != nil {
			fmt.Printf("DEBUG GetCSVReader: job_files query error: %v\n", err)
		} else {
			fmt.Printf("DEBUG GetCSVReader: Found job file - bucket=%s, key=%s, status=%s\n",
				jobFile.BucketName, jobFile.ObjectKey, jobFile.Status)

			if jobFile.Status == models.JobFileStatusAvailable {
				// File found in S3, download it
				fmt.Printf("DEBUG GetCSVReader: Attempting S3 download from bucket=%s, key=%s\n",
					jobFile.BucketName, jobFile.ObjectKey)
				reader, err := s.s3Uploader.Download(ctx, jobFile.BucketName, jobFile.ObjectKey)
				if err == nil {
					fileName := id + ".csv"
					fmt.Printf("DEBUG GetCSVReader: S3 download successful for job %s\n", id)
					return reader, fileName, nil
				}
				// If S3 download fails, fall through to local filesystem
				fmt.Printf("WARNING: S3 download failed for job %s: %v, trying local filesystem\n", id, err)
			} else {
				fmt.Printf("DEBUG GetCSVReader: Job file status is %s (not available), trying local filesystem\n", jobFile.Status)
			}
		}
	} else {
		fmt.Printf("DEBUG GetCSVReader: S3 not configured, using local filesystem\n")
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

func (s *Service) Cancel(ctx context.Context, id string) error {
	fmt.Printf("DEBUG: Service.Cancel called for job %s\n", id)

	// Get current job status before cancellation
	job, err := s.repo.Get(ctx, id)
	if err != nil {
		fmt.Printf("DEBUG: Failed to get job %s before cancellation: %v\n", id, err)
		return err
	}

	fmt.Printf("DEBUG: Job %s current status before cancel: %s\n", id, job.Status)

	// Call repository Cancel method
	err = s.repo.Cancel(ctx, id)
	if err != nil {
		fmt.Printf("DEBUG: Failed to cancel job %s: %v\n", id, err)
		return err
	}

	fmt.Printf("DEBUG: Job %s cancel operation completed\n", id)

	// Verify the status was updated
	updatedJob, err := s.repo.Get(ctx, id)
	if err != nil {
		fmt.Printf("DEBUG: Failed to get job %s after cancellation: %v\n", id, err)
	} else {
		fmt.Printf("DEBUG: Job %s status after cancel: %s\n", id, updatedJob.Status)
	}

	return nil
}
