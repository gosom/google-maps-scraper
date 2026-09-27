package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/web/leads"
)

// The Verify page tests v1 against v2 from the dashboard: a smoke check of each collector (one
// small job, checked end to end), and comparisons (the same searches run by each collector, in
// alternation, several times), with the numbers and a verdict. Its jobs are ordinary jobs in the
// queue, tagged so the runner and this page know them.

const (
	verifyTemplate = "static/templates/verify.html"

	// DefaultSmokeQuery is what a smoke check searches unless told otherwise.
	DefaultSmokeQuery = "dentist in Austin TX"

	// TagSmoke marks a smoke-check job; the runner checks it when it finishes.
	TagSmoke = "smoke"
	// tagComparison prefixes a comparison job's tag, before the comparison's id.
	tagComparison = "cmp:"

	settingQueries = "queries"

	maxComparisonRuns    = 5
	maxComparisonQueries = 200
	recentComparisons    = 10
	smokeMaxTime         = 15 * time.Minute

	armV1     = "v1"
	armV2     = "v2"
	armV2Deep = "v2-deep"
	// deepContactPages is how many contact pages the "v2 deep" arm reads.
	deepContactPages = 2
)

// defaultComparisonQueries are the searches a comparison starts with: 30 trades across 30 US
// counties, the shape of the county-by-county programme the collectors are for.
var defaultComparisonQueries = []string{
	"roofing contractor in Travis County TX", "plumber in Maricopa County AZ", "hvac repair in Cook County IL",
	"electrician in King County WA", "dentist in Fulton County GA", "landscaping in Orange County FL",
	"auto repair in Harris County TX", "pest control in Mecklenburg County NC", "chiropractor in Hennepin County MN",
	"garage door repair in Clark County NV", "roofing contractor in Jefferson County KY", "plumber in Wake County NC",
	"hvac repair in Salt Lake County UT", "electrician in Franklin County OH", "dentist in Davidson County TN",
	"landscaping in Denver County CO", "auto repair in Marion County IN", "pest control in Shelby County TN",
	"chiropractor in Multnomah County OR", "garage door repair in Bexar County TX", "roofing contractor in Hillsborough County FL",
	"plumber in Allegheny County PA", "hvac repair in Jackson County MO", "electrician in Pima County AZ",
	"dentist in Oklahoma County OK", "landscaping in Sacramento County CA", "auto repair in Milwaukee County WI",
	"pest control in Duval County FL", "chiropractor in Wayne County MI", "garage door repair in Tarrant County TX",
}

// Arm is one side of a comparison: a collector and its settings.
type Arm struct {
	Name      string      `json:"name"`
	Collector string      `json:"collector"`
	Depth     int         `json:"depth,omitempty"`
	Steady    *SteadySpec `json:"steady,omitempty"`
}

// Label describes the arm's settings in a few words.
func (a *Arm) Label() string {
	if a.Collector != gmaps.CollectorV2 {
		return fmt.Sprintf("browser, depth %d", a.Depth)
	}

	d := JobData{Collector: gmaps.CollectorV2, Steady: a.Steady}
	c := d.SteadyConfig()

	return fmt.Sprintf("%d page(s), %d contact page(s), %s gap, %s a search", c.Pages, c.ContactPages, c.Delay, c.QueryBudget)
}

func (s *Server) registerVerifyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /verify", s.verifyPage)
	mux.HandleFunc("GET /verify/smoke", s.verifySmokePartial)
	mux.HandleFunc("POST /verify/smoke/{collector}", s.verifyRunSmoke)
	mux.HandleFunc("POST /verify/compare", s.verifyStartComparison)
	mux.HandleFunc("GET /verify/compare/{id}", s.verifyComparisonPartial)
	mux.HandleFunc("POST /verify/compare/{id}/stop", s.verifyStopComparison)
	mux.HandleFunc("GET /verify/compare/{id}/export", s.verifyExport)
	mux.HandleFunc("GET /api/v1/verify/smoke", s.apiVerifySmoke)
	mux.HandleFunc("GET /api/v1/verify/compare/{id}", s.apiVerifyComparison)
	mux.HandleFunc("GET /api/v1/runs", s.apiRuns)
}

