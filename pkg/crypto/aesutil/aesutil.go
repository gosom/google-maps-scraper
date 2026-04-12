package aesutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce.
// The returned byte slice is formatted as: nonce || ciphertext || tag.
func Encrypt(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aesutil: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aesutil: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("aesutil: generate nonce: %w", err)
	}

	// Seal appends the ciphertext+tag to nonce, producing nonce || ciphertext || tag.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts ciphertext produced by Encrypt using AES-256-GCM.
// It expects the input formatted as: nonce || ciphertext || tag.
func Decrypt(key [32]byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aesutil: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aesutil: new gcm: %w", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("aesutil: ciphertext too short")
	}

	nonce := ciphertext[:gcm.NonceSize()]
	enc := ciphertext[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return nil, fmt.Errorf("aesutil: decrypt failed (data may be tampered): %w", err)
	}

	return plaintext, nil
}

// DeriveKey derives a 32-byte key from a server secret and a purpose string
// using HMAC-SHA256.
func DeriveKey(serverSecret []byte, purpose string) [32]byte {
	mac := hmac.New(sha256.New, serverSecret)
	mac.Write([]byte(purpose))
	sum := mac.Sum(nil)

	var key [32]byte
	copy(key[:], sum)
	return key
}
