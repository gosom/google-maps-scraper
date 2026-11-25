package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	viewExport        = "export"
	viewFull          = "full"
	waitForRows       = "rows"
	fieldTitle        = "title"
	fieldEmails       = "emails"
	fieldReviews      = "user_reviews"
	fieldExtraReviews = "user_reviews_extended"
)

const (
	defaultPreviewRows = 3
	maxPageRows        = 25
	previewPageBytes   = 8 << 10
	fullPageBytes      = 64 << 10
	maxResultWait      = 20 * time.Second
)

type resultsInput struct {
	ID      string   `json:"job_id"`
	Offset  int      `json:"offset,omitempty"`
	Limit   int      `json:"limit,omitempty" jsonschema:"Page size, default 3, maximum 25."`
	View    string   `json:"view,omitempty" jsonschema:"preview (default), export for paths only, or full for complete records."`
	Fields  []string `json:"fields,omitempty" jsonschema:"Preview columns, e.g. phone, website, emails, rating, reviews. Defaults to fields from the search or a contact summary."`
	WaitFor string   `json:"wait_for,omitempty" jsonschema:"complete (default), rows for the next available row, or none for an immediate snapshot."`
}

type resultsPage struct {
	resultData
	Job progress `json:"job"`
}

type resultData struct {
	Rows    []map[string]any `json:"rows,omitzero"`
	Next    *int             `json:"next_offset,omitempty"`
	CSV     string           `json:"csv_path,omitempty"`
	JSON    string           `json:"jsonl_path,omitempty"`
	Partial bool             `json:"partial"`
}

func (s store) results(ctx context.Context, in *resultsInput) (resultsPage, error) {
	if in.Offset < 0 || in.Limit < 0 || in.Limit > maxPageRows {
		return resultsPage{}, fmt.Errorf("provide a nonnegative offset and limit of at most %d", maxPageRows)
	}

	switch in.View {
	case "", "preview":
	case viewExport, viewFull:
		if len(in.Fields) > 0 {
			return resultsPage{}, errors.New("fields can only be selected for a preview")
		}
	default:
		return resultsPage{}, errors.New("view must be preview, export, or full")
	}

	switch in.WaitFor {
	case "", "complete", waitForRows, "none":
	default:
		return resultsPage{}, errors.New("wait_for must be complete, rows, or none")
	}

	j, err := s.waitForResults(ctx, in, maxResultWait)
	if err != nil {
		return resultsPage{}, err
	}

	page := resultsPage{Job: j.progress(), resultData: resultData{Partial: j.Status != statusCompleted}}
	dir := filepath.Join(s.root, in.ID)

	file, err := os.Open(filepath.Join(dir, jsonResults))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return resultsPage{}, err
	}

	if file != nil {
		defer file.Close()

		page.JSON = file.Name()

		csvPath := filepath.Join(dir, csvResults)
		if _, err := os.Stat(csvPath); err == nil {
			page.CSV = csvPath
		} else if !errors.Is(err, os.ErrNotExist) {
			return resultsPage{}, err
		}
	}

	if in.View == viewExport {
		return page, nil
	}

	page.Rows = []map[string]any{}

	if !j.finished() {
		// At a live EOF the same cursor is valid for the next long poll.
		next := in.Offset
		page.Next = &next
	}

	if file == nil {
		return page, nil
	}

	if err := page.readRows(ctx, file, in, &j); err != nil {
		return resultsPage{}, err
	}

	return page, nil
}

func (j *job) finished() bool {
	return j.Status != statusQueued && j.Status != statusRunning
}

// Wait on the persisted state, so a new MCP session can follow the same job.
func (s store) waitForResults(ctx context.Context, in *resultsInput, maxWait time.Duration) (job, error) {
	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		j, err := s.read(in.ID)
		if err != nil || j.finished() || in.WaitFor == "none" || (in.WaitFor == waitForRows && j.Count > in.Offset) {
			return j, err
		}

		select {
		case <-ctx.Done():
			return job{}, ctx.Err()
		case <-timer.C:
			return s.read(in.ID)
		case <-ticker.C:
		}
	}
}

func (page *resultsPage) readRows(ctx context.Context, file io.Reader, in *resultsInput, j *job) error {
	reader := bufio.NewReader(file)

	limit := in.Limit
	if limit == 0 {
		limit = defaultPreviewRows
	}

	fields := in.Fields
	if len(fields) == 0 {
		fields = j.Plan.Fields
	}

	budget := previewPageBytes
	if in.View == viewFull {
		budget = fullPageBytes
	}

	for index := 0; j.finished() || index < j.Count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		if index < in.Offset {
			continue
		}

		if len(page.Rows) == limit {
			page.Next = &index
			return nil
		}

		var row map[string]any

		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()

		if err := decoder.Decode(&row); err != nil {
			return err
		}

		size := len(line)

		if in.View != viewFull {
			row = previewRow(row, fields)

			data, err := json.Marshal(row)
			if err != nil {
				return err
			}

			size = len(data)
		}

		if size > budget {
			if len(page.Rows) > 0 {
				page.Next = &index
				return nil
			}
			// Even a single Maps record can exceed a client's context window.
			// Identify the exported record and keep its cursor advancing.
			row = map[string]any{fieldTitle: excerpt(row[fieldTitle], 160), "details_in_export": true}
			size = budget
		}

		budget -= size

		page.Rows = append(page.Rows, row)
	}

	if !j.finished() {
		next := in.Offset + len(page.Rows)
		page.Next = &next
	}

	return nil
}

func fieldKey(field string) string {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "name":
		return fieldTitle
	case "email":
		return fieldEmails
	case "website":
		return "web_site"
	case "rating":
		return "review_rating"
	case "reviews", "review_text":
		return fieldReviews
	case "extra_reviews":
		return fieldExtraReviews
	default:
		return strings.ToLower(strings.TrimSpace(field))
	}
}

func previewRow(row map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		fields = []string{"address", "phone", "web_site", fieldEmails, "review_rating", "review_count"}
	}

	result := map[string]any{fieldTitle: excerpt(row[fieldTitle], 160)}

	for _, field := range fields {
		key := fieldKey(field)

		value := row[key]
		if key == fieldTitle || value == nil || value == "" {
			continue
		}

		switch key {
		case fieldReviews, fieldExtraReviews:
			reviews, _ := value.([]any)
			if len(reviews) > 0 {
				result[key] = reviewSample(reviews)
				result["details_in_export"] = true
			}
		default:
			raw, err := json.Marshal(value)
			if err != nil || len(raw) > 2048 {
				result["details_in_export"] = true
				continue
			}

			result[key] = value
		}
	}

	return result
}

func excerpt(value any, limit int) string {
	text, _ := value.(string)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}

	return string([]rune(text)[:limit]) + "…"
}

func reviewSample(reviews []any) []map[string]any {
	sample := make([]map[string]any, 0, 2)

	for _, value := range reviews[:min(2, len(reviews))] {
		review, _ := value.(map[string]any)

		text := excerpt(review["Description"], 240)
		if text == "" {
			text = excerpt(review["text_original"], 240)
		}

		sample = append(sample, map[string]any{"rating": review["Rating"], "text": text})
	}

	return sample
}
