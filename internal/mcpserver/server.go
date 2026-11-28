// Package mcpserver provides a local MCP interface for the scraper.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	statusQueued      = "queued"
	statusRunning     = "running"
	statusFailed      = "failed"
	statusCompleted   = "completed"
	statusInterrupted = "interrupted"
	statusCancelled   = "cancelled"
)

const stateFile = "state.json"
const staleAfter = 45 * time.Second

type job struct {
	ID          string    `json:"id"`
	Plan        plan      `json:"plan"`
	Status      string    `json:"status"`
	Created     time.Time `json:"created_at"`
	Updated     time.Time `json:"updated_at"`
	CellsDone   int       `json:"cells_done"`
	Count       int       `json:"result_count"`
	EmailCount  int       `json:"email_count"`
	Error       string    `json:"error,omitempty"`
	RequestHash string    `json:"request_hash,omitempty"`
}

// Keep worker state private. A client only needs progress and the resolved
// search area, regardless of how many coordinates the job uses internally.
type progress struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	Count      int    `json:"result_count"`
	EmailCount *int   `json:"email_count,omitempty"`
	CellsDone  int    `json:"cells_done"`
	CellsTotal int    `json:"cells_total"`
	Error      string `json:"error,omitempty"`
}

func (j *job) progress() progress {
	p := progress{ID: j.ID, Status: j.Status, Mode: j.Plan.Mode, Count: j.Count,
		CellsDone: j.CellsDone, CellsTotal: len(j.Plan.Cells), Error: j.Error}
	if j.Plan.Email {
		p.EmailCount = &j.EmailCount
	}

	return p
}

type startedJob struct {
	progress
	Location    string  `json:"location,omitempty"`
	Center      *point  `json:"center,omitempty"`
	RadiusKM    float64 `json:"radius_km,omitempty"`
	Approximate bool    `json:"approximate_area,omitempty"`
	Limit       int     `json:"limit"`
}

type locationChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type startResult struct {
	Result   *resultData      `json:"result,omitempty"`
	Job      *startedJob      `json:"job,omitempty"`
	Choices  []locationChoice `json:"location_choices,omitempty"`
	Question string           `json:"question,omitempty"`
}

func started(j *job) startResult {
	result := startedJob{progress: j.progress(), Location: j.Plan.Location,
		RadiusKM: j.Plan.RadiusKM, Approximate: j.Plan.Approximate, Limit: j.Plan.Limit}
	if result.Location == "" {
		result.Center = &j.Plan.Center
	}

	return startResult{Job: &result}
}

type jobInput struct {
	ID string `json:"job_id"`
}

type listInput struct {
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Default 5, maximum 20."`
	Offset int    `json:"offset,omitempty"`
}

type historyItem struct {
	ID       string    `json:"id"`
	Query    string    `json:"query"`
	Location string    `json:"location,omitempty"`
	Created  time.Time `json:"created_at"`
	Status   string    `json:"status"`
	Count    int       `json:"result_count"`
}

type listResult struct {
	Jobs []historyItem `json:"jobs"`
	Next *int          `json:"next_offset,omitempty"`
}

type store struct{ root string }

// Serve exposes local scrape tools using the MCP stdio transport.
func Serve(ctx context.Context, root string) error {
	if root == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return err
		}

		root = filepath.Join(config, "google-maps-scraper", "mcp")
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}

	s := store{root: root}
	server := mcp.NewServer(&mcp.Implementation{Name: "google-maps-scraper"}, &mcp.ServerOptions{
		Instructions: "Use these tools for Google Maps business searches. If start_scrape returns location_choices, ask which place the user means and pass its location_id. " +
			"Use the same job_id for follow-ups. When the user wants the finished CSV, call get_scrape_results while the job is queued or running. " +
			"Report the final result_count and link the returned csv_path. If the job failed, was interrupted, or was cancelled, report its status and label any available export as partial. " +
			"An 'up to' count is a maximum. Answer from the listings, mark missing fields as unavailable, and use external sources only when the user asks for independent research or verification.",
	})
	addTool(server, "start_scrape", "Start a Google Maps business search. Use response=preview for a few rows and available export paths, export for paths only, live for early rows, or job for background work.", s.start)
	addTool(server, "get_scrape_status", "Get an immediate progress snapshot when asked for status.", s.status)
	addTool(server, "list_scrapes", "Find recent searches by business query or place, including previous sessions.", s.list)
	addTool(server, "get_scrape_results", "Read a job's listings and exports. Waits up to 20s per call by default. Pass next_offset as offset for the next page. For live rows, use wait_for=rows.", s.results)
	addTool(server, "cancel_scrape", "Request cancellation and retain completed rows.", s.cancel)

	return server.Run(ctx, &mcp.StdioTransport{})
}

