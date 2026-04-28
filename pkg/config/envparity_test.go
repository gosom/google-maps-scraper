package config_test

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractStructEnvVars walks the struct type t (recursively for nested
// structs), collecting every `env:"NAME"` tag, respecting envPrefix
// inherited from the parent field's tag.
func extractStructEnvVars(t reflect.Type, prefix string) []string {
	vars := make([]string, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)
		fieldType := field.Type

		// Resolve pointer types.
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		// Get the envPrefix for nested structs.
		envPrefix := field.Tag.Get("envPrefix")

		if fieldType.Kind() == reflect.Struct && envPrefix != "" {
			// Recurse into nested struct with accumulated prefix.
			nested := extractStructEnvVars(fieldType, prefix+envPrefix)
			vars = append(vars, nested...)

			continue
		}

		if fieldType.Kind() == reflect.Struct && envPrefix == "" {
			// Struct without envPrefix (e.g. BuildConfig) — fields have their
			// own env tags with no prefix accumulation.
			nested := extractStructEnvVars(fieldType, prefix)
			vars = append(vars, nested...)

			continue
		}

		tag := field.Tag.Get("env")
		if tag == "" {
			continue
		}

		// Strip options like ",required" from the tag value.
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}

		vars = append(vars, prefix+name)
	}

	return vars
}

// readEnvExampleKeys parses the project-root .env.example and returns
// all non-empty, non-comment KEY names (everything before the first '=').
func readEnvExampleKeys(t *testing.T) map[string]struct{} {
	t.Helper()

	path := "../../.env.example"

	f, err := os.Open(path)
	require.NoError(t, err, "open .env.example")

	defer f.Close()

	keys := make(map[string]struct{})

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		if key != "" {
			keys[key] = struct{}{}
		}
	}

	require.NoError(t, scanner.Err())

	return keys
}

// TestEnvExampleParity asserts that the symmetric difference between the
// env vars declared in Config (plus PLAYWRIGHT_INSTALL_ONLY from
// CLIBootstrap) and the keys present in .env.example is empty.
func TestEnvExampleParity(t *testing.T) {
	// Collect vars from the Config struct.
	structVars := extractStructEnvVars(reflect.TypeOf(config.Config{}), "")

	// Add the CLI bootstrap var (not in Config; loaded separately before startup).
	structVars = append(structVars, "PLAYWRIGHT_INSTALL_ONLY")

	structSet := make(map[string]struct{}, len(structVars))

	for _, v := range structVars {
		structSet[v] = struct{}{}
	}

	exampleSet := readEnvExampleKeys(t)

	// Keys in struct but missing from .env.example.
	var missingFromExample []string

	for k := range structSet {
		if _, ok := exampleSet[k]; !ok {
			missingFromExample = append(missingFromExample, k)
		}
	}

	// Keys in .env.example but not in struct (or bootstrap).
	var extraInExample []string

	for k := range exampleSet {
		if _, ok := structSet[k]; !ok {
			extraInExample = append(extraInExample, k)
		}
	}

	assert.Empty(t, missingFromExample,
		fmt.Sprintf("env vars in Config/CLIBootstrap but missing from .env.example:\n  %s",
			strings.Join(missingFromExample, "\n  ")))

	assert.Empty(t, extraInExample,
		fmt.Sprintf("keys in .env.example not found in Config/CLIBootstrap:\n  %s",
			strings.Join(extraInExample, "\n  ")))
}
