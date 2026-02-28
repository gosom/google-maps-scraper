package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/gosom/google-maps-scraper/log"
)

// JobsPageHandler renders the jobs list page with state filtering and cursor pagination.
func JobsPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		state := r.URL.Query().Get("state")
		cursor := r.URL.Query().Get("cursor")

		result, err := appState.RQueueClient.ListJobs(r.Context(), state, 20, cursor)
		if err != nil {
			log.Error("failed to list jobs", "error", err)
			http.Redirect(w, r, "/admin/?error=Failed+to+list+jobs", http.StatusSeeOther)

			return
		}

		// Build next page URL preserving state filter
		var nextPageURL string

		if result.HasMore && result.NextCursor != "" {
			params := url.Values{}
			params.Set("cursor", result.NextCursor)

			if state != "" {
				params.Set("state", state)
			}

			nextPageURL = "/admin/jobs?" + params.Encode()
		}

		data := map[string]any{
			"Jobs":          result.Jobs,
			"HasMore":       result.HasMore,
			"NextPageURL":   nextPageURL,
			"CurrentState":  state,
			"CurrentCursor": cursor,
			"Success":       r.URL.Query().Get("success"),
			"Error":         r.URL.Query().Get("error"),
		}
		renderTemplate(appState, w, r, "jobs.html", data)
	}
}

// DownloadJobResultsHandler streams job results as a JSON file download.
func DownloadJobResultsHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		jobID := chi.URLParam(r, "job_id")

		results, keyword, err := appState.RQueueClient.GetJobResults(r.Context(), jobID)
		if err != nil {
			log.Error("failed to get job results", "error", err, "job_id", jobID)
			http.Redirect(w, r, "/admin/jobs?error=Failed+to+download+results", http.StatusSeeOther)

			return
		}

		// Build filename from keyword (sanitize for use in Content-Disposition)
		safeName := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}

			return '_'
		}, keyword)
		if safeName == "" {
			safeName = "results"
		}

		filename := jobID + "-" + safeName + ".json"

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		_, _ = w.Write(results)
	}
}

// DeleteJobHandler deletes a job and redirects back to the jobs page.
func DeleteJobHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		jobID := chi.URLParam(r, "job_id")

		if err := appState.RQueueClient.DeleteJob(r.Context(), jobID); err != nil {
			log.Error("failed to delete job", "error", err, "job_id", jobID)
			http.Redirect(w, r, buildJobsRedirectURL(r, "error", "Failed to delete job"), http.StatusSeeOther)

			return
		}

		http.Redirect(w, r, buildJobsRedirectURL(r, "success", "Job deletion queued"), http.StatusSeeOther)
	}
}

// BatchDeleteJobsHandler queues deletion for multiple jobs and redirects back to the jobs page.
func BatchDeleteJobsHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, buildJobsRedirectURL(r, "error", "Invalid form submission"), http.StatusSeeOther)
			return
		}

		jobIDs := r.Form["job_ids"]
		if len(jobIDs) == 0 {
			http.Redirect(w, r, buildJobsRedirectURL(r, "error", "No jobs selected"), http.StatusSeeOther)
			return
		}

		deleted := 0

		for _, jobID := range jobIDs {
			if strings.TrimSpace(jobID) == "" {
				continue
			}

			if err := appState.RQueueClient.DeleteJob(r.Context(), jobID); err != nil {
				log.Error("failed to delete job in batch", "error", err, "job_id", jobID)

				msg := fmt.Sprintf("Batch delete partially failed (%d/%d queued)", deleted, len(jobIDs))
				http.Redirect(w, r, buildJobsRedirectURL(r, "error", msg), http.StatusSeeOther)

				return
			}

			deleted++
		}

		msg := fmt.Sprintf("Queued deletion for %d job(s)", deleted)
		http.Redirect(w, r, buildJobsRedirectURL(r, "success", msg), http.StatusSeeOther)
	}
}

// DeleteAllFilteredJobsHandler queues deletion for all jobs matching the current state filter.
func DeleteAllFilteredJobsHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, buildJobsRedirectURL(r, "error", "Invalid form submission"), http.StatusSeeOther)
			return
		}

		state := strings.TrimSpace(r.FormValue("state"))
		totalQueued, err := queueDeleteAllFiltered(r.Context(), appState, state)

		if err != nil {
			log.Error("failed to delete all filtered jobs", "error", err, "state", state, "queued", totalQueued)
			msg := fmt.Sprintf("Delete all filtered partially failed (%d queued)", totalQueued)
			http.Redirect(w, r, buildJobsStateRedirectURL(state, "error", msg), http.StatusSeeOther)

			return
		}

		msg := fmt.Sprintf("Queued deletion for %d filtered job(s)", totalQueued)
		http.Redirect(w, r, buildJobsStateRedirectURL(state, "success", msg), http.StatusSeeOther)
	}
}

func queueDeleteAllFiltered(ctx context.Context, appState *AppState, state string) (int, error) {
	const (
		pageSize = 100
		maxPages = 2000
	)

	var (
		cursor string
		queued int
	)

	for page := 0; page < maxPages; page++ {
		result, err := appState.RQueueClient.ListJobs(ctx, state, pageSize, cursor)
		if err != nil {
			return queued, err
		}

		if len(result.Jobs) == 0 {
			return queued, nil
		}

		for _, job := range result.Jobs {
			if err := appState.RQueueClient.DeleteJob(ctx, job.JobID); err != nil {
				return queued, err
			}

			queued++
		}

		if !result.HasMore || result.NextCursor == "" {
			return queued, nil
		}

		cursor = result.NextCursor
	}

	return queued, fmt.Errorf("reached safety page limit (%d pages)", maxPages)
}

func buildJobsRedirectURL(r *http.Request, key, value string) string {
	params := url.Values{}
	params.Set(key, value)

	state := r.FormValue("state")
	if state == "" {
		state = r.URL.Query().Get("state")
	}

	if state != "" {
		params.Set("state", state)
	}

	cursor := r.FormValue("cursor")
	if cursor == "" {
		cursor = r.URL.Query().Get("cursor")
	}

	if cursor != "" {
		params.Set("cursor", cursor)
	}

	return "/admin/jobs?" + params.Encode()
}

func buildJobsStateRedirectURL(state, key, value string) string {
	params := url.Values{}
	params.Set(key, value)

	if state != "" {
		params.Set("state", state)
	}

	return "/admin/jobs?" + params.Encode()
}
