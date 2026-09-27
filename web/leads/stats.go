package leads

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The Verify page's numbers: each run of each arm measured the same way, the arms summarised over
// their runs, and the verdict on whether v2 is more predictable than v1. Pure functions of what the
// ledger and the job list hold, so every number here is tested without a database.

// Verdict thresholds (see the plan's pass criteria).
const (
	// VerdictRuns is how many finished, fair runs each arm needs before a verdict.
	VerdictRuns = 3
	// MaxTailRatio is the most a search's p95 time may be of its p50.
	MaxTailRatio = 2.0
	// BoundSlack is how far past its logged bound a job may run and still count as inside it.
	BoundSlack = 1.10
	// MaxRunCV is the most run-to-run spread (coefficient of variation) of yield per search.
	MaxRunCV = 0.15
	// MinEmailShare is the least share of v1's unique emails v2 must reach.
	MinEmailShare = 0.70
)

// Job statuses as the job list writes them (the web package's constants; leads cannot import it).
const (
	jobStatusOK      = "ok"
	jobStatusPartial = "partial"
	jobStatusFailed  = "failed"
	// JobDeleted is a comparison job that is no longer in the job list (deleted, or dropped when
	// the comparison was stopped): it neither counts nor is waited for.
	JobDeleted = "deleted"
)

// ArmRun is everything known about one run of one arm.
type ArmRun struct {
	Arm          string
	RunNo        int
	JobID        string
	JobStatus    string // from the job list: pending, working, ok, partial, failed (or "" if deleted)
	Job          JobRun // from the ledger; zero until the job has started
	Searches     []RunRow
	UniqueEmails int
}

// Finished is whether the run's job is over.
func (r *ArmRun) Finished() bool {
	return r.JobStatus == jobStatusOK || r.JobStatus == jobStatusPartial || r.JobStatus == jobStatusFailed
}

// Fair is a finished run that counts: not interrupted by the pause window or AxioIntel's yield.
func (r *ArmRun) Fair() bool { return r.Finished() && !r.Job.Discarded }

// RunMetrics are one run's numbers.
type RunMetrics struct {
	Arm             string
	RunNo           int
	JobID           string
	Status          string
	Searches        int
	Places          int
	LeadsPerSearch  float64
	UniqueEmails    int
	EmailsPerSearch float64
	HitRate         float64 // searches' emails_found / email_attempted
	FetchFailShare  float64 // email_fetch_failed / email_attempted
	SearchP50       float64 // seconds; for v1, the job's seconds per search
	SearchP95       float64
	SearchMax       float64
	JobLevelTiming  bool
	WallSeconds     float64
	BoundSeconds    float64
	InsideBound     bool
	Blocks          int
	BlocksPerSearch float64
	Restarts        int
	Stalls          int
	Discarded       bool
	CPUP50, CPUP95  float64
	MemP50, MemP95  float64
}

// MeasureRun computes one run's numbers.
func MeasureRun(r *ArmRun) RunMetrics {
	m := RunMetrics{
		Arm: r.Arm, RunNo: r.RunNo, JobID: r.JobID, Status: r.JobStatus, Searches: len(r.Searches),
		UniqueEmails: r.UniqueEmails, WallSeconds: r.Job.Seconds(), BoundSeconds: r.Job.Bound.Seconds(),
		Blocks: r.Job.Blocks, Restarts: r.Job.Restarts, Stalls: r.Job.Stalls, Discarded: r.Job.Discarded,
		CPUP50: r.Job.CPUP50, CPUP95: r.Job.CPUP95, MemP50: r.Job.MemP50, MemP95: r.Job.MemP95,
	}

	var attempted, found, failed int

	secs := make([]float64, 0, len(r.Searches))

	for i := range r.Searches {
		s := &r.Searches[i]
		m.Places += s.PlacesFound
		attempted += s.EmailAttempted
		found += s.EmailsFound
		failed += s.EmailFetchFailed

		if s.JobLevel {
			m.JobLevelTiming = true
		} else {
			secs = append(secs, s.Seconds())
		}
	}

	if m.JobLevelTiming && m.Searches > 0 {
		per := m.WallSeconds / float64(m.Searches)
		secs = []float64{per}
	}

	m.SearchP50, m.SearchP95, m.SearchMax = Percentile(secs, 50), Percentile(secs, 95), maxOf(secs)
	m.LeadsPerSearch = ratio(float64(m.Places), float64(m.Searches))
	m.EmailsPerSearch = ratio(float64(m.UniqueEmails), float64(m.Searches))
	m.HitRate = ratio(float64(found), float64(attempted))
	m.FetchFailShare = ratio(float64(failed), float64(attempted))
	m.BlocksPerSearch = ratio(float64(m.Blocks), float64(m.Searches))
	m.InsideBound = m.BoundSeconds <= 0 || m.WallSeconds <= m.BoundSeconds*BoundSlack

	return m
}

