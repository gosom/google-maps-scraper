package gmaps

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type Entry struct {
	Title        string
	Category     string
	Address      string
	OpenHours    string
	WebSite      string
	Phone        string
	PlusCode     string
	ReviewCount  int
	ReviewRating float64
	Latitude     float64
	Longtitude   float64
}

func (e *Entry) Validate() error {
	if e.Title == "" {
		return fmt.Errorf("title is empty")
	}

	if e.Category == "" {
		return fmt.Errorf("category is empty")
	}

	return nil
}

func (e *Entry) CsvHeaders() []string {
	return []string{"title", "category", "address", "open_hours", "website", "phone", "plus_code", "review_count", "review_rating", "latitude", "longitude"}
}

func (e *Entry) CsvRow() []string {
	return []string{e.Title, e.Category, e.Address, e.OpenHours, e.WebSite, e.Phone, e.PlusCode, strconv.Itoa(e.ReviewCount), strconv.FormatFloat(e.ReviewRating, 'f', 2, 64),
		fmt.Sprintf("%f", e.Latitude), fmt.Sprintf("%f", e.Longtitude)}
}

func EntryFromGoQuery(doc *goquery.Document) (Entry, error) {
	var entry Entry

	entry.Title = doc.Find("h1>span").First().Parent().Text()
	entry.Category = doc.Find("button[jsaction='pane.rating.category']").Text()

	el := doc.Find(`button[data-item-id="address"]`).First()
	txt := el.AttrOr("aria-label", "")
	_, addr, ok := strings.Cut(txt, ":")

	if ok {
		entry.Address = strings.TrimSpace(addr)
	}

	sel := `div[jsaction^='pane.openhours']+div`
	el = doc.Find(sel).First()
	entry.OpenHours = el.AttrOr("aria-label", "")

	sel = `a[aria-label^="Website:"]`
	el = doc.Find(sel).First()
	entry.WebSite = el.AttrOr("href", "")

	sel = `button[aria-label^="Phone:"]`
	el = doc.Find(sel).First()
	txt = el.AttrOr("aria-label", "")
	_, phone, ok := strings.Cut(txt, ":")

	if ok {
		entry.Phone = strings.ReplaceAll(phone, " ", "")
	}

	sel = `button[aria-label^="Plus code:"]`
	el = doc.Find(sel).First()
	txt = el.AttrOr("aria-label", "")
	_, code, ok := strings.Cut(txt, ":")

	if ok {
		entry.PlusCode = strings.TrimSpace(code)
	}

	sel = `div[jsaction="pane.reviewChart.moreReviews"]>div:nth-child(2)`
	el = doc.Find(sel).First()
	el2 := el.Find(`div.fontDisplayLarge`).First()
	entry.ReviewRating = parseFloat(el2.Text())
	el2 = el.Find("button[jsaction='pane.reviewChart.moreReviews']>span").First()

	entry.ReviewCount = parseInt(el2.Text())

	entry.Latitude, entry.Longtitude = extractLatLng(doc)

	if err := entry.Validate(); err != nil {
		return entry, err
	}

	return entry, nil
}

var coordsRegex = regexp.MustCompile(`@(-?\d+\.\d+),(-?\d+\.\d+)`)

func extractLatLng(doc *goquery.Document) (lat, lon float64) {
	sel := `div[guidedhelpid=gbsib]>a`
	el := doc.Find(sel).First()

	txt := el.AttrOr("href", "")
	if txt == "" {
		return 0, 0
	}

	u, err := url.Parse(txt)
	if err != nil {
		return 0, 0
	}

	con := u.Query().Get("continue")
	if con == "" {
		return 0, 0
	}

	matches := coordsRegex.FindStringSubmatch(con)

	const expectedMatches = 2
	if len(matches) > expectedMatches {
		return parseFloat(matches[1]), parseFloat(matches[2])
	}

	return 0, 0
}

func parseInt(s string) int {
	var i int

	_, err := fmt.Sscanf(s, "%d", &i)
	if err != nil {
		return 0
	}

	return i
}

func parseFloat(s string) float64 {
	var f float64

	_, err := fmt.Sscanf(s, "%f", &f)
	if err != nil {
		return 0
	}

	return f
}