func addTool[In, Out any](server *mcp.Server, name, description string, handler func(context.Context, *In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, &input)
		return nil, output, err
	})
}

func hash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (s store) start(ctx context.Context, in *startInput) (startResult, error) {
	response := in.Response
	switch response {
	case "", "job", "preview", viewExport, "live":
	default:
		return startResult{}, errors.New("response must be job, preview, export, or live")
	}

	result, err := s.startJob(ctx, in)
	if err != nil || result.Job == nil || response == "" || response == "job" {
		return result, err
	}

	read := resultsInput{ID: result.Job.ID}

	switch response {
	case viewExport:
		read.View = viewExport
	case "live":
		read.WaitFor = waitForRows
	}

	page, err := s.results(ctx, &read)
	if err != nil {
		return result, err
	}

	result.Job.progress = page.Job
	result.Result = &page.resultData

	return result, nil
}

func (s store) startJob(ctx context.Context, in *startInput) (startResult, error) {
	var (
		p           plan
		id          string
		requestHash string
	)

	if in.RequestID != "" {
		if len(in.RequestID) > 200 {
			return startResult{}, errors.New("request_id is too long")
		}

		id = hash([]byte(in.RequestID))

		// Presentation can change when a client retries the same job.
		request := *in
		request.Response = ""

		encoded, err := json.Marshal(&request)
		if err != nil {
			return startResult{}, err
		}

		requestHash = hash(encoded)

		existing, err := s.read(id)
		if err == nil {
			return existingStart(&existing, requestHash)
		}

		if !errors.Is(err, os.ErrNotExist) {
			return startResult{}, err
		}
	}

	if in.PreviousJobID != "" {
		previous, err := s.previousPlan(in)
		if err != nil {
			return startResult{}, err
		}

		p = previous
	} else {
		resolved, choices, err := makePlan(in)
		if err != nil {
			return startResult{}, err
		}

		if len(choices) > 0 {
			options := make([]locationChoice, 0, len(choices))
			for _, city := range choices {
				label := city.Name + ", "
				if city.Region != "" {
					label += city.Region + ", "
				}

				options = append(options, locationChoice{ID: city.ID, Label: label + city.CountryName})
			}

			return startResult{Choices: options, Question: "Which location did you mean?"}, nil
		}

		p = resolved
	}

	if id == "" {
		newID, err := uuid.NewRandom()
		if err != nil {
			return startResult{}, err
		}

		id = newID.String()
	}

	if err := ctx.Err(); err != nil {
		return startResult{}, err
	}

	now := time.Now().UTC()
	j := job{ID: id, Plan: p, Status: statusQueued, Created: now, Updated: now, RequestHash: requestHash}

	dir := filepath.Join(s.root, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return startResult{}, err
		}

		existing, readErr := s.read(id)
		if errors.Is(readErr, os.ErrNotExist) {
			return startResult{}, errors.New("job is being created; retry using the same request ID")
		}

		if readErr != nil {
			return startResult{}, readErr
		}

		return existingStart(&existing, requestHash)
	}

	if err := writeState(dir, &j); err != nil {
		_ = os.Remove(dir)
		return startResult{}, err
	}

	if err := launch(dir); err != nil {
		j.Status, j.Error = statusFailed, err.Error()
		if saveErr := writeState(dir, &j); saveErr != nil {
			return startResult{}, errors.Join(err, saveErr)
		}
	}

	return started(&j), nil
}

