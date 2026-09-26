// Package secrets holds the server's secret key material: one app secret
// (from the environment, or generated once and kept in the data folder),
// from which purpose-specific keys are derived - a JWT signing key and an
// AES-256-GCM key for values stored encrypted (TOTP secrets, SMTP
// passwords).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Keys are derived from the app secret.
type Keys struct {
	secret []byte
	aead   cipher.AEAD
}

// Load uses envSecret if set (at least 32 characters), else the secret in
// <dataDir>/secret.key, creating it (0600) on first use.
func Load(envSecret, dataDir string) (*Keys, error) {
	var secret []byte
	switch {
	case envSecret != "":
		if len(envSecret) < 32 {
			return nil, errors.New("APP_SECRET must be at least 32 characters")
		}
		secret = []byte(envSecret)
	default:
		p := filepath.Join(dataDir, "secret.key")
		b, err := os.ReadFile(p)
		switch {
		case err == nil:
			secret = []byte(strings.TrimSpace(string(b)))
		case errors.Is(err, os.ErrNotExist):
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return nil, err
			}
			secret = []byte(base64.RawURLEncoding.EncodeToString(raw))
			if err := os.WriteFile(p, append(secret, '\n'), 0o600); err != nil {
				return nil, fmt.Errorf("storing the generated app secret: %w", err)
			}
		default:
			return nil, err
		}
	}
	return New(secret)
}

// New derives the keys from a secret.
func New(secret []byte) (*Keys, error) {
	k := &Keys{secret: secret}
	block, err := aes.NewCipher(k.Derive("stlib encryption v1", 32))
	if err != nil {
		return nil, err
	}
	if k.aead, err = cipher.NewGCM(block); err != nil {
		return nil, err
	}
	return k, nil
}

// Derive returns a key of n bytes for one purpose (HKDF-SHA256).
func (k *Keys) Derive(purpose string, n int) []byte {
	b, err := hkdf.Key(sha256.New, k.secret, nil, purpose, n)
	if err != nil {
		panic(err) // only for n larger than HKDF allows
	}
	return b
}

// Seal encrypts a value for storage (AES-256-GCM, random nonce,
// base64). An empty value stays empty.
func (k *Keys) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(k.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts a value from Seal.
func (k *Keys) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	if err != nil || len(b) < k.aead.NonceSize() || !strings.HasPrefix(sealed, "v1:") {
		return "", errors.New("not a sealed value")
	}
	plain, err := k.aead.Open(nil, b[:k.aead.NonceSize()], b[k.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("sealed value can't be opened with this app secret")
	}
	return string(plain), nil
}
