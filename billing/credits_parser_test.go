package billing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCreditsStrict(t *testing.T) {
	t.Parallel()

	t.Run("valid_whole_number", func(t *testing.T) {
		n, err := parseCreditsStrict("100")
		require.NoError(t, err)
		require.Equal(t, 100, n)
	})

	t.Run("trimmed_whitespace", func(t *testing.T) {
		n, err := parseCreditsStrict("  500  ")
		require.NoError(t, err)
		require.Equal(t, 500, n)
	})

	t.Run("rejects_trailing_garbage", func(t *testing.T) {
		_, err := parseCreditsStrict("1000 garbage")
		require.Error(t, err)
		require.Contains(t, err.Error(), "whole positive integer")
	})

	t.Run("rejects_decimal", func(t *testing.T) {
		_, err := parseCreditsStrict("10.5")
		require.Error(t, err)
	})

	t.Run("rejects_zero", func(t *testing.T) {
		_, err := parseCreditsStrict("0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "> 0")
	})

	t.Run("rejects_negative", func(t *testing.T) {
		_, err := parseCreditsStrict("-10")
		require.Error(t, err)
	})

	t.Run("rejects_empty", func(t *testing.T) {
		_, err := parseCreditsStrict("")
		require.Error(t, err)
	})

	t.Run("rejects_only_whitespace", func(t *testing.T) {
		_, err := parseCreditsStrict("   ")
		require.Error(t, err)
	})

	t.Run("rejects_above_cap", func(t *testing.T) {
		_, err := parseCreditsStrict("10001")
		require.Error(t, err)
		require.Contains(t, strings.ToLower(err.Error()), "maximum")
	})

	t.Run("accepts_exactly_at_cap", func(t *testing.T) {
		n, err := parseCreditsStrict("10000")
		require.NoError(t, err)
		require.Equal(t, 10_000, n)
	})
}
