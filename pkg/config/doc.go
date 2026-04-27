// Package config is the single boundary between the operating-system
// environment and the Go process. APP_ENV, DSN, CLERK_SECRET_KEY, every
// secret and every tunable, are read here exactly once at startup.
//
// All other packages receive a *Config (or a focused sub-view of it) by
// dependency injection. After this package is wired in, the codebase
// must satisfy this invariant:
//
//	grep -rn 'os.Getenv\|os.LookupEnv' --include='*.go' .  | \
//	    grep -v '_test.go' | grep -v 'pkg/config/'
//	(empty result)
//
// Why one place:
//
//   - Single manifest. The Config struct *is* the documentation of what
//     this binary consumes from the environment.
//   - Fail-fast. Required fields are validated at Load() time before any
//     handler can serve a request.
//   - Test-friendly. Tests construct *Config directly; no t.Setenv plumbing.
//   - Immutable. *Config is treated as read-only after Load() returns.
//
// This package depends only on appenv and the caarlos0/env/v11 parser.
// It does not depend on any other internal package — keeping the import
// graph one-directional out of config into everything else.
package config
