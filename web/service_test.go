package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
)

// fakeJobRepo is a minimal stand-in for models.JobRepository used to
// exercise Service.Delete in isolation. Only the three methods Delete
// actually calls (Get, Cancel, Delete) are implemented; the embedded
// nil interface makes any unexpected method call panic, which is the
// behavior we want — a test that exercises an unexpected code path
// should fail loudly rather than silently no-op.
type fakeJobRepo struct {
	models.JobRepository

	getFn    func(ctx context.Context, id, userID string) (models.Job, error)
	cancelFn func(ctx context.Context, id, userID string) error
	deleteFn func(ctx context.Context, id, userID string) error

	cancelCalls []string // userIDs Cancel was called with
	deleteCalls []string // userIDs Delete was called with
}

func (f *fakeJobRepo) Get(ctx context.Context, id, userID string) (models.Job, error) {
	return f.getFn(ctx, id, userID)
}

func (f *fakeJobRepo) Cancel(ctx context.Context, id, userID string) error {
	f.cancelCalls = append(f.cancelCalls, userID)
	if f.cancelFn != nil {
		return f.cancelFn(ctx, id, userID)
	}
	return nil
}

func (f *fakeJobRepo) Delete(ctx context.Context, id, userID string) error {
	f.deleteCalls = append(f.deleteCalls, userID)
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id, userID)
	}
	return nil
}

// TestService_Delete_RejectsCrossTenantAccess is a regression test for
// the IDOR fix in Task 4.2. Before the fix, Service.Delete called
// repo.Get and repo.Cancel with userID="" (the repo's admin-bypass
// sentinel), which let an attacker who knew a victim's job UUID read
// the job, cancel it if running, and remove the victim's CSV from
// dataFolder. Only the final repo.Delete was ownership-scoped, by
// which point the destructive side effects had already executed.
//
// This test asserts that a cross-tenant DELETE:
//  1. Returns the not-found error from the scoped Get,
//  2. Never invokes Cancel (no cross-tenant cancellation),
//  3. Never invokes the final Delete (no extra repo work),
//  4. Leaves the victim's CSV file on disk.
func TestService_Delete_RejectsCrossTenantAccess(t *testing.T) {
	ownerID := "user-owner"
	attackerID := "user-attacker"
	jobID := uuid.Must(uuid.NewV7()).String()

	// Seed a CSV file in a temp dataFolder so we can prove the
	// attacker's request did NOT remove it.
	dataFolder := t.TempDir()
	csvPath := filepath.Join(dataFolder, jobID+".csv")
	if err := os.WriteFile(csvPath, []byte("victim,data\n"), 0o600); err != nil {
		t.Fatalf("seed csv: %v", err)
	}

	notFoundErr := errors.New("job not found")
	repo := &fakeJobRepo{
		// Mirror the repo's real behavior: returning the job only
		// when the userID matches, ErrNoRows-equivalent otherwise.
		getFn: func(_ context.Context, id, userID string) (models.Job, error) {
			if id == jobID && userID == ownerID {
				return models.Job{
					ID:     jobID,
					UserID: ownerID,
					Status: models.StatusRunning, // ensures Cancel WOULD fire if not gated
				}, nil
			}
			return models.Job{}, notFoundErr
		},
	}
	svc := NewService(repo, dataFolder)

	// Cross-tenant DELETE attempt.
	err := svc.Delete(context.Background(), jobID, attackerID)

	if !errors.Is(err, notFoundErr) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	if len(repo.cancelCalls) != 0 {
		t.Errorf("Cancel must not be invoked on cross-tenant DELETE, got calls: %v", repo.cancelCalls)
	}
	if len(repo.deleteCalls) != 0 {
		t.Errorf("repo.Delete must not be invoked when ownership check fails, got calls: %v", repo.deleteCalls)
	}
	if _, err := os.Stat(csvPath); err != nil {
		t.Errorf("victim CSV must remain on disk after cross-tenant DELETE, stat err: %v", err)
	}
}

// TestService_Delete_OwnerHappyPath asserts the owner can still delete
// their own running job: Get + Cancel + repo.Delete are all called
// with the owner's userID, and the CSV file is removed.
func TestService_Delete_OwnerHappyPath(t *testing.T) {
	ownerID := "user-owner"
	jobID := uuid.Must(uuid.NewV7()).String()

	dataFolder := t.TempDir()
	csvPath := filepath.Join(dataFolder, jobID+".csv")
	if err := os.WriteFile(csvPath, []byte("owner,data\n"), 0o600); err != nil {
		t.Fatalf("seed csv: %v", err)
	}

	repo := &fakeJobRepo{
		getFn: func(_ context.Context, id, userID string) (models.Job, error) {
			if id == jobID && userID == ownerID {
				return models.Job{ID: jobID, UserID: ownerID, Status: models.StatusRunning}, nil
			}
			return models.Job{}, errors.New("not found")
		},
	}
	svc := NewService(repo, dataFolder)

	if err := svc.Delete(context.Background(), jobID, ownerID); err != nil {
		t.Fatalf("owner delete failed: %v", err)
	}
	if got := repo.cancelCalls; len(got) != 1 || got[0] != ownerID {
		t.Errorf("expected exactly one Cancel(ownerID), got %v", got)
	}
	if got := repo.deleteCalls; len(got) != 1 || got[0] != ownerID {
		t.Errorf("expected exactly one repo.Delete(ownerID), got %v", got)
	}
	if _, err := os.Stat(csvPath); !os.IsNotExist(err) {
		t.Errorf("expected CSV to be removed, stat err: %v", err)
	}
}
