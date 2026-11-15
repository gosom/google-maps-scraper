package resume_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/runner/resume"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

func TestCSVAppendWriterWritesHeaderForNewFile(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ids := resume.NewIdentitySet()
	writer := resume.NewCSVAppendWriter(csv.NewWriter(&out), true, ids, nil, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", Title: "A", Link: "https://maps/place/a", PlaceID: "place-a"}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Equal(t, "input_id,link,title,category,address,open_hours,popular_times,website,phone,plus_code,review_count,review_rating,reviews_per_rating,latitude,longitude,cid,status,descriptions,reviews_link,thumbnail,timezone,price_range,data_id,street_view_url,place_id,images,reservations,order_online,menu,owner,complete_address,credit_cards_accepted,about,user_reviews,user_reviews_extended,emails,review_collection", lines[0])
	require.Len(t, lines, 2)
	require.True(t, ids.Has("place-a"))
}

func TestCSVAppendWriterPreservesExistingColumns(t *testing.T) {
	t.Parallel()

	for _, headers := range [][]string{{"input_id", "title", "place_id"}, {"place_id", "title", "input_id"}} {
		t.Run(strings.Join(headers, ","), func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			writer := resume.NewCSVAppendWriter(csv.NewWriter(&out), false, nil, nil, nil, headers)
			in := make(chan scrapemate.Result, 1)
			entry := &gmaps.Entry{ID: "query", Title: "Business", PlaceID: "place"}

			entry.ReviewCollection = &gmaps.ReviewCollection{Status: "limited", Collected: 5, Expected: 92}
			in <- scrapemate.Result{Data: entry}

			close(in)

			require.NoError(t, writer.Run(context.Background(), in))

			rows, err := csv.NewReader(&out).ReadAll()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Len(t, rows[0], len(headers))

			for i, header := range headers {
				require.Equal(t, map[string]string{"input_id": "query", "title": "Business", "place_id": "place"}[header], rows[0][i])
			}
		})
	}
}

func TestCSVAppendWriterReturnsHeaderWriteError(t *testing.T) {
	t.Parallel()

	ids := resume.NewIdentitySet()
	writer := resume.NewCSVAppendWriter(csv.NewWriter(failingWriter{}), true, ids, nil, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", Title: "A", Link: "https://maps/place/a", PlaceID: "place-a"}}

	close(in)

	require.ErrorContains(t, writer.Run(context.Background(), in), "write failed")
	require.False(t, ids.Has("place-a"))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestCSVAppendWriterSkipsHeaderForExistingFile(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ids := resume.NewIdentitySet()
	writer := resume.NewCSVAppendWriter(csv.NewWriter(&out), false, ids, nil, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", Title: "A", Link: "https://maps/place/a", PlaceID: "place-a"}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))

	require.NotContains(t, out.String(), "input_id,link,title")
	require.True(t, ids.Has("place-a"))
}

func TestJSONLAppendWriterWritesOneObjectPerLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ids := resume.NewIdentitySet()
	writer := resume.NewJSONLAppendWriter(&out, ids, nil, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: []*gmaps.Entry{
		{ID: "q1", Title: "A", Link: "https://maps/place/a", PlaceID: "place-a"},
		{ID: "q1", Title: "B", Link: "https://maps/place/b", Cid: "cid-b"},
	}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	require.JSONEq(t, `{"input_id":"q1","link":"https://maps/place/a","title":"A","place_id":"place-a","longitude":0,"categories":null,"open_hours":null,"popular_times":null,"review_count":0,"review_rating":0,"reviews_per_rating":null,"latitude":0,"longtitude":0,"images":null,"reservations":null,"order_online":null,"credit_cards_accepted":null,"about":null,"user_reviews":null,"user_reviews_extended":null,"emails":null,"cid":"","category":"","address":"","web_site":"","phone":"","plus_code":"","status":"","description":"","reviews_link":"","thumbnail":"","timezone":"","price_range":"","data_id":"","street_view_url":"","owner":{"id":"","name":"","link":""},"complete_address":{"borough":"","street":"","city":"","postal_code":"","state":"","country":""},"menu":{"link":"","source":""}}`, lines[0])
	require.True(t, ids.Has("place-a"))
	require.True(t, ids.Has("cid-b"))
}

func TestJSONLAppendWriterSkipsExistingIdentity(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ids := resume.NewIdentitySet()
	ids.Add("place-a")
	writer := resume.NewJSONLAppendWriter(&out, ids, nil, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: []*gmaps.Entry{
		{ID: "q1", Title: "A", Link: "https://maps/place/a", PlaceID: "place-a"},
		{ID: "q1", Title: "B", Link: "https://maps/place/b", Cid: "cid-b"},
	}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], `"cid":"cid-b"`)
	require.True(t, ids.Has("cid-b"))
}

type recordingResultTracker struct {
	inputIDs []string
}

func (t *recordingResultTracker) ResultPersisted(inputID string, syncOutput func() error) error {
	if syncOutput != nil {
		if err := syncOutput(); err != nil {
			return err
		}
	}

	t.inputIDs = append(t.inputIDs, inputID)

	return nil
}

func TestJSONLAppendWriterAcknowledgesResultAfterWrite(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	tracker := &recordingResultTracker{}
	writer := resume.NewJSONLAppendWriter(&out, resume.NewIdentitySet(), tracker, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", PlaceID: "place-a"}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))
	require.Equal(t, []string{"q1"}, tracker.inputIDs)
}

func TestJSONLAppendWriterDoesNotAcknowledgeFailedWrite(t *testing.T) {
	t.Parallel()

	tracker := &recordingResultTracker{}
	writer := resume.NewJSONLAppendWriter(failingWriter{}, resume.NewIdentitySet(), tracker, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", PlaceID: "place-a"}}

	close(in)

	require.Error(t, writer.Run(context.Background(), in))
	require.Empty(t, tracker.inputIDs)
}

func TestJSONLAppendWriterChecksAllExistingIdentities(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	ids := resume.NewIdentitySet()
	ids.Add("cid-a")

	tracker := &recordingResultTracker{}
	writer := resume.NewJSONLAppendWriter(&out, ids, tracker, nil)
	in := make(chan scrapemate.Result, 1)

	in <- scrapemate.Result{Data: &gmaps.Entry{ID: "q1", PlaceID: "place-a", Cid: "cid-a"}}

	close(in)

	require.NoError(t, writer.Run(context.Background(), in))
	require.Empty(t, out.String())
	require.Equal(t, []string{"q1"}, tracker.inputIDs)
}