// smokeCard is one collector's smoke check as the page shows it.
type smokeCard struct {
	Collector string
	Running   *Job // its smoke job, while queued or running
	Last      *leads.SmokeCheck
}

type verifyPageData struct {
	Smoke       []smokeCard
	SmokeQuery  string
	Queries     string
	Comparisons []comparisonRow
	Selected    *comparisonReport
}

type comparisonRow struct {
	ID, Name       string
	CreatedAt      time.Time
	Done, Total    int
	Stopped, Ended bool
}

func (s *Server) requireLeads(w http.ResponseWriter) bool {
	if s.leads == nil {
		http.Error(w, "the lead list is not enabled", http.StatusNotFound)

		return false
	}

	return true
}

func (s *Server) verifyPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	ctx := r.Context()

	smoke, err := s.smokeCards(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	queries, err := s.leads.Setting(ctx, settingQueries)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if queries == "" {
		queries = strings.Join(defaultComparisonQueries, "\n")
	}

	data := verifyPageData{Smoke: smoke, SmokeQuery: DefaultSmokeQuery, Queries: queries}

	cmps, err := s.leads.Comparisons(ctx, recentComparisons)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	for i := range cmps {
		data.Comparisons = append(data.Comparisons, s.comparisonRow(ctx, &cmps[i]))
	}

	selected := r.URL.Query().Get("c")
	if selected == "" && len(cmps) > 0 {
		selected = cmps[0].ID
	}

	if selected != "" {
		if rep, err := s.comparisonReport(ctx, selected); err == nil {
			data.Selected = &rep
		}
	}

	s.renderVerify(w, "page", data)
}

