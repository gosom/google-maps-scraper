package gmaps

import (
	"context"
	"fmt"
	"net/http"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

type PlaceJob struct {
	scrapemate.Job
}

func NewPlaceJob(langCode, u string) *PlaceJob {
	job := PlaceJob{
		Job: scrapemate.Job{
			Method:     "GET",
			URL:        u,
			UrlParams:  map[string]string{"hl": langCode},
			MaxRetries: 3,
			Priority:   0,
		},
	}
	return &job
}

func (j *PlaceJob) Process(ctx context.Context, resp scrapemate.Response) (any, []scrapemate.IJob, error) {
	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}
	entry, err := EntryFromGoQuery(doc)
	if err != nil {
		return nil, nil, err
	}
	return entry, nil, err
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response
	pageResponse, err := page.Goto(j.GetURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		resp.Error = err
		return resp
	}

	if err := clickRejectCookiesIfRequired(page); err != nil {
		resp.Error = err
		return resp
	}

	page.WaitForNavigation(playwright.PageWaitForNavigationOptions{
		URL:     "*@*",
		Timeout: playwright.Float(5000),
	})

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))
	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}
	body, err := page.Content()
	if err != nil {
		resp.Error = err
		return resp
	}
	resp.Body = []byte(body)
	return resp
}