// Stat is a metric over an arm's fair runs: its median and range.
type Stat struct {
	Median, Min, Max float64
	N                int
}

func statOf(vals []float64) Stat {
	if len(vals) == 0 {
		return Stat{}
	}

	return Stat{Median: Percentile(vals, 50), Min: minOf(vals), Max: maxOf(vals), N: len(vals)}
}

// ArmSummary is one arm over its fair runs.
type ArmSummary struct {
	Arm          string
	Runs         []RunMetrics // every run, fair or not, in run order
	FairRuns     int
	PendingRuns  int
	OK           int
	Partial      int
	Failed       int
	Leads        Stat // per search
	LeadsCV      float64
	Emails       Stat // unique emails per search
	EmailsCV     float64
	UniqueEmails Stat
	HitRate      Stat
	FetchFail    Stat
	SearchP50    Stat
	SearchP95    Stat
	SearchMax    Stat
	TailRatio    Stat // p95 / p50 per run
	JobLevel     bool
	Wall         Stat
	AllInBound   bool
	Blocks       Stat // per search
	Restarts     int
	Stalls       int
	CPUP95       Stat
	MemP95       Stat
}

// Summarize computes one arm's summary.
func Summarize(arm string, runs []ArmRun) ArmSummary {
	s := ArmSummary{Arm: arm, AllInBound: true}

	n := len(runs)
	leads, emails, unique := make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n)
	hit, fail, tail := make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n)
	p50, p95, pmax := make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n)
	wall, blocks, cpu, mem := make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n)

	for i := range runs {
		r := &runs[i]
		m := MeasureRun(r)
		s.Runs = append(s.Runs, m)

		if r.JobStatus == JobDeleted {
			continue
		}

		if !r.Finished() {
			s.PendingRuns++

			continue
		}

		if !r.Fair() {
			continue
		}

		s.FairRuns++

		switch m.Status {
		case jobStatusOK:
			s.OK++
		case jobStatusPartial:
			s.Partial++
		case jobStatusFailed:
			s.Failed++
		}

		s.Restarts += m.Restarts
		s.Stalls += m.Stalls
		s.JobLevel = s.JobLevel || m.JobLevelTiming
		s.AllInBound = s.AllInBound && m.InsideBound

		leads = append(leads, m.LeadsPerSearch)
		emails = append(emails, m.EmailsPerSearch)
		unique = append(unique, float64(m.UniqueEmails))
		hit = append(hit, m.HitRate)
		fail = append(fail, m.FetchFailShare)
		p50 = append(p50, m.SearchP50)
		p95 = append(p95, m.SearchP95)
		pmax = append(pmax, m.SearchMax)
		tail = append(tail, ratio(m.SearchP95, m.SearchP50))
		wall = append(wall, m.WallSeconds)
		blocks = append(blocks, m.BlocksPerSearch)
		cpu = append(cpu, m.CPUP95)
		mem = append(mem, m.MemP95)
	}

	s.Leads, s.LeadsCV = statOf(leads), CV(leads)
	s.Emails, s.EmailsCV = statOf(emails), CV(emails)
	s.UniqueEmails, s.HitRate, s.FetchFail = statOf(unique), statOf(hit), statOf(fail)
	s.SearchP50, s.SearchP95, s.SearchMax, s.TailRatio = statOf(p50), statOf(p95), statOf(pmax), statOf(tail)
	s.Wall, s.Blocks, s.CPUP95, s.MemP95 = statOf(wall), statOf(blocks), statOf(cpu), statOf(mem)

	return s
}

// Criterion is one line of the verdict.
type Criterion struct {
	Name     string
	Passed   bool
	Measured string
}

// Verdict is whether v2 is more predictable than v1.
type Verdict struct {
	// Outcome is "yes", "no" or "wait" (not enough fair runs yet).
	Outcome  string
	Headline string
	Criteria []Criterion
}

// Verdict outcomes.
const (
	VerdictYes  = "yes"
	VerdictNo   = "no"
	VerdictWait = "wait"
)

