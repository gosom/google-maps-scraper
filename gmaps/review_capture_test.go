//nolint:testpackage // exercise event lifecycle and RPC framing without a live browser
package gmaps

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"
)

func reviewFixturePayload(t *testing.T, limited bool) string {
	t.Helper()

	raw, err := os.ReadFile("testdata/review_native_with_reply.json")
	require.NoError(t, err)

	var reviews []any
	require.NoError(t, json.Unmarshal(raw, &reviews))

	payload, err := json.Marshal([]any{nil, "next-page-token", reviews, nil, nil, limited})
	require.NoError(t, err)

	return string(payload)
}

func browserReviewFrame(t *testing.T, rpc, payload string) string {
	t.Helper()

	frame, err := json.Marshal([]any{[]any{"wrb.fr", rpc, payload}})
	require.NoError(t, err)

	return strconv.Itoa(len(frame)) + "\n" + string(frame) + "\n"
}

func TestParseBrowserReviewResponse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		rpc       string
		limited   bool
		wantPages int
	}{
		{"current RPC", reviewRPC, false, 1},
		{"limited view", reviewRPC, true, 1},
		{"service RPC", "/MapsUgcPostService.ListUgcPosts", false, 1},
		{"unrelated RPC", "unrelated", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := ")]}'\n\n" + browserReviewFrame(t, "other", "[]") + browserReviewFrame(t, tc.rpc, reviewFixturePayload(t, tc.limited))
			pages, limited, err := parseBrowserReviewResponse([]byte(body))
			require.NoError(t, err)
			require.Len(t, pages, tc.wantPages)
			require.Equal(t, tc.limited, limited)

			if len(pages) > 0 {
				reviews := extractReviews(pages[0])
				require.Len(t, reviews, 1)
				require.NotEmpty(t, reviews[0].ReviewID)
				require.NotEmpty(t, reviews[0].ReplyText)
				require.NotZero(t, reviews[0].PostedAtUnixMicros)
			}
		})
	}
}

func TestBrowserReviewFrameMalformedData(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		")]}'\n\n100\n[[", // truncated frame must fail, preserving any DOM data
		"not json",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			_, _, err := parseBrowserReviewResponse([]byte(raw))
			require.Error(t, err)
		})
	}

	pages, _, err := parseBrowserReviewResponse([]byte(browserReviewFrame(t, reviewRPC, "null")))
	require.NoError(t, err)
	require.Empty(t, pages)
}

func TestReviewResponseURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://www.google.com/maps/_/MapsWizUi/data/batchexecute?rpcids=qv9Egd", true},
		{"https://www.google.com/maps/_/MapsWizUi/data/batchexecute?rpcids=other,qv9Egd", true},
		{"https://www.google.com/maps/_/MapsWizUi/data/batchexecute?rpcids=other", false},
		{"https://example.org/maps/_/MapsWizUi/data/batchexecute?rpcids=qv9Egd", false},
		{"https://www.google.com/other?rpcids=qv9Egd", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, isReviewResponseURL(tc.url))
		})
	}
}

type reviewTestPage struct {
	response func(playwright.Response)
	finished func(playwright.Request)
	removed  []string
}

func (p *reviewTestPage) OnResponse(fn func(playwright.Response))       { p.response = fn }
func (p *reviewTestPage) OnRequestFinished(fn func(playwright.Request)) { p.finished = fn }
func (p *reviewTestPage) RemoveListener(event string, _ any)            { p.removed = append(p.removed, event) }

type reviewTestRequest struct{ playwright.Request }
type reviewTestResponse struct {
	playwright.Response
	request playwright.Request
	body    []byte
	reads   int
}

func (r *reviewTestResponse) Status() int { return 200 }
func (r *reviewTestResponse) URL() string {
	return "https://www.google.com/maps/_/MapsWizUi/data/batchexecute?rpcids=qv9Egd"
}
func (r *reviewTestResponse) Request() playwright.Request { return r.request }
func (r *reviewTestResponse) Body() ([]byte, error)       { r.reads++; return r.body, nil }

func TestReviewCaptureWaitsForFinishedResponseAndCleansUp(t *testing.T) {
	t.Parallel()

	hooks := &reviewTestPage{}
	page := &mockBrowserPage{unwrapped: hooks}
	capture := startReviewCapture(page)
	require.NotNil(t, capture)

	response := &reviewTestResponse{
		request: &reviewTestRequest{},
		body:    []byte(")]}'\n\n" + browserReviewFrame(t, reviewRPC, reviewFixturePayload(t, true))),
	}
	hooks.response(response)
	capture.drain(context.Background())
	require.Zero(t, response.reads, "response handlers must not read incomplete bodies")
	hooks.finished(response.request)
	capture.drain(context.Background())
	require.Equal(t, 1, response.reads)
	require.Len(t, capture.pages, 1)
	require.Len(t, capture.ids, 1)
	require.True(t, capture.limited)

	capture.close()
	require.ElementsMatch(t, []string{"response", "requestfinished"}, hooks.removed)

	next := startReviewCapture(page)
	defer next.close()

	require.Empty(t, next.pages, "reused pages must not inherit a prior place's reviews")
}

func TestReviewCaptureCancellationDoesNotReadBody(t *testing.T) {
	t.Parallel()

	hooks := &reviewTestPage{}

	capture := startReviewCapture(&mockBrowserPage{unwrapped: hooks})
	defer capture.close()

	response := &reviewTestResponse{request: &reviewTestRequest{}}
	hooks.response(response)
	hooks.finished(response.request)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	capture.drain(ctx)
	require.Zero(t, response.reads)
}
