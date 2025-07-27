//go:build plugin

package main

import (
	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/gosom/scrapemate"
)

// StreamingDuplicateFilterWriterFactory creates a new instance of the streaming duplicate filter
func StreamingDuplicateFilterWriterFactory() scrapemate.ResultWriter {
	return plugins.NewStreamingDuplicateFilterWriter()
}
