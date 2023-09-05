package gmaps_test

import (
	"os"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func createGoQueryFromFile(t *testing.T, path string) *goquery.Document {
	t.Helper()

	fd, err := os.Open(path)
	require.NoError(t, err)

	defer fd.Close()

	doc, err := goquery.NewDocumentFromReader(fd)
	require.NoError(t, err)

	return doc
}

func Test_EntryFromJSON(t *testing.T) {
	expected := gmaps.Entry{
		Link:       "https://www.google.com/maps/place/Kipriakon/data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		Title:      "Kipriakon",
		Category:   "Restaurant",
		Categories: []string{"Restaurant"},
		Address:    "Kipriakon, Old port, Limassol 3042",
		OpenHours: map[string][]string{
			"Monday":    {"12:30–10 pm"},
			"Tuesday":   {"12:30–10 pm"},
			"Wednesday": {"12:30–10 pm"},
			"Thursday":  {"12:30–10 pm"},
			"Friday":    {"12:30–10 pm"},
			"Saturday":  {"12:30–10 pm"},
			"Sunday":    {"12:30–10 pm"},
		},
		WebSite:      "",
		Phone:        "25 101555",
		PlusCode:     "M2CR+6X Limassol",
		ReviewCount:  396,
		ReviewRating: 4.2,
		Latitude:     34.670595399999996,
		Longtitude:   33.042456699999995,
		Cid:          "16519582940102929223",
		Status:       "Closed ⋅ Opens 12:30\u202fpm Tue",
		ReviewsLink:  "https://search.google.com/local/reviews?placeid=ChIJDdnwdv0y5xQRRytw1ihZQeU&q=Kipriakon&authuser=0&hl=en&gl=CY",
		Thumbnail:    "https://lh5.googleusercontent.com/p/AF1QipP4Y7A8nYL3KKXznSl69pXSq9p2IXCYUjVvOh0F=w408-h408-k-no",
		Timezone:     "Asia/Nicosia",
		PriceRange:   "€€",
		DataID:       "0x14e732fd76f0d90d:0xe5415928d6702b47",
	}

	raw, err := os.ReadFile("../testdata/raw.json")
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)

	require.Equal(t, expected, entry)
}

func Test_EntryFromJSON2(t *testing.T) {
	fnames := []string{
		"../testdata/panic.json",
		"../testdata/panic2.json",
	}
	for _, fname := range fnames {
		raw, err := os.ReadFile(fname)
		require.NoError(t, err)
		require.NotEmpty(t, raw)

		_, err = gmaps.EntryFromJSON(raw)
		require.NoError(t, err)
	}
}
