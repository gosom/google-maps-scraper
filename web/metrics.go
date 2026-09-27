package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/web/leads"
)

// The metrics strip at the top of every page: how hard the machine is working, and how fast the
// running job is pulling. Sampled every few seconds in the background, so a page load reads the
// last sample instead of measuring.

const (
	metricsEvery = 5 * time.Second
	// rateWindow is how far back the pull rate is averaged.
	rateWindow = 5 * time.Minute
	// SlowAfter and StallAfter grade a running job that has written no new listing for a while.
	// The job runner acts on StallAfter (see runner/webrunner); SlowAfter is only shown.
	SlowAfter  = 4 * time.Minute
	StallAfter = 10 * time.Minute
)

// hostSample is what the operating system reports; zero where it cannot (hostStats is a no-op
// outside Linux).
type hostSample struct {
	cpuBusy, cpuTotal   uint64
	memTotal, memAvail  uint64 // bytes
	diskTotal, diskFree uint64 // bytes, of the data folder's filesystem
	netIn, netOut       uint64 // bytes since boot, all interfaces but loopback
}

// JobProgress is the running job as the strip shows it.
type JobProgress struct {
	ID         string
	Name       string
	Collector  string // v1 or v2
	Searches   int
	Rows       int
	Expected   int     // rough: searches x results a search at its depth
	Percent    int     // Rows of Expected, capped at 99 while running
	RatePerMin float64 // listings a minute over rateWindow
	LastRowAgo time.Duration
	ETA        time.Duration
	State      string // running, slow, stalled
}

// Metrics is one sample.
type Metrics struct {
	CPU, Mem, Disk      float64 // percent
	MemUsedGB, MemTotGB float64
	NetInKBs, NetOutKBs float64
	Job                 *JobProgress
	Blocks              gmaps.BlockStats
	Pending             int
	Notes               []string
	Paused              string
	At                  time.Time
	Supported           bool
}

type rowSample struct {
	at   time.Time
	rows int
}

type metricsState struct {
	mu       sync.Mutex
	last     Metrics
	prevHost hostSample
	prevAt   time.Time
	jobID    string
	history  []rowSample
	lastRow  time.Time
	notes    []string
	paused   string
	// jobRes is the machine's CPU and RAM at each sample while a job ran, by job id, for the
	// run ledger (TakeJobResources).
	jobRes map[string][]resSample
}

type resSample struct{ cpu, mem float64 }

// maxResSamples bounds one job's samples (every 5 s: about 11 hours).
const maxResSamples = 8000

// TakeJobResources is the machine's CPU and RAM use while a job ran, as p50 and p95 percentages,
// and forgets them.
func (s *Server) TakeJobResources(jobID string) (cpuP50, cpuP95, memP50, memP95 float64) {
	s.metrics.mu.Lock()
	samples := s.metrics.jobRes[jobID]
	delete(s.metrics.jobRes, jobID)
	s.metrics.mu.Unlock()

	cpu := make([]float64, 0, len(samples))
	mem := make([]float64, 0, len(samples))

	for _, r := range samples {
		cpu = append(cpu, r.cpu)
		mem = append(mem, r.mem)
	}

	return leads.Percentile(cpu, 50), leads.Percentile(cpu, 95), leads.Percentile(mem, 50), leads.Percentile(mem, 95)
}

// SetPaused shows (or, with "", clears) the lead jobs' pause on the strip.
func (s *Server) SetPaused(msg string) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()

	s.metrics.paused = msg
}

// Note records something the job runner did (a stall recovered, say); the strip shows the last few.
func (s *Server) Note(msg string) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()

	s.metrics.notes = append([]string{time.Now().UTC().Format("15:04") + " " + msg}, s.metrics.notes...)
	if len(s.metrics.notes) > 3 {
		s.metrics.notes = s.metrics.notes[:3]
	}
}

func (s *Server) sampleMetricsLoop(ctx context.Context) {
	t := time.NewTicker(metricsEvery)
	defer t.Stop()

	s.sampleMetrics(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sampleMetrics(ctx)
		}
	}
}

