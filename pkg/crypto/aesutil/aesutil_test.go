package aesutil

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) [32]byte {
	t.Helper()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("hello, AES-256-GCM!")

	ct, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := testKey(t)
	key2 := testKey(t)
	plaintext := []byte("secret data")

	ct, err := Encrypt(key1, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(key2, ct)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("tamper-proof data")

	ct, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a byte in the ciphertext portion (after the 12-byte nonce).
	ct[len(ct)-1] ^= 0xff

	_, err = Decrypt(key, ct)
	if err == nil {
		t.Fatal("expected error decrypting tampered ciphertext, got nil")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	key := testKey(t)

	_, err := Decrypt(key, []byte("short"))
	if err == nil {
		t.Fatal("expected error for too-short ciphertext, got nil")
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	key := testKey(t)

	ct, err := Encrypt(key, []byte{})
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(got))
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	secret := []byte("my-server-secret")

	k1 := DeriveKey(secret, "encryption")
	k2 := DeriveKey(secret, "encryption")

	if k1 != k2 {
		t.Fatal("DeriveKey is not deterministic for same inputs")
	}
}

func TestDeriveKey_DifferentPurpose(t *testing.T) {
	secret := []byte("my-server-secret")

	k1 := DeriveKey(secret, "encryption")
	k2 := DeriveKey(secret, "signing")

	if k1 == k2 {
		t.Fatal("DeriveKey produced same key for different purposes")
	}
}

func TestEncrypt_UniqueNonces(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("same plaintext")

	ct1, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}

	ct2, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	if bytes.Equal(ct1, ct2) {
		t.Fatal("two encryptions of same plaintext produced identical ciphertext (nonces should differ)")
	}
}
