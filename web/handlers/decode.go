package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrInvalidJSONBody is the sentinel returned by decodeStrict /
// decodeStrictOptional when the request body fails to decode for ANY
// reason — malformed JSON, unknown field, trailing data, type mismatch,
// or oversized body. Callers should respond with a generic 4xx and log
// the wrapped error server-side rather than echoing it to the client.
//
// Why generic responses matter: encoding/json error messages can include
// the user-supplied field name verbatim. A request like
// `{"<script>alert(1)</script>":1}` produces the error
// `json: unknown field "<script>alert(1)</script>"`. Reflecting this in
// an HTML response or a client-side toast would expose the API to
// stored/reflected XSS or log injection. Always render a generic
// "invalid request body" message to the client.
var ErrInvalidJSONBody = errors.New("invalid request body")

// decodeStrict decodes a JSON request body into v with the defensive
// settings the global middleware does NOT cover:
//
//   - DisallowUnknownFields: rejects payloads with extra fields. This
//     blocks confusion / request-smuggling attacks where a client sends
//     fields the server silently ignores hoping a future deserializer
//     accepts them.
//   - More() check: rejects trailing non-whitespace bytes after the
//     document. Without this, JSON streams ({"a":1}{"b":2}) decode the
//     first object successfully and silently drop the rest, which is a
//     known foothold for parser-divergence attacks.
//
// Body size is enforced at the router boundary by the MaxBodySize
// middleware (web/middleware/middleware.go) — this helper does NOT
// install MaxBytesReader. Handlers that need a tighter per-route cap
// (e.g., webhook handlers wanting 64 KB instead of the global 1 MB)
// should wrap r.Body with http.MaxBytesReader BEFORE calling this
// helper; the wrapping is preserved through the underlying io.Reader.
//
// Returns ErrInvalidJSONBody (wrapped with the underlying error for
// server-side logging) on any decode failure. Callers MUST render a
// generic message to the client and log err for debugging.
func decodeStrict(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSONBody, err)
	}
	if d.More() {
		return fmt.Errorf("%w: unexpected trailing data", ErrInvalidJSONBody)
	}
	return nil
}

// decodeStrictOptional behaves like decodeStrict but treats an empty
// request body as a successful no-op (v is left at its zero value).
// Use this for endpoints where the JSON body is optional, e.g., a POST
// that takes optional configuration.
//
// Unknown fields and trailing data are still rejected when a body IS
// present — "optional" applies only to the empty-body case, never to
// the schema.
func decodeStrictOptional(r *http.Request, v any) error {
	err := decodeStrict(r, v)
	if err == nil {
		return nil
	}
	// io.EOF surfaces from the underlying decoder when the body is
	// empty. The decodeStrict wrapper preserves the chain via %w, so
	// errors.Is walks through ErrInvalidJSONBody to find io.EOF.
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
