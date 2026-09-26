package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AxioIntel/Axio-CRED/web/leads"
	"github.com/AxioIntel/Axio-CRED/web/saleshandy"
	"github.com/AxioIntel/Axio-CRED/web/whatsapp"
)

//go:embed static
var static embed.FS

type Server struct {
	tmpl       map[string]*template.Template
	srv        *http.Server
	svc        *Service
	leads      *leads.Store
	saleshandy *saleshandy.Client
	shConfig   *saleshandy.Config
	metrics    metricsState
	geocode    *geocoder
	whatsapp   *whatsapp.Client
}

func New(svc *Service, addr string, opts ...Option) (*Server, error) {
	ans := Server{
		svc:     svc,
		tmpl:    make(map[string]*template.Template),
		geocode: newGeocoder(),
		srv: &http.Server{
			Addr:              addr,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		return nil, err
	}

	for _, opt := range opts {
		opt(&ans)
	}

	fileServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()

	ans.registerLeadRoutes(mux)
	ans.registerSaleshandyRoutes(mux)
	ans.registerWhatsAppRoutes(mux)
	mux.HandleFunc("GET /metrics", ans.metricsPartial)
	mux.HandleFunc("GET /grid/preview", ans.gridPreview)
	mux.HandleFunc("POST /rerun", func(w http.ResponseWriter, r *http.Request) {
		ans.rerun(w, requestWithID(r))
	})
	mux.HandleFunc("GET /api/v1/metrics", ans.metricsJSON)
	mux.HandleFunc("GET /spec", func(w http.ResponseWriter, _ *http.Request) {
		ans.render(w, "static/templates/spec.html", nil)
	})

	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("/scrape", ans.scrape)
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		ans.download(w, r)
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		ans.delete(w, r)
	})
	mux.HandleFunc("/jobs", ans.getJobs)
	mux.HandleFunc("/view", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		ans.viewJob(w, r)
	})
	mux.HandleFunc("/", ans.index)

	// api routes
	mux.HandleFunc("/api/docs", ans.redocHandler)
	mux.HandleFunc("/api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			ans.apiScrape(w, r)
		case http.MethodGet:
			ans.apiGetJobs(w, r)
		default:
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)
		}
	})

	mux.HandleFunc("/api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		switch r.Method {
		case http.MethodGet:
			ans.apiGetJob(w, r)
		case http.MethodDelete:
			ans.apiDeleteJob(w, r)
		default:
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)
		}
	})

	mux.HandleFunc("/api/v1/jobs/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		if r.Method != http.MethodGet {
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)

			return
		}

		ans.download(w, r)
	})

	handler := securityHeaders(mux)
	ans.srv.Handler = handler

	tmplsKeys := []string{
		"static/templates/index.html",
		"static/templates/job_rows.html",
		"static/templates/job_row.html",
		"static/templates/job_view.html",
		"static/templates/redoc.html",
		"static/templates/leads.html",
		"static/templates/leads_table.html",
		"static/templates/spec.html",
	}

	for _, key := range tmplsKeys {
		tmp, err := template.ParseFS(static, key)
		if err != nil {
			return nil, err
		}

		ans.tmpl[key] = tmp
	}

	return &ans, nil
}

