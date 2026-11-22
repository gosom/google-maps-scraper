package gmaps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestSearchJobPagination(t *testing.T) {
	tests := []struct {
		name      string
		depth     int
		pages     [][2]int // first identity and number of rows
		wantCount int
	}{
		{"one batch", 1, [][2]int{{0, 20}}, 20},
		{"three batches", 3, [][2]int{{0, 20}, {20, 20}, {40, 20}}, 60},
		{"short batch", 10, [][2]int{{0, 20}, {20, 5}}, 25},
		{"empty batch", 10, [][2]int{{0, 20}, {20, 0}}, 20},
		{"first page repeated", 10, [][2]int{{0, 20}, {20, 20}, {0, 20}}, 40},
		{"overlapping pages", 3, [][2]int{{0, 20}, {10, 20}, {20, 20}}, 40},
		{"zero option defaults to one", 0, [][2]int{{0, 20}}, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := &searchMonitor{}
			job := newPaginatedSearch(tt.depth, monitor)
			job.ID = "custom-input"
			seen := make(map[string]bool)

			for page, bounds := range tt.pages {
				require.Contains(t, job.GetURLParams()["pb"], fmt.Sprintf("!7i20!8i%d!", page*20))
				response := searchResponse(t, bounds[0], bounds[1], false)
				data, next, err := job.Process(context.Background(), response)
				require.NoError(t, err)
				require.Nil(t, response.Body)

				entries, ok := data.([]*gmaps.Entry)
				require.True(t, ok)

				for _, entry := range entries {
					require.False(t, seen[entry.PlaceID], "duplicate %s", entry.PlaceID)
					seen[entry.PlaceID] = true
					require.Equal(t, "custom-input", entry.ID)
				}

				if page == len(tt.pages)-1 {
					require.Empty(t, next)
					require.Equal(t, 1, monitor.seeds)

					continue
				}

				require.Zero(t, monitor.seeds, "query must remain active between batches")
				require.Len(t, next, 1)
				child, ok := next[0].(*gmaps.SearchJob)
				require.True(t, ok)
				require.NotEqual(t, job.ID, child.ID)
				require.Equal(t, "custom-input", child.ParentID)
				require.Equal(t, job.GetURLParams()["q"], child.GetURLParams()["q"])
				job = child
			}

			require.Len(t, seen, tt.wantCount)
			require.Equal(t, tt.wantCount, monitor.found)
			require.Equal(t, tt.wantCount, monitor.completed)
		})
	}
}

func TestSearchJobRadiusDoesNotStopPagination(t *testing.T) {
	monitor := &searchMonitor{}
	job := newPaginatedSearch(3, monitor)
	data, next, err := job.Process(context.Background(), searchResponse(t, 0, 20, true))
	require.NoError(t, err)
	require.Empty(t, data)
	require.Len(t, next, 1)
	require.Zero(t, monitor.seeds)
	require.Zero(t, monitor.found)

	data, next, err = next[0].Process(context.Background(), searchResponse(t, 20, 2, false))
	require.NoError(t, err)
	require.Len(t, data, 2)
	require.Empty(t, next)
	require.Equal(t, 1, monitor.seeds)
	require.Equal(t, 2, monitor.found)
}

func TestSearchJobWriterManagedCompletion(t *testing.T) {
	monitor := &searchMonitor{}
	job := newPaginatedSearch(2, monitor)
	gmaps.WithSearchJobWriterManagedCompletion()(job)
	_, next, err := job.Process(context.Background(), searchResponse(t, 0, 20, false))
	require.NoError(t, err)
	require.Zero(t, monitor.seeds)
	require.Zero(t, monitor.completed)
	require.Equal(t, 20, monitor.found)

	child, ok := next[0].(*gmaps.SearchJob)
	require.True(t, ok)
	require.True(t, child.WriterManagedCompletion)
	_, next, err = child.Process(context.Background(), searchResponse(t, 20, 20, false))
	require.NoError(t, err)
	require.Empty(t, next)
	require.Equal(t, 1, monitor.seeds)
	require.Zero(t, monitor.completed, "the writer must acknowledge results")
	require.Equal(t, 40, monitor.found)
}

func TestSearchJobLaterFailure(t *testing.T) {
	tests := []struct {
		name string
		resp scrapemate.Response
	}{
		{"fetch error", scrapemate.Response{Error: errors.New("connection failed")}},
		{"blocked", scrapemate.Response{StatusCode: 429}},
		{"empty body", scrapemate.Response{StatusCode: 200}},
		{"invalid JSON", scrapemate.Response{Body: []byte(")]}'\nnot json")}},
		{"invalid structure", scrapemate.Response{Body: []byte(")]}'\n[[null,42]]")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := &searchMonitor{}
			job := newPaginatedSearch(3, monitor)
			data, next, err := job.Process(context.Background(), searchResponse(t, 0, 20, false))
			require.NoError(t, err)
			require.Len(t, data, 20)
			data, next, err = next[0].Process(context.Background(), &tt.resp)
			require.Error(t, err)
			require.Nil(t, data)
			require.Empty(t, next)
			require.Equal(t, 1, monitor.seeds)
			require.Equal(t, 20, monitor.completed)
		})
	}
}

func TestSearchJobCancellation(t *testing.T) {
	job := newPaginatedSearch(3, &searchMonitor{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, next, err := job.Process(ctx, searchResponse(t, 0, 20, false))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, next)
}

func newPaginatedSearch(depth int, monitor *searchMonitor) *gmaps.SearchJob {
	return gmaps.NewSearchJob(&gmaps.MapSearchParams{
		Query: "cafes", Hl: "en",
		Location: gmaps.MapLocation{Lat: 35, Lon: 33, ZoomLvl: 15, Radius: 10000},
	}, gmaps.WithSearchJobDepth(depth), gmaps.WithSearchJobExitMonitor(monitor))
}

func searchResponse(t *testing.T, start, count int, outside bool) *scrapemate.Response {
	t.Helper()

	items := make([]any, 1, count+1)
	for i := start; i < start+count; i++ {
		business := make([]any, 79)
		business[11] = fmt.Sprintf("Cafe %d", i)
		business[78] = fmt.Sprintf("place-%d", i)

		business[9] = []any{nil, nil, 35, 33}
		if outside {
			business[9] = []any{nil, nil, 0, 0}
		}

		item := make([]any, 15)
		item[14] = business
		items = append(items, item)
	}

	raw, err := json.Marshal([]any{[]any{nil, items}})
	require.NoError(t, err)

	return &scrapemate.Response{StatusCode: 200, Body: append([]byte(")]}'\n"), raw...)}
}

type searchMonitor struct {
	seeds, found, completed int
}

func (m *searchMonitor) SetSeedCount(int)                 {}
func (m *searchMonitor) SetCancelFunc(context.CancelFunc) {}
func (m *searchMonitor) Run(context.Context)              {}
func (m *searchMonitor) IncrSeedCompleted(n int)          { m.seeds += n }
func (m *searchMonitor) IncrPlacesFound(n int)            { m.found += n }
func (m *searchMonitor) IncrPlacesCompleted(n int)        { m.completed += n }
