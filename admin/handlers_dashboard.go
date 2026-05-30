package admin

import (
	"encoding/json"
	"net/http"

	"github.com/gosom/google-maps-scraper/log"
)

// DashboardHandler renders the admin dashboard.
func DashboardHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		user, err := appState.Store.GetUserByID(r.Context(), session.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=User+not+found", http.StatusSeeOther)
			return
		}

		stats, err := appState.RQueueClient.GetDashboardStats(r.Context())
		if err != nil {
			log.Error("failed to get dashboard stats", "error", err)
		}

		data := map[string]any{
			"TOTPEnabled": user.TOTPEnabled,
			"Username":    user.Username,
			"Success":     r.URL.Query().Get("success"),
			"Error":       r.URL.Query().Get("error"),
		}

		if stats != nil {
			data["JobsToday"] = stats.JobsToday
			data["TotalResults"] = stats.TotalResults
		} else {
			data["StatsError"] = "Could not load dashboard stats"
		}

		workers, err := appState.Store.ListProvisionedResources(r.Context(), "")
		if err != nil {
			log.Error("failed to list workers for aggregate stats", "error", err)
		} else {
			var (
				totalWorkers      int
				reachableWorkers  int
				clusterResultsRPM float64
				clusterJobs       int64
			)

			for i := range workers {
				wr := &workers[i]
				if wr.ResourceType != "worker" || wr.DeletedAt != nil {
					continue
				}

				totalWorkers++

				healthRaw, ok := wr.Metadata["health"]
				if !ok {
					continue
				}

				b, err := json.Marshal(healthRaw)
				if err != nil {
					continue
				}

				var health WorkerHealth
				if err := json.Unmarshal(b, &health); err != nil {
					continue
				}

				if health.Reachable {
					reachableWorkers++
				}

				clusterResultsRPM += health.ResultsPerMinute
				clusterJobs += health.JobsProcessed
			}

			data["WorkersTotal"] = totalWorkers
			data["WorkersReachable"] = reachableWorkers
			data["ClusterResultsPerMinute"] = clusterResultsRPM
			data["ClusterJobsProcessed"] = clusterJobs
		}

		renderTemplate(appState, w, r, "dashboard.html", data)
	}
}