func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}

	return 100 * part / whole
}

func (s *Server) sampleMetrics(ctx context.Context) {
	now := time.Now()
	h, ok := hostStats(s.svc.dataFolder)

	m := Metrics{At: now.UTC(), Supported: ok, Blocks: gmaps.Blocks.Stats()}

	st := &s.metrics
	st.mu.Lock()
	prev, prevAt := st.prevHost, st.prevAt
	st.prevHost, st.prevAt = h, now
	st.mu.Unlock()

	if ok {
		if dt := h.cpuTotal - prev.cpuTotal; prev.cpuTotal > 0 && dt > 0 {
			m.CPU = pct(float64(h.cpuBusy-prev.cpuBusy), float64(dt))
		}

		m.Mem = pct(float64(h.memTotal-h.memAvail), float64(h.memTotal))
		m.MemUsedGB = float64(h.memTotal-h.memAvail) / (1 << 30)
		m.MemTotGB = float64(h.memTotal) / (1 << 30)
		m.Disk = pct(float64(h.diskTotal-h.diskFree), float64(h.diskTotal))

		if secs := now.Sub(prevAt).Seconds(); !prevAt.IsZero() && secs > 0 && h.netIn >= prev.netIn && h.netOut >= prev.netOut {
			m.NetInKBs = float64(h.netIn-prev.netIn) / 1024 / secs
			m.NetOutKBs = float64(h.netOut-prev.netOut) / 1024 / secs
		}
	}

	// Two cheap, indexed lookups instead of decoding every job: a campaign can queue tens of
	// thousands of pending jobs, and this runs every few seconds.
	if pending, err := s.svc.PendingCount(ctx); err == nil {
		m.Pending = pending
	}

	if running, err := s.svc.Working(ctx); err == nil {
		if running != nil {
			m.Job = s.jobProgress(running, now)
		} else {
			st.mu.Lock()
			st.jobID, st.history = "", nil
			st.mu.Unlock()
		}
	}

	st.mu.Lock()
	m.Notes = append([]string(nil), st.notes...)
	m.Paused = st.paused
	st.last = m

	if m.Job != nil && ok {
		if st.jobRes == nil {
			st.jobRes = map[string][]resSample{}
		}

		if len(st.jobRes[m.Job.ID]) < maxResSamples {
			st.jobRes[m.Job.ID] = append(st.jobRes[m.Job.ID], resSample{cpu: m.CPU, mem: m.Mem})
		}
	}
	st.mu.Unlock()
}

// resultsPerSearch is roughly how many listings one search yields at a depth (Google caps ~120).
// expectedRows is a rough size for a job: searches x results a search gives at its depth, or for
// a map grid, squares x the few new listings a square adds once its neighbours overlap.
func expectedRows(job *Job) int {
	if job.Data.IsV2() {
		return len(job.Data.Keywords) * 20 * job.Data.SteadyConfig().Pages
	}

	if g := job.Data.Grid; g != nil {
		return len(job.Data.Keywords) * g.Cells * resultsPerCell
	}

	return len(job.Data.Keywords) * resultsPerSearch(job.Data.Depth)
}

func resultsPerSearch(depth int) int {
	switch {
	case depth <= 1:
		return 20
	case depth <= 5:
		return 60
	default:
		return 120
	}
}

