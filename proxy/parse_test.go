package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseProxyURL_PasswordWithEquals pins that a Decodo-style password
// containing '=' round-trips through url.Parse → WebshareProxy.Password
// without corruption. RFC 3986 lists '=' in sub-delims, which is permitted
// in userinfo, so Go's url.Parse must accept it raw — but if a future
// refactor swaps to a stricter parser or pre-percent-encodes user input,
// this test catches the silent credential mangling that would otherwise
// surface only as 407 Proxy Authentication Required at the upstream gate.
func TestParseProxyURL_PasswordWithEquals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantUser string
		wantPass string
		wantHost string
		wantPort string
	}{
		{
			name:     "decodo password with single =",
			input:    "http://spf9syt8fu:ogCy6lj1Q9db=1eYAu@gate.decodo.com:10001",
			wantUser: "spf9syt8fu",
			wantPass: "ogCy6lj1Q9db=1eYAu",
			wantHost: "gate.decodo.com",
			wantPort: "10001",
		},
		{
			name:     "password with multiple sub-delims",
			input:    "http://user:p!a$s&w'o(r)d*+,;=word@gate.decodo.com:10005",
			wantUser: "user",
			wantPass: "p!a$s&w'o(r)d*+,;=word",
			wantHost: "gate.decodo.com",
			wantPort: "10005",
		},
		{
			name:     "no password (anonymous proxy)",
			input:    "http://gate.decodo.com:10001",
			wantUser: "",
			wantPass: "",
			wantHost: "gate.decodo.com",
			wantPort: "10001",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseProxyURL(tc.input)
			require.NoError(t, err, "parseProxyURL must accept RFC-3986-valid userinfo")
			assert.Equal(t, tc.wantUser, got.Username, "username mismatch")
			assert.Equal(t, tc.wantPass, got.Password, "password mismatch — '=' must NOT be stripped or decoded")
			assert.Equal(t, tc.wantHost, got.Address)
			assert.Equal(t, tc.wantPort, got.Port)
		})
	}
}

// TestParseProxyURL_RejectsMissingPort pins the host:port shape — without
// a port, net.SplitHostPort fails and the URL is rejected. (Note: empty
// host with valid port currently slips through; flagged separately.)
func TestParseProxyURL_RejectsMissingPort(t *testing.T) {
	t.Parallel()
	_, err := parseProxyURL("http://user:pass@gate.decodo.com")
	assert.Error(t, err, "missing port must return an error, not a zero-value proxy")
}
