package gmaps_test

import (
	"os"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func Test_EntryFromGoQuery(t *testing.T) {
	doc := createGoQueryFromFile(t, "../testdata/entry1.html")
	expected := gmaps.Entry{
		Title:        "Tipsy Sticks",
		Category:     "Restaurant",
		Address:      "Stasandrou 10, Nicosia 1060",
		OpenHours:    `Saturday, 10 am to 1 am; Sunday, 10 am to 5 pm; Monday, Closed; Tuesday, 5 pm to 1 am; Wednesday, 5 pm to 1 am; Thursday, 5 pm to 1 am; Friday, 5 pm to 1 am. Hide open hours for the week`,
		WebSite:      "https://www.thetipsysticks.com/",
		Phone:        "22783333",
		PlusCode:     "5987+WC Nicosia",
		ReviewCount:  20,
		ReviewRating: 4.7,
	}
	entry, err := gmaps.EntryFromGoQuery(doc)
	require.NoError(t, err)
	require.Equal(t, expected.Title, entry.Title)
	require.Equal(t, expected.Category, entry.Category)
	require.Equal(t, expected.Address, entry.Address)
	require.Equal(t, expected.OpenHours, entry.OpenHours)
	require.Equal(t, expected.WebSite, entry.WebSite)
	require.Equal(t, expected.Phone, entry.Phone)
	require.Equal(t, expected.PlusCode, entry.PlusCode)
	require.Equal(t, expected.ReviewCount, entry.ReviewCount)
	require.Equal(t, expected.ReviewRating, entry.ReviewRating)
}

func createGoQueryFromFile(t *testing.T, path string) *goquery.Document {
	t.Helper()

	fd, err := os.Open(path)
	require.NoError(t, err)

	defer fd.Close()

	doc, err := goquery.NewDocumentFromReader(fd)
	require.NoError(t, err)

	return doc
}
