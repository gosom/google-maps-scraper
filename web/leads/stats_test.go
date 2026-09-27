//nolint:testpackage // tests the Verify page's numbers next to their unexported helpers
package leads

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

func TestPercentileAndCV(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50}
	assert.InDelta(t, 30, Percentile(vals, 50), 1e-9)
	assert.InDelta(t, 48, Percentile(vals, 95), 1e-9)
	assert.InDelta(t, 10, Percentile(vals, 0), 1e-9)
	assert.Zero(t, Percentile(nil, 50))

	assert.Zero(t, CV([]float64{5}), "one value has no spread")
	assert.Zero(t, CV([]float64{4, 4, 4}))
	assert.InDelta(t, 0.1, CV([]float64{90, 100, 110}), 1e-9)
}

// armRun is one fair, finished run: searches taking the given seconds, each with places and
// emails, inside a bound.
func armRun(arm string, runNo int, status string, secs []float64, places, uniqueEmails, blocks int) ArmRun {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r := ArmRun{Arm: arm, RunNo: runNo, JobID: arm + "-" + string(rune('0'+runNo)), JobStatus: status, UniqueEmails: uniqueEmails}

	var total float64

	for _, s := range secs {
		d := time.Duration(s * float64(time.Second))
		r.Searches = append(r.Searches, RunRow{SearchRun: gmaps.SearchRun{
			StartedAt: start, FinishedAt: start.Add(d), PlacesFound: places / len(secs),
			EmailAttempted: 10, EmailsFound: 4, EmailFetchFailed: 1,
		}})
		total += s
	}

	r.Job = JobRun{StartedAt: start, FinishedAt: start.Add(time.Duration(total) * time.Second),
		Bound: time.Duration(total*2) * time.Second, Blocks: blocks}

	return r
}

// v1Run is a v1 run: job-level timing, wall seconds over its searches.
func v1Run(runNo int, wall float64, places, uniqueEmails, blocks int) ArmRun {
	r := armRun("v1", runNo, "ok", []float64{1, 1, 1}, places, uniqueEmails, blocks)
	for i := range r.Searches {
		r.Searches[i].JobLevel = true
	}

	r.Job.FinishedAt = r.Job.StartedAt.Add(time.Duration(wall) * time.Second)
	r.Job.Bound = 2 * time.Duration(wall) * time.Second

	return r
}

func TestMeasureRunUsesEachSearchsTimeOrTheJobsForV1(t *testing.T) {
	m := MeasureRun(new(armRun("v2", 1, "ok", []float64{10, 20, 30}, 60, 12, 0)))
	assert.False(t, m.JobLevelTiming)
	assert.InDelta(t, 20, m.SearchP50, 1e-9)
	assert.InDelta(t, 30, m.SearchMax, 1e-9)
	assert.InDelta(t, 20, m.LeadsPerSearch, 1e-9)
	assert.InDelta(t, 4, m.EmailsPerSearch, 1e-9)
	assert.InDelta(t, 0.4, m.HitRate, 1e-9)
	assert.InDelta(t, 0.1, m.FetchFailShare, 1e-9)
	assert.True(t, m.InsideBound)

	v1 := MeasureRun(new(v1Run(1, 300, 60, 20, 3)))
	assert.True(t, v1.JobLevelTiming)
	assert.InDelta(t, 100, v1.SearchP50, 1e-9, "300 s over 3 searches")
	assert.InDelta(t, 1, v1.BlocksPerSearch, 1e-9)
}

func steadyArms() (v1, v2 []ArmRun) {
	for i := 1; i <= 3; i++ {
		v1 = append(v1, v1Run(i, []float64{300, 420, 250}[i-1], []int{60, 45, 70}[i-1], []int{30, 22, 35}[i-1], 2))
		v2 = append(v2, armRun("v2", i, "ok", []float64{40, 42, 45}, []int{57, 60, 60}[i-1], []int{24, 25, 25}[i-1], 0))
	}

	return v1, v2
}

func TestJudgeSaysYesWhenV2IsSteadierOnEveryCriterion(t *testing.T) {
	v1runs, v2runs := steadyArms()
	v1, v2 := Summarize("v1", v1runs), Summarize("v2", v2runs)

	v := Judge(&v1, &v2, nil)
	for _, c := range v.Criteria {
		assert.True(t, c.Passed, "%s: %s", c.Name, c.Measured)
	}

	assert.Equal(t, VerdictYes, v.Outcome, v.Headline)
}

func TestJudgeWaitsForEnoughFairRuns(t *testing.T) {
	v1runs, v2runs := steadyArms()
	v2runs[2].JobStatus = "pending"
	v2runs[1].Job.Discarded = true

	v1, v2 := Summarize("v1", v1runs), Summarize("v2", v2runs)
	assert.Equal(t, 1, v2.FairRuns)
	assert.Equal(t, 1, v2.PendingRuns)

	v := Judge(&v1, &v2, nil)
	assert.Equal(t, VerdictWait, v.Outcome)
	assert.Contains(t, v.Headline, "2 more fair runs")
}

func TestJudgeSaysNoAndNamesTheCriteria(t *testing.T) {
	v1runs, v2runs := steadyArms()
	v2runs[0].JobStatus = "partial"
	v2runs[1].Searches[2].FinishedAt = v2runs[1].Searches[2].StartedAt.Add(5 * time.Minute) // a long tail

	v1, v2 := Summarize("v1", v1runs), Summarize("v2", v2runs)
	v := Judge(&v1, &v2, nil)

	assert.Equal(t, VerdictNo, v.Outcome)
	assert.False(t, v.Criteria[0].Passed, "the tail")
	assert.False(t, v.Criteria[1].Passed, "a partial job")
	assert.True(t, strings.HasPrefix(v.Headline, "No: v2 falls short on criterion 1, 2"), v.Headline)
}

