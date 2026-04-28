package config_test

import (
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestLoadCLIBootstrap_InstallPlaywrightOnly(t *testing.T) {
	t.Run("set to 1 enables install-only mode", func(t *testing.T) {
		t.Setenv("PLAYWRIGHT_INSTALL_ONLY", "1")

		b := config.LoadCLIBootstrap()
		assert.True(t, b.InstallPlaywrightOnly)
	})

	t.Run("empty string disables install-only mode", func(t *testing.T) {
		t.Setenv("PLAYWRIGHT_INSTALL_ONLY", "")

		b := config.LoadCLIBootstrap()
		assert.False(t, b.InstallPlaywrightOnly)
	})

	t.Run("any other value disables install-only mode", func(t *testing.T) {
		t.Setenv("PLAYWRIGHT_INSTALL_ONLY", "true")

		b := config.LoadCLIBootstrap()
		assert.False(t, b.InstallPlaywrightOnly)
	})

	t.Run("unset disables install-only mode", func(t *testing.T) {
		// t.Setenv restores the previous value after the test, so setting to
		// empty here simulates the var being absent.
		t.Setenv("PLAYWRIGHT_INSTALL_ONLY", "")

		b := config.LoadCLIBootstrap()
		assert.False(t, b.InstallPlaywrightOnly)
	})
}