func (s store) previousPlan(in *startInput) (plan, error) {
	if in.Query != "" || in.Center != nil || in.Location != "" || in.LocationID != "" || in.Bounds != "" ||
		in.RadiusKM != 0 || in.GridCellKM != 0 || in.Coverage != "" || in.Limit != 0 || len(in.Fields) != 0 {
		return plan{}, errors.New("a rerun accepts only a previous job ID, optional mode, and request ID")
	}

	previous, err := s.read(in.PreviousJobID)
	if err != nil {
		return plan{}, err
	}

	p := previous.Plan
	if in.Mode != "" {
		p.Mode, err = modeFor(in.Mode, p.RequiresDetails)
		if err != nil {
			return plan{}, err
		}
	}

	return p, nil
}

func existingStart(j *job, requestHash string) (startResult, error) {
	if j.RequestHash != requestHash {
		return startResult{}, errors.New("request_id already belongs to a different search")
	}

	return started(j), nil
}

func launch(dir string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	logFile, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	command := exec.Command(executable, "-mcp-worker", dir)
	command.Stdout, command.Stderr = logFile, logFile
	detach(command)

	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()

	return nil
}

func (s store) read(id string) (job, error) {
	var j job

	if _, err := uuid.Parse(id); err != nil {
		decoded, hexErr := hex.DecodeString(id)
		if hexErr != nil || len(decoded) != sha256.Size {
			return j, errors.New("invalid job ID")
		}
	}

	if !filepath.IsLocal(id) || filepath.Base(id) != id {
		return j, errors.New("invalid job ID")
	}

	data, err := os.ReadFile(filepath.Join(s.root, id, stateFile))
	if err != nil {
		return j, err
	}

	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}

	if (j.Status == statusQueued || j.Status == statusRunning) && time.Since(j.Updated) > staleAfter {
		info, err := os.Stat(filepath.Join(s.root, id, "worker.lock"))
		if err != nil || time.Since(info.ModTime()) > staleAfter {
			j.Status, j.Error = statusInterrupted, "The worker stopped updating. Start a new run to retry."
		}
	}

	return j, nil
}

func (s store) status(_ context.Context, in *jobInput) (progress, error) {
	j, err := s.read(in.ID)
	return j.progress(), err
}

func (s store) list(_ context.Context, in *listInput) (listResult, error) {
	if in.Limit < 0 || in.Limit > 20 || in.Offset < 0 {
		return listResult{}, errors.New("use a nonnegative offset and limit of at most 20")
	}

	if in.Limit == 0 {
		in.Limit = 5
	}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		return listResult{}, err
	}

	history := make([]historyItem, 0)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		j, err := s.read(entry.Name())
		if err == nil && strings.Contains(normalize(j.Plan.Query+" "+j.Plan.Location), normalize(in.Search)) {
			history = append(history, historyItem{j.ID, j.Plan.Query, j.Plan.Location, j.Created, j.Status, j.Count})
		}
	}

	sort.Slice(history, func(i, j int) bool { return history[i].Created.After(history[j].Created) })
	start := min(len(history), in.Offset)
	end := start + min(len(history)-start, in.Limit)

	result := listResult{Jobs: history[start:end]}
	if end < len(history) {
		result.Next = &end
	}

	return result, nil
}

func (s store) cancel(_ context.Context, in *jobInput) (progress, error) {
	j, err := s.read(in.ID)
	if err != nil || (j.Status != statusQueued && j.Status != statusRunning) {
		return j.progress(), err
	}

	err = os.WriteFile(filepath.Join(s.root, in.ID, "cancel"), nil, 0o600)
	if err == nil {
		j.Status = "cancellation_requested"
	}

	return j.progress(), err
}

func writeState(dir string, j *job) error {
	j.Updated = time.Now().UTC()

	return replaceFile(dir, stateFile, func(file *os.File) error {
		return json.NewEncoder(file).Encode(j)
	})
}

func replaceFile(dir, name string, write func(*os.File) error) error {
	file, err := os.CreateTemp(dir, "."+name)
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	err = write(file)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return os.Rename(file.Name(), filepath.Join(dir, name))
}
