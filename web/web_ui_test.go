//nolint:testpackage // tests unexported scrape/download handlers and JobData helpers directly
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobDataGeoCoordinatesRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		lat  string
		lon  string
		want string
	}{
		{"both empty", "", "", ""},
		{"zero zero means unset", "0", "0", ""},
		{"genuine zero lat with real lon", "0", "-122.4194", "0,-122.4194"},
		{"real lat with genuine zero lon", "37.7749", "0", "37.7749,0"},
		{"normal pair", "40.30", "-3.80", "40.30,-3.80"},
		{"only lat set", "40.30", "", ""},
		{"only lon set", "", "-3.80", ""},
		{"non numeric lat", "abc", "-3.80", ""},
		{"non numeric lon", "40.30", "abc", ""},
		{"whitespace only", "  ", "  ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := JobData{Lat: tc.lat, Lon: tc.lon}
			if got := d.GeoCoordinates(); got != tc.want {
				t.Fatalf("GeoCoordinates(%q, %q) = %q, want %q", tc.lat, tc.lon, got, tc.want)
			}
		})
	}
}

func TestJobDataValidateOutputFormat(t *testing.T) {
	t.Parallel()

	valid := []string{"", "csv", "json"}
	for _, format := range valid {
		d := JobData{Keywords: []string{"coffee"}, Lang: "en", Depth: 1, MaxTime: 60000000000, OutputFormat: format}
		if err := d.Validate(); err != nil {
			t.Fatalf("Validate with output format %q: unexpected error %v", format, err)
		}
	}

	d := JobData{Keywords: []string{"coffee"}, Lang: "en", Depth: 1, MaxTime: 60000000000, OutputFormat: "xml"}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for unsupported output format xml")
	}
}

func TestJobDataValidateNewOptions(t *testing.T) {
	t.Parallel()

	base := func() JobData {
		return JobData{Keywords: []string{"coffee"}, Lang: "en", Depth: 1, MaxTime: 60000000000}
	}

	baseJob := base()
	if err := baseJob.Validate(); err != nil {
		t.Fatalf("base job must validate: %v", err)
	}

	// grid bbox requires a well formed "minLat,minLon,maxLat,maxLon"
	withBBox := base()
	withBBox.GridBBox = "40.30,-3.80,40.50"
	if err := withBBox.Validate(); err == nil {
		t.Fatal("expected error for malformed grid bbox")
	}

	goodBBox := base()
	goodBBox.GridBBox = "40.30,-3.80,40.50,-3.60"
	if err := goodBBox.Validate(); err != nil {
		t.Fatalf("valid grid bbox must validate: %v", err)
	}

	// grid and fastmode are incompatible (same rule as the CLI)
	gridFast := goodBBox
	gridFast.FastMode = true
	if err := gridFast.Validate(); err == nil {
		t.Fatal("expected error for grid bbox combined with fast mode")
	}

	// negative scalars are rejected
	negConcurrency := base()
	negConcurrency.Concurrency = -1
	if err := negConcurrency.Validate(); err == nil {
		t.Fatal("expected error for negative concurrency")
	}

	negPages := base()
	negPages.PagesPerBrowser = -2
	if err := negPages.Validate(); err == nil {
		t.Fatal("expected error for negative pages per browser")
	}

	negPool := base()
	negPool.BrowserPoolSize = -3
	if err := negPool.Validate(); err == nil {
		t.Fatal("expected error for negative browser pool size")
	}

	negCell := goodBBox
	negCell.GridCellKm = -1
	if err := negCell.Validate(); err == nil {
		t.Fatal("expected error for negative grid cell size")
	}
}

