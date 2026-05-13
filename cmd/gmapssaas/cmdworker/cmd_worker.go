package cmdworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/log"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/rqueue"
	saas "github.com/gosom/google-maps-scraper/saas"
	"github.com/gosom/google-maps-scraper/scraper"
)

// workerStats tracks runtime statistics for the health endpoint.
var workerStats struct {
	startedAt   time.Time
	concurrency int
}

// jobsProcessed is an atomic counter for jobs processed.
var jobsProcessed atomic.Int64

// resultsCollected is an atomic counter for total results (places) scraped.
var resultsCollected atomic.Int64

var Command = &cli.Command{
	Name:  "worker",
	Usage: "Start the scraper worker",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "database-url",
			Usage:   "PostgreSQL connection string",
			Value:   "postgres://postgres:postgres@localhost:5432/gmaps_pro?sslmode=disable",
			Sources: cli.EnvVars(saas.EnvDatabaseURL),
		},
		&cli.IntFlag{
			Name:    "concurrency",
			Aliases: []string{"c"},
			Usage:   "Requested worker instances for host provisioning (each process runs one River job at a time)",
			Value:   1,
			Sources: cli.EnvVars(saas.EnvConcurrency),
		},
		&cli.BoolFlag{
			Name:    "fast",
			Usage:   "Enable fast mode (stealth HTTP requests)",
			Value:   false,
			Sources: cli.EnvVars(saas.EnvFastMode),
		},
		&cli.BoolFlag{
			Name:    "debug",
			Usage:   "Enable debug mode (headful browser)",
			Value:   false,
			Sources: cli.EnvVars(saas.EnvDebug),
		},
		&cli.Int64Flag{
			Name:    "max-jobs-per-cycle",
			Usage:   "Maximum jobs before restarting scraper",
			Value:   100,
			Sources: cli.EnvVars(saas.EnvMaxJobsPerCycle),
		},
		&cli.StringFlag{
			Name:    "proxies",
			Usage:   "Comma-separated list of proxy URLs",
			Value:   "",
			Sources: cli.EnvVars(saas.EnvProxies),
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		// Setup shutdown signal handling
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			<-sigChan
			log.Info("shutdown signal received")
			cancel()
		}()

		configuredConcurrency := max(1, cmd.Int("concurrency"))
		concurrency := 1

		// Initialize stats
		workerStats.startedAt = time.Now()
		workerStats.concurrency = concurrency

		// Get configuration
		maxRiverWorkers := 1
		fastMode := cmd.Bool("fast")
		debug := cmd.Bool("debug")
		maxJobsPerCycle := cmd.Int64("max-jobs-per-cycle")
		proxies := parseProxies(cmd.String("proxies"))

		dsn := cmd.String("database-url")
		dbMaxConns := int32(3)
		dbMinConns := int32(1)
		dbPool, err := postgres.Connect(ctx, dsn,
			postgres.WithMaxConns(dbMaxConns),
			postgres.WithMinConns(dbMinConns),
		)
		if err != nil {
			return err
		}
		defer dbPool.Close()

		log.Info("worker configuration",
			"configured_container_instances", configuredConcurrency,
			"scraper_concurrency", concurrency,
			"river_max_workers", maxRiverWorkers,
			"db_max_conns", dbMaxConns,
			"db_min_conns", dbMinConns,
			"fast_mode", fastMode,
			"max_jobs_per_cycle", maxJobsPerCycle,
			"proxy_count", len(proxies),
		)

		// Create the scraper manager
		manager := scraper.NewScraperManager(dbPool, concurrency, fastMode, debug, maxJobsPerCycle, proxies)
		manager.OnJobComplete = IncrementJobsProcessed
		manager.CentralWriter().OnResultsSaved = AddResultsCollected

		// Start health endpoint server
		go runHealthServer(ctx, manager)

		// Create the River client with worker support
		client, err := rqueue.NewWorkerClient(dbPool, manager)
		if err != nil {
			return err
		}

		// Start River client to process jobs
		log.Info("starting River worker")
		if err := client.Start(ctx); err != nil {
			return err
		}

		// Periodically promote retryable jobs for immediate retry.
		client.StartRetryPromoter(ctx)

		// Run the scraper manager (handles scraper lifecycle with restarts)
		log.Info("starting scraper manager")
		if err := manager.Run(ctx); err != nil {
			if ctx.Err() != nil {
				log.Info("scraper manager stopped due to shutdown")
			} else {
				return err
			}
		}

		// Stop River client
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()

		if err := client.Stop(stopCtx); err != nil {
			log.Error("error stopping River client", "error", err)
		}

		log.Info("worker shutdown complete")

		return nil
	},
}

func parseProxies(val string) []string {
	if val == "" {
		return nil
	}

	parts := strings.Split(val, ",")
	proxies := make([]string, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			proxies = append(proxies, p)
		}
	}

	return proxies
}

func runHealthServer(ctx context.Context, manager *scraper.ScraperManager) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		uptime := time.Since(workerStats.startedAt).Truncate(time.Second)

		totalResults := resultsCollected.Load()
		uptimeMinutes := uptime.Minutes()

		var resultsPerMinute float64

		if uptimeMinutes > 0 {
			resultsPerMinute = float64(totalResults) / uptimeMinutes
		}

		watchdog := rqueue.GetScrapeWatchdogMetrics()

		resp := map[string]any{
			"status":             "ok",
			"worker":             "running",
			"uptime":             formatDuration(uptime),
			"jobs_processed":     jobsProcessed.Load(),
			"results_collected":  totalResults,
			"results_per_minute": resultsPerMinute,
			"concurrency":        workerStats.concurrency,
			"river_max_workers":  1,
			"active_jobs":        manager.ActiveJobs(),
			"watchdog": map[string]any{
				"flush_wait_warn_total":   watchdog.FlushWaitWarnTotal,
				"long_runtime_warn_total": watchdog.LongRuntimeWarnTotal,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	// Shutdown server when context is cancelled
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	log.Info("starting health server on :8080")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("health server error", "error", err)
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}

	return fmt.Sprintf("%dm", mins)
}

// IncrementJobsProcessed increments the jobs processed counter.
// Called by the scraper manager after each job completes.
func IncrementJobsProcessed() {
	jobsProcessed.Add(1)
}

// AddResultsCollected adds to the results collected counter.
// Called by the scraper manager after saving results.
func AddResultsCollected(count int) {
	resultsCollected.Add(int64(count))
}
