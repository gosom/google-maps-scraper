package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"golang.org/x/crypto/argon2"
)

const (
	// APIKeyPrefix is the required prefix for all BrezelScraper API keys.
	// Tokens beginning with this prefix are routed to API key auth instead of Clerk JWT.
	APIKeyPrefix = "bscraper_"

	// apiKeyRandomLength is the number of base62 characters in the random portion.
	apiKeyRandomLength = 32
)

// ErrInvalidAPIKey is returned when API key validation fails.
var ErrInvalidAPIKey = errors.New("invalid API key")

// argon2Semaphore limits concurrent Argon2id computations to prevent memory
// exhaustion from parallel requests (each operation allocates 64 MB).
var argon2Semaphore = make(chan struct{}, 4)

// base62 alphabet used for encoding random bytes.
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// GenerateAPIKey creates a new API key with secure dual-hash storage.
// It returns the populated APIKey model (ready to persist) and the plaintext
// key that must be shown to the user exactly once.
func GenerateAPIKey(userID, name string, serverSecret []byte) (*models.APIKey, string, error) {
	// 1. Generate 32 random bytes (256 bits entropy).
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// 2. Encode to base62, truncate to apiKeyRandomLength chars.
	randomStr := encodeBase62(randomBytes)[:apiKeyRandomLength]
	fullKey := APIKeyPrefix + randomStr

	// 3. Fast lookup hash: HMAC-SHA256(serverSecret, fullKey).
	lookupHash := computeLookupHash(fullKey, serverSecret)

	// 4. Unique salt for verification hash.
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// 5. Slow verification hash: Argon2id.
	keyHashBytes := argon2.IDKey([]byte(fullKey), salt, 1, 64*1024, 4, 32)

	// 6. Display hints: prefix first 8 chars, suffix last 4 chars.
	prefix := fullKey[:8]
	suffix := fullKey[len(fullKey)-4:]

	apiKey := &models.APIKey{
		ID:            uuid.New().String(),
		UserID:        userID,
		Name:          name,
		LookupHash:    lookupHash,
		KeyHash:       hex.EncodeToString(keyHashBytes),
		KeySalt:       salt,
		KeyHintPrefix: prefix,
		KeyHintSuffix: suffix,
		HashAlgorithm: "argon2id",
		Scopes:        []string{"full_access"},
	}

	return apiKey, fullKey, nil
}

// ValidateAPIKey verifies a raw API key against stored hashes.
// It uses constant-time operations throughout to prevent timing attacks.
// Returns (userID, keyID, nil) on success.
func ValidateAPIKey(ctx context.Context, providedKey string, serverSecret []byte, repo models.APIKeyRepository) (string, string, error) {
	// Fast path: lookup by HMAC hash.
	lookupHash := computeLookupHash(providedKey, serverSecret)
	apiKey, err := repo.GetByLookupHash(ctx, lookupHash)

	keyExists := (err == nil && apiKey != nil)

	// Always compute Argon2id to maintain constant execution time.
	var salt []byte
	if keyExists {
		salt = apiKey.KeySalt
	} else {
		// Deterministic dummy salt — prevents timing distinguishability.
		salt = dummySalt(providedKey)
	}

	// Acquire semaphore to limit concurrent Argon2 operations (DoS mitigation).
	select {
	case argon2Semaphore <- struct{}{}:
		defer func() { <-argon2Semaphore }()
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	computedHash := argon2.IDKey([]byte(providedKey), salt, 1, 64*1024, 4, 32)

	// Constant-time comparison.
	var storedHash []byte
	if keyExists {
		storedHash, _ = hex.DecodeString(apiKey.KeyHash)
	} else {
		storedHash = make([]byte, 32)
	}

	if !keyExists || !hmac.Equal(computedHash, storedHash) {
		return "", "", ErrInvalidAPIKey
	}

	return apiKey.UserID, apiKey.ID, nil
}

// computeLookupHash returns HMAC-SHA256(serverSecret, key) as a hex string.
func computeLookupHash(key string, serverSecret []byte) string {
	h := hmac.New(sha256.New, serverSecret)
	h.Write([]byte(key))
	return hex.EncodeToString(h.Sum(nil))
}

// dummySalt returns a deterministic salt for non-existent keys.
// This ensures Argon2id always runs with a valid salt, preventing timing leaks.
func dummySalt(key string) []byte {
	h := sha256.Sum256([]byte("bscraper_dummy_salt_" + key))
	return h[:16]
}

// encodeBase62 encodes a byte slice to a base62 string of at least apiKeyRandomLength chars.
func encodeBase62(data []byte) string {
	num := new(big.Int).SetBytes(data)
	base := big.NewInt(62)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var result []byte
	for num.Cmp(zero) > 0 {
		num.DivMod(num, base, mod)
		result = append([]byte{base62Chars[mod.Int64()]}, result...)
	}

	for len(result) < apiKeyRandomLength {
		result = append([]byte{base62Chars[0]}, result...)
	}

	return string(result)
}
