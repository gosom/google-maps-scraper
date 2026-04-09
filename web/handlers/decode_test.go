package handlers

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// helperPayload is a tiny target struct used by the decodeStrict tests.
// It must NOT include any field besides Name so the unknown-field test
// has a clear surface to assert against.
type helperPayload struct {
	Name string `json:"name"`
}

func TestDecodeStrict_AcceptsValidPayload(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"foo"}`))
	var v helperPayload
	if err := decodeStrict(req, &v); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if v.Name != "foo" {
		t.Errorf("expected Name=foo, got %q", v.Name)
	}
}

// TestDecodeStrict_RejectsUnknownFields locks the DisallowUnknownFields
// behavior. A malicious client adding an extra field — `admin`, in the
// classic confusion-attack pattern — must be rejected with the sentinel
// error so handlers can render a generic 4xx.
func TestDecodeStrict_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"foo","admin":true}`))
	var v helperPayload
	err := decodeStrict(req, &v)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONBody) {
		t.Errorf("expected ErrInvalidJSONBody, got: %v", err)
	}
}

// TestDecodeStrict_RejectsTrailingData covers the d.More() check. Two
// concatenated JSON documents must be rejected — without this, a parser-
// divergence attacker can send {"a":1}{"b":2} and rely on the server
// decoding only the first object.
func TestDecodeStrict_RejectsTrailingData(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"foo"}{"name":"bar"}`))
	var v helperPayload
	err := decodeStrict(req, &v)
	if err == nil {
		t.Fatal("expected error for trailing data, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONBody) {
		t.Errorf("expected ErrInvalidJSONBody, got: %v", err)
	}
}

func TestDecodeStrict_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{not json`))
	var v helperPayload
	err := decodeStrict(req, &v)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONBody) {
		t.Errorf("expected ErrInvalidJSONBody, got: %v", err)
	}
}

func TestDecodeStrict_RejectsTypeMismatch(t *testing.T) {
	t.Parallel()
	// Name is a string in the target struct; sending a number is a type
	// error that decodeStrict should surface as ErrInvalidJSONBody.
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":12345}`))
	var v helperPayload
	err := decodeStrict(req, &v)
	if err == nil {
		t.Fatal("expected error for type mismatch, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONBody) {
		t.Errorf("expected ErrInvalidJSONBody, got: %v", err)
	}
}

// TestDecodeStrict_RejectsEmptyBody is the negative-space sibling of
// TestDecodeStrictOptional_AcceptsEmptyBody — the strict variant treats
// empty as an error so endpoints that genuinely require a body don't
// silently accept empty requests.
func TestDecodeStrict_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(``))
	var v helperPayload
	err := decodeStrict(req, &v)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected wrapped io.EOF for empty body, got: %v", err)
	}
}

func TestDecodeStrictOptional_AcceptsEmptyBody(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(``))
	var v helperPayload
	if err := decodeStrictOptional(req, &v); err != nil {
		t.Errorf("expected nil for empty body, got: %v", err)
	}
	if v.Name != "" {
		t.Errorf("expected zero value, got %q", v.Name)
	}
}

func TestDecodeStrictOptional_AcceptsValidPayload(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"foo"}`))
	var v helperPayload
	if err := decodeStrictOptional(req, &v); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if v.Name != "foo" {
		t.Errorf("expected Name=foo, got %q", v.Name)
	}
}

// TestDecodeStrictOptional_StillRejectsUnknownFields locks the contract
// that "optional" applies only to the empty-body case, NEVER to the
// schema. A non-empty body with extra fields is still a 4xx.
func TestDecodeStrictOptional_StillRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"admin":true}`))
	var v helperPayload
	err := decodeStrictOptional(req, &v)
	if err == nil {
		t.Fatal("expected error for unknown field on non-empty body, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONBody) {
		t.Errorf("expected ErrInvalidJSONBody, got: %v", err)
	}
}

func TestDecodeStrictOptional_RejectsTrailingDataOnNonEmptyBody(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"foo"}{"name":"bar"}`))
	var v helperPayload
	if err := decodeStrictOptional(req, &v); err == nil {
		t.Fatal("expected error for trailing data, got nil")
	}
}
