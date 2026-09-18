package web

import (
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

type jobCounter interface {
	Count(context.Context, SelectParams) (int, error)
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

// ListJobs returns a page of jobs, clamping invalid page and limit values.
func (s *Service) ListJobs(ctx context.Context, page, limit int) (JobPage, error) {
	var ans JobPage

	if page < 1 {
		page = 1
	}

	if limit < 1 {
		limit = 20
	}

	total, err := s.countJobs(ctx, SelectParams{})
	if err != nil {
		return ans, err
	}

	totalPages := (total + limit - 1) / limit
	if totalPages == 0 {
		totalPages = 1
	}

	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * limit
	params := SelectParams{
		Limit:  limit,
		Offset: offset,
	}

	jobs, err := s.repo.Select(ctx, params)
	if err != nil {
		return ans, err
	}

	ans = JobPage{
		Jobs:        jobs,
		CurrentPage: page,
		TotalPages:  totalPages,
		Total:       total,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		HasPages:    totalPages > 1,
	}

	return ans, nil
}

func (s *Service) countJobs(ctx context.Context, params SelectParams) (int, error) {
	if counter, ok := s.repo.(jobCounter); ok {
		return counter.Count(ctx, params)
	}

	jobs, err := s.repo.Select(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("select jobs for count: %w", err)
	}

	return len(jobs), nil
}

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid file name")
	}

	for i := range resultFormats {
		datapath := filepath.Join(s.dataFolder, id+resultFormats[i].filesuffix)
		if err := os.Remove(datapath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete result file %s: %w", datapath, err)
		}
	}

	return s.repo.Delete(ctx, id)
}

func (s *Service) Update(ctx context.Context, job *Job) error {
	return s.repo.Update(ctx, job)
}

func (s *Service) SelectPending(ctx context.Context) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{Status: StatusPending, Limit: 1})
}

// resultFormat pairs a file suffix with the HTTP content type used to download it.
type resultFormat struct {
	filesuffix string
	contentTyp string
}

var resultFormats = []resultFormat{
	{filesuffix: ".csv", contentTyp: "text/csv"},
	{filesuffix: ".json", contentTyp: "application/json"},
}

// GetResult returns the on-disk path and content type of a job's result file,
// looking for every supported format so that older CSV jobs keep downloading.
func (s *Service) GetResult(_ context.Context, id string) (string, string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", "", fmt.Errorf("invalid file name")
	}

	for i := range resultFormats {
		datapath := filepath.Join(s.dataFolder, id+resultFormats[i].filesuffix)
		if _, err := os.Stat(datapath); err == nil {
			return datapath, resultFormats[i].contentTyp, nil
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
	}

	return "", "", fmt.Errorf("result file not found for job %s", id)
}
