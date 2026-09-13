//nolint:testpackage // tests the private SQLite repository and schema directly
package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/web"
)

func newTestRepo(t *testing.T) *repo {
	t.Helper()

	db, err := initDatabase(":memory:")
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	return &repo{db: db}
}

func createTestJob(t *testing.T, repo *repo, id string, date time.Time) {
	t.Helper()

	job := web.Job{ID: id, Name: id, Date: date, Status: web.StatusPending}
	if err := repo.Create(context.Background(), &job); err != nil {
		t.Fatalf("Create(%q): %v", id, err)
	}
}

func TestSelectSupportsOffsetWithoutLimit(t *testing.T) {
	repo := newTestRepo(t)

	createTestJob(t, repo, "a", time.Unix(1, 0))
	createTestJob(t, repo, "b", time.Unix(2, 0))
	createTestJob(t, repo, "c", time.Unix(3, 0))

	jobs, err := repo.Select(context.Background(), web.SelectParams{Offset: 1})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	if len(jobs) != 2 || jobs[0].ID != "b" || jobs[1].ID != "a" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
}

func TestSelectOrdersSameSecondJobsByID(t *testing.T) {
	repo := newTestRepo(t)
	createdAt := time.Unix(1, 0)

	createTestJob(t, repo, "b", createdAt)
	createTestJob(t, repo, "c", createdAt)
	createTestJob(t, repo, "a", createdAt)

	jobs, err := repo.Select(context.Background(), web.SelectParams{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	want := []string{"c", "b", "a"}
	for i := range want {
		if jobs[i].ID != want[i] {
			t.Fatalf("job %d: got %q, want %q", i, jobs[i].ID, want[i])
		}
	}
}

func TestSchemaIndexesJobPaginationOrder(t *testing.T) {
	repo := newTestRepo(t)

	var statement string

	err := repo.db.QueryRowContext(
		context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_jobs_created_at_id'`,
	).Scan(&statement)
	if err != nil {
		t.Fatalf("query pagination index: %v", err)
	}

	want := strings.Fields("CREATE INDEX idx_jobs_created_at_id ON jobs (created_at DESC, id DESC)")
	if strings.Join(strings.Fields(statement), " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected index: %q", statement)
	}
}
