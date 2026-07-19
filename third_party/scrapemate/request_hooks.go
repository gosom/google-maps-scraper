package scrapemate

// RequestHookProvider is an optional capability for BrowserPage implementations
// that support intercepting outgoing browser requests and responses.
//
// It is intentionally separate from BrowserPage so it can be added without
// breaking existing BrowserPage implementations. Consumers check for the
// capability via a type assertion:
//
//	func (j *MyJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
//	    if hook, ok := page.(scrapemate.RequestHookProvider); ok {
//	        hook.OnRequest(func(url string, headers map[string]string) {
//	            if strings.Contains(url, "/api/auth") {
//	                // header keys are lower-cased
//	                captureToken(headers["authorization"])
//	            }
//	        })
//	    }
//	    page.Goto(j.URL, scrapemate.WaitUntilNetworkIdle)
//	    // ...
//	}
//
// The Playwright (jshttp) page adapter implements this interface. Other adapters
// may or may not — always check via type assertion so code stays forward
// compatible with adapters that do not support request interception.
//
// A common use case is capturing tokens emitted by SPA auth wrappers: when a
// JavaScript SPA fetches an API resource it typically attaches an Authorization
// header client-side. Registering an OnRequest handler lets the job read that
// header without an Unwrap() cast to the underlying browser library type.
type RequestHookProvider interface {
	// OnRequest registers a handler called for every outgoing browser request.
	// url is the full request URL; headers is a map of request headers with
	// lower-cased keys.
	//
	// The handler runs synchronously in the browser event loop. It MUST NOT
	// perform blocking I/O or blocking browser calls (e.g. evaluating JS, or
	// fetching all headers via a protocol round-trip). Non-blocking operations
	// such as channel sends and atomic stores are safe.
	OnRequest(handler func(url string, headers map[string]string))

	// OnResponse registers a handler called for every browser response. url is
	// the response URL; statusCode is the HTTP status; headers is a map of
	// response headers with lower-cased keys.
	//
	// The same non-blocking threading constraints as OnRequest apply.
	OnResponse(handler func(url string, statusCode int, headers map[string]string))
}
