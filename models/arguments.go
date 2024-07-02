package models

import (
	"flag"
	"log"
	"runtime"
	"time"
)

type Arguments struct {
	Concurrency              int
	CacheDir                 string
	MaxDepth                 int
	InputFile                string
	ResultsFile              string
	Json                     bool
	LangCode                 string
	Debug                    bool
	Dsn                      string
	ProduceOnly              bool
	ExitOnInactivityDuration time.Duration
	Email                    bool

	ProxyTxtFile string
	UseLatLong   bool
	Api          bool
}

func ParseArgs() (args Arguments) {
	const (
		defaultDepth      = 10
		defaultCPUDivider = 2
	)

	defaultConcurency := runtime.NumCPU() / defaultCPUDivider
	if defaultConcurency < 1 {
		defaultConcurency = 1
	}

	flag.IntVar(&args.Concurrency, "c", defaultConcurency, "sets the concurrency. By default it is set to half of the number of CPUs")
	flag.StringVar(&args.CacheDir, "cache", "cache", "sets the cache directory (no effect at the moment)")
	flag.IntVar(&args.MaxDepth, "depth", defaultDepth, "is how much you allow the scraper to scroll in the search results. Experiment with that value")
	flag.StringVar(&args.ResultsFile, "results", "stdout", "is the path to the file where the results will be written")
	flag.StringVar(&args.InputFile, "input", "stdin", "is the path to the file where the queries are stored (one query per line). By default it reads from stdin")
	flag.StringVar(&args.LangCode, "lang", "en", "is the languate code to use for google (the hl urlparam).Default is en . For example use de for German or el for Greek")
	flag.BoolVar(&args.Debug, "debug", false, "Use this to perform a headfull crawl (it will open a browser window) [only when using without docker]")
	flag.StringVar(&args.Dsn, "dsn", "", "Use this if you want to use a database provider")
	flag.BoolVar(&args.ProduceOnly, "produce", false, "produce seed jobs only (only valid with dsn)")
	flag.DurationVar(&args.ExitOnInactivityDuration, "exit-on-inactivity", 0, "program exits after this duration of inactivity(example value '5m')")
	flag.BoolVar(&args.Json, "json", false, "Use this to produce a json file instead of csv (not available when using db)")
	flag.BoolVar(&args.Email, "email", false, "Use this to extract emails from the websites")
	flag.StringVar(&args.ProxyTxtFile, "proxyfile", "", "Txt file containing the proxies to use (one proxy per line)")
	flag.BoolVar(&args.UseLatLong, "uselatlong", false, "Use Latlong in input file")
	flag.BoolVar(&args.Api, "api", false, "use Api")

	flag.Parse()
	log.Printf("Load flags: %v", args)
	return args
}
