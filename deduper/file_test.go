package deduper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	placeA = "https://www.google.com/maps/place/A/data=!4m7!3m6!1s0x14b8cfc36e53c281:0xea50539229014980!8m2!3d37.8!4d28.1?hl=tr&g_ep=EgoyMDI2MTAxOC4w&rclk=1"
	placeB = "https://www.google.com/maps/place/B/data=!4m7!3m6!1s0x14b8ceb0ba2d213d:0xa88505df865f9e4c!8m2!3d37.8!4d28.1?hl=tr&g_ep=EgoyMDI2MTAxOC4w&rclk=1"
)

func openFile(t *testing.T, path string) *FileDeduper {
	t.Helper()

	d, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d
}

func TestJobDeduperSkipsCommittedPlacesAcrossJobsAndRestarts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seen.txt")

	shared := openFile(t, path)
	first := shared.NewJob()

	if !first.AddIfNotExists(ctx, placeA) {
		t.Fatal("A is new to the first job")
	}

	if first.AddIfNotExists(ctx, placeA) {
		t.Fatal("A repeats inside the first job")
	}

	if err := first.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	second := shared.NewJob()
	if second.AddIfNotExists(ctx, placeA) {
		t.Fatal("A was committed by the first job")
	}

	if !second.AddIfNotExists(ctx, placeB) {
		t.Fatal("B is new")
	}

	if err := second.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	reopened := openFile(t, path).NewJob()
	for _, key := range []string{placeA, placeB} {
		if reopened.AddIfNotExists(ctx, key) {
			t.Fatalf("%s was committed before the restart", key)
		}
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if got := strings.Fields(string(body)); len(got) != 2 {
		t.Fatalf("file keys = %q, want each place once", got)
	}
}

func TestJobDeduperWithoutCommitLeavesPlacesForTheRerun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seen.txt")

	shared := openFile(t, path)

	interrupted := shared.NewJob()
	if !interrupted.AddIfNotExists(ctx, placeA) {
		t.Fatal("A is new")
	}

	rerun := shared.NewJob()
	if !rerun.AddIfNotExists(ctx, placeA) {
		t.Fatal("an uncommitted job must not hide A from its rerun")
	}

	if !openFile(t, path).NewJob().AddIfNotExists(ctx, placeA) {
		t.Fatal("an uncommitted job must not write A to the file")
	}
}

func TestFileDeduperMatchesPlaceAcrossVolatileParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seed.txt")

	seed := "  " + strings.Replace(placeA, "MTAxOC4w", "MTAxOS4w", 1) + " \n\n0x14B8CEB0BA2D213D:0xA88505DF865F9E4C\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	job := openFile(t, path).NewJob()

	if job.AddIfNotExists(ctx, placeA) {
		t.Fatal("A is seeded under a different g_ep")
	}

	if job.AddIfNotExists(ctx, placeB) {
		t.Fatal("B is seeded as a bare data ID")
	}

	other := "https://www.google.com/maps/place/C/data=!4m7!3m6!1s0x14b8cffa6713322f:0xbd37d41b236dae30?hl=tr"
	if !job.AddIfNotExists(ctx, other) {
		t.Fatal("C is new")
	}
}
