package goqueryparser

import (
	"bytes"
	"context"

	"github.com/PuerkitoBio/goquery"
)

type GoQueryHTMLParser struct {
}

func New() *GoQueryHTMLParser {
	return &GoQueryHTMLParser{}
}

func (g *GoQueryHTMLParser) Parse(_ context.Context, body []byte) (any, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	return doc, nil
}
