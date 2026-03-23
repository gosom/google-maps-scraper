package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// Encryptor handles AES-256-GCM encryption/decryption.
type Encryptor struct {
	gcm cipher.AEAD
}

// New creates an Encryptor from a raw key string (must be exactly 32 bytes).
// Returns nil, nil if key is empty (encryption disabled).
func New(key string) (*Encryptor, error) {
	if key == "" {
		return nil, nil
	}
	keyBytes := []byte(key)
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes long, got %d", len(keyBytes))
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &Encryptor{gcm: gcm}, nil
}

// Encrypt encrypts a plaintext string using AES-GCM.
// It returns a base64 encoded string containing the nonce and ciphertext.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64 encoded string (nonce+ciphertext) using AES-GCM.
func (e *Encryptor) Decrypt(cryptoText string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}
	nonceSize := e.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}
	return string(plaintext), nil
}

// Deprecated: Use New() and inject the *Encryptor instead.
// Encrypt encrypts a plaintext string using AES-GCM with the key from ENCRYPTION_KEY env var.
func Encrypt(plaintext string) (string, error) {
	e, err := New(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return "", err
	}
	if e == nil {
		return "", errors.New("ENCRYPTION_KEY environment variable not set")
	}
	return e.Encrypt(plaintext)
}

// Deprecated: Use New() and inject the *Encryptor instead.
// Decrypt decrypts a base64 encoded string using AES-GCM with the key from ENCRYPTION_KEY env var.
func Decrypt(cryptoText string) (string, error) {
	e, err := New(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return "", err
	}
	if e == nil {
		return "", errors.New("ENCRYPTION_KEY environment variable not set")
	}
	return e.Decrypt(cryptoText)
}
