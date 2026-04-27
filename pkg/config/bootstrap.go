package config

import "os"

// CLIBootstrap holds the single flag that is read before normal startup to
// short-circuit into Playwright-browser-install mode. It is intentionally
// separate from Config because PLAYWRIGHT_INSTALL_ONLY must be checked
// before the database DSN and other required fields are validated — the
// install sub-command exits immediately without any server initialization.
//
// This is the only place in pkg/config where os.Getenv is used outside of
// the caarlos0/env parser, and it is documented here to explain why:
// the flag must be evaluated before Load() is called.
type CLIBootstrap struct {
	// InstallPlaywrightOnly is true when PLAYWRIGHT_INSTALL_ONLY=1.
	// When set, the binary installs Playwright browsers and exits.
	InstallPlaywrightOnly bool
}

// LoadCLIBootstrap reads the PLAYWRIGHT_INSTALL_ONLY environment variable
// and returns a CLIBootstrap. It intentionally uses os.Getenv directly
// (rather than caarlos0/env) because this check occurs before the main
// Config is loaded — the caller needs to know whether to skip all normal
// initialization and just install Playwright browsers.
func LoadCLIBootstrap() CLIBootstrap {
	return CLIBootstrap{
		InstallPlaywrightOnly: os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1",
	}
}
