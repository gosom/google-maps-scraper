package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
)

// Encrypt encrypts a plaintext string using AES-GCM with the key from ENCRYPTION_KEY env var.
// It returns a base64 encoded string containing the nonce and ciphertext.
func Encrypt(plaintext string) (string, error) {
	keyHex := os.Getenv("ENCRYPTION_KEY")
	if keyHex == "" {
		return "", errors.New("ENCRYPTION_KEY environment variable not set")
	}

	// In a real app, you might want to decode hex if the key is stored as hex string
	// For simplicity here, assuming the key is raw bytes or we just use the string bytes if it's 32 chars.
	// Ideally, ENCRYPTION_KEY should be a 32-byte hex string or base64 string.
	// Let's assume it's a 32-byte string for now or handle it properly.
	// Better: Expect a hex encoded string.

	// Let's stick to a simple implementation: Key must be 32 bytes.
	// If the user provides a string, we should probably hash it or ensure it's 32 bytes.
	// For this implementation, let's assume the user provides a 32-byte string or we error.
	key := []byte(keyHex)
	if len(key) != 32 {
		return "", errors.New("ENCRYPTION_KEY must be exactly 32 bytes long")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64 encoded string (nonce+ciphertext) using AES-GCM.
func Decrypt(cryptoText string) (string, error) {
	keyHex := os.Getenv("ENCRYPTION_KEY")
	if keyHex == "" {
		return "", errors.New("ENCRYPTION_KEY environment variable not set")
	}
	key := []byte(keyHex)
	if len(key) != 32 {
		return "", errors.New("ENCRYPTION_KEY must be exactly 32 bytes long")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
