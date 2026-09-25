package gmaps

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_normalizeCountryCode(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", "   ", ""},
		{"iso code is upper cased", "cy", "CY"},
		{"iso code already upper is kept", "GR", "GR"},
		{"country name resolves to iso", "Greece", "GR"},
		{"country name is case insensitive", "greece", "GR"},
		{"country name with padding", "  Indonesia  ", "ID"},
		{"three letter alias resolves", "USA", "US"},
		{"united kingdom resolves to gb not uk", "United Kingdom", "GB"},
		{"two letter uk is corrected to gb", "UK", "GB"},
		{"unknown name is preserved verbatim", "Wakanda", "Wakanda"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, normalizeCountryCode(tc.input))
		})
	}
}

func Test_AddressFromFlat(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected Address
	}{
		{
			name:     "empty input yields empty address",
			input:    "",
			expected: Address{},
		},
		{
			name:  "street, city with postal code, country",
			input: "Eakou 74, Ilion 131 22, Greece",
			expected: Address{
				Street:     "Eakou 74",
				City:       "Ilion",
				PostalCode: "131 22",
				Country:    "GR",
			},
		},
		{
			name:  "no country segment when search locale matches",
			input: "Old port, Limassol 3042",
			expected: Address{
				Street:     "Old port",
				City:       "Limassol",
				PostalCode: "3042",
			},
		},
		{
			name:  "multiple street segments are joined",
			input: "65 Natalia Court, Poseidonos Ave, Paphos 8042",
			expected: Address{
				Street:     "65 Natalia Court, Poseidonos Ave",
				City:       "Paphos",
				PostalCode: "8042",
			},
		},
		{
			name:  "city without postal code",
			input: "29RR+CPC Island Beach Bar and Restaurant, Λεοφόρος Ακάμαντος, Poli Crysochous",
			expected: Address{
				Street: "29RR+CPC Island Beach Bar and Restaurant, Λεοφόρος Ακάμαντος",
				City:   "Poli Crysochous",
			},
		},
		{
			name:  "non ascii city keeps its postal code split",
			input: "Old port, Λεμεσός 3042",
			expected: Address{
				Street:     "Old port",
				City:       "Λεμεσός",
				PostalCode: "3042",
			},
		},
		{
			name:  "us style state abbreviation is not mistaken for the city",
			input: "742 Evergreen Terrace, Springfield, IL 62704, USA",
			expected: Address{
				Street:     "742 Evergreen Terrace",
				City:       "Springfield",
				State:      "IL",
				PostalCode: "62704",
				Country:    "US",
			},
		},
		{
			name:  "australian state abbreviation sits next to the city",
			input: "100 Queen St, Brisbane QLD 4000, Australia",
			expected: Address{
				Street:     "100 Queen St",
				City:       "Brisbane",
				State:      "QLD",
				PostalCode: "4000",
				Country:    "AU",
			},
		},
		{
			name:     "country only input yields country only",
			input:    "Greece",
			expected: Address{Country: "GR"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, AddressFromFlat(tc.input))
		})
	}
}

// Test_ParseSearchResults_populatesCompleteAddress covers the fast-mode path, which
// used to leave every structured address field empty even though node 183 of the
// search payload carries the very same data the place-detail parser reads.
func Test_ParseSearchResults_populatesCompleteAddress(t *testing.T) {
	raw, err := os.ReadFile("../testdata/output.json")
	require.NoError(t, err)

	entries, err := ParseSearchResults(raw)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	require.Equal(t, Address{
		Street:     "Eakou 74",
		City:       "Ilion",
		PostalCode: "131 22",
		Country:    "GR",
	}, entries[0].CompleteAddress)

	for _, entry := range entries {
		require.NotEmpty(t, entry.CompleteAddress.City, "city missing for %q", entry.Title)
		require.NotEmpty(t, entry.CompleteAddress.Country, "country missing for %q", entry.Title)
	}
}

// Test_EntryFromJSON_fallsBackWhenNode183Missing covers the place-detail path for
// listings whose payload has no structured address block at all (service-area
// businesses, plus-code-only listings). The flat address must still be mined.
func Test_EntryFromJSON_fallsBackWhenNode183Missing(t *testing.T) {
	raw, err := os.ReadFile("../testdata/raw.json")
	require.NoError(t, err)

	var jd []any
	require.NoError(t, json.Unmarshal(raw, &jd))

	darray, ok := jd[6].([]any)
	require.True(t, ok)
	require.Greater(t, len(darray), 183)

	darray[183] = nil

	stripped, err := json.Marshal(jd)
	require.NoError(t, err)

	entry, err := EntryFromJSON(stripped)
	require.NoError(t, err)

	require.Equal(t, "Old port, Limassol 3042", entry.Address)
	require.Equal(t, "Limassol", entry.CompleteAddress.City)
	require.Equal(t, "Old port", entry.CompleteAddress.Street)
	require.Equal(t, "3042", entry.CompleteAddress.PostalCode)
}

// Test_EntryFromJSON_fallsBackToCityFromTag4 covers a payload where the structured
// block exists but its city slot is empty, so the redundant copy Google ships
// alongside it (the entry tagged 4) has to be used instead.
func Test_EntryFromJSON_fallsBackToCityFromTag4(t *testing.T) {
	raw, err := os.ReadFile("../testdata/output.json")
	require.NoError(t, err)

	var data []any
	require.NoError(t, json.Unmarshal(raw, &data))

	container, ok := data[0].([]any)
	require.True(t, ok)

	items := getNthElementAndCast[[]any](container, 1)
	require.Greater(t, len(items), 1)

	business := getNthElementAndCast[[]any](getNthElementAndCast[[]any](items, 1), 14)
	node := getNthElementAndCast[[]any](business, 183)
	require.Greater(t, len(node), 1)

	// Keep the tag list (which holds "Ilion, Greece") but blank the structured slots.
	node[1] = nil

	blanked, err := json.Marshal(data)
	require.NoError(t, err)

	entries, err := ParseSearchResults(blanked)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	require.Equal(t, "Ilion", entries[0].CompleteAddress.City)
	require.Equal(t, "GR", entries[0].CompleteAddress.Country)
}

// Test_completeAddressFromPlaceArray_structuredSlotsWin guards the precedence rule:
// a value already present in the structured slots must never be replaced by one mined
// from the redundant tag list or the flat address.
func Test_completeAddressFromPlaceArray_structuredSlotsWin(t *testing.T) {
	node := []any{
		[]any{}, // tag list, deliberately empty
		[]any{"Ano Ilion", "Eakou 74", "", "Ilion", "131 22", "", "gr"},
	}

	darray := make([]any, placeAddressNode+1)
	darray[placeAddressNode] = node

	got := completeAddressFromPlaceArray(darray, "Totally Different St 1, Othercity 99999, Greece")

	require.Equal(t, "Ano Ilion", got.Borough)
	require.Equal(t, "Eakou 74", got.Street)
	require.Equal(t, "Ilion", got.City)
	require.Equal(t, "131 22", got.PostalCode)
	require.Equal(t, "GR", got.Country)
}
