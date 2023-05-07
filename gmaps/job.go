package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/kit/logging"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

type GmapJob struct {
	scrapemate.Job

	MaxDepth int
	LangCode string
}

func NewGmapJob(langCode, query string, maxDepth int) *GmapJob {
	query = url.QueryEscape(query)
	job := GmapJob{
		Job: scrapemate.Job{
			Method:     "GET",
			URL:        "https://www.google.com/maps/search/" + query,
			UrlParams:  map[string]string{"hl": langCode},
			MaxRetries: 3,
			Priority:   1,
		},
		MaxDepth: maxDepth,
		LangCode: langCode,
	}

	return &job
}

func (j *GmapJob) UseInResults() bool {
	return false
}

func (j *GmapJob) Process(ctx context.Context, resp scrapemate.Response) (any, []scrapemate.IJob, error) {
	log := ctx.Value("log").(logging.Logger)
	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}
	var next []scrapemate.IJob
	doc.Find(`div[role='article']>a`).Each(func(i int, s *goquery.Selection) {
		if href := s.AttrOr("href", ""); href != "" {
			nextJob := NewPlaceJob(j.LangCode, href)
			next = append(next, nextJob)
		}
	})
	log.Info(fmt.Sprintf("%d places found", len(next)))
	return nil, next, nil
}

func (j *GmapJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response
	pageResponse, err := page.Goto(j.GetFullURL(), playwright.PageGotoOptions{
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
		URL: "*@*",
	})

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))
	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}

	_, err = scroll(ctx, page, j.MaxDepth)
	if err != nil {
		resp.Error = err
		return resp
	}

	body, err := page.Content()
	if err != nil {
		resp.Error = err
		return resp
	}
	resp.Body = []byte(body)

	return resp
}

func clickRejectCookiesIfRequired(page playwright.Page) error {
	// click the cookie reject button if exists
	sel := `form[action="https://consent.google.com/save"]:first-of-type button:first-of-type`
	el, err := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(500),
	})
	if err != nil {
		return nil
	}
	if el == nil {
		return nil
	}
	return el.Click()
}

func scroll(ctx context.Context, page playwright.Page, maxDepth int) (int, error) {
	scrollSelector := `div[role='feed']`
	expr := `async () => {
		const el = document.querySelector("` + scrollSelector + `");
		el.scrollTop = el.scrollHeight;

		return new Promise((resolve, reject) => {
  			setTimeout(() => {
    		resolve(el.scrollHeight);
  			}, %d);
		});
	}`
	var currentScrollHeight int
	// Scroll to the bottom of the page.
	waitTime := 100.
	cnt := 0
	for i := 0; i < maxDepth; i++ {
		cnt++
		waitTime2 := 500 * cnt
		if waitTime2 > 2000 {
			waitTime2 = 2000
		}
		// Scroll to the bottom of the page.
		scrollHeight, err := page.Evaluate(fmt.Sprintf(expr, waitTime2))
		if err != nil {
			return cnt, err
		}

		height, ok := scrollHeight.(int)
		if !ok {
			return cnt, fmt.Errorf("scrollHeight is not an int")
		}

		if height == currentScrollHeight {
			break
		}
		currentScrollHeight = height
		select {
		case <-ctx.Done():
			return currentScrollHeight, nil
		default:
		}

		waitTime = waitTime * 1.5
		if waitTime > 2000 {
			waitTime = 2000
		}
		page.WaitForTimeout(float64(waitTime))
	}
	return cnt, nil
}
