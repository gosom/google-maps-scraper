package scrapemate

import (
	"net/http"
	"time"
)

// Response is the struct that it is returned when crawling finishes
type Response struct {
	URL        string
	StatusCode int
	Headers    http.Header
	Duration   time.Duration
	Body       []byte
	Error      error
	Meta       map[string]any
	Screenshot []byte

	// Document is the parsed document
	// if you don't set an html parser the document will be nil
	// Since each html parser has it's own document type
	// the document is an interface
	// You need to cast it to the type of the parser you are using
	// For example if you are using goquery you need to cast it to *goquery.Document
	// If you are using the stdib parser net/html the it will be *html.Node
	Document any
}