func (s *Server) renderVerify(w http.ResponseWriter, name string, data any) {
	tmpl, ok := s.tmpl[verifyTemplate]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// smokeCards are both collectors' smoke state: a smoke job in the queue, and the last result.
func (s *Server) smokeCards(ctx context.Context) ([]smokeCard, error) {
	last, err := s.leads.LastSmokeChecks(ctx)
	if err != nil {
		return nil, err
	}

	queued, err := s.svc.PendingPreview(ctx, pendingPreviewSize)
	if err != nil {
		return nil, err
	}

	if running, err := s.svc.Working(ctx); err == nil && running != nil {
		queued = append(queued, *running)
	}

	cards := make([]smokeCard, 0, 2)

	for _, c := range []string{gmaps.CollectorV1, gmaps.CollectorV2} {
		card := smokeCard{Collector: c}

		if l, ok := last[c]; ok {
			card.Last = &l
		}

		for i := range queued {
			if queued[i].Data.Tag == TagSmoke && queued[i].Data.CollectorName() == c {
				card.Running = &queued[i]
			}
		}

		cards = append(cards, card)
	}

	return cards, nil
}

func (s *Server) verifySmokePartial(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	cards, err := s.smokeCards(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	s.renderVerify(w, "smoke", cards)
}

// smokeJob is the one small job a smoke check runs: one search, about 20 places, emails on.
func smokeJob(collector, query string) Job {
	job := Job{
		ID: uuid.New().String(), Name: "Smoke check · " + collector, Date: time.Now().UTC(), Status: StatusPending,
		Data: JobData{
			Keywords: []string{query}, Lang: "en", Zoom: 15, Depth: 1, Email: true, MaxTime: smokeMaxTime,
			Collector: collector, Tag: TagSmoke,
		},
	}

	if collector == gmaps.CollectorV2 {
		job.Data.Steady = &SteadySpec{Pages: 1}
	}

	return job
}

func (s *Server) verifyRunSmoke(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	collector := r.PathValue("collector")
	if collector != gmaps.CollectorV1 && collector != gmaps.CollectorV2 {
		http.Error(w, "unknown collector", http.StatusNotFound)

		return
	}

	query := strings.TrimSpace(r.FormValue("query"))
	if query == "" {
		query = DefaultSmokeQuery
	}

	job := smokeJob(collector, query)

	// It runs next, ahead of any queued comparison: a deploy is checked before anything else.
	if first, err := s.svc.PendingPreview(r.Context(), 1); err == nil && len(first) > 0 && first[0].Date.Before(job.Date) {
		job.Date = first[0].Date.Add(-time.Second)
	}

	if err := job.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	if err := s.svc.Create(r.Context(), &job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	s.Note(collector + " smoke check queued")
	s.verifySmokePartial(w, r)
}

// comparisonForm is a comparison as the form describes it.
type comparisonForm struct {
	Name     string
	Queries  []string
	Arms     []Arm
	Runs     int
	MaxTime  time.Duration
	SaveList bool
}

func parseComparisonForm(get func(string) string) (comparisonForm, error) {
	f := comparisonForm{Name: strings.TrimSpace(get("name")), SaveList: get("save_queries") == formOn}

	for _, q := range strings.Split(get("queries"), "\n") {
		if q = strings.TrimSpace(q); q != "" {
			f.Queries = append(f.Queries, q)
		}
	}

	if len(f.Queries) == 0 || len(f.Queries) > maxComparisonQueries {
		return f, fmt.Errorf("give 1 to %d searches", maxComparisonQueries)
	}

	runs, err := strconv.Atoi(get("runs"))
	if err != nil || runs < 1 || runs > maxComparisonRuns {
		return f, fmt.Errorf("runs per collector must be 1 to %d", maxComparisonRuns)
	}

	f.Runs = runs

	f.MaxTime, err = time.ParseDuration(get("maxtime"))
	if err != nil || f.MaxTime < 3*time.Minute {
		return f, errors.New(`"Stop each job after" must be at least 3m`)
	}

	depth, err := strconv.Atoi(get("v1_depth"))
	if err != nil || depth < 1 {
		return f, errors.New("invalid v1 depth")
	}

	f.Arms = append(f.Arms, Arm{Name: armV1, Collector: gmaps.CollectorV1, Depth: depth})

	// v2's settings come from the same fields as the scrape form's.
	v2get := func(name string) string {
		if name == "collector" {
			return gmaps.CollectorV2
		}

		return get(name)
	}

	_, spec, err := collectorFromForm(v2get)
	if err != nil {
		return f, err
	}

	f.Arms = append(f.Arms, Arm{Name: armV2, Collector: gmaps.CollectorV2, Steady: spec})

	if get("arm_v2_deep") == formOn {
		deep := *spec
		deep.ContactPages = deepContactPages
		f.Arms = append(f.Arms, Arm{Name: armV2Deep, Collector: gmaps.CollectorV2, Steady: &deep})
	}

	if f.Name == "" {
		f.Name = fmt.Sprintf("%d searches × %d runs, %s", len(f.Queries), f.Runs, time.Now().UTC().Format("Jan 2 15:04"))
	}

	return f, nil
}

// comparisonJobs are a comparison's jobs in queue order: run 1 of every arm, then run 2 of every
// arm, and so on, so the arms alternate and meet the same hours of the day and the same proxies.
func comparisonJobs(id string, f *comparisonForm, now time.Time) ([]Job, []leads.ComparisonJob) {
	jobs := make([]Job, 0, f.Runs*len(f.Arms))
	links := make([]leads.ComparisonJob, 0, f.Runs*len(f.Arms))

	for run := 1; run <= f.Runs; run++ {
		for _, arm := range f.Arms {
			job := Job{
				ID:     uuid.New().String(),
				Name:   fmt.Sprintf("%s · %s · run %d", f.Name, arm.Name, run),
				Date:   now,
				Status: StatusPending,
				Data: JobData{
					Keywords: append([]string(nil), f.Queries...), Lang: "en", Zoom: 15, Depth: max(arm.Depth, 1),
					Email: true, MaxTime: f.MaxTime, Collector: arm.Collector, Steady: arm.Steady,
					Tag: tagComparison + id,
				},
			}

			links = append(links, leads.ComparisonJob{Seq: len(jobs), Arm: arm.Name, RunNo: run, JobID: job.ID})
			jobs = append(jobs, job)
		}
	}

	return jobs, links
}

func (s *Server) verifyStartComparison(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	f, err := parseComparisonForm(r.Form.Get)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()
	jobs, links := comparisonJobs(id, &f, now)

	for i := range jobs {
		if err := jobs[i].Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)

			return
		}
	}

	arms, err := json.Marshal(f.Arms)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	ctx := r.Context()

	cmp := leads.Comparison{ID: id, Name: f.Name, CreatedAt: now, Queries: f.Queries, Arms: arms, RunsPerArm: f.Runs, Jobs: links}
	if err := s.leads.CreateComparison(ctx, &cmp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	for i := range jobs {
		if err := s.svc.Create(ctx, &jobs[i]); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
	}

	if f.SaveList {
		if err := s.leads.SetSetting(ctx, settingQueries, strings.Join(f.Queries, "\n")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
	}

	s.Note(fmt.Sprintf("comparison %q queued: %d jobs", f.Name, len(jobs)))
	w.Header().Set("HX-Redirect", "/verify?c="+id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyStopComparison(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	cmp, err := s.leads.Comparison(ctx, id)
	if errors.Is(err, leads.ErrNoComparison) {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if err := s.leads.StopComparison(ctx, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// Its queued jobs go; the one running now (if any) finishes, inside its own bound.
	dropped := 0

	for _, j := range cmp.Jobs {
		if job, err := s.svc.Get(ctx, j.JobID); err == nil && job.Status == StatusPending {
			if err := s.svc.Delete(ctx, j.JobID); err == nil {
				dropped++
			}
		}
	}

	s.Note(fmt.Sprintf("comparison %q stopped: %d queued job(s) dropped", cmp.Name, dropped))
	s.verifyComparisonPartial(w, r)
}

func (s *Server) verifyComparisonPartial(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	rep, err := s.comparisonReport(r.Context(), r.PathValue("id"))
	if errors.Is(err, leads.ErrNoComparison) {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	s.renderVerify(w, "report", rep)
}

// comparisonReport is a comparison's state and numbers.
type comparisonReport struct {
	ID, Name    string
	CreatedAt   time.Time
	Queries     int
	RunsPerArm  int
	Stopped     bool
	Done, Total int
	Ended       bool
	Arms        []Arm
	Summaries   []leads.ArmSummary
	Verdict     leads.Verdict
	Searches    []searchLine
}

// searchLine is one search of one run, for the drill-down table.
type searchLine struct {
	Arm   string
	RunNo int
	leads.RunRow
}

func (s *Server) comparisonRow(ctx context.Context, c *leads.Comparison) comparisonRow {
	row := comparisonRow{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, Total: len(c.Jobs), Stopped: c.Stopped}

	for _, j := range c.Jobs {
		if st := s.jobStatus(ctx, j.JobID); Finished(st) || st == leads.JobDeleted {
			row.Done++
		}
	}

	row.Ended = row.Done == row.Total

	return row
}

// jobStatus is a job's status, or leads.JobDeleted when it is no longer in the job list.
func (s *Server) jobStatus(ctx context.Context, id string) string {
	job, err := s.svc.Get(ctx, id)
	if err != nil || job.ID == "" {
		return leads.JobDeleted
	}

	return job.Status
}

func (s *Server) comparisonReport(ctx context.Context, id string) (comparisonReport, error) {
	c, err := s.leads.Comparison(ctx, id)
	if err != nil {
		return comparisonReport{}, err
	}

	rep := comparisonReport{
		ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, Queries: len(c.Queries), RunsPerArm: c.RunsPerArm,
		Stopped: c.Stopped, Total: len(c.Jobs),
	}

	if err := json.Unmarshal(c.Arms, &rep.Arms); err != nil {
		return rep, fmt.Errorf("comparison %s arms: %w", id, err)
	}

	ids := make([]string, 0, len(c.Jobs))
	for _, j := range c.Jobs {
		ids = append(ids, j.JobID)
	}

	jobRuns, err := s.leads.JobRuns(ctx, ids...)
	if err != nil {
		return rep, err
	}

	runs, err := s.leads.Runs(ctx, ids...)
	if err != nil {
		return rep, err
	}

	byJob := map[string][]leads.RunRow{}
	for i := range runs {
		byJob[runs[i].JobID] = append(byJob[runs[i].JobID], runs[i])
	}

	perArm := map[string][]leads.ArmRun{}

	for _, j := range c.Jobs {
		ar := leads.ArmRun{Arm: j.Arm, RunNo: j.RunNo, JobID: j.JobID, Job: jobRuns[j.JobID], Searches: byJob[j.JobID],
			UniqueEmails: jobRuns[j.JobID].UniqueEmails}

		ar.JobStatus = s.jobStatus(ctx, j.JobID)

		if Finished(ar.JobStatus) || ar.JobStatus == leads.JobDeleted {
			rep.Done++
		}

		perArm[j.Arm] = append(perArm[j.Arm], ar)

		for i := range byJob[j.JobID] {
			rep.Searches = append(rep.Searches, searchLine{Arm: j.Arm, RunNo: j.RunNo, RunRow: byJob[j.JobID][i]})
		}
	}

	rep.Ended = rep.Done == rep.Total

	named := map[string]*leads.ArmSummary{}

	for _, a := range rep.Arms {
		rep.Summaries = append(rep.Summaries, leads.Summarize(a.Name, perArm[a.Name]))
	}

	for i := range rep.Summaries {
		named[rep.Summaries[i].Arm] = &rep.Summaries[i]
	}

	if v1, v2 := named[armV1], named[armV2]; v1 != nil && v2 != nil {
		rep.Verdict = leads.Judge(v1, v2, named[armV2Deep])
	}

	return rep, nil
}

func (s *Server) verifyExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	rep, err := s.comparisonReport(r.Context(), r.PathValue("id"))
	if errors.Is(err, leads.ErrNoComparison) {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	table := r.URL.Query().Get("table")
	if table != searchesTable {
		table = "summary"
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")

	short := rep.ID

	if len(short) > 8 {
		short = short[:8]
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="comparison-%s-%s.csv"`, short, table))

	cw := csv.NewWriter(w)

	if table == searchesTable {
		writeSearchesCSV(cw, rep.Searches)
	} else {
		writeRunsCSV(cw, rep.Summaries)
	}

	cw.Flush()
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func writeRunsCSV(cw *csv.Writer, sums []leads.ArmSummary) {
	_ = cw.Write([]string{"arm", "run", "job_id", "status", "discarded", "searches", "places", "leads_per_search",
		"unique_emails", "emails_per_search", "hit_rate", "fetch_fail_share", "search_p50_s", "search_p95_s",
		"search_max_s", "timing", "wall_s", "bound_s", "inside_bound", "blocks", "restarts", "stalls",
		"cpu_p50", "cpu_p95", "mem_p50", "mem_p95"})

	for i := range sums {
		for k := range sums[i].Runs {
			m := &sums[i].Runs[k]

			timing := "per_search"
			if m.JobLevelTiming {
				timing = "job_level"
			}

			_ = cw.Write([]string{m.Arm, strconv.Itoa(m.RunNo), m.JobID, m.Status, strconv.FormatBool(m.Discarded),
				strconv.Itoa(m.Searches), strconv.Itoa(m.Places), f2(m.LeadsPerSearch), strconv.Itoa(m.UniqueEmails),
				f2(m.EmailsPerSearch), f2(m.HitRate), f2(m.FetchFailShare), f2(m.SearchP50), f2(m.SearchP95),
				f2(m.SearchMax), timing, f2(m.WallSeconds), f2(m.BoundSeconds), strconv.FormatBool(m.InsideBound),
				strconv.Itoa(m.Blocks), strconv.Itoa(m.Restarts), strconv.Itoa(m.Stalls),
				f2(m.CPUP50), f2(m.CPUP95), f2(m.MemP50), f2(m.MemP95)})
		}
	}
}

func writeSearchesCSV(cw *csv.Writer, lines []searchLine) {
	_ = cw.Write([]string{"arm", "run", "job_id", "search", "method", "timing", "seconds", "status", "pages",
		"places_found", "duplicates", "websites", "email_attempted", "emails_found", "email_none",
		"email_fetch_failed", "email_skipped", "unique_emails", "blocks", "min_site_gap_ms", "error"})

	for i := range lines {
		l := &lines[i]

		timing := "per_search"
		if l.JobLevel {
			timing = "job_level"
		}

		_ = cw.Write([]string{l.Arm, strconv.Itoa(l.RunNo), l.JobID, l.Search, l.Method, timing, f2(l.Seconds()),
			l.Status, strconv.Itoa(l.Pages), strconv.Itoa(l.PlacesFound), strconv.Itoa(l.Duplicates),
			strconv.Itoa(l.Websites), strconv.Itoa(l.EmailAttempted), strconv.Itoa(l.EmailsFound),
			strconv.Itoa(l.EmailNone), strconv.Itoa(l.EmailFetchFailed), strconv.Itoa(l.EmailSkipped),
			strconv.Itoa(l.UniqueEmails), strconv.Itoa(l.Blocks), strconv.FormatInt(l.MinSiteGap.Milliseconds(), 10),
			l.Error})
	}
}

func (s *Server) apiVerifySmoke(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	last, err := s.leads.LastSmokeChecks(r.Context())
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, apiError{Code: http.StatusInternalServerError, Message: err.Error()})

		return
	}

	renderJSON(w, http.StatusOK, last)
}

func (s *Server) apiVerifyComparison(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	rep, err := s.comparisonReport(r.Context(), r.PathValue("id"))
	if errors.Is(err, leads.ErrNoComparison) {
		renderJSON(w, http.StatusNotFound, apiError{Code: http.StatusNotFound, Message: err.Error()})

		return
	}

	if err != nil {
		renderJSON(w, http.StatusInternalServerError, apiError{Code: http.StatusInternalServerError, Message: err.Error()})

		return
	}

	renderJSON(w, http.StatusOK, rep)
}

// apiRuns is the run ledger's search rows for one or more jobs (?job=<id>, repeatable), as JSON or
// (format=csv) CSV.
func (s *Server) apiRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeads(w) {
		return
	}

	ids := r.URL.Query()["job"]
	if len(ids) == 0 {
		renderJSON(w, http.StatusUnprocessableEntity, apiError{Code: http.StatusUnprocessableEntity, Message: "give ?job=<id>"})

		return
	}

	runs, err := s.leads.Runs(r.Context(), ids...)
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, apiError{Code: http.StatusInternalServerError, Message: err.Error()})

		return
	}

	if r.URL.Query().Get("format") != "csv" {
		renderJSON(w, http.StatusOK, runs)

		return
	}

	lines := make([]searchLine, 0, len(runs))
	for i := range runs {
		lines = append(lines, searchLine{RunRow: runs[i]})
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")

	cw := csv.NewWriter(w)
	writeSearchesCSV(cw, lines)
	cw.Flush()
}

// noValue is shown for a number there is nothing yet to compute from.
const noValue = "–"

// searchesTable is the export of every search (the other is the runs summary).
const searchesTable = "searches"

// verifyFuncs format the Verify page's numbers.
var verifyFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"f1":  func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
	"pct": func(v float64) string { return strconv.FormatFloat(v*100, 'f', 0, 64) + "%" },
	// stat is a median with its range; "–" before any fair run.
	"stat": func(st leads.Stat, digits int) template.HTML {
		if st.N == 0 {
			return noValue
		}

		f := func(v float64) string { return template.HTMLEscapeString(strconv.FormatFloat(v, 'f', digits, 64)) }

		if st.N == 1 {
			return template.HTML(f(st.Median)) //nolint:gosec // numbers formatted here, escaped
		}

		return template.HTML(f(st.Median) + ` <span class="range">` + f(st.Min) + "–" + f(st.Max) + "</span>") //nolint:gosec // numbers formatted here, escaped
	},
	"statPct": func(st leads.Stat) template.HTML {
		if st.N == 0 {
			return noValue
		}

		p := func(v float64) string { return strconv.FormatFloat(v*100, 'f', 0, 64) + "%" }

		if st.N == 1 {
			return template.HTML(p(st.Median)) //nolint:gosec // numbers formatted here
		}

		return template.HTML(p(st.Median) + ` <span class="range">` + p(st.Min) + "–" + p(st.Max) + "</span>") //nolint:gosec // numbers formatted here
	},
}
