package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

type PlaceJob struct {
	scrapemate.Job

	UsageInResultststs bool
	ExtractEmail       bool
}

func NewPlaceJob(parentID, langCode, u string, extractEmail bool) *PlaceJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
	)

	job := PlaceJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        u,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.UsageInResultststs = true
	job.ExtractEmail = extractEmail

	return &job
}

func (j *PlaceJob) Process(_ context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to []byte")
	}

	entry, err := EntryFromJSON(raw)
	if err != nil {
		return nil, nil, err
	}

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetURL()
	}

	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		emailJob := NewEmailJob(j.ID, &entry)

		j.UsageInResultststs = false

		return nil, []scrapemate.IJob{emailJob}, nil
	}

	return &entry, nil, err
}

func (j *PlaceJob) BrowserActions(_ context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})

	if err != nil {
		resp.Error = err

		return resp
	}

	if err = clickRejectCookiesIfRequired(page); err != nil {
		resp.Error = err

		return resp
	}

	const defaultTimeout = 5000

	err = page.WaitForURL(page.URL(), playwright.PageWaitForURLOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(defaultTimeout),
	})
	if err != nil {
		resp.Error = err

		return resp
	}

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))

	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}

	rawI, err := page.Evaluate(js)
	if err != nil {
		resp.Error = err

		return resp
	}

	raw, ok := rawI.(string)
	if !ok {
		resp.Error = fmt.Errorf("could not convert to string")

		return resp
	}

	const prefix = ")]}'"

	raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))

	if resp.Meta == nil {
		resp.Meta = make(map[string]any)
	}

	resp.Meta["json"] = []byte(raw)

	return resp
}

func (j *PlaceJob) UseInResults() bool {
	return j.UsageInResultststs
}

const js = `
function parse() {
  const inputString = window.APP_INITIALIZATION_STATE[3][6]
  return inputString
}
`
