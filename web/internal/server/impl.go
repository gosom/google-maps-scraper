package server

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/gosom/google-maps-scraper/entities"
)

type server struct {
	workhorse entities.Worker
	store     entities.JobStore
}

func NewServer(workhorse entities.Worker, store entities.JobStore) Server {
	ans := server{
		workhorse: workhorse,
		store:     store,
	}

	return &ans
}

type Job struct {
	entities.Job
	Count int
}

func (o *Job) DownloadLink() string {
	if o.Count > 0 && (o.Status == entities.JobStatusDone || o.Status == entities.JobStatusStopped) {
		return fmt.Sprintf("/downloads/%s", o.ID)
	}
	return ""
}

type IndexData struct {
	Jobs []Job
}

func (s *server) Index(c echo.Context) error {
	jobs, err := s.store.SelectAllJobs(c.Request().Context())
	if err != nil {
		panic(err)
	}

	m, err := s.store.GetResultCount(c.Request().Context())
	if err != nil {
		panic(err)
	}

	items := make([]Job, len(jobs))
	for i, job := range jobs {
		items[i] = Job{
			Job:   job,
			Count: m[job.ID],
		}
	}

	data := IndexData{
		Jobs: items,
	}

	return c.Render(http.StatusOK, "index.html", data)
}

type JobForm struct {
	ID         string `form:"jobId"`
	LangCode   string `form:"langCode"`
	MaxDepth   int    `form:"maxDepth"`
	Concurreny int    `form:"concurrency"`
	DebugMode  string `form:"debugMode"`
	Queries    string `form:"queries"`
}

func (s *server) SubmitJob(c echo.Context) error {
	var form JobForm

	if err := c.Bind(&form); err != nil {
		panic(err)
		return err
	}

	file, err := c.FormFile("fileUpload")
	if err == nil {
		fd, err := file.Open()
		if err != nil {
			panic(err)
		}

		defer fd.Close()

		var queries []string
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			queries = append(queries, strings.TrimSpace(scanner.Text()))
		}

		form.Queries = strings.Join(queries, "\n")
	}

	form.Queries = strings.TrimSpace(form.Queries)

	if form.Queries == "" {
		panic("no queries")
	}

	job := entities.Job{
		ID:          form.ID,
		LangCode:    form.LangCode,
		MaxDepth:    form.MaxDepth,
		Debug:       form.DebugMode == "on",
		Concurrency: form.Concurreny,
		CreatedAt:   time.Now().UTC(),
		Queries:     strings.Split(form.Queries, "\n"),
	}

	const defaultCPUDivider = 2
	if job.Concurrency == 0 {
		defaultConcurency := runtime.NumCPU() / defaultCPUDivider
		if defaultConcurency < 1 {
			defaultConcurency = 1
		}
		job.Concurrency = defaultConcurency
	}

	if err := s.store.CreateJob(c.Request().Context(), &job); err != nil {
		panic(err)
	}

	if err := s.workhorse.ScheduleJob(c.Request().Context(), job); err != nil {
		if err := s.store.SetJobStatus(c.Request().Context(), job.ID, entities.JobStatusFailed); err != nil {
			panic(err)
		}

		panic(err)
		return err
	}

	return c.Redirect(http.StatusFound, c.Echo().Reverse("index"))
}

func (s *server) JobDownload(c echo.Context) error {
	id := c.Param("id")
	format := c.QueryParam("format")
	if format != "json" && format != "csv" {
		panic("invalid format")
	}

	results, err := s.store.GetJobResult(c.Request().Context(), id)
	if err != nil {
		panic(err)
	}

	if len(results) == 0 {
		panic("no results")
	}

	tmpFile, err := os.CreateTemp("", "scrapemate-*.json")
	if err != nil {
		panic(err)
	}

	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	if format == "json" {
		enc := json.NewEncoder(tmpFile)
		for _, result := range results {
			if err := enc.Encode(result.Data); err != nil {
				panic(err)
			}
		}
	} else if format == "csv" {
		enc := csv.NewWriter(tmpFile)
		if err := enc.Write(results[0].Data.CsvHeaders()); err != nil {
			panic(err)
		}
		for _, result := range results {
			if err := enc.Write(result.Data.CsvRow()); err != nil {
				panic(err)
			}
		}
	}

	tmpFile.Close()

	fname := fmt.Sprintf("%s.%s", id, format)

	return c.Attachment(tmpFile.Name(), fname)
}
