//nolint:testpackage // tests the run ledger with the store's unexported helpers
package leads

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

// statusCSV is a job CSV with the email_status column both collectors now write.
const statusCSV = "input_id,link,title,website,place_id,emails,email_status\n"

func statusRow(search, title, placeID, website, emails, status string) string {
	return strings.Join([]string{search, "https://maps.google.com/?cid=" + placeID, title, website, placeID,
		`"` + emails + `"`, status}, ",")
}

func TestV1SearchesAreCountedFromTheCSV(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()

	path := writeFile(t, dir, "j1.csv", statusCSV+strings.Join([]string{
		statusRow("dentist in Austin TX", "A", "P1", "https://a.com", "x@a.com, y@a.com", gmaps.EmailFound),
		statusRow("dentist in Austin TX", "B", "P2", "https://b.com", "", gmaps.EmailNone),
		statusRow("dentist in Austin TX", "C", "P3", "https://c.com", "", gmaps.EmailFetchFailed+"timeout"),
		statusRow("dentist in Austin TX", "D", "P4", "", "", gmaps.EmailNoWebsite),
		statusRow("dentist in Dallas TX", "E", "P5", "https://e.com", "x@a.com", gmaps.EmailFound),
	}, "\n")+"\n")

	started := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	finished := started.Add(10 * time.Minute)
	searches := []string{"dentist in Austin TX", "dentist in Dallas TX", "dentist in Houston TX"}

	require.NoError(t, s.RecordRunsFromCSV(ctx, "j1", path, searches, started, finished))
	// Recording again (a rerun) replaces, never doubles.
	require.NoError(t, s.RecordRunsFromCSV(ctx, "j1", path, searches, started, finished))

	runs, err := s.Runs(ctx, "j1")
	require.NoError(t, err)
	require.Len(t, runs, 3, "one row per search, the empty one too")

	austin := runs[0]
	assert.Equal(t, V1Method, austin.Method)
	assert.True(t, austin.JobLevel)
	assert.Equal(t, gmaps.SearchOK, austin.Status)
	assert.Equal(t, 4, austin.PlacesFound)
	assert.Equal(t, 3, austin.Websites)
	assert.Equal(t, 3, austin.EmailAttempted)
	assert.Equal(t, 1, austin.EmailsFound)
	assert.Equal(t, 1, austin.EmailNone)
	assert.Equal(t, 1, austin.EmailFetchFailed)
	assert.Equal(t, 2, austin.UniqueEmails)
	assert.Equal(t, 600.0, austin.Seconds())

	assert.Equal(t, gmaps.SearchEmpty, runs[2].Status)
	assert.Equal(t, 0, runs[2].PlacesFound)
}

func TestAnOldCSVWithoutEmailStatusStillCounts(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()

	path := writeFile(t, dir, "old.csv", mapsCSV(
		row("dentist in Austin TX", "Smile Dental", "P1", "", "info@smile.com", "4.6", "952", "Austin"),
		row("dentist in Austin TX", "Tooth Co", "P2", "", "", "3.9", "12", "Austin"),
	))

	require.NoError(t, s.RecordRunsFromCSV(ctx, "old", path, []string{"dentist in Austin TX"}, time.Now(), time.Now()))

	runs, err := s.Runs(ctx, "old")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, 2, runs[0].Websites)
	assert.Equal(t, 1, runs[0].EmailsFound)
	assert.Equal(t, 1, runs[0].EmailNone)
}

func TestV2SearchRunsAndTheJobRunRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	require.NoError(t, s.StartJobRun(ctx, "j2", gmaps.CollectorV2, 2, 9*time.Minute, at))

	in := gmaps.SearchRun{
		Search: "q", Method: gmaps.SteadyMethod, StartedAt: at, FinishedAt: at.Add(95 * time.Second), Pages: 2,
		PlacesFound: 38, Duplicates: 2, Websites: 30, EmailAttempted: 29, EmailsFound: 12, EmailNone: 15,
		EmailFetchFailed: 2, EmailSkipped: 1, UniqueEmails: 14, Blocks: 1, MinSiteGap: 1200 * time.Millisecond,
		Status: gmaps.SearchPartial, Error: "late",
	}
	require.NoError(t, s.RecordSearchRun(ctx, "j2", &in))

	runs, err := s.Runs(ctx, "j2")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.False(t, runs[0].JobLevel)
	assert.Equal(t, in, runs[0].SearchRun)

	require.NoError(t, s.BumpJobRun(ctx, "j2", CounterStalls))
	require.NoError(t, s.BumpJobRun(ctx, "j2", CounterRestarts))
	require.NoError(t, s.BumpJobRun(ctx, "j2", CounterDiscarded))
	require.Error(t, s.BumpJobRun(ctx, "j2", "nonsense"))

	require.NoError(t, s.FinishJobRun(ctx, &JobRun{JobID: "j2", FinishedAt: at.Add(8 * time.Minute), Status: "ok",
		Blocks: 3, CPUP50: 20, CPUP95: 55, MemP50: 40, MemP95: 61}))

	jr, err := s.JobRuns(ctx, "j2", "missing")
	require.NoError(t, err)
	require.Len(t, jr, 1)

	got := jr["j2"]
	assert.Equal(t, gmaps.CollectorV2, got.Collector)
	assert.Equal(t, 480.0, got.Seconds())
	assert.Equal(t, 9*time.Minute, got.Bound)
	assert.Equal(t, 3, got.Blocks)
	assert.Equal(t, 1, got.Stalls)
	assert.Equal(t, 1, got.Restarts)
	assert.True(t, got.Discarded)
	assert.Equal(t, 55.0, got.CPUP95)

	// A new attempt starts clean: its search rows go, its counters stay.
	require.NoError(t, s.StartJobRun(ctx, "j2", gmaps.CollectorV2, 2, 9*time.Minute, at.Add(time.Hour)))

	runs, err = s.Runs(ctx, "j2")
	require.NoError(t, err)
	assert.Empty(t, runs)

	jr, err = s.JobRuns(ctx, "j2")
	require.NoError(t, err)
	assert.Equal(t, 1, jr["j2"].Restarts)

	require.Error(t, s.FinishJobRun(ctx, &JobRun{JobID: "never-started"}))
}

func TestComparisonsSmokeChecksAndSettingsRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	c := Comparison{ID: "c1", Name: "first", CreatedAt: at, Queries: []string{"a", "b"}, Arms: []byte(`[{"name":"v1"}]`),
		RunsPerArm: 2, Jobs: []ComparisonJob{{Seq: 0, Arm: "v1", RunNo: 1, JobID: "j1"}, {Seq: 1, Arm: "v2", RunNo: 1, JobID: "j2"}}}
	require.NoError(t, s.CreateComparison(ctx, &c))

	got, err := s.Comparison(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got.Queries)
	assert.Equal(t, c.Jobs, got.Jobs)
	assert.JSONEq(t, `[{"name":"v1"}]`, string(got.Arms))

	require.NoError(t, s.StopComparison(ctx, "c1"))

	list, err := s.Comparisons(ctx, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Stopped)

	_, err = s.Comparison(ctx, "nope")
	require.ErrorIs(t, err, ErrNoComparison)

	for i, passed := range []bool{false, true} {
		require.NoError(t, s.RecordSmokeCheck(ctx, &SmokeCheck{Collector: gmaps.CollectorV2, JobID: "s", At: at.Add(time.Duration(i) * time.Minute),
			Passed: passed, Checks: []Check{{Name: "x", Result: CheckPass}}}))
	}

	last, err := s.LastSmokeChecks(ctx)
	require.NoError(t, err)
	assert.True(t, last[gmaps.CollectorV2].Passed, "the latest check wins")
	assert.Len(t, last[gmaps.CollectorV2].Checks, 1)

	v, err := s.Setting(ctx, "queries")
	require.NoError(t, err)
	assert.Empty(t, v)
	require.NoError(t, s.SetSetting(ctx, "queries", "a\nb"))
	require.NoError(t, s.SetSetting(ctx, "queries", "c"))
	v, err = s.Setting(ctx, "queries")
	require.NoError(t, err)
	assert.Equal(t, "c", v)
}
