package web

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Service struct {
	repo       JobRepository
	dataFolder string
}

func NewService(repo JobRepository, dataFolder string) *Service {
	return &Service{
		repo:       repo,
		dataFolder: dataFolder,
	}
}

func (s *Service) Create(ctx context.Context, job *Job) error {
	return s.repo.Create(ctx, job)
}

func (s *Service) All(ctx context.Context) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{})
}

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	datapath, err := s.csvPath(id)
	if err != nil {
		return err
	}

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
	return s.repo.Select(ctx, SelectParams{Status: StatusPending, Limit: 1, Oldest: true})
}

// PendingCount is how many jobs are queued to run, without decoding any of them.
func (s *Service) PendingCount(ctx context.Context) (int, error) {
	return s.repo.Count(ctx, SelectParams{Status: StatusPending})
}

// PendingPreview is up to n pending jobs, in the order they will run.
func (s *Service) PendingPreview(ctx context.Context, n int) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{Status: StatusPending, Limit: n, Oldest: true})
}

// RecentHistory is the last n jobs no longer pending (working, ok, or failed).
func (s *Service) RecentHistory(ctx context.Context, n int) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{ExcludeStatus: StatusPending, Limit: n})
}

// Working is the one job running now, or nil when none is.
func (s *Service) Working(ctx context.Context) (*Job, error) {
	jobs, err := s.repo.Select(ctx, SelectParams{Status: StatusWorking, Limit: 1})
	if err != nil || len(jobs) == 0 {
		return nil, err
	}

	return &jobs[0], nil
}

// csvPath returns the on-disk path of a job's CSV output, rejecting ids that
// could escape the data folder.
func (s *Service) csvPath(id string) (string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid file name")
	}

	return filepath.Join(s.dataFolder, id+".csv"), nil
}

func (s *Service) GetCSV(_ context.Context, id string) (string, error) {
	datapath, err := s.csvPath(id)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(datapath); os.IsNotExist(err) {
		return "", fmt.Errorf("csv file not found for job %s", id)
	}

	return datapath, nil
}

// CountRows is how many result rows a job's CSV holds so far: the live count for a running job,
// and the fallback for one whose leads are not in the lead list. 0 when there is no file yet.
func (s *Service) CountRows(id string) int {
	path, err := s.csvPath(id)
	if err != nil {
		return 0
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	return max(bytes.Count(b, []byte("\n"))-1, 0)
}
