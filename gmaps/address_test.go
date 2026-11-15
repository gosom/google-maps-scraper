//nolint:testpackage // verify missing and malformed structured address slots
package gmaps

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompleteAddressFromPlaceArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node any
		want Address
	}{
		{
			name: "structured slots",
			node: []any{nil, []any{"Borough", "Eakou 74", nil, "Ilion", "131 22", "Attica", "GR"}},
			want: Address{Borough: "Borough", Street: "Eakou 74", City: "Ilion", PostalCode: "131 22", State: "Attica", Country: "GR"},
		},
		{name: "missing block"},
		{name: "wrong block type", node: "not an address"},
		{name: "partial slots", node: []any{nil, []any{nil, "Street"}}, want: Address{Street: "Street"}},
		{name: "wrong slot types", node: []any{nil, []any{4, false, nil, []any{}, 123}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			business := make([]any, 184)
			business[183] = tc.node
			require.Equal(t, tc.want, completeAddressFromPlaceArray(business))
		})
	}
}

func TestParseSearchResultsCompleteAddress(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../testdata/output.json")
	require.NoError(t, err)

	entries, err := ParseSearchResults(raw)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	require.Equal(t, Address{
		Street: "Eakou 74", City: "Ilion", PostalCode: "131 22", Country: "GR",
	}, entries[0].CompleteAddress)
}