func TestJudgeFallsBackToTheDeepArmOnEmailsOnly(t *testing.T) {
	v1runs, v2runs := steadyArms()

	deepRuns := make([]ArmRun, 0, len(v2runs))

	for i := range v2runs {
		v2runs[i].UniqueEmails = 10 // well under 70% of v1's ~30

		d := v2runs[i]
		d.Arm, d.UniqueEmails = "v2-deep", 28
		deepRuns = append(deepRuns, d)
	}

	v1, v2, deep := Summarize("v1", v1runs), Summarize("v2", v2runs), Summarize("v2-deep", deepRuns)

	require.Equal(t, VerdictNo, Judge(&v1, &v2, nil).Outcome)

	v := Judge(&v1, &v2, &deep)
	assert.Equal(t, VerdictYes, v.Outcome)
	assert.Contains(t, v.Headline, "v2-deep")
}

func TestDeletedRunsAreNeitherCountedNorAwaited(t *testing.T) {
	_, v2runs := steadyArms()
	v2runs[2].JobStatus = JobDeleted

	s := Summarize("v2", v2runs)
	assert.Equal(t, 2, s.FairRuns)
	assert.Zero(t, s.PendingRuns)
}

func TestEvaluateSmokePassesAGoodV2Run(t *testing.T) {
	in := goodSmoke()

	checks, passed := EvaluateSmoke(&in)
	assert.True(t, passed, "%+v", checks)

	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
	}

	assert.Contains(t, names, "Websites were read one at a time, the gap apart")
}

func goodSmoke() SmokeInput {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	return SmokeInput{
		Collector: gmaps.CollectorV2, JobStatus: "ok", Wall: 90 * time.Second, Bound: 5 * time.Minute,
		Searches: []string{"q"},
		CSV: CSVFacts{Rows: 20, HasStatusColumn: true, Found: 5, Attempted: 15,
			Places: map[string]bool{"a": true, "b": true}},
		LeadsLinked: 2,
		Runs: []RunRow{{SearchRun: gmaps.SearchRun{Method: gmaps.SteadyMethod, StartedAt: start,
			FinishedAt: start.Add(80 * time.Second), MinSiteGap: 1100 * time.Millisecond}}},
		QueryBudget: 4 * time.Minute, Delay: time.Second,
	}
}

func TestEvaluateSmokeFailsEachBrokenPromise(t *testing.T) {
	for name, breakIt := range map[string]func(*SmokeInput){
		"Job finished ok":                                         func(in *SmokeInput) { in.JobStatus = "partial" },
		"Finished inside its time bound":                          func(in *SmokeInput) { in.Wall = 6 * time.Minute },
		"Wrote at least one row":                                  func(in *SmokeInput) { in.CSV.Rows = 0 },
		"Every row names its place (title, and place_id or link)": func(in *SmokeInput) { in.CSV.MissingPlace = 1 },
		"Every row carries its search in input_id":                func(in *SmokeInput) { in.CSV.WrongSearch = 3 },
		"Every place reached the lead list":                       func(in *SmokeInput) { in.LeadsLinked = 0 },
		"Every row says what its email search found (email_status)": func(in *SmokeInput) {
			in.CSV.HasStatusColumn = false
		},
		"One ledger row per search": func(in *SmokeInput) { in.Runs[0].Method = V1Method },
		"Every search finished inside its time limit": func(in *SmokeInput) {
			in.Runs[0].FinishedAt = in.Runs[0].StartedAt.Add(5 * time.Minute)
		},
		"Websites were read one at a time, the gap apart": func(in *SmokeInput) { in.Runs[0].MinSiteGap = 200 * time.Millisecond },
	} {
		in := goodSmoke()
		in.Runs = append([]RunRow(nil), in.Runs...)
		breakIt(&in)

		checks, passed := EvaluateSmoke(&in)
		assert.False(t, passed, name)

		for _, c := range checks {
			if c.Name == name {
				assert.Equal(t, CheckFail, c.Result, name)
			}
		}
	}
}

func TestEvaluateSmokeOnlyWarnsAboutNoEmailsOrRefusals(t *testing.T) {
	in := goodSmoke()
	in.CSV.Found = 0
	in.Blocks = 2

	checks, passed := EvaluateSmoke(&in)
	assert.True(t, passed)

	warned := 0

	for _, c := range checks {
		if c.Result == CheckWarn {
			warned++
		}
	}

	assert.Equal(t, 2, warned)
}

func TestReadCSVFactsFromAJobCSV(t *testing.T) {
	_, dir := newStore(t)
	path := writeFile(t, dir, "s.csv", statusCSV+strings.Join([]string{
		statusRow("q", "A", "P1", "https://a.com", "x@a.com", gmaps.EmailFound),
		statusRow("q", "B", "P2", "https://b.com", "", gmaps.EmailFetchFailed+"http_4xx"),
		statusRow("other", "", "P3", "", "", ""),
	}, "\n")+"\n")

	f, err := ReadCSVFacts(path, []string{"q"})
	require.NoError(t, err)
	assert.Equal(t, 3, f.Rows)
	assert.Equal(t, 1, f.MissingPlace)
	assert.Equal(t, 1, f.WrongSearch)
	assert.Equal(t, 1, f.MissingStatus)
	assert.Equal(t, 1, f.Found)
	assert.Equal(t, 1, f.FetchFailed)
	assert.Len(t, f.Places, 2)

	missing, err := ReadCSVFacts(dir+"/none.csv", nil)
	require.NoError(t, err)
	assert.Zero(t, missing.Rows)
}
