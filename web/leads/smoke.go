package leads

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

// A smoke check is one small job run through a collector, then checked end to end: that it
// finished in time, wrote rows that identify their place and search, reached the lead list, said
// what happened to every email search, and left one ledger row per search. It is what to run after
// a deploy to know both collectors still work.

// CSVFacts are what a smoke check reads from a job's CSV.
type CSVFacts struct {
	Rows            int
	MissingPlace    int // rows without a title, or without both place_id and link
	WrongSearch     int // rows whose input_id is not one of the job's searches
	MissingStatus   int // rows without an email_status (or a file without the column)
	HasStatusColumn bool
	Found           int
	FetchFailed     int
	Attempted       int
	Places          map[string]bool // distinct place_id (or link)
}

// ReadCSVFacts reads a job's CSV for a smoke check; a missing file has no rows.
func ReadCSVFacts(csvPath string, searches []string) (CSVFacts, error) {
	facts := CSVFacts{Places: map[string]bool{}}

	f, err := os.Open(csvPath)
	if errors.Is(err, os.ErrNotExist) {
		return facts, nil
	}

	if err != nil {
		return facts, err
	}

	defer f.Close()

	t, err := newCSVTable(f)
	if t == nil || err != nil {
		return facts, err
	}

	facts.HasStatusColumn = t.has("email_status")

	want := map[string]bool{}
	for _, s := range searches {
		want[strings.TrimSpace(s)] = true
	}

	for {
		rec, err := t.next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return facts, err
		}

		facts.Rows++

		id := t.get(rec, "place_id")
		if id == "" {
			id = t.get(rec, "link")
		}

		if t.get(rec, "title") == "" || id == "" {
			facts.MissingPlace++
		} else {
			facts.Places[id] = true
		}

		if !want[searchOf(t.get(rec, "input_id"))] {
			facts.WrongSearch++
		}

		status := t.get(rec, "email_status")

		switch {
		case status == "":
			facts.MissingStatus++
		case status == gmaps.EmailFound:
			facts.Found++
			facts.Attempted++
		case status == gmaps.EmailNone:
			facts.Attempted++
		case strings.HasPrefix(status, gmaps.EmailFetchFailed):
			facts.FetchFailed++
			facts.Attempted++
		}
	}

	return facts, nil
}

// SmokeInput is everything a smoke check is judged on.
type SmokeInput struct {
	Collector   string
	JobStatus   string
	Wall        time.Duration
	Bound       time.Duration
	Searches    []string
	CSV         CSVFacts
	LeadsLinked int // leads the lead list links to the job
	Runs        []RunRow
	Blocks      int
	// v2's settings; zero for v1.
	QueryBudget time.Duration
	Delay       time.Duration
}

// EvaluateSmoke is the smoke check's checklist; passed is every check that can fail passing.
func EvaluateSmoke(in *SmokeInput) (checks []Check, passed bool) {
	add := func(name string, ok bool, detail string) {
		result := CheckPass
		if !ok {
			result = CheckFail
		}

		checks = append(checks, Check{Name: name, Result: result, Detail: detail})
	}

	add("Job finished ok", in.JobStatus == jobStatusOK, "status "+in.JobStatus)
	add("Finished inside its time bound", in.Bound <= 0 || in.Wall <= in.Bound,
		fmt.Sprintf("%s of %s", in.Wall.Round(time.Second), in.Bound.Round(time.Second)))
	add("Wrote at least one row", in.CSV.Rows > 0, fmt.Sprintf("%d rows", in.CSV.Rows))
	add("Every row names its place (title, and place_id or link)", in.CSV.Rows > 0 && in.CSV.MissingPlace == 0,
		fmt.Sprintf("%d missing", in.CSV.MissingPlace))
	add("Every row carries its search in input_id", in.CSV.Rows > 0 && in.CSV.WrongSearch == 0,
		fmt.Sprintf("%d wrong", in.CSV.WrongSearch))
	add("Every place reached the lead list", len(in.CSV.Places) > 0 && in.LeadsLinked >= len(in.CSV.Places),
		fmt.Sprintf("%d of %d", in.LeadsLinked, len(in.CSV.Places)))
	add("Every row says what its email search found (email_status)",
		in.CSV.Rows > 0 && in.CSV.HasStatusColumn && in.CSV.MissingStatus == 0, fmt.Sprintf("%d without", in.CSV.MissingStatus))

	emails := Check{Name: "Found at least one email", Result: CheckPass,
		Detail: fmt.Sprintf("%d of %d sites", in.CSV.Found, in.CSV.Attempted)}
	if in.CSV.Found == 0 {
		emails.Result = CheckWarn
		emails.Detail += fmt.Sprintf("; %d could not be read", in.CSV.FetchFailed)
	}

	checks = append(checks, emails)

	method := V1Method
	if in.Collector == gmaps.CollectorV2 {
		method = gmaps.SteadyMethod
	}

	rightMethod := len(in.Runs) == len(in.Searches)
	for i := range in.Runs {
		rightMethod = rightMethod && in.Runs[i].Method == method
	}

	add("One ledger row per search", rightMethod, fmt.Sprintf("%d rows for %d searches (%s)", len(in.Runs), len(in.Searches), method))

	blocks := Check{Name: "No Google refusals", Result: CheckPass, Detail: fmt.Sprintf("%d", in.Blocks)}
	if in.Blocks > 0 {
		blocks.Result = CheckWarn
	}

	checks = append(checks, blocks)

	if in.Collector == gmaps.CollectorV2 {
		checks = append(checks, v2SmokeChecks(in)...)
	}

	passed = true

	for _, c := range checks {
		passed = passed && c.Result != CheckFail
	}

	return checks, passed
}

// v2SmokeChecks are the checks only v2 promises: each search inside its limit, and websites read
// one at a time at least Delay apart.
func v2SmokeChecks(in *SmokeInput) []Check {
	slowest, closest := 0.0, time.Duration(0)

	for i := range in.Runs {
		slowest = max(slowest, in.Runs[i].Seconds())

		if g := in.Runs[i].MinSiteGap; g > 0 && (closest == 0 || g < closest) {
			closest = g
		}
	}

	limit := in.QueryBudget + time.Second
	inLimit := Check{Name: "Every search finished inside its time limit", Result: CheckPass,
		Detail: fmt.Sprintf("slowest %.0fs of %s", slowest, in.QueryBudget)}

	if in.QueryBudget > 0 && slowest > limit.Seconds() {
		inLimit.Result = CheckFail
	}

	paced := Check{Name: "Websites were read one at a time, the gap apart", Result: CheckPass,
		Detail: fmt.Sprintf("closest %s, gap %s", closest.Round(time.Millisecond), in.Delay)}

	if closest > 0 && closest < in.Delay {
		paced.Result = CheckFail
	}

	return []Check{inLimit, paced}
}
