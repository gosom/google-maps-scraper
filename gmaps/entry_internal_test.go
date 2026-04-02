package gmaps

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_getNthElementAndCast_DoesNotPanicOnOutOfRangeFinalIndex(t *testing.T) {
	t.Parallel()

	arr := []any{
		[]any{"a"},
	}

	require.NotPanics(t, func() {
		v := getNthElementAndCast[string](arr, 0, 1) // inner index out of range
		require.Equal(t, "", v)
	})
}

func Test_getNthElementAndCast_DoesNotPanicOnOutOfRangeSingleIndex(t *testing.T) {
	t.Parallel()

	arr := []any{"a"}

	require.NotPanics(t, func() {
		v := getNthElementAndCast[string](arr, 1) // index out of range
		require.Equal(t, "", v)
	})
}

func Test_getNthElementAndCast_DoesNotPanicOnNegativeIndex(t *testing.T) {
	t.Parallel()

	arr := []any{"a"}

	require.NotPanics(t, func() {
		v := getNthElementAndCast[string](arr, -1)
		require.Equal(t, "", v)
	})
}
