package scrapemate

import (
	"context"
	"crypto/md5" //nolint:gosec // we don't need a cryptographically secure hash here
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

var _ IJob = (*Job)(nil)

// IJob is a job to be processed by the scrapemate
type IJob interface {
	fmt.Stringer
	// GetID returns the unique identifier of the job.
	GetID() string
	// GetParentID returns the parent id of the job
	GetParentID() string
	// GetMethod returns the http method to use
	GetMethod() string
	// GetBody returns the body of the request
	GetBody() []byte
	// GetURL returns the url to request
	GetURL() string
	// GetHeaders returns the headers to use
	GetHeaders() map[string]string
	// GetURLParams returns the url params to use
	GetURLParams() map[string]string
	// GetFullURL returns the full url to request
	// it includes the url params
	GetFullURL() string
	// GetTimeout returns the timeout of the job
	GetTimeout() time.Duration
	// GetPriority returns the priority of the job
	GetPriority() int
	// CheckResponse checks the response of the job
	DoCheckResponse(resp *Response) bool
	// GetActionOnResponse returns the action to perform on the response
	GetRetryPolicy() RetryPolicy
	// GetMaxRetries returns the max retries of the job
	GetMaxRetries() int
	// Process processes the job
	Process(ctx context.Context, resp *Response) (any, []IJob, error)
	// GetMaxRetryDelay returns the delay to wait before retrying
	GetMaxRetryDelay() time.Duration
	BrowserActions(ctx context.Context, page BrowserPage) Response
	// DoScreenshot takes a screenshot of the page
	// Only works if the scraper uses jsfetcher
	DoScreenshot() bool
	// GetCacheKey returns the key to use for caching
	GetCacheKey() string
	// UseInResults returns true if the job should be used in the results
	UseInResults() bool
	// ProcessOnFetchError returns true if the job should be processed even if the job failed
	ProcessOnFetchError() bool
}

// Job is the base job that we may use
type Job struct {
	// ID is an identifier for the job
	ID string
	// ParentID is the parent id of the job
	ParentID string
	// Method can be one valid HTTP method
	Method string
	// Body is the request's body
	Body []byte
	// URL is the url to sent a request
	URL string
	// Headers is the map of headers to use in HTTP
	Headers map[string]string
	// URLParams are the url parameters to use in the query string
	URLParams map[string]string
	// Timeout is the timeout of that job. By timeout we mean the time
	// it takes to finish a single crawl
	Timeout time.Duration
	// Priority is a number indicating the priority. By convention the higher
	// the priority
	Priority int
	// MaxRetries defines the maximum number of retries when a job fails
	MaxRetries int
	// CheckResponse is a function that takes as an input a Response and returns:
	// true: when the response is to be accepted
	// false: when the response is to be rejected
	// By default a response is accepted if status code is 200
	CheckResponse func(resp *Response) bool
	// RetryPolicy can be one of:
	// RetryJob: to retry the job untl it's successful
	// DiscardJob:for not accepted responses just discard them and do not retry the job
	// RefreshIP: Similar to RetryJob with an importan difference
	// 				Before the job is retried the IP is refreshed.
	RetryPolicy RetryPolicy
	// MaxRetryDelay By default when a job is rejected is retried with an exponential backof
	// for a MaxRetries numbers of time. If the sleep time between the retries is more than
	// MaxRetryDelay then it's capped to that. (Default is 2 seconds)
	MaxRetryDelay time.Duration
	// TakeScreenshot if true takes a screenshot of the page
	TakeScreenshot bool
	Response       Response
}

// ProcessOnFetchError returns true if the job should be processed even if the job failed
func (j *Job) ProcessOnFetchError() bool {
	return false
}

// UseInResults returns true if the job should be used in the results
func (j *Job) UseInResults() bool {
	return true
}

// GetCacheKey returns the key to use for caching
func (j *Job) GetCacheKey() string {
	u := j.GetFullURL()

	toHash := fmt.Sprintf("%s:%s", j.GetMethod(), u)

	if j.GetMethod() == http.MethodPost {
		toHash += string(j.GetBody())
	}

	hashValue := md5.Sum([]byte(toHash)) //nolint:gosec // we don't need a cryptographically secure hash here
	cacheKey := hex.EncodeToString(hashValue[:])

	return cacheKey
}

// DoScreenshot used to check if we need a screenshot
// It's here since it's a common use case
func (j *Job) DoScreenshot() bool {
	return j.TakeScreenshot
}

// BrowserActions is the function that will be executed in the browser
// This is the function that will be executed in the browser
// this is a default implementation that will just return the response
// override this function to perform actions in the browser
func (j *Job) BrowserActions(_ context.Context, page BrowserPage) Response {
	var resp Response

	pageResponse, err := page.Goto(j.GetFullURL(), WaitUntilNetworkIdle)
	if err != nil {
		resp.Error = err
		return resp
	}

	resp.URL = pageResponse.URL
	resp.StatusCode = pageResponse.StatusCode
	resp.Headers = pageResponse.Headers
	resp.Body = pageResponse.Body

	if j.DoScreenshot() {
		screenshot, err := page.Screenshot(true)
		if err != nil {
			resp.Error = err
			return resp
		}

		resp.Screenshot = screenshot
	}

	return resp
}

// String returns the string representation of the job
func (j *Job) String() string {
	return fmt.Sprintf("Job{ID: %s, Method: %s, URL: %s, UrlParams: %v}", j.ID, j.Method, j.URL, j.URLParams)
}

// Process processes the job
func (j *Job) Process(_ context.Context, _ *Response) (any, []IJob, error) {
	return nil, nil, nil
}

// CheckResponse checks the response of the job
func (j *Job) DoCheckResponse(resp *Response) bool {
	if j.CheckResponse == nil {
		return func(resp *Response) bool {
			return resp.StatusCode >= 200 && resp.StatusCode < 300
		}(resp)
	}

	return j.CheckResponse(resp)
}

// GetRetryPolicy returns the action to perform on the response
func (j *Job) GetRetryPolicy() RetryPolicy {
	return j.RetryPolicy
}

// GetMaxRetry returns the max retry of the job
func (j *Job) GetMaxRetries() int {
	return j.MaxRetries
}

// GetID returns the unique identifier of the job.
func (j *Job) GetID() string {
	return j.ID
}

// GetParentID returns the parent id of the job
func (j *Job) GetParentID() string {
	return j.ParentID
}

// GetMethod returns the http method to use
func (j *Job) GetMethod() string {
	return j.Method
}

// GetBody returns the body of the request
func (j *Job) GetBody() []byte {
	return j.Body
}

// GetURL returns the url to request
func (j *Job) GetURL() string {
	return j.URL
}

func (j *Job) GetFullURL() string {
	urlvals := url.Values{}
	keys := make([]string, 0, len(j.URLParams))

	for k := range j.URLParams {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		urlvals.Add(k, j.URLParams[k])
	}

	var u string
	if len(urlvals) > 0 {
		u = fmt.Sprintf("%s?%s", j.URL, urlvals.Encode())
	} else {
		u = j.URL
	}

	return u
}

// GetHeaders returns the headers to use
func (j *Job) GetHeaders() map[string]string {
	return j.Headers
}

// GetURLParams returns the url params to use
func (j *Job) GetURLParams() map[string]string {
	return j.URLParams
}

// GetTimeout returns the timeout of the job
func (j *Job) GetTimeout() time.Duration {
	return j.Timeout
}

// GetPriority returns the priority of the job
func (j *Job) GetPriority() int {
	return j.Priority
}

// GetRetryDelay returns the delay to wait before retrying
func (j *Job) GetMaxRetryDelay() time.Duration {
	if j.MaxRetryDelay == 0 {
		return DefaultMaxRetryDelay
	}

	return j.MaxRetryDelay
}