func (s *Server) Start(ctx context.Context) error {
	go s.sampleMetricsLoop(ctx)

	go func() {
		<-ctx.Done()

		err := s.srv.Shutdown(context.Background())
		if err != nil {
			log.Println(err)

			return
		}

		log.Println("server stopped")
	}()

	fmt.Fprintf(os.Stderr, "visit http://localhost%s\n", s.srv.Addr)

	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

type formData struct {
	Name     string
	MaxTime  string
	Keywords []string
	Language string
	Zoom     int
	FastMode bool
	Radius   int
	Lat      string
	Lon      string
	Depth    int
	Email    bool
	Proxies  []string
}

type ctxKey string

const idCtxKey ctxKey = "id"

func requestWithID(r *http.Request) *http.Request {
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	parsed, err := uuid.Parse(id)
	if err == nil {
		r = r.WithContext(context.WithValue(r.Context(), idCtxKey, parsed))
	}

	return r
}

func getIDFromRequest(r *http.Request) (uuid.UUID, bool) {
	id, ok := r.Context().Value(idCtxKey).(uuid.UUID)

	return id, ok
}

//nolint:gocritic // this is used in template
func (f formData) ProxiesString() string {
	return strings.Join(f.Proxies, "\n")
}

//nolint:gocritic // this is used in template
func (f formData) KeywordsString() string {
	return strings.Join(f.Keywords, "\n")
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	tmpl, ok := s.tmpl["static/templates/index.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	data := formData{
		Name:     "",
		MaxTime:  "1h",
		Keywords: []string{},
		Language: "en",
		Zoom:     15,
		FastMode: false,
		Radius:   10000,
		Lat:      "0",
		Lon:      "0",
		Depth:    5,
		Email:    true,
	}

	_ = tmpl.Execute(w, data)
}

func (s *Server) scrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	newJob := Job{
		ID:     uuid.New().String(),
		Name:   r.Form.Get("name"),
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   JobData{},
	}

	maxTimeStr := r.Form.Get("maxtime")

	maxTime, err := time.ParseDuration(maxTimeStr)
	if err != nil {
		http.Error(w, "invalid max time", http.StatusUnprocessableEntity)

		return
	}

	if maxTime < time.Minute*3 {
		http.Error(w, "max time must be more than 3m", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.MaxTime = maxTime

	keywordsStr, ok := r.Form["keywords"]
	if !ok {
		http.Error(w, "missing keywords", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.Keywords = expandSearches(keywordsStr[0], r.Form.Get("locations"))

	// The map grid: every search runs once per square of one city, instead of once per place.
	if r.Form.Get("grid") == "on" {
		if strings.TrimSpace(r.Form.Get("locations")) != "" {
			http.Error(w, `use "Where" or the map grid, not both`, http.StatusUnprocessableEntity)

			return
		}

		req, gerr := gridRequestFromForm(r.Form.Get)
		if gerr != nil {
			http.Error(w, gerr.Error(), http.StatusUnprocessableEntity)

			return
		}

		spec, _, gerr := s.resolveGrid(r.Context(), req)
		if gerr != nil {
			http.Error(w, gerr.Error(), http.StatusUnprocessableEntity)

			return
		}

		if n := len(newJob.Data.Keywords) * spec.Cells; n > maxGridSearches {
			http.Error(w, fmt.Sprintf("%d map searches is over the %d limit: pick bigger squares or a radius", n, maxGridSearches), http.StatusUnprocessableEntity)

			return
		}

		newJob.Data.Grid = &spec
	}

	if newJob.Name == "" && len(newJob.Data.Keywords) > 0 {
		newJob.Name = newJob.Data.Keywords[0]
		if n := len(newJob.Data.Keywords); n > 1 {
			newJob.Name += fmt.Sprintf(" (+%d more)", n-1)
		}

		if g := newJob.Data.Grid; g != nil {
			newJob.Name += fmt.Sprintf(" · grid %s (%d squares)", g.Area, g.Cells)
		}
	}

	newJob.Data.Lang = r.Form.Get("lang")

	newJob.Data.Zoom, err = strconv.Atoi(r.Form.Get("zoom"))
	if err != nil {
		http.Error(w, "invalid zoom", http.StatusUnprocessableEntity)

		return
	}

	if newJob.Data.Grid != nil {
		newJob.Data.Zoom = zoomForCell(newJob.Data.Grid.CellKm)
	}

	if r.Form.Get("fastmode") == "on" {
		newJob.Data.FastMode = true
	}

	newJob.Data.Radius, err = strconv.Atoi(r.Form.Get("radius"))
	if err != nil {
		http.Error(w, "invalid radius", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.Lat = r.Form.Get("latitude")
	newJob.Data.Lon = r.Form.Get("longitude")

	newJob.Data.Depth, err = strconv.Atoi(r.Form.Get("depth"))
	if err != nil {
		http.Error(w, "invalid depth", http.StatusUnprocessableEntity)

		return
	}

	// The dashboard always collects the emails on each listing's website; the API still chooses.
	newJob.Data.Email = true

	proxies := strings.Split(r.Form.Get("proxies"), "\n")
	if len(proxies) > 0 {
		for _, p := range proxies {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}

			newJob.Data.Proxies = append(newJob.Data.Proxies, p)
		}
	}

	err = newJob.Validate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	tmpl, ok := s.tmpl["static/templates/job_row.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	_ = tmpl.Execute(w, newJob)
}

func (s *Server) getJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	tmpl, ok := s.tmpl["static/templates/job_rows.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}

	view, err := s.jobListView(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_ = tmpl.Execute(w, view)
}

// jobRow is a job as the job list shows it: with how many leads it found (so far, while running).
type jobRow struct {
	Job
	Leads    int
	Searches int
}

// jobListView is what the /jobs partial renders: pending jobs in the order they will run, then
// history, most recent first. A campaign can queue tens of thousands of searches; MoreQueued
// counts pending jobs beyond the preview so the page never has to decode all of them.
type jobListView struct {
	Rows       []jobRow
	MoreQueued int
}

// pendingPreviewSize and recentHistorySize bound the /jobs page: enough to see the queue and
// recent runs, never a full-table scan.
const (
	pendingPreviewSize = 50
	recentHistorySize  = 200
)

func (s *Server) jobListView(ctx context.Context) (jobListView, error) {
	pendingCount, err := s.svc.PendingCount(ctx)
	if err != nil {
		return jobListView{}, err
	}

	pending, err := s.svc.PendingPreview(ctx, pendingPreviewSize)
	if err != nil {
		return jobListView{}, err
	}

	recent, err := s.svc.RecentHistory(ctx, recentHistorySize)
	if err != nil {
		return jobListView{}, err
	}

	jobs := make([]Job, 0, len(pending)+len(recent))
	jobs = append(jobs, pending...)
	jobs = append(jobs, recent...)

	return jobListView{
		Rows:       s.jobRows(ctx, jobs),
		MoreQueued: max(pendingCount-len(pending), 0),
	}, nil
}

func (s *Server) jobRows(ctx context.Context, jobs []Job) []jobRow {
	var counts map[string]int

	if s.leads != nil {
		counts, _ = s.leads.JobLeadCounts(ctx)
	}

	out := make([]jobRow, 0, len(jobs))

	for i := range jobs {
		row := jobRow{Job: jobs[i], Searches: len(jobs[i].Data.Keywords)}

		if n, ok := counts[jobs[i].ID]; ok {
			row.Leads = n
		} else {
			row.Leads = s.svc.CountRows(jobs[i].ID)
		}

		out = append(out, row)
	}

	return out
}

// expandSearches turns the form's searches and optional locations into the job's keywords: with
// locations, every search runs in every location ("dentist" x "Austin TX" -> "dentist in Austin
// TX"); without, each search line runs as typed. Duplicates are dropped.
func expandSearches(searches, locations string) []string {
	lines := func(s string) []string {
		var out []string

		for _, l := range strings.Split(s, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}

		return out
	}

	terms, places := lines(searches), lines(locations)
	seen := map[string]bool{}

	var out []string

	add := func(k string) {
		if !seen[strings.ToLower(k)] {
			seen[strings.ToLower(k)] = true

			out = append(out, k)
		}
	}

	for _, t := range terms {
		if len(places) == 0 {
			add(t)

			continue
		}

		for _, p := range places {
			add(t + " in " + p)
		}
	}

	return out
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	ctx := r.Context()

	id, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)

		return
	}

	filePath, err := s.svc.GetCSV(ctx, id.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	fileName := filepath.Base(filePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "text/csv")

	_, err = io.Copy(w, file)
	if err != nil {
		http.Error(w, "Failed to send file", http.StatusInternalServerError)
		return
	}
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	deleteID, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)

		return
	}

	err := s.svc.Delete(r.Context(), deleteID.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiScrapeRequest struct {
	Name string
	JobData
}

type apiScrapeResponse struct {
	ID string `json:"id"`
}

func (s *Server) redocHandler(w http.ResponseWriter, _ *http.Request) {
	tmpl, ok := s.tmpl["static/templates/redoc.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	_ = tmpl.Execute(w, nil)
}

func (s *Server) apiScrape(w http.ResponseWriter, r *http.Request) {
	var req apiScrapeRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)

		return
	}

	newJob := Job{
		ID:     uuid.New().String(),
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   req.JobData,
	}

	// convert to seconds
	newJob.Data.MaxTime *= time.Second

	err = newJob.Validate()
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)

		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		ans := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, ans)

		return
	}

	ans := apiScrapeResponse{
		ID: newJob.ID,
	}

	renderJSON(w, http.StatusCreated, ans)
}

func (s *Server) apiGetJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.svc.All(r.Context())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, apiError)

		return
	}

	renderJSON(w, http.StatusOK, jobs)
}

