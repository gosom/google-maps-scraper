package jsonbsanitize_test

import (
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/internal/jsonbsanitize"
	"github.com/stretchr/testify/require"
)

func TestStripNULFromEntry_NestedFields(t *testing.T) {
	entry := &gmaps.Entry{
		Title:      "A\x00B",
		Categories: []string{"cat\x00egory"},
		OpenHours: map[string][]string{
			"Mon\x00day": {"08:\x0000"},
		},
		PopularTimes: map[string]map[int]int{
			"Mon\x00day": {8: 10},
		},
		Images: []gmaps.Image{
			{Title: "im\x00age", Image: "url\x00"},
		},
		Menu: gmaps.LinkSource{
			Link:   "link\x00",
			Source: "source\x00",
		},
		Owner: gmaps.Owner{
			ID:   "id\x00",
			Name: "name\x00",
			Link: "owner\x00link",
		},
		About: []gmaps.About{
			{
				ID:   "about\x00",
				Name: "about-name\x00",
				Options: []gmaps.Option{
					{Name: "opt\x00", Enabled: true},
				},
			},
		},
		UserReviews: []gmaps.Review{
			{
				Name:           "reviewer\x00",
				ProfilePicture: "pp\x00",
				Description:    "desc\x00",
				Images:         []string{"img\x00"},
				When:           "now\x00",
			},
		},
		Emails: []string{"a\x00@b.com"},
	}

	jsonbsanitize.StripNULFromEntry(entry)

	require.Equal(t, "AB", entry.Title)
	require.Equal(t, []string{"category"}, entry.Categories)
	require.Contains(t, entry.OpenHours, "Monday")
	require.Equal(t, []string{"08:00"}, entry.OpenHours["Monday"])
	require.Contains(t, entry.PopularTimes, "Monday")
	require.Equal(t, map[int]int{8: 10}, entry.PopularTimes["Monday"])
	require.Equal(t, "image", entry.Images[0].Title)
	require.Equal(t, "url", entry.Images[0].Image)
	require.Equal(t, "link", entry.Menu.Link)
	require.Equal(t, "source", entry.Menu.Source)
	require.Equal(t, "id", entry.Owner.ID)
	require.Equal(t, "name", entry.Owner.Name)
	require.Equal(t, "ownerlink", entry.Owner.Link)
	require.Equal(t, "about", entry.About[0].ID)
	require.Equal(t, "about-name", entry.About[0].Name)
	require.Equal(t, "opt", entry.About[0].Options[0].Name)
	require.Equal(t, "reviewer", entry.UserReviews[0].Name)
	require.Equal(t, "pp", entry.UserReviews[0].ProfilePicture)
	require.Equal(t, "desc", entry.UserReviews[0].Description)
	require.Equal(t, []string{"img"}, entry.UserReviews[0].Images)
	require.Equal(t, "now", entry.UserReviews[0].When)
	require.Equal(t, []string{"a@b.com"}, entry.Emails)
}

func TestStripNULFromEntry_PreservesLiteralEscapedText(t *testing.T) {
	entry := &gmaps.Entry{
		Description: "literal \\u0000 text and real\x00nul",
	}

	jsonbsanitize.StripNULFromEntry(entry)

	require.Equal(t, "literal \\u0000 text and realnul", entry.Description)
}

func TestStripNULFromEntries_NilEntryNoop(_ *testing.T) {
	entries := []*gmaps.Entry{nil}
	jsonbsanitize.StripNULFromEntries(entries)
}
