package sqlite

import (
	"context"
	"errors"
	"time"

	driver "github.com/glebarez/sqlite"
	"github.com/gosom/google-maps-scraper/entities"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"gorm.io/gorm"
)

var (
	_ entities.JobStore       = (*Store)(nil)
	_ scrapemate.ResultWriter = (*Store)(nil)
)

type Store struct {
	db *gorm.DB
}

func New(path string) (*Store, error) {
	db, err := gorm.Open(driver.Open(path), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	ans := Store{
		db: db,
	}

	return &ans, nil
}

func (s *Store) AutoMigrate(_ context.Context) error {
	models := []any{
		&job{},
		&jobResult{},
	}

	return s.db.AutoMigrate(models...)
}

func (s *Store) CreateJob(ctx context.Context, job *entities.Job) error {
	dbo := jobFromEntitiesJob(job)
	job.Status = entities.JobStatusCreated

	if err := s.db.WithContext(ctx).Create(&dbo).Error; err != nil {
	}

	return nil
}

func (s *Store) CleanUpIncompleteJobs(ctx context.Context) error {
	return s.db.WithContext(ctx).Model(&job{}).
		Where("status = ?", entities.JobStatusRunning).
		Update("status", entities.JobStatusStopped).
		Error
}

func (s *Store) SelectAllJobs(ctx context.Context) ([]entities.Job, error) {
	var dbos []job

	db := s.db.WithContext(ctx)
	db = db.Order("created_at DESC")

	if err := db.Find(&dbos).Error; err != nil {
		return nil, err
	}

	ans := make([]entities.Job, len(dbos))
	for i, dbo := range dbos {
		ans[i] = dbo.toEntitiesJob()
	}

	return ans, nil
}

func (s *Store) GetResultCount(ctx context.Context) (map[string]int, error) {
	var results []jobCount

	err := s.db.WithContext(ctx).Model(&jobResult{}).
		Select("job_id, COUNT(1) as count").
		Group("job_id").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	ans := make(map[string]int, len(results))
	for _, result := range results {
		ans[result.JobID] = result.Count
	}

	return ans, nil
}

func (s *Store) SetJobStatus(ctx context.Context, id string, status string) error {
	var dbo job

	if err := s.db.WithContext(ctx).First(&dbo, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return entities.ErrJobNotFound
		}
	}

	dbo.Status = status
	if dbo.Status == entities.JobStatusDone || dbo.Status == entities.JobStatusFailed {
		now := time.Now().UTC()
		dbo.FinishedAt = &now
	}

	if err := s.db.WithContext(ctx).Save(&dbo).Error; err != nil {
		return err
	}

	return nil
}

func (s *Store) GetJobResult(ctx context.Context, id string) ([]entities.JobResult, error) {
	var dbos []jobResult

	if err := s.db.WithContext(ctx).Find(&dbos, "job_id = ?", id).Error; err != nil {
		return nil, err
	}

	ans := make([]entities.JobResult, len(dbos))
	for i, dbo := range dbos {
		ans[i] = dbo.toEntitiesJobResult()
	}

	return ans, nil
}

func (s *Store) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entry, ok := result.Data.(*gmaps.Entry)
		if !ok {
			return errors.New("invalid data type")
		}

		if err := s.saveEntry(ctx, result.Job.GetParentID(), entry); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) saveEntry(ctx context.Context, id string, entry *gmaps.Entry) error {
	dbo := jobResult{
		JobID: id,
		Data:  placeEntry(*entry),
	}

	if err := s.db.WithContext(ctx).Create(&dbo).Error; err != nil {
		return err
	}

	return nil
}
