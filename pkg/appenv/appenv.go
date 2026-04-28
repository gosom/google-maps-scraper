// Package appenv represents the runtime deployment environment as a typed
// value parsed once at startup from the APP_ENV environment variable.
//
// Why a typed value instead of os.Getenv("APP_ENV") at every call site:
//
//  1. Single source of truth. APP_ENV is read exactly once, at the binary's
//     entry point. Handlers and services receive Environment via dependency
//     injection — they never touch os.Getenv. This eliminates the class of
//     bug where one site checks "production" and another silently misses
//     because of a typo, casing difference, or trailing whitespace.
//
//  2. Whitelist parsing. Parse rejects unknown values at startup with a
//     descriptive error, instead of degrading silently to non-production
//     behavior at runtime. APP_ENV=Productoin (typo) fails fast — it does
//     not weaken security headers, cookie flags, or fail-fast guards.
//
//  3. Compile-time safety. IsProduction() is a method on the type, so a
//     reviewer cannot accidentally write env == "prouction" or compare to
//     the wrong literal — the compiler enforces the constants.
//
// This consolidates the previous APP_ENV + IS_PRODUCTION pair into one
// signal. The CWE-614 cookie-secure path that previously read IS_PRODUCTION
// now reads Environment.IsProduction() from the injected handler config.
package appenv

import (
	"fmt"
	"strings"
)

// Environment is the parsed value of the APP_ENV environment variable.
// The zero value is Development, which is the safe default for tests and
// local runs that don't set APP_ENV.
type Environment int

const (
	// Development is the default for local runs and tests. Production
	// fail-fast guards are bypassed; cookie Secure flags follow r.TLS only.
	Development Environment = iota
	// Staging is a production-like environment without the strict secret
	// requirements. IsProduction() returns false — staging deploys must
	// tolerate the same security defaults as production by setting them
	// explicitly, not by relying on this flag.
	Staging
	// Production is the live customer-facing environment. Triggers strict
	// startup validation (required secrets, required S3 credentials) and
	// forces cookie Secure=true regardless of r.TLS / X-Forwarded-Proto.
	Production
)

// Parse converts a raw APP_ENV string into a typed Environment. The empty
// string parses as Development (the convention for unset env in local runs).
// Any other unrecognized value returns an error so the caller can fail fast
// rather than silently default to a non-production code path.
//
// Accepted values (case-insensitive, whitespace-trimmed):
//   - "" | "development" | "dev"   -> Development
//   - "staging" | "stage"          -> Staging
//   - "production" | "prod"        -> Production
func Parse(raw string) (Environment, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "development", "dev":
		return Development, nil
	case "staging", "stage":
		return Staging, nil
	case "production", "prod":
		return Production, nil
	default:
		return Development, fmt.Errorf("invalid APP_ENV %q (expected one of: development, staging, production)", raw)
	}
}

// IsProduction reports whether this environment is the live production
// deployment. Use at the call site whenever a behavior must be hardened
// for live traffic (cookie Secure flag, strict validation, etc.).
func (e Environment) IsProduction() bool { return e == Production }

// String returns the canonical lowercase name suitable for logs.
func (e Environment) String() string {
	switch e {
	case Production:
		return "production"
	case Staging:
		return "staging"
	case Development:
		return "development"
	default:
		return "unknown"
	}
}