func TestScrapeParsesNewFormFields(t *testing.T) {
	repo := &mutableJobRepo{mockJobRepo: mockJobRepo{}}
	srv := newTestServerWithRepo(t, repo)

	form := url.Values{
		"name":               {"web-ui-parity"},
		"maxtime":            {"4m"},
		"keywords":           {"coffee"},
		"lang":               {"en"},
		"zoom":               {"15"},
		"radius":             {"10000"},
		"latitude":           {""},
		"longitude":          {""},
		"depth":              {"10"},
		"output":             {"json"},
		"extrareviews":       {"on"},
		"concurrency":        {"4"},
		"grid_bbox":          {"40.30,-3.80,40.50,-3.60"},
		"grid_cell":          {"2.5"},
		"pages_per_browser":  {"2"},
		"browser_pool_size":  {"3"},
		"disable_page_reuse": {"on"},
	}

	req := httptest.NewRequest(http.MethodPost, "/scrape", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.scrape(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(repo.jobs) != 1 {
		t.Fatalf("expected 1 stored job, got %d", len(repo.jobs))
	}

	data := repo.jobs[0].Data

	switch {
	case data.OutputFormat != "json":
		t.Errorf("OutputFormat = %q, want json", data.OutputFormat)
	case !data.ExtraReviews:
		t.Error("ExtraReviews = false, want true")
	case data.Concurrency != 4:
		t.Errorf("Concurrency = %d, want 4", data.Concurrency)
	case data.GridBBox != "40.30,-3.80,40.50,-3.60":
		t.Errorf("GridBBox = %q", data.GridBBox)
	case data.GridCellKm != 2.5:
		t.Errorf("GridCellKm = %v, want 2.5", data.GridCellKm)
	case data.PagesPerBrowser != 2:
		t.Errorf("PagesPerBrowser = %d, want 2", data.PagesPerBrowser)
	case data.BrowserPoolSize != 3:
		t.Errorf("BrowserPoolSize = %d, want 3", data.BrowserPoolSize)
	case !data.DisablePageReuse:
		t.Error("DisablePageReuse = false, want true")
	case data.Lat != "" || data.Lon != "":
		t.Errorf("empty form coordinates must stay empty, got lat=%q lon=%q", data.Lat, data.Lon)
	}
}

func TestScrapeRejectsUnknownOutputFormat(t *testing.T) {
	repo := &mutableJobRepo{mockJobRepo: mockJobRepo{}}
	srv := newTestServerWithRepo(t, repo)

	form := url.Values{
		"name":     {"bad-format"},
		"maxtime":  {"4m"},
		"keywords": {"coffee"},
		"lang":     {"en"},
		"zoom":     {"15"},
		"radius":   {"10000"},
		"depth":    {"10"},
		"output":   {"parquet"},
	}

	req := httptest.NewRequest(http.MethodPost, "/scrape", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.scrape(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(repo.jobs) != 0 {
		t.Fatalf("job must not be stored, got %d", len(repo.jobs))
	}
}

func TestIndexDefaultsLatitudeLongitudeEmpty(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	srv.index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{`id="latitude" name="latitude" value=""`, `id="longitude" name="longitude" value=""`} {
		if !strings.Contains(body, want) {
			t.Fatalf("index body missing %q", want)
		}
	}
}

func TestIndexRendersNewControls(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	srv.index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`name="output"`,
		`name="extrareviews"`,
		`name="concurrency"`,
		`name="grid_bbox"`,
		`name="grid_cell"`,
		`name="pages_per_browser"`,
		`name="browser_pool_size"`,
		`name="disable_page_reuse"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index body missing form control %q", want)
		}
	}
}

func TestDownloadServesJSONContentType(t *testing.T) {
	dir := t.TempDir()
	id := "33333333-3333-3333-3333-333333333333"

	jsonBody := []byte(`[{"title":"Cafe","owner":{"id":"1","name":"Jane","link":"x"}}]`)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), jsonBody, 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	srv := newTestServer(t, dir)

	req := requestWithID(httptest.NewRequest(http.MethodGet, "/download?id="+id, http.NoBody))
	rec := httptest.NewRecorder()
	srv.download(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, id+".json") {
		t.Fatalf("Content-Disposition = %q, want it to contain %s.json", got, id)
	}

	if rec.Body.String() != string(jsonBody) {
		t.Fatalf("body mismatch: %s", rec.Body.String())
	}
}

func TestDownloadCSVBackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	id := "44444444-4444-4444-4444-444444444444"

	csvBody := []byte("title,latitude,longitude\nPlace,1.5,2.5\n")
	if err := os.WriteFile(filepath.Join(dir, id+".csv"), csvBody, 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	srv := newTestServer(t, dir)

	req := requestWithID(httptest.NewRequest(http.MethodGet, "/download?id="+id, http.NoBody))
	rec := httptest.NewRecorder()
	srv.download(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Fatalf("Content-Type = %q, want text/csv", got)
	}
}

func TestDeleteRemovesJSONResult(t *testing.T) {
	dir := t.TempDir()
	id := "55555555-5555-5555-5555-555555555555"

	for _, name := range []string{id + ".json", id + ".csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	repo := &mutableJobRepo{mockJobRepo: mockJobRepo{jobs: []Job{{ID: id}}}}
	svc := NewService(repo, dir)

	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, name := range []string{id + ".json", id + ".csv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after delete", name)
		}
	}
}

func TestIndexRendersPresetAndTemplateControls(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	srv.index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	presetIdx := strings.Index(body, `id="preset"`)
	nameIdx := strings.Index(body, `id="name"`)
	if presetIdx == -1 || nameIdx == -1 || presetIdx > nameIdx {
		t.Fatalf("preset select must exist above Job Name (preset=%d name=%d)", presetIdx, nameIdx)
	}

	if strings.Contains(body, `name="preset"`) {
		t.Fatal("preset select must not have a name attribute (client-only, must not be submitted)")
	}

	for _, want := range []string{
		"Abandoned-listing hunt",
		"Quick scan",
		"Full-area sweep",
		"Deep dive on one spot",
		"Custom",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("preset option label %q missing", want)
		}
	}

	tmplIdx := strings.Index(body, `id="keyword_template"`)
	kwIdx := strings.Index(body, `id="keywords"`)
	if tmplIdx == -1 || kwIdx == -1 || tmplIdx < kwIdx {
		t.Fatalf("keyword template select must exist after the keywords textarea (kw=%d tmpl=%d)", kwIdx, tmplIdx)
	}

	if strings.Contains(body, `name="keyword_template"`) {
		t.Fatal("keyword template select must not have a name attribute (client-only, must not be submitted)")
	}

	for _, want := range []string{
		"Nightclubs — Dubai",
		"Night Lounges — Dubai",
		"Night Bars — Dubai",
		"Arabic Nightlife — Dubai",
		"Luxury Nightlife — Dubai",
		"Live Music Nightlife — Dubai",
		"Late Night Venues — Dubai",
		"Arabic Search Terms — Dubai",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("keyword template label %q missing", want)
		}
	}

	if !strings.Contains(body, "TEMPLATE_CONFIG") {
		t.Fatal("template dropdown must be generated from a centralised TEMPLATE_CONFIG")
	}

	applyIdx := strings.Index(body, `id="apply_template_defaults"`)
	if applyIdx == -1 {
		t.Fatal("apply template defaults button missing")
	}
	if applyIdx < tmplIdx {
		t.Fatalf("apply template defaults button must sit next to the template select (tmpl=%d apply=%d)", tmplIdx, applyIdx)
	}

	revIdx := strings.Index(body, `id="reviews_metadata_only"`)
	if revIdx == -1 {
		t.Fatal("reviews metadata only checkbox missing")
	}
	revInput := body[revIdx:]
	if !strings.HasPrefix(revInput, `id="reviews_metadata_only" name="reviews_metadata_only" checked>`) {
		t.Fatal("reviews metadata only checkbox must default to checked")
	}

	if !strings.Contains(body, "Applies only when coordinates are set") {
		t.Fatal("zoom helper line missing")
	}
	if !strings.Contains(body, "Applies only in Fast Mode") {
		t.Fatal("radius helper line missing")
	}

	for _, want := range []string{"localStorage", "applyPreset"} {
		if !strings.Contains(body, want) {
			t.Fatalf("inline preset script missing %q", want)
		}
	}
}
