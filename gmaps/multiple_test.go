package gmaps_test

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestSearchResultAdditionalFields(t *testing.T) {
	business := make([]any, 204)
	business[11] = "Example Cafe"
	business[13] = []any{"Cafe", "Coffee shop"}
	business[78] = "ChIJexample"
	business[4] = []any{nil, nil, nil, []any{"https://search.google.com/local/reviews?placeid=ChIJexample"}}
	business[32] = []any{nil, []any{nil, "An independent cafe."}}
	business[203] = []any{nil, []any{nil, nil, nil, nil, []any{"Closed ⋅ Opens 9 AM"}}}
	business[100] = []any{nil, []any{
		[]any{"accessibility", "Accessibility", []any{
			[]any{"wheelchair", "Wheelchair accessible entrance", []any{nil, []any{[]any{1}}}},
			[]any{"parking", "Wheelchair accessible parking lot", []any{nil, []any{[]any{0}}}},
		}},
	}}

	entries := parseFastEntities(t, business)
	require.Len(t, entries, 1)
	entry := entries[0]
	require.Equal(t, "Cafe", entry.Category)
	require.Equal(t, []string{"Cafe", "Coffee shop"}, entry.Categories)
	require.Equal(t, "ChIJexample", entry.PlaceID)
	require.Equal(t, "https://search.google.com/local/reviews?placeid=ChIJexample", entry.ReviewsLink)
	require.Equal(t, "An independent cafe.", entry.Description)
	require.Equal(t, "Closed ⋅ Opens 9 AM", entry.Status)
	require.Len(t, entry.About, 1)
	require.Equal(t, "Accessibility", entry.About[0].Name)
	require.Len(t, entry.About[0].Options, 2)
	require.True(t, entry.About[0].Options[0].Enabled)
	require.False(t, entry.About[0].Options[1].Enabled)

	row := map[string]string{}
	for i, name := range entry.CsvHeaders() {
		row[name] = entry.CsvRow()[i]
	}

	require.Equal(t, "Cafe", row["category"])
	require.Equal(t, entry.PlaceID, row["place_id"])
	require.Equal(t, entry.ReviewsLink, row["reviews_link"])
	require.Equal(t, entry.Description, row["descriptions"])
	require.Contains(t, row["about"], "Wheelchair accessible entrance")

	encoded, err := json.Marshal(entry)
	require.NoError(t, err)

	var output map[string]any
	require.NoError(t, json.Unmarshal(encoded, &output))
	require.Equal(t, "ChIJexample", output["place_id"])
	require.Equal(t, []any{"Cafe", "Coffee shop"}, output["categories"])
}

func TestSearchResultListingIdentifiers(t *testing.T) {
	tests := []struct {
		name, dataID, placeID, wantCID string
	}{
		{"unsigned CID", "0x14e732fd76f0d90d:0xe5415928d6702b47", "ChIJexample", "16519582940102929223"},
		{"maximum CID", "0x1:0xffffffffffffffff", "", "18446744073709551615"},
		{"place ID only", "", "ChIJexample", ""},
		{"missing identifiers", "", "", ""},
		{"malformed CID with place ID", "0x1:0xnope", "ChIJexample", ""},
		{"overflow", "0x1:0x10000000000000000", "", ""},
		{"missing delimiter", "0x123", "", ""},
		{"missing prefix", "0x1:123", "", ""},
		{"extra delimiter", "0x1:0x2:0x3", "", ""},
		{"malformed location", "0xnope:0x123", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			business := make([]any, 79)
			business[10] = tt.dataID
			business[11] = "Καφέ & Tea #1 + More"
			business[78] = tt.placeID
			entry := parseFastEntities(t, business)[0]
			require.Equal(t, tt.wantCID, entry.Cid)

			switch {
			case tt.placeID != "":
				link, err := url.Parse(entry.Link)
				require.NoError(t, err)
				require.Equal(t, "https", link.Scheme)
				require.Equal(t, "www.google.com", link.Host)
				require.Equal(t, "/maps/search/", link.Path)
				require.Equal(t, "1", link.Query().Get("api"))
				require.Equal(t, entry.Title, link.Query().Get("query"))
				require.Equal(t, tt.placeID, link.Query().Get("query_place_id"))
				require.Empty(t, link.Fragment)
			case tt.wantCID != "":
				require.Equal(t, "https://www.google.com/maps?cid="+tt.wantCID, entry.Link)
			default:
				require.Empty(t, entry.Link)
			}

			for i, header := range entry.CsvHeaders() {
				if header == "link" {
					require.Equal(t, entry.Link, entry.CsvRow()[i])
				}

				if header == "cid" {
					require.Equal(t, tt.wantCID, entry.CsvRow()[i])
				}
			}

			encoded, err := json.Marshal(entry)
			require.NoError(t, err)

			var output map[string]any
			require.NoError(t, json.Unmarshal(encoded, &output))
			require.Equal(t, entry.Link, output["link"])
			require.Equal(t, tt.wantCID, output["cid"])
		})
	}
}

