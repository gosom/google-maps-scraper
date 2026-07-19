# Local Scrapemate patch

This directory contains the production packages imported from
`github.com/gosom/scrapemate` v1.2.1. Upstream tests, CI configuration,
commands, mocks, and documentation are omitted because this is a local module
replacement rather than a standalone Scrapemate checkout.

The only intentional functional difference is that
`adapters/fetchers/jshttp/jshttp.go` does not pass Chromium's
`--single-process` argument. Google Maps closes the browser target while its
Reviews panel loads under that mode, which prevents DOM review pagination.

Keep the local `replace` directive in the repository root `go.mod` until the
upstream launch arguments no longer include `--single-process`.
