package gmaps

import (
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

const (
	urlSchemeHTTP      = "http"
	urlSchemeHTTPS     = "https"
	maxContactPages    = 3
	contactPageTimeout = 10 * time.Second
)

// discoverContactPages only considers links from the initial website page.
// Contact pages rank ahead of about pages, preserving page order within each rank.
func discoverContactPages(doc *goquery.Document, base *url.URL) []string {
	type candidate struct {
		url  string
		rank int
	}

	var candidates []candidate

	seen := map[string]bool{contactPageKey(base): true}

	doc.Find("a[href]").Each(func(_ int, link *goquery.Selection) {
		href, _ := link.Attr("href")

		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}

		target, err := base.Parse(href)
		if err != nil || !sameContactSite(base, target) {
			return
		}

		target.Fragment = ""
		rank := contactLinkRank(target.Path + " " + link.Text())

		key := contactPageKey(target)
		if rank == 0 || seen[key] {
			return
		}

		seen[key] = true

		candidates = append(candidates, candidate{url: target.String(), rank: rank})
	})

	slices.SortStableFunc(candidates, func(a, b candidate) int { return a.rank - b.rank })

	pages := make([]string, 0, min(len(candidates), maxContactPages))
	for _, candidate := range candidates {
		pages = append(pages, candidate.url)
		if len(pages) == maxContactPages {
			break
		}
	}

	return pages
}

func contactLinkRank(label string) int {
	words := strings.FieldsFunc(strings.ToLower(label), func(r rune) bool { return !unicode.IsLetter(r) })
	rank := 0

	for _, word := range words {
		switch word {
		case "contact", "contacts", "kontakt", "impressum":
			return 1
		case "about", "team", "staff":
			rank = 2
		}
	}

	return rank
}

func sameContactSite(base, target *url.URL) bool {
	if base == nil || target == nil || target.User != nil ||
		(target.Scheme != urlSchemeHTTP && target.Scheme != urlSchemeHTTPS) || target.Hostname() == "" {
		return false
	}

	// Permit the common www alias and HTTP-to-HTTPS transition, but not arbitrary
	// subdomains, lookalike domains, or links to another service on the same host.
	host := func(u *url.URL) string { return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") }

	return host(base) == host(target) && base.Port() == target.Port()
}

func contactPageKey(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.") +
		strings.TrimRight(u.Path, "/") + "?" + u.Query().Encode()
}