func TestSearchResultOptionalFields(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "missing"
		if malformed {
			name = "wrong types"
		}

		t.Run(name, func(t *testing.T) {
			business := make([]any, 204)
			business[11] = "Example business"

			if malformed {
				for _, index := range []int{4, 13, 32, 46, 75, 78, 100, 203} {
					business[index] = true
				}
			}

			// These old offsets can contain amenities, not operating status.
			business[34] = []any{nil, nil, nil, nil, []any{nil, nil, nil, nil, "Dogs allowed"}}
			business[88] = []any{"Dogs allowed"}
			entry := parseFastEntities(t, business)[0]
			require.Empty(t, entry.Category)
			require.Empty(t, entry.PlaceID)
			require.Empty(t, entry.ReviewsLink)
			require.Empty(t, entry.Description)
			require.Empty(t, entry.Status)
			require.Empty(t, entry.About)
			require.Empty(t, entry.Reservations)
			require.Empty(t, entry.OrderOnline)
		})
	}
}

func TestSearchResultActions(t *testing.T) {
	const link = "https://example.com/book"

	provider := []any{[]any{"Example"}, []any{nil, nil, []any{link}}}
	tests := []struct {
		name         string
		group        []any
		reservations int
		orders       int
	}{
		{"reservation provider", []any{1, nil, []any{provider}}, 1, 0},
		{"appointment provider", []any{3, nil, []any{provider}, nil, 21633}, 1, 0},
		{"ordering provider", []any{3, nil, []any{provider}}, 0, 1},
		{"appointment button", []any{3, nil, nil, nil, 21633, []any{"Book online", []any{nil, nil, []any{link}}}}, 1, 0},
		{"reservation button", []any{1, nil, nil, nil, 21631, []any{"Reserve a table", []any{nil, nil, []any{link}}}}, 1, 0},
		{"unknown group", []any{99, nil, []any{provider}}, 0, 0},
		{"invalid provider", []any{1, nil, []any{nil, "bad", []any{[]any{"Example"}, []any{nil, nil, []any{"javascript:alert(1)"}}}}}, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			business := make([]any, 76)
			business[11] = "Example"
			business[75] = []any{[]any{nil, "bad group", tt.group, tt.group}}
			entry := parseFastEntities(t, business)[0]
			require.Len(t, entry.Reservations, tt.reservations)
			require.Len(t, entry.OrderOnline, tt.orders)

			for _, got := range append(entry.Reservations, entry.OrderOnline...) {
				require.Equal(t, link, got.Link)
				require.NotEmpty(t, got.Source)
			}
		})
	}

	t.Run("deduplicate reservation sources", func(t *testing.T) {
		business := make([]any, 76)
		business[11] = "Example"
		business[46] = []any{[]any{link, "Example"}, nil, []any{"not a URL"}}
		business[75] = []any{[]any{[]any{1, nil, []any{provider}}}}
		entry := parseFastEntities(t, business)[0]
		require.Equal(t, []gmaps.LinkSource{{Link: link, Source: "Example"}}, entry.Reservations)
		require.Empty(t, entry.OrderOnline)
	})
}

func parseFastEntities(t *testing.T, businesses ...[]any) []*gmaps.Entry {
	t.Helper()

	items := make([]any, 1, 1+len(businesses))

	for _, business := range businesses {
		item := make([]any, 15)
		item[14] = business
		items = append(items, item)
	}

	raw, err := json.Marshal([]any{[]any{nil, items}})
	require.NoError(t, err)
	entries, err := gmaps.ParseSearchResults(raw)
	require.NoError(t, err)

	return entries
}
