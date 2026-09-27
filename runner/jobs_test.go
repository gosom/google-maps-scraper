package runner_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/grid"
	"github.com/AxioIntel/Axio-CRED/runner"
)

func TestCreateGridSeedJobsRejectsInvalidZoom(t *testing.T) {
	t.Parallel()

	bbox := grid.BoundingBox{
		MinLat: 40.30,
		MinLon: -3.80,
		MaxLat: 40.50,
		MaxLon: -3.60,
	}

	_, err := runner.CreateGridSeedJobs(
		"en",
		strings.NewReader("coffee"),
		10,
		false,
		bbox,
		1.0,
		0,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid zoom level") {
		t.Fatalf("expected invalid zoom level error, got %v", err)
	}
}

func TestCreateSeedJobsRejectsEmptyQueryBeforeCustomID(t *testing.T) {
	t.Parallel()

	_, err := runner.CreateSeedJobs(
		false,
		"en",
		strings.NewReader("  #!#my-id\n"),
		10,
		false,
		"",
		15,
		10000,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "empty query text") {
		t.Fatalf("expected empty query text error, got %v", err)
	}
}

func TestCreateGridSeedJobsRejectsEmptyQueryBeforeCustomID(t *testing.T) {
	t.Parallel()

	bbox := grid.BoundingBox{
		MinLat: 40.30,
		MinLon: -3.80,
		MaxLat: 40.50,
		MaxLon: -3.60,
	}

	_, err := runner.CreateGridSeedJobs(
		"en",
		strings.NewReader(" #!#my-id\n"),
		10,
		false,
		bbox,
		1.0,
		15,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "empty query text") {
		t.Fatalf("expected empty query text error, got %v", err)
	}
}

func TestReadSteadyQueriesTagsEachSearchWithItself(t *testing.T) {
	t.Parallel()

	got, err := runner.ReadSteadyQueries(strings.NewReader("dentist in Austin TX\n\n  plumber in Waco TX#!#waco  \n"))
	require.NoError(t, err)
	assert.Equal(t, []gmaps.SteadyQuery{
		{ID: "dentist in Austin TX", Text: "dentist in Austin TX"},
		{ID: "waco", Text: "plumber in Waco TX"},
	}, got)
}