// Judge compares the v2 arm against the v1 arm on the five criteria. deep, when not nil, is a v2
// arm with more contact pages, looked at only when v2 falls short on emails alone.
func Judge(v1, v2, deep *ArmSummary) Verdict {
	crit := []Criterion{
		{
			Name:     fmt.Sprintf("v2 search time is steady: p95 ≤ %.0f× p50, and every job inside its bound", MaxTailRatio),
			Passed:   v2.FairRuns > 0 && v2.TailRatio.Max <= MaxTailRatio && v2.AllInBound,
			Measured: fmt.Sprintf("p95/p50 %.2f (worst %.2f); %s", v2.TailRatio.Median, v2.TailRatio.Max, boundText(v2.AllInBound)),
		},
		{
			Name:     "v2 ran clean: no partial or failed job, no stall, no restart",
			Passed:   v2.FairRuns > 0 && v2.Partial+v2.Failed+v2.Stalls+v2.Restarts == 0,
			Measured: fmt.Sprintf("%d partial, %d failed, %d stalls, %d restarts (v1: %d, %d, %d, %d)", v2.Partial, v2.Failed, v2.Stalls, v2.Restarts, v1.Partial, v1.Failed, v1.Stalls, v1.Restarts),
		},
		{
			Name: fmt.Sprintf("v2 yield is repeatable: run-to-run spread ≤ %.0f%% and below v1's", MaxRunCV*100),
			Passed: v2.FairRuns >= 2 && v1.FairRuns >= 2 && v2.LeadsCV <= MaxRunCV && v2.EmailsCV <= MaxRunCV &&
				v2.LeadsCV <= v1.LeadsCV && v2.EmailsCV <= v1.EmailsCV,
			Measured: fmt.Sprintf("leads %s, emails %s (v1: %s, %s)", pct(v2.LeadsCV), pct(v2.EmailsCV), pct(v1.LeadsCV), pct(v1.EmailsCV)),
		},
		{
			Name:     "v2 is refused no more often than v1",
			Passed:   v2.FairRuns > 0 && v1.FairRuns > 0 && v2.Blocks.Median <= v1.Blocks.Median,
			Measured: fmt.Sprintf("%.2f blocks per search (v1: %.2f)", v2.Blocks.Median, v1.Blocks.Median),
		},
		emailCriterion(v1, v2),
	}

	v := Verdict{Criteria: crit}

	if need := VerdictRuns - min(v1.FairRuns, v2.FairRuns); need > 0 {
		v.Outcome = VerdictWait
		v.Headline = fmt.Sprintf("Not yet: need %d more fair run%s of each arm", need, plural(need))

		return v
	}

	failed := []string{}

	for i, c := range crit {
		if !c.Passed {
			failed = append(failed, fmt.Sprintf("%d", i+1))
		}
	}

	switch {
	case len(failed) == 0:
		v.Outcome, v.Headline = VerdictYes, "Yes: v2 is more predictable than v1 on every criterion"
	case len(failed) == 1 && failed[0] == "5" && deep != nil && deep.FairRuns > 0 && emailCriterion(v1, deep).Passed:
		v.Outcome = VerdictYes
		v.Headline = fmt.Sprintf("Yes, with the %q settings: v2 with more contact pages reaches the email bar", deep.Arm)
	default:
		v.Outcome = VerdictNo
		v.Headline = "No: v2 falls short on criterion " + strings.Join(failed, ", ")
	}

	return v
}

func emailCriterion(v1, v2 *ArmSummary) Criterion {
	share := ratio(v2.UniqueEmails.Median, v1.UniqueEmails.Median)

	return Criterion{
		Name:     fmt.Sprintf("%s finds at least %.0f%% of v1's unique emails", v2.Arm, MinEmailShare*100),
		Passed:   v2.FairRuns > 0 && v1.FairRuns > 0 && v1.UniqueEmails.Median > 0 && share >= MinEmailShare,
		Measured: fmt.Sprintf("%.0f vs %.0f (%s)", v2.UniqueEmails.Median, v1.UniqueEmails.Median, pct(share)),
	}
}

func boundText(in bool) string {
	if in {
		return "every job inside its bound"
	}

	return "a job ran past its bound"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}

	return "s"
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

// Percentile is the p-th percentile of vals (linear between ranks); 0 for none.
func Percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}

	s := append([]float64(nil), vals...)
	sort.Float64s(s)

	rank := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))

	return s[lo] + (s[hi]-s[lo])*(rank-float64(lo))
}

// CV is the coefficient of variation (sample standard deviation / mean); 0 for fewer than two
// values or a zero mean.
func CV(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}

	var sum float64
	for _, v := range vals {
		sum += v
	}

	mean := sum / float64(len(vals))
	if mean == 0 {
		return 0
	}

	var sq float64
	for _, v := range vals {
		sq += (v - mean) * (v - mean)
	}

	return math.Sqrt(sq/float64(len(vals)-1)) / mean
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}

	return a / b
}

func minOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}

	m := vals[0]
	for _, v := range vals[1:] {
		m = math.Min(m, v)
	}

	return m
}

func maxOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}

	m := vals[0]
	for _, v := range vals[1:] {
		m = math.Max(m, v)
	}

	return m
}