func (s *Server) apiGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, apiError)

		return
	}

	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusNotFound,
			Message: http.StatusText(http.StatusNotFound),
		}

		renderJSON(w, http.StatusNotFound, apiError)

		return
	}

	renderJSON(w, http.StatusOK, job)
}

// viewJob renders the map modal fragment for a job, embedding the job's places
// directly so the client needs no separate data request.
func (s *Server) viewJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	id, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)

		return
	}

	places, err := s.svc.GetPlaces(r.Context(), id.String())

	if err != nil {
		if !errors.Is(err, ErrPlacesNotFound) {
			log.Printf("view job %s: %v", id, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)

			return
		}

		// No CSV yet: render the modal with an empty state rather than an error.
		places = []Place{}
	}

	tmpl, ok := s.tmpl["static/templates/job_view.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, places); err != nil {
		log.Printf("view job %s: render: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)

		return
	}

	_, _ = buf.WriteTo(w)
}

func (s *Server) apiDeleteJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, apiError)

		return
	}

	err := s.svc.Delete(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, apiError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func renderJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_ = json.NewEncoder(w).Encode(data)
}

func formatDate(t time.Time) string {
	return t.Format("Jan 02, 2006 15:04:05")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' cdn.redoc.ly cdnjs.cloudflare.com 'unsafe-inline' 'unsafe-eval'; "+
				"worker-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline' fonts.googleapis.com cdnjs.cloudflare.com; "+
				"img-src 'self' data: cdn.redoc.ly cdnjs.cloudflare.com *.tile.openstreetmap.org; "+
				"font-src 'self' fonts.gstatic.com; "+
				"connect-src 'self'")

		next.ServeHTTP(w, r)
	})
}

// rerun puts a finished job back in the queue, next in line, to run again from its first search: the way back
// on track for a job that ended short. Its earlier results stay in the lead list, which merges the
// two runs.
func (s *Server) rerun(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "invalid job id", http.StatusUnprocessableEntity)

		return
	}

	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)

		return
	}

	if job.Status == StatusPending || job.Status == StatusWorking {
		http.Error(w, "the job is already queued or running", http.StatusConflict)

		return
	}

	// It keeps its place by age, so it runs next: getting a job back on track comes first.
	job.Status = StatusPending

	if err := s.svc.Update(r.Context(), &job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	s.Note(fmt.Sprintf("%q queued to run again", job.Name))
	w.Header().Set("HX-Trigger", "jobs-changed")
	w.WriteHeader(http.StatusNoContent)
}