func (s *Server) jobProgress(job *Job, now time.Time) *JobProgress {
	rows := s.svc.CountRows(job.ID)

	st := &s.metrics
	st.mu.Lock()
	defer st.mu.Unlock()

	// A new job, or the same job run again after a restart (its results file starts over): the
	// rate is measured afresh rather than against the old run's count.
	if n := len(st.history); st.jobID != job.ID || (n > 0 && rows < st.history[n-1].rows) {
		st.jobID, st.history, st.lastRow = job.ID, nil, now
	}

	if n := len(st.history); n == 0 || st.history[n-1].rows != rows {
		st.lastRow = now
	}

	st.history = append(st.history, rowSample{at: now, rows: rows})
	for len(st.history) > 1 && now.Sub(st.history[0].at) > rateWindow {
		st.history = st.history[1:]
	}

	p := &JobProgress{
		ID: job.ID, Name: job.Name, Collector: job.Data.CollectorName(), Searches: len(job.Data.Keywords), Rows: rows,
		Expected:   expectedRows(job),
		LastRowAgo: now.Sub(st.lastRow).Truncate(time.Second),
	}

	if first := st.history[0]; now.Sub(first.at) >= 30*time.Second {
		p.RatePerMin = float64(rows-first.rows) / now.Sub(first.at).Minutes()
	}

	if p.Expected > 0 {
		p.Percent = min(99, 100*rows/p.Expected)
	}

	if p.RatePerMin > 0 && p.Expected > rows {
		p.ETA = time.Duration(float64(p.Expected-rows) / p.RatePerMin * float64(time.Minute)).Truncate(time.Minute)
	}

	switch {
	case p.LastRowAgo >= StallAfter:
		p.State = "stalled"
	case p.LastRowAgo >= SlowAfter:
		p.State = "slow"
	default:
		p.State = "running"
	}

	return p
}

// CurrentMetrics is the last sample.
func (s *Server) CurrentMetrics() Metrics {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()

	return s.metrics.last
}

func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "–"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

var metricsTmpl = template.Must(template.New("metrics").Funcs(template.FuncMap{
	"dur": humanDuration,
	"f0":  func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"f1":  func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"lvl": func(v float64) string {
		switch {
		case v >= 90:
			return "hot"
		case v >= 70:
			return "warm"
		default:
			return "ok"
		}
	},
}).Parse(`<div class="metrics">
{{if .Supported}}
<span class="m m-{{lvl .CPU}}" title="Processor use across the machine">CPU <b>{{f0 .CPU}}%</b></span>
<span class="m m-{{lvl .Mem}}" title="Memory in use">RAM <b>{{f0 .Mem}}%</b> <i>{{f1 .MemUsedGB}}/{{f1 .MemTotGB}} GB</i></span>
<span class="m m-{{lvl .Disk}}" title="Disk used on the data volume">Disk <b>{{f0 .Disk}}%</b></span>
<span class="m" title="Network, all traffic">Net <b>↓{{f0 .NetInKBs}}</b> <b>↑{{f0 .NetOutKBs}}</b> <i>KB/s</i></span>
{{else}}<span class="m faint">Machine metrics are read on the Linux server only</span>{{end}}
{{with .Job}}
<span class="m m-job m-{{.State}}" title="{{.Name}}">▶ <span class="chip chip-{{.Collector}}">{{.Collector}}</span> <b>{{.Rows}}</b> <i>of ~{{.Expected}} ({{.Percent}}%)</i> · <b>{{f1 .RatePerMin}}</b> <i>/min</i> · ETA <b>{{dur .ETA}}</b> · last <b>{{dur .LastRowAgo}}</b> ago · <span class="state">{{.State}}</span></span>
{{else}}<span class="m faint">No job running</span>{{end}}
{{if .Pending}}<span class="m" title="Jobs waiting">Queue <b>{{.Pending}}</b></span>{{end}}
<span class="m{{if .Blocks.Recent}} m-hot{{end}}" title="Pages Google refused in the last 10 minutes; each refusal is retried on another browser and slows new page loads">Google blocks <b>{{.Blocks.Recent}}</b> <i>/10 min · {{.Blocks.Total}} total</i>{{if gt .Blocks.Backoff 0}} · slowed <b>{{dur .Blocks.Backoff}}</b>{{end}}</span>
{{with .Paused}}<span class="m m-note" title="LEADS_PAUSE_WINDOW or LEADS_YIELD_FILE">⏸ {{.}}</span>{{end}}
{{range .Notes}}<span class="m m-note">{{.}}</span>{{end}}
</div>`))

func (s *Server) metricsPartial(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = metricsTmpl.Execute(w, s.CurrentMetrics())
}

func (s *Server) metricsJSON(w http.ResponseWriter, _ *http.Request) {
	renderJSON(w, http.StatusOK, s.CurrentMetrics())
}
