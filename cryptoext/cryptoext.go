package cryptoext

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"strings"

	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// GenerateBackupCodes generates a set of backup codes for 2FA.
func GenerateBackupCodes(num int) ([]string, error) {
	codes := make([]string, num)

	for i := range 10 {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}

		codes[i] = fmt.Sprintf("%s-%s", hex.EncodeToString(b[:2]), hex.EncodeToString(b[2:]))
	}

	return codes, nil
}

// HashBackupCodes hashes all backup codes and joins them.
func HashBackupCodes(codes []string) string {
	hashes := make([]string, len(codes))

	for i, code := range codes {
		hash := sha256.Sum256([]byte(code))
		hashes[i] = hex.EncodeToString(hash[:])
	}

	return strings.Join(hashes, ",")
}

// GenerateEncryptionKey generates a new encryption key.
//
//nolint:gocritic // unnamed results are intentional for clarity
func GenerateEncryptionKey() ([]byte, string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}

	return key, hex.EncodeToString(key), nil
}

// GetOrCreateEncryptionKey gets an encryption key from the provided hex string or generates a new one.
func GetOrCreateEncryptionKey(hexKey string) ([]byte, bool, error) { //nolint:gocritic // unnamedResult: return types are self-explanatory (key, isNew, error)
	if hexKey != "" {
		key, err := ParseEncryptionKey(hexKey)
		if err != nil {
			return nil, false, err
		}

		return key, false, nil
	}

	key, _, err := GenerateEncryptionKey()
	if err != nil {
		return nil, false, err
	}

	return key, true, nil
}

func MustParseEncryptionKey(hexKey string) []byte {
	key, err := ParseEncryptionKey(hexKey)
	if err != nil {
		panic(err)
	}

	return key
}

// ParseEncryptionKey parses a hex-encoded encryption key.
func ParseEncryptionKey(hexKey string) ([]byte, error) {
	return hex.DecodeString(hexKey)
}

// Encrypt encrypts a plaintext string using AES-GCM.
func Encrypt(plaintext string, key []byte) (string, error) {
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

// Decrypt decrypts a ciphertext string using AES-GCM.
func Decrypt(ciphertext string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
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

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, cipherData := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GenerateRandomHexString generates a random hex string of the specified byte length.
func GenerateRandomHexString(numBytes int) (string, error) {
	b := make([]byte, numBytes)

	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// Sha256Hash returns the SHA-256 hash of the input string in hexadecimal format.
func Sha256Hash(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// ConstantTimeCompare performs a constant-time comparison of two strings.
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func GenerateRandomHex(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

func GenerateSSHKey() (pubKey, privKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key: %w", err)
	}

	sshPubKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("failed to create SSH public key: %w", err)
	}

	pubKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPubKey)))

	privKeyBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	privKey = string(pem.EncodeToMemory(privKeyBytes))

	return pubKey, privKey, nil
}

// GenerateQRCode generates a QR code PNG image for the given content.
func GenerateQRCode(content string, size int) ([]byte, error) {
	return qrcode.Encode(content, qrcode.Medium, size)
}

// VerifyPassword compares a plaintext password with a bcrypt hash.
func VerifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hash), nil
}
