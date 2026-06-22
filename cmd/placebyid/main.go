package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
)

// ====================================================================
// scrapemate.ResultWriter that emits Google-format JSON Lines (CLI mode)
// ====================================================================

type googleFormatWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func newGoogleFormatWriter(f *os.File) *googleFormatWriter {
	return &googleFormatWriter{w: bufio.NewWriter(f)}
}

func (g *googleFormatWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	defer g.w.Flush()

	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			continue
		}
		placeID := job.GetParentID()
		if placeID == "" {
			placeID = job.GetID()
		}

		entries, err := asEntries(result.Data)
		if err != nil {
			log.Printf("[%s] convert: %v", placeID, err)
			continue
		}
		for _, e := range entries {
			gp := convertEntry(e, placeID)
			buf, err := json.Marshal(gp)
			if err != nil {
				log.Printf("[%s] marshal: %v", placeID, err)
				continue
			}
			g.mu.Lock()
			g.w.Write(buf)
			g.w.WriteByte('\n')
			g.w.Flush() // flush per record so streaming consumers see results live
			g.mu.Unlock()
		}
	}
	return nil
}

var resultsFilePattern = regexp.MustCompile(`^results(\d+)_\d{4}_\d{4}\.(json|csv)$`)

// nextNumberedResultsPath returns results{N}_MMDD_HHMM.ext in dir.
func nextNumberedResultsPath(dir string, jsonOutput bool) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("abs dir %q: %w", dir, err)
	}

	ext := ".csv"
	if jsonOutput {
		ext = ".json"
	}

	log.Printf("[output] auto-naming: scan dir=%s (from %q), ext=%s", absDir, dir, ext)

	maxN := 0
	var matched []string
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("read dir %q: %w", absDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := resultsFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			log.Printf("[output] skip %q: bad number %q", e.Name(), m[1])
			continue
		}
		matched = append(matched, fmt.Sprintf("%s (N=%d)", e.Name(), n))
		if n > maxN {
			maxN = n
		}
	}

	if len(matched) == 0 {
		log.Printf("[output] no existing results{N}_MMDD_HHMM files in %s", absDir)
	} else {
		log.Printf("[output] matched %d file(s): %s", len(matched), strings.Join(matched, ", "))
	}

	nextN := maxN + 1
	stamp := time.Now().Format("0102_1504")
	name := fmt.Sprintf("results%d_%s%s", nextN, stamp, ext)
	outPath := filepath.Join(absDir, name)
	log.Printf("[output] maxN=%d -> nextN=%d, stamp=%s, file=%s", maxN, nextN, stamp, outPath)
	return outPath, nil
}

// ====================================================================
// main
// ====================================================================

func main() {
	var (
		inputFile    string
		outputFile   string
		concurrency  int
		langCode     string
		extractEmail bool
		extraReviews bool
		proxies      string
		inactivity   time.Duration
		serve        bool
		port         int
	)

	flag.StringVar(&inputFile, "input", "place_ids.txt", "input file with one place_id per line")
	flag.StringVar(&outputFile, "results", "auto", "output: 'auto' (resultsN_MMDD_HHMM.json), 'stdout', or a file path")
	flag.IntVar(&concurrency, "c", 2, "concurrency")
	flag.StringVar(&langCode, "lang", "en", "language code")
	flag.BoolVar(&extractEmail, "email", false, "crawl business website for emails")
	flag.BoolVar(&extraReviews, "extra-reviews", false, "fetch up to ~300 reviews per place")
	flag.StringVar(&proxies, "proxies", "", "comma-separated proxies")
	flag.DurationVar(&inactivity, "exit-on-inactivity", 3*time.Minute, "stop after idle")
	flag.BoolVar(&serve, "serve", false, "run as HTTP API server (GET /v1/places/{placeId})")
	flag.IntVar(&port, "port", 3001, "port for HTTP server (used with -serve)")
	flag.Parse()

	if serve {
		runServer(port, concurrency, langCode, extractEmail, extraReviews, proxies, inactivity)
		return
	}

	// Read place IDs
	f, err := os.Open(inputFile)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	var placeIDs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		placeIDs = append(placeIDs, line)
	}
	f.Close()
	if len(placeIDs) == 0 {
		log.Fatal("no place IDs in input")
	}

	// Output (auto: results1_0519_1430.json, results2_..., etc.)
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	log.Printf("[output] cwd=%s, -results=%q", cwd, outputFile)

	out := os.Stdout
	if outputFile != "stdout" {
		path := outputFile
		switch {
		case path == "" || path == "auto":
			log.Printf("[output] mode=auto (empty or \"auto\")")
			path, err = nextNumberedResultsPath(".", true)
			if err != nil {
				log.Fatalf("resolve output path: %v", err)
			}
		default:
			log.Printf("[output] mode=explicit path=%q", path)
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
				log.Printf("[output] resolved relative path -> %s", path)
			}
		}
		log.Printf("[output] creating file: %s", path)
		out, err = os.Create(path)
		if err != nil {
			log.Fatalf("create output: %v", err)
		}
		defer out.Close()
		if st, err := out.Stat(); err != nil {
			log.Printf("[output] warn: stat after create: %v", err)
		} else {
			log.Printf("[output] ready: %s (size=%d bytes)", path, st.Size())
		}
	} else {
		log.Printf("[output] mode=stdout")
	}

	writers := []scrapemate.ResultWriter{newGoogleFormatWriter(out)}

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(concurrency),
		scrapemateapp.WithExitOnInactivity(inactivity),
		scrapemateapp.WithJS(scrapemateapp.DisableImages()),
		scrapemateapp.WithPageReuseLimit(2),
		scrapemateapp.WithPageReuseLimit(200),
	}
	if proxies != "" {
		opts = append(opts, scrapemateapp.WithProxies(strings.Split(proxies, ",")))
	}

	matecfg, err := scrapemateapp.NewConfig(writers, opts...)
	if err != nil {
		log.Fatalf("scrapemate config: %v", err)
	}
	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		log.Fatalf("scrapemate app: %v", err)
	}
	defer app.Close()

	exitMonitor := exiter.New()
	var jobs []scrapemate.IJob
	for _, pid := range placeIDs {
		// place-by-id URL — Google Maps redirects to the place page
		u := fmt.Sprintf("https://www.google.com/maps/place/?q=%s",
			url.QueryEscape("place_id:"+pid))
		job := gmaps.NewPlaceJob(pid, langCode, u, extractEmail, extraReviews,
			gmaps.WithPlaceJobExitMonitor(exitMonitor))
		jobs = append(jobs, job)
	}
	exitMonitor.SetSeedCount(len(jobs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exitMonitor.SetCancelFunc(cancel)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigChan; cancel() }()

	go exitMonitor.Run(ctx)

	fmt.Fprintf(os.Stderr, "scraping %d place(s), concurrency=%d\n", len(jobs), concurrency)
	if err := app.Start(ctx, jobs...); err != nil && err != context.Canceled {
		log.Fatalf("scrape: %v", err)
	}
}
