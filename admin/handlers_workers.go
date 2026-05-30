package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/log"
	"github.com/gosom/google-maps-scraper/rqueue"
)

var validWorkerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{1,61}[a-zA-Z0-9]$`)

// WorkerHealth holds the cached health check result for a worker.
type WorkerHealth struct {
	Reachable        bool    `json:"reachable"`
	Status           string  `json:"status"`
	Uptime           string  `json:"uptime"`
	JobsProcessed    int64   `json:"jobs_processed"`
	ResultsCollected int64   `json:"results_collected"`
	ResultsPerMinute float64 `json:"results_per_minute"`
	Concurrency      int     `json:"concurrency"`
	CheckedAt        string  `json:"checked_at"`
}

// WorkerView extends ProvisionedResource with health info, SSH command, and error details for the template.
type WorkerView struct {
	ProvisionedResource
	Health       *WorkerHealth
	SSHCmd       string
	ErrorMessage string
}

// buildWorkerViews converts a list of ProvisionedResource into WorkerViews
// with health info, SSH command, and error details extracted from metadata.
func buildWorkerViews(resources []ProvisionedResource) []WorkerView {
	views := make([]WorkerView, 0, len(resources))

	for i := range resources {
		wr := &resources[i]
		view := WorkerView{ProvisionedResource: *wr}

		// Read cached health from metadata
		if healthRaw, ok := wr.Metadata["health"]; ok {
			healthJSON, err := json.Marshal(healthRaw)
			if err == nil {
				var health WorkerHealth
				if json.Unmarshal(healthJSON, &health) == nil {
					view.Health = &health
				}
			}
		}

		// Error message from metadata
		if errMsg, ok := wr.Metadata["error"]; ok {
			if s, ok := errMsg.(string); ok {
				view.ErrorMessage = s
			}
		}

		// SSH connection command
		if wr.IPAddress != "" {
			view.SSHCmd = "ssh -p 2222 -i <private_key_file> root@" + wr.IPAddress
		}

		views = append(views, view)
	}

	return views
}

// workerStreamPayload is the JSON payload sent via SSE.
type workerStreamPayload struct {
	Workers []WorkerView `json:"workers"`
}

// WorkersStreamHandler streams worker data via Server-Sent Events.
func WorkersStreamHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		sendEvent := func() error {
			resources, err := appState.Store.ListProvisionedResources(r.Context(), "")
			if err != nil {
				return err
			}

			views := buildWorkerViews(resources)
			payload := workerStreamPayload{Workers: views}

			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}

			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return err
			}

			flusher.Flush()

			return nil
		}

		// Send initial event immediately.
		if err := sendEvent(); err != nil {
			log.Error("workers stream: initial send failed", "error", err)
			return
		}

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if err := sendEvent(); err != nil {
					log.Error("workers stream: send failed", "error", err)
					return
				}
			}
		}
	}
}

// WorkersPageHandler renders the workers management page.
func WorkersPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		workers, err := appState.Store.ListProvisionedResources(r.Context(), "")
		if err != nil {
			log.Error("failed to list workers", "error", err)
			http.Redirect(w, r, "/admin/?error=Failed+to+load+workers", http.StatusSeeOther)

			return
		}

		workerViews := buildWorkerViews(workers)

		// Check if SSH key pair exists
		sshKeyExists := false
		sshKeyCfg, _ := appState.Store.GetConfig(r.Context(), AppKeyPairKey)

		if sshKeyCfg != nil && sshKeyCfg.Value != "" {
			sshKeyExists = true
		}

		data := map[string]any{
			"Workers":           workerViews,
			"SSHKeyExists":      sshKeyExists,
			"DOConfigured":      false,
			"HetznerConfigured": false,
			"Success":           r.URL.Query().Get("success"),
			"Error":             r.URL.Query().Get("error"),
		}

		// Check DO token
		doToken, _ := appState.Store.GetConfig(r.Context(), DOTokenKey)
		if doToken != nil && doToken.Value != "" {
			data["DOConfigured"] = true

			if prov, err := infra.NewWorkerProvisioner("digitalocean", doToken.Value); err == nil {
				if regions, err := prov.ListRegions(r.Context()); err == nil {
					data["DORegions"] = regions
				}

				if sizes, err := prov.ListSizes(r.Context()); err == nil {
					data["DOSizes"] = sizes
				}
			}
		}

		// Check Hetzner token
		hetznerToken, _ := appState.Store.GetConfig(r.Context(), HetznerTokenKey)
		if hetznerToken != nil && hetznerToken.Value != "" {
			data["HetznerConfigured"] = true

			if prov, err := infra.NewWorkerProvisioner("hetzner", hetznerToken.Value); err == nil {
				if regions, err := prov.ListRegions(r.Context()); err == nil {
					data["HetznerRegions"] = regions
				}

				if sizes, err := prov.ListSizes(r.Context()); err == nil {
					data["HetznerSizes"] = sizes
				}
			}
		}

		doConfigured, _ := data["DOConfigured"].(bool)
		hetznerConfigured, _ := data["HetznerConfigured"].(bool)
		data["AnyConfigured"] = doConfigured || hetznerConfigured

		renderTemplate(appState, w, r, "workers.html", data)
	}
}

// SaveProviderTokenHandler saves a cloud provider API token.
func SaveProviderTokenHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		provider := r.FormValue("provider")
		token := r.FormValue("token")

		if token == "" {
			http.Redirect(w, r, "/admin/workers?error=API+token+is+required", http.StatusSeeOther)
			return
		}

		var configKey string

		switch provider {
		case "digitalocean":
			configKey = DOTokenKey
		case "hetzner":
			configKey = HetznerTokenKey
		default:
			http.Redirect(w, r, "/admin/workers?error=Unknown+provider", http.StatusSeeOther)
			return
		}

		cfg := &AppConfig{Key: configKey, Value: token}
		if err := appState.Store.SetConfig(r.Context(), cfg, true); err != nil {
			log.Error("failed to save provider token", "error", err, "provider", provider)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+save+API+token", http.StatusSeeOther)

			return
		}

		http.Redirect(w, r, "/admin/workers?success=API+token+saved+for+"+provider, http.StatusSeeOther)
	}
}

// ProvisionWorkerHandler handles worker provisioning requests.
func ProvisionWorkerHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		provider := r.FormValue("provider")
		name := r.FormValue("name")
		region := r.FormValue("region")
		size := r.FormValue("size")

		if name == "" || region == "" || size == "" || provider == "" {
			http.Redirect(w, r, "/admin/workers?error=Provider,+name,+region+and+size+are+required", http.StatusSeeOther)
			return
		}

		if !validWorkerName.MatchString(name) {
			http.Redirect(w, r, "/admin/workers?error=Name+must+be+3-63+chars,+alphanumeric+and+hyphens+only", http.StatusSeeOther)
			return
		}

		concurrency, _ := strconv.Atoi(r.FormValue("concurrency"))
		if concurrency <= 0 {
			concurrency = 8
		}

		maxJobsPerCycle, _ := strconv.Atoi(r.FormValue("max_jobs_per_cycle"))
		if maxJobsPerCycle <= 0 {
			maxJobsPerCycle = 100
		}

		fastMode := r.FormValue("fast_mode") == "on"
		proxies := r.FormValue("proxies")

		// Validate provider token is configured
		var configKey string

		switch provider {
		case "digitalocean":
			configKey = DOTokenKey
		case "hetzner":
			configKey = HetznerTokenKey
		default:
			http.Redirect(w, r, "/admin/workers?error=Unknown+provider", http.StatusSeeOther)
			return
		}

		tokenCfg, err := appState.Store.GetConfig(r.Context(), configKey)
		if err != nil || tokenCfg == nil || tokenCfg.Value == "" {
			http.Redirect(w, r, "/admin/workers?error=Provider+API+token+not+configured", http.StatusSeeOther)
			return
		}

		// Ensure SSH key pair exists (generate if needed)
		sshKeyCfg, _ := appState.Store.GetConfig(r.Context(), AppKeyPairKey)
		if sshKeyCfg == nil || sshKeyCfg.Value == "" {
			pub, priv, err := cryptoext.GenerateSSHKey()
			if err != nil {
				log.Error("failed to generate SSH key", "error", err)
				http.Redirect(w, r, "/admin/workers?error=Failed+to+generate+SSH+key", http.StatusSeeOther)

				return
			}

			keyJSON, _ := json.Marshal(infra.SSHKey{Pub: pub, Key: priv})

			cfg := &AppConfig{Key: AppKeyPairKey, Value: string(keyJSON)}
			if err := appState.Store.SetConfig(r.Context(), cfg, true); err != nil {
				log.Error("failed to save SSH key", "error", err)
				http.Redirect(w, r, "/admin/workers?error=Failed+to+save+SSH+key", http.StatusSeeOther)

				return
			}
		}

		// Create provisioned_resources record
		tempResourceID := "pending-" + cryptoext.GenerateRandomHex(8)

		resource := &ProvisionedResource{
			Provider:     provider,
			ResourceType: "worker",
			ResourceID:   tempResourceID,
			Name:         name,
			Region:       region,
			Size:         size,
			Status:       "provisioning",
			Metadata:     map[string]any{},
		}

		created, err := appState.Store.CreateProvisionedResource(r.Context(), resource)
		if err != nil {
			log.Error("failed to create provisioned resource", "error", err)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+create+worker+record", http.StatusSeeOther)

			return
		}

		// Insert River job — NO secrets in args; worker fetches them from app_config
		jobArgs := rqueue.WorkerProvisionArgs{
			ResourceID:      created.ID,
			Provider:        provider,
			Name:            name,
			Region:          region,
			Size:            size,
			Concurrency:     concurrency,
			MaxJobsPerCycle: maxJobsPerCycle,
			FastMode:        fastMode,
			Proxies:         proxies,
		}

		if err := appState.RQueueClient.InsertWorkerProvisionJob(r.Context(), jobArgs); err != nil {
			log.Error("failed to insert provision job", "error", err)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+queue+provisioning+job", http.StatusSeeOther)

			return
		}

		log.Info("audit", "action", "worker_provision", "user_id", session.UserID, "provider", provider, "name", name, "ip", r.RemoteAddr)

		http.Redirect(w, r, "/admin/workers?success=Worker+provisioning+started", http.StatusSeeOther)
	}
}

// DownloadSSHKeyHandler serves the SSH private or public key as a file download.
func DownloadSSHKeyHandler(appState *AppState, keyType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		sshKeyCfg, err := appState.Store.GetConfig(r.Context(), AppKeyPairKey)
		if err != nil || sshKeyCfg == nil || sshKeyCfg.Value == "" {
			log.Error("SSH key pair not found")
			http.Redirect(w, r, "/admin/workers?error=SSH+key+pair+not+found", http.StatusSeeOther)

			return
		}

		var sshKey infra.SSHKey
		if err := json.Unmarshal([]byte(sshKeyCfg.Value), &sshKey); err != nil {
			log.Error("failed to parse SSH key", "error", err)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+read+SSH+key", http.StatusSeeOther)

			return
		}

		var content, filename string

		switch keyType {
		case "private":
			content = sshKey.Key
			filename = "gmapssaas_id_ed25519"
		case "public":
			content = sshKey.Pub
			filename = "gmapssaas_id_ed25519.pub"
		default:
			http.Redirect(w, r, "/admin/workers?error=Invalid+key+type", http.StatusSeeOther)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		_, _ = w.Write([]byte(content))
	}
}

// DeleteWorkerHandler handles worker deletion requests.
func DeleteWorkerHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Redirect(w, r, "/admin/workers?error=Invalid+worker+ID", http.StatusSeeOther)
			return
		}

		resource, err := appState.Store.GetProvisionedResource(r.Context(), id)
		if err != nil {
			log.Error("failed to get provisioned resource", "error", err, "id", id)
			http.Redirect(w, r, "/admin/workers?error=Worker+not+found", http.StatusSeeOther)

			return
		}

		// Update status to deleting
		if err := appState.Store.UpdateProvisionedResourceStatus(r.Context(), id, "deleting", resource.IPAddress); err != nil {
			log.Error("failed to update resource status", "error", err, "id", id)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+update+worker+status", http.StatusSeeOther)

			return
		}

		// Insert River job — NO secrets; worker fetches token from app_config
		jobArgs := rqueue.WorkerDeleteArgs{
			ResourceID:         id,
			Provider:           resource.Provider,
			ProviderResourceID: resource.ResourceID,
		}

		if err := appState.RQueueClient.InsertWorkerDeleteJob(r.Context(), jobArgs); err != nil {
			log.Error("failed to insert delete job", "error", err, "id", id)
			http.Redirect(w, r, "/admin/workers?error=Failed+to+queue+deletion+job", http.StatusSeeOther)

			return
		}

		log.Info("audit", "action", "worker_delete", "user_id", session.UserID, "worker_id", id, "ip", r.RemoteAddr)

		http.Redirect(w, r, "/admin/workers?success=Worker+deletion+started", http.StatusSeeOther)
	}
}
