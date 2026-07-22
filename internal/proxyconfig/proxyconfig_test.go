package proxyconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/internal/proxyconfig"
)

func TestResolveInlineCompatibility(t *testing.T) {
	t.Parallel()

	got, err := proxyconfig.Resolve("http://first:8080,socks5://second:1080", "")

	require.NoError(t, err)
	require.Equal(t, []string{"http://first:8080", "socks5://second:1080"}, got)
}

func TestResolveFromFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxies.txt")
	err := os.WriteFile(path, []byte("\n# comment\n http://user:secret@host:8080 \n\tsocks5://host:1080\n"), 0o600)
	require.NoError(t, err)

	got, err := proxyconfig.Resolve("", path)

	require.NoError(t, err)
	require.Equal(t, []string{"http://user:secret@host:8080", "socks5://host:1080"}, got)
}

func TestResolveRejectsConflictWithoutLeakingSecret(t *testing.T) {
	t.Parallel()

	const secret = "do-not-leak"

	_, err := proxyconfig.Resolve("http://user:"+secret+"@host:8080", "proxies.txt")

	require.ErrorIs(t, err, proxyconfig.ErrConflict)
	require.NotContains(t, err.Error(), secret)
}

func TestResolveRejectsEmptyFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxies.txt")
	err := os.WriteFile(path, []byte("\n # comment only\n"), 0o600)
	require.NoError(t, err)

	_, err = proxyconfig.Resolve("", path)

	require.ErrorIs(t, err, proxyconfig.ErrEmptyFile)
}

func TestResolveReportsMissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.txt")

	_, err := proxyconfig.Resolve("", path)

	require.Error(t, err)
	require.Contains(t, err.Error(), path)
}

func TestResolveWithoutConfiguration(t *testing.T) {
	t.Parallel()

	got, err := proxyconfig.Resolve("", "")

	require.NoError(t, err)
	require.Nil(t, got)
}
