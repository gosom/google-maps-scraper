package gmaps_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// minimalJD marshals the real wrapper layout EntryFromJSON expects:
// jd[6] is the venue darray. Slots are injected at the paths the parser reads.
func minimalJD(t *testing.T, mutate func(darray []any)) []byte {
	t.Helper()

	darray := make([]any, 89)
	if mutate != nil {
		mutate(darray)
	}

	jd := []any{nil, nil, nil, nil, nil, nil, darray}

	raw, err := json.Marshal(jd)
	require.NoError(t, err)

	return raw
}

// The genuinely closed venues in Nabel's 466-venue run carried "CLOSED" at
// [88][0]. That path is the authority for business status.
func Test_EntryFromJSONClosedEnumIsStatus(t *testing.T) {
	raw := minimalJD(t, func(darray []any) {
		darray[88] = []any{"CLOSED"}
	})

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)
	require.Equal(t, "CLOSED", entry.Status)
}

// panic.json carries 'Brunch' at [88][0] and the 466-venue production run
// showed it 31 times there: it is an attribute, not a business status.
func Test_EntryFromJSONAttributeNoiseNeverBecomesStatus(t *testing.T) {
	for _, noise := range []string{"Brunch", "Dogs allowed", "AED\u00a0146"} {
		raw := minimalJD(t, func(darray []any) {
			darray[88] = []any{noise}
		})

		entry, err := gmaps.EntryFromJSON(raw)
		require.NoError(t, err)
		require.Empty(t, entry.Status, "noise %q must not pollute status", noise)
		require.Equal(t, noise, entry.Descriptor, "noise %q belongs in the descriptor", noise)
	}
}

// panic.json carries 'Open ⋅ Closes 11 pm' at [34][4][4] and raw.json
// 'Closed ⋅ Opens 12:30 pm Tue': operating-hours lines, not business status.
func Test_EntryFromJSONHoursLineNeverBecomesStatus(t *testing.T) {
	hours := "Open ⋅ Closes 11\u202fpm"

	raw := minimalJD(t, func(darray []any) {
		darray[34] = []any{nil, nil, nil, nil, []any{nil, nil, nil, nil, hours}}
	})

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)
	require.Empty(t, entry.Status)
	require.Equal(t, hours, entry.Descriptor)
}

// A closed venue whose [34][4][4] still holds an editorial line must keep
// status CLOSED while the line survives in the descriptor.
func Test_EntryFromJSONClosedWinsOverNoise(t *testing.T) {
	raw := minimalJD(t, func(darray []any) {
		darray[34] = []any{nil, nil, nil, nil, []any{nil, nil, nil, nil, "Raucous spot for hookah & Russian eats"}}
		darray[88] = []any{"CLOSED"}
	})

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)
	require.Equal(t, "CLOSED", entry.Status)
	require.Equal(t, "Raucous spot for hookah & Russian eats", entry.Descriptor)
}

// An operational entry yields no status, no descriptor, and does not crash.
func Test_EntryFromJSONOperationalEntryHasEmptyStatus(t *testing.T) {
	raw := minimalJD(t, nil)

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)
	require.Empty(t, entry.Status)
	require.Empty(t, entry.Descriptor)
}

// The displaced one-liner is distinct from Description ([32][1][1]): the
// 466-venue run shows 13 venues carrying both, with different values.
func Test_EntryFromJSONDescriptorIsDistinctFromDescription(t *testing.T) {
	raw := minimalJD(t, func(darray []any) {
		darray[32] = []any{nil, []any{nil, "Longer editorial description sentence"}}
		darray[88] = []any{"Nightspot awash with Swarovski crystal"}
	})

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)
	require.Equal(t, "Longer editorial description sentence", entry.Description)
	require.Equal(t, "Nightspot awash with Swarovski crystal", entry.Descriptor)
}

func Test_CsvHeadersAndRowStayInLockstep(t *testing.T) {
	raw := minimalJD(t, func(darray []any) {
		darray[88] = []any{"CLOSED"}
	})

	entry, err := gmaps.EntryFromJSON(raw)
	require.NoError(t, err)

	headers := entry.CsvHeaders()
	row := entry.CsvRow()

	require.Equal(t, len(headers), len(row), "header/row length mismatch shifts every column silently")
	require.Contains(t, headers, "descriptor")
	require.Contains(t, row, "CLOSED")

	// real fixture must not have regressed either
	raw2, err := os.ReadFile("../testdata/raw2.json")
	require.NoError(t, err)

	full, err := gmaps.EntryFromJSON(raw2)
	require.NoError(t, err)
	require.Equal(t, len(full.CsvHeaders()), len(full.CsvRow()))
}
