package appenv

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in           string
		want         Environment
		wantErr      bool
		isProduction bool
	}{
		{in: "", want: Development, isProduction: false},
		{in: "development", want: Development, isProduction: false},
		{in: "Development", want: Development, isProduction: false},
		{in: "  dev  ", want: Development, isProduction: false},
		{in: "staging", want: Staging, isProduction: false},
		{in: "STAGE", want: Staging, isProduction: false},
		{in: "production", want: Production, isProduction: true},
		{in: "PRODUCTION", want: Production, isProduction: true},
		{in: "  prod\n", want: Production, isProduction: true},
		{in: "prouction", wantErr: true}, // typo
		{in: "live", wantErr: true},
		{in: "test", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q): want error, got nil (returned %v)", tc.in, got)
				}
				if !strings.Contains(err.Error(), "APP_ENV") {
					t.Errorf("Parse(%q): error should reference APP_ENV, got: %v", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if got.IsProduction() != tc.isProduction {
				t.Errorf("Parse(%q).IsProduction() = %v, want %v", tc.in, got.IsProduction(), tc.isProduction)
			}
		})
	}
}

func TestEnvironment_String(t *testing.T) {
	cases := map[Environment]string{
		Development:     "development",
		Staging:         "staging",
		Production:      "production",
		Environment(99): "unknown",
	}
	for e, want := range cases {
		if got := e.String(); got != want {
			t.Errorf("Environment(%d).String() = %q, want %q", e, got, want)
		}
	}
}

// TestZeroValueIsDevelopment locks in the contract that the zero value of
// Environment is Development — relied on by test code and any caller that
// constructs an Environment without parsing.
func TestZeroValueIsDevelopment(t *testing.T) {
	var e Environment
	if e != Development {
		t.Errorf("zero value = %v, want Development", e)
	}
	if e.IsProduction() {
		t.Error("zero value must not be production")
	}
}
