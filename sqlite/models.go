package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/entities"
	"github.com/gosom/google-maps-scraper/gmaps"
)

type job struct {
	ID         string     `gorm:"column:id;primaryKey"`
	LangCode   string     `gorm:"column:lang_code;not null"`
	MaxDepth   int        `gorm:"column:max_depth;not null"`
	Debug      bool       `gorm:"column:debug;not null"`
	Queries    queries    `gorm:"column:queries;type:blob;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	Status     string     `gorm:"column:status;not null"`

	Results []jobResult `gorm:"foreignKey:JobID;references:ID"`
}

func (j *job) toEntitiesJob() entities.Job {
	return entities.Job{
		ID:         j.ID,
		LangCode:   j.LangCode,
		MaxDepth:   j.MaxDepth,
		Debug:      j.Debug,
		Queries:    j.Queries,
		CreatedAt:  j.CreatedAt,
		FinishedAt: j.FinishedAt,
		Status:     j.Status,
	}
}

func jobFromEntitiesJob(j *entities.Job) job {
	return job{
		ID:         j.ID,
		LangCode:   j.LangCode,
		MaxDepth:   j.MaxDepth,
		Debug:      j.Debug,
		Queries:    queries(j.Queries),
		CreatedAt:  j.CreatedAt,
		FinishedAt: j.FinishedAt,
		Status:     j.Status,
	}
}

type jobResult struct {
	ID    int        `gorm:"column:_id;primaryKey"`
	JobID string     `gorm:"column:job_id;not null"`
	Data  placeEntry `gorm:"column:data;type:blob;not null"`
}

func (j *jobResult) toEntitiesJobResult() entities.JobResult {
	return entities.JobResult{
		ID:    j.ID,
		JobID: j.JobID,
		Data:  gmaps.Entry(j.Data),
	}
}

type placeEntry gmaps.Entry

func (p *placeEntry) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSON value:", value))
	}

	var result gmaps.Entry
	err := json.Unmarshal(bytes, &result)
	if err != nil {
		return err
	}

	*p = placeEntry(result)

	return nil
}

func (p placeEntry) Value() (driver.Value, error) {
	return json.Marshal(p)
}

type queries []string

func (q *queries) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSON value:", value))
	}

	result := []string{}
	err := json.Unmarshal(bytes, &result)
	*q = queries(result)

	return err
}

// Value return json value, implement driver.Valuer interface
func (q queries) Value() (driver.Value, error) {
	if len(q) == 0 {
		return nil, nil
	}

	return json.Marshal(q)
}

type jobCount struct {
	JobID string `gorm:"column:job_id"`
	Count int `gorm:"column:count"`
}
