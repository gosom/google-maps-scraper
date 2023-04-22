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

func NewPlaceJob(u string) *PlaceJob {
	job := PlaceJob{
		Job: scrapemate.Job{
			Method:     "GET",
			URL:        u,
			MaxRetries: 3,
			Priority:   1,
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

func (j *PlaceJob) BrowserActions(browser playwright.Browser) scrapemate.Response {
	var resp scrapemate.Response
	bctx, err := browser.NewContext(playwright.BrowserNewContextOptions{})
	if err != nil {
		resp.Error = err
		return resp
	}
	defer bctx.Close()

	page, err := bctx.NewPage()
	if err != nil {
		resp.Error = err
		return resp
	}
	defer page.Close()
	if err := page.SetViewportSize(1920, 1080); err != nil {
		resp.Error = err
		return resp
	}
	pageResponse, err := page.Goto(j.GetURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	})
	if err != nil {
		resp.Error = err
		return resp
	}

	// Now we need to click that we do not accept cookies.
	if err := page.Click(`button[aria-label='Reject all']`); err != nil {
		resp.Error = err
		return resp
	}

	page.WaitForNavigation(playwright.PageWaitForNavigationOptions{
		URL: "*@*",
	})

	page.WaitForTimeout(100)

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
