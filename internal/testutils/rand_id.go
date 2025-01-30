package testutils

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	mathrand "math/rand"
	"sync"
	"testing"
	"time"
)

var (
	rnd = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
)

// Package testutils provides testing utilities, including secure and non-secure random ID generation
// functionality. It offers both simple functions for basic use cases and a configurable generator
// for more complex scenarios.

// GenerateRandomId generates a random integer between min and max (inclusive).
// This function uses math/rand for better performance but is not cryptographically secure.
// If min is greater than max, the values will be swapped.
//
// Parameters:
//   - min: The minimum value (inclusive) of the range
//   - max: The maximum value (inclusive) of the range
//
// Returns:
//   - A random integer within the specified range
func GenerateRandomId(min, max int) int {
	if min > max {
		min, max = max, min
	}

	rndMu.Lock()
	defer rndMu.Unlock()

	return rnd.Intn(max-min+1) + min
}

// GenerateSecureRandomId generates a cryptographically secure random integer between
// min and max (inclusive) using crypto/rand. If the secure random number generation
// fails, it falls back to the less secure GenerateRandomId function.
//
// Parameters:
//   - min: The minimum value (inclusive) of the range
//   - max: The maximum value (inclusive) of the range
//
// Returns:
//   - A cryptographically secure random integer within the specified range
func GenerateSecureRandomId(min, max int) int {
	if min > max {
		min, max = max, min
	}

	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		// Fall back to less secure version if crypto/rand fails
		return GenerateRandomId(min, max)
	}

	return int(nBig.Int64()) + min
}

// RandomIdGenerator provides a thread-safe, configurable random ID generation facility.
// It supports both secure (crypto/rand) and non-secure (math/rand) number generation.
type RandomIdGenerator struct {
	mu        sync.Mutex
	useSecure bool
	source    mathrand.Source
}

// NewRandomIdGenerator creates a new RandomIdGenerator with the specified security preference.
//
// Parameters:
//   - useSecure: If true, uses crypto/rand for secure number generation;
//     if false, uses math/rand for faster but less secure generation
//
// Returns:
//   - A pointer to a new RandomIdGenerator instance
func NewRandomIdGenerator(useSecure bool) *RandomIdGenerator {
	return &RandomIdGenerator{
		useSecure: useSecure,
		source:    mathrand.NewSource(time.Now().UnixNano()),
	}
}

// GenerateId generates a random ID with comprehensive error handling and validation.
// It supports both secure and non-secure number generation based on the generator's configuration.
//
// Parameters:
//   - min: The minimum value (inclusive) of the range
//   - max: The maximum value (inclusive) of the range
//
// Returns:
//   - A random integer within the specified range
//   - An error if the input parameters are invalid or if random number generation fails
func (g *RandomIdGenerator) GenerateId(min, max int) (int, error) {
	// Input validation
	if min > max {
		return 0, fmt.Errorf("min (%d) cannot be greater than max (%d)", min, max)
	}

	if min < 0 {
		return 0, fmt.Errorf("min (%d) cannot be negative", min)
	}

	if max > math.MaxInt32 {
		return 0, fmt.Errorf("max (%d) cannot be greater than MaxInt32", max)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.useSecure {
		// Use crypto/rand
		rangeSize := big.NewInt(int64(max - min + 1))
		n, err := rand.Int(rand.Reader, rangeSize)
		if err != nil {
			return 0, fmt.Errorf("failed to generate secure random number: %v", err)
		}
		return int(n.Int64()) + min, nil
	}

	// Use math/rand
	return mathrand.New(g.source).Intn(max-min+1) + min, nil
}

// GenerateUniqueIds generates a slice of unique random integers within the specified range.
// This function always uses secure random number generation.
//
// Parameters:
//   - min: The minimum value (inclusive) of the range
//   - max: The maximum value (inclusive) of the range
//   - count: The number of unique IDs to generate
//
// Returns:
//   - A slice of unique random integers
//   - An error if the parameters are invalid or if generation fails
func GenerateUniqueIds(min, max, count int) ([]int, error) {
	if count > (max - min + 1) {
		return nil, fmt.Errorf("cannot generate %d unique numbers in range [%d, %d]", count, min, max)
	}

	generator := NewRandomIdGenerator(true)
	seen := make(map[int]bool)
	results := make([]int, 0, count)

	for len(results) < count {
		id, err := generator.GenerateId(min, max)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			results = append(results, id)
		}
	}

	return results, nil
}

// GenerateRandomIdRange generates a random ID within a predefined range type.
// Supported range types are:
//   - "uint16": [0, 65535]
//   - "uint32": [0, MaxInt32]
//   - "port": [1024, 65535]
//   - "year": [1970, current year]
//   - "age": [0, 120]
//
// Parameters:
//   - rangeType: A string identifying the desired range type
//
// Returns:
//   - A random integer within the specified range type
//   - An error if the range type is unsupported or if generation fails
func GenerateRandomIdRange(rangeType string) (int, error) {
	ranges := map[string]struct{ min, max int }{
		"uint16": {0, 65535},
		"uint32": {0, math.MaxInt32},
		"port":   {1024, 65535},
		"year":   {1970, time.Now().Year()},
		"age":    {0, 120},
	}

	if r, exists := ranges[rangeType]; exists {
		return NewRandomIdGenerator(true).GenerateId(r.min, r.max)
	}
	return 0, fmt.Errorf("unsupported range type: %s", rangeType)
}

// GenerateRandomIdWithPrefix generates an ID with a string prefix and a random number component.
// The random number is generated securely using crypto/rand.
//
// Parameters:
//   - prefix: The string prefix to prepend to the random number
//   - min: The minimum value (inclusive) for the random number
//   - max: The maximum value (inclusive) for the random number
//
// Returns:
//   - A string combining the prefix and random number (format: "prefix123")
//   - An error if the parameters are invalid or if generation fails
func GenerateRandomIdWithPrefix(prefix string, min, max int) (string, error) {
	id, err := NewRandomIdGenerator(true).GenerateId(min, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d", prefix, id), nil
}

// StringPtr returns a pointer to the given string
func StringPtr(s string) *string {
	return &s
}

// Uint64Ptr returns a pointer to the given uint64
func Uint64Ptr(i uint64) *uint64 {
	return &i
}

func Int64Ptr(i int64) *int64 {
	return &i
}

// GetTimeoutContext returns a context that's already timed out
func GetTimeoutContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	return ctx
}
