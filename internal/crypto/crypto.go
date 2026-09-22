// Package crypto provides AES-256-GCM encryption for GitHub user credentials
// stored at rest. Every user-to-server access token and refresh token is
// encrypted before it is written to the SQL store and decrypted on demand.
// The key is injected through BURP_ENCRYPTION_KEY as 32 bytes hex encoded.
//
// When the Vault backend is used the store itself provides encryption at
// rest, and a passthrough Encryptor is used instead.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Encryptor encrypts and decrypts short strings.
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor builds an Encryptor from a hex-encoded 32-byte key.
func NewEncryptor(hexKey string) (*Encryptor, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decoding encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes (64 hex characters), got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &Encryptor{aead: aead}, nil
}

// NewPassthroughEncryptor returns an Encryptor that stores values verbatim.
// It is used with backends that already encrypt at rest (Vault).
func NewPassthroughEncryptor() *Encryptor {
	return &Encryptor{}
}

// Passthrough reports whether the encryptor stores values verbatim.
func (e *Encryptor) Passthrough() bool { return e.aead == nil }

// Encrypt returns base64(nonce || ciphertext) for plaintext.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e.aead == nil {
		return plaintext, nil
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	sealed := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	if e.aead == nil {
		return encoded, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}
	ns := e.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := e.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}
	return string(plain), nil
}

// GenerateKey returns a new random key, hex encoded, suitable for
// BURP_ENCRYPTION_KEY.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generating key: %w", err)
	}
	return hex.EncodeToString(key), nil
}
