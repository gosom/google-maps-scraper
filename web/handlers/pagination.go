package handlers

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
)

// Pagination caps are unified across every paginated endpoint to keep
// the API surface predictable and to make resource-exhaustion analysis
// straightforward — every paginated request consumes at most MaxPageLimit
// rows, full stop.
const (
	// DefaultPageLimit is the limit applied when the client omits ?limit=.
	// Aligned with the existing GetJobResults / GetUserResults defaults.
	DefaultPageLimit = 50

	// MaxPageLimit is the hard ceiling. Previously the result endpoints
	// allowed up to 1000 while the job-list endpoint allowed only 100;
	// the divergence let an attacker pull 10x more data per request from
	// the result endpoints than from the job list. Unified at 100.
	MaxPageLimit = 100
)

// parsePagination decodes ?page= and ?limit= for page-based endpoints
// (ListJobs, GetJobResults). Returns (page, limit, offset, error).
//
// Security guarantees:
//   - Negative or non-integer page/limit return 400.
//   - Limit above MaxPageLimit returns 400 (no silent coercion — the
//     audit plan calls out that silent clamping masks client bugs and
//     lets attackers fingerprint the cap by binary search).
//   - The multiplication `(page - 1) * limit` is checked against
//     math.MaxInt32 BEFORE it executes. Without this guard, an attacker
//     can pass `page=2147483647` and overflow the int32 the value
//     ultimately serializes back to (Page field on the response is
//     int — which is int64 on 64-bit hosts but the JSON consumer or
//     SQL OFFSET column may be 32-bit). The check uses MaxInt32 as the
//     conservative ceiling so the same code is safe on 32-bit builds.
//
// If you change the cap, remember the SQL OFFSET column on PostgreSQL
// is bigint (int64), so the database itself can handle much larger
// values — the cap exists to bound user-visible behavior and to keep
// `total + limit < math.MaxInt` arithmetic safe in the response builder.
func parsePagination(r *http.Request, defaultLimit int) (page, limit, offset int, err error) {
	page = 1
	if v := r.URL.Query().Get("page"); v != "" {
		p, parseErr := strconv.Atoi(v)
		if parseErr != nil || p < 1 {
			return 0, 0, 0, errors.New("page must be a positive integer")
		}
		page = p
	}

	limit, err = parseLimitParam(r, defaultLimit)
	if err != nil {
		return 0, 0, 0, err
	}

	// Overflow guard: (page-1)*limit must fit in a non-negative int32.
	// Equivalent to `page-1 <= MaxInt32/limit` rearranged so the
	// multiplication never executes when it would overflow.
	if page-1 > math.MaxInt32/limit {
		return 0, 0, 0, errors.New("page out of range")
	}

	offset = (page - 1) * limit
	return page, limit, offset, nil
}

// parseOffsetPagination decodes ?limit= and ?offset= for offset-based
// endpoints (GetUserResults). Returns (limit, offset, error).
//
// Security guarantees:
//   - Negative or non-integer values return 400.
//   - Limit above MaxPageLimit returns 400 (same unified cap).
//   - Offset has its own ceiling at math.MaxInt32 to mirror the
//     parsePagination guard — without it, a client can pass
//     `offset=9223372036854775806` and break downstream arithmetic
//     that assumes offset+limit fits in int32.
func parseOffsetPagination(r *http.Request, defaultLimit int) (limit, offset int, err error) {
	limit, err = parseLimitParam(r, defaultLimit)
	if err != nil {
		return 0, 0, err
	}

	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		o, parseErr := strconv.Atoi(v)
		if parseErr != nil || o < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		if o > math.MaxInt32 {
			return 0, 0, errors.New("offset out of range")
		}
		offset = o
	}

	return limit, offset, nil
}

// parseLimitParam is the shared limit-validation primitive used by
// both page-based and offset-based helpers. Keeping the cap logic in
// one place ensures parsePagination and parseOffsetPagination cannot
// drift apart.
func parseLimitParam(r *http.Request, defaultLimit int) (int, error) {
	if defaultLimit < 1 || defaultLimit > MaxPageLimit {
		// Programmer error, not user error — surface it loudly so we
		// notice during a code review rather than at runtime.
		return 0, fmt.Errorf("internal: defaultLimit %d outside [1, %d]", defaultLimit, MaxPageLimit)
	}
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		l, parseErr := strconv.Atoi(v)
		if parseErr != nil || l < 1 || l > MaxPageLimit {
			return 0, fmt.Errorf("limit must be between 1 and %d", MaxPageLimit)
		}
		limit = l
	}
	return limit, nil
}
