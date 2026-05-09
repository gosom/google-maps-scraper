package writers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// nulEscapeStr is the 6-character JSON escape for a NUL byte. Built from
// runes so this source file itself never contains a literal NUL byte
// (which the Go compiler rejects). Mirrors `nulEscape` in the production
// file but as a string for substring assertions.
var nulEscapeStr = string([]byte{'\\', 'u', '0', '0', '0', '0'})

// TestMustMarshalJSON_StripsNulEscape pins the production fix for the May
// 2026 incident: scraped Google Maps strings sometimes contain NUL bytes
// (review text, image alt text), and json.Marshal renders these as the
// literal 6-character escape . Postgres' json/jsonb columns reject
// that sequence with SQLSTATE 22P02 ("invalid input syntax for type
// json"), causing the whole result row to fail and the job to hang in
// "scraping" because results_written never increments.
func TestMustMarshalJSON_StripsNulEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          any
		mustNotContain string
		mustBeJSON     bool
	}{
		{
			name:           "string field with embedded NUL",
			input:          map[string]string{"text": "hello\x00world"},
			mustNotContain: nulEscapeStr,
			mustBeJSON:     true,
		},
		{
			name:           "nested struct field with NUL in slice",
			input:          map[string][]string{"tags": {"clean", "dir\x00ty", "ok"}},
			mustNotContain: nulEscapeStr,
			mustBeJSON:     true,
		},
		{
			name:           "no NULs is a no-op",
			input:          map[string]string{"text": "perfectly clean"},
			mustNotContain: nulEscapeStr,
			mustBeJSON:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustMarshalJSON(tc.input)
			assert.NotContains(t, string(got), tc.mustNotContain,
				"mustMarshalJSON must strip the literal \\u0000 escape — Postgres json columns reject it")
			if tc.mustBeJSON {
				// Round-trip through encoding/json on the consumer side
				// would also reject , so any well-formed output
				// minus the NUL escape is still valid JSON.
				assert.True(t, strings.HasPrefix(string(got), "{") || strings.HasPrefix(string(got), "["),
					"output must remain valid JSON after stripping (got: %q)", string(got))
			}
		})
	}
}

// TestMustMarshalJSON_MarshalErrorReturnsNull pins the safety fallback —
// when json.Marshal itself fails (e.g., a function-typed field), the
// writer must produce "null" rather than crashing the whole row.
func TestMustMarshalJSON_MarshalErrorReturnsNull(t *testing.T) {
	t.Parallel()
	// Channels can't be marshaled to JSON.
	got := mustMarshalJSON(make(chan int))
	assert.Equal(t, "null", string(got))
}
