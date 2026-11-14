package drive

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

type tokenCipher struct {
	aead  cipher.AEAD
	keyID string
}

func newTokenCipher(key []byte) (*tokenCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid token encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid token encryption key")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errors.New("initialize token encryption")
	}
	digest := sha256.Sum256(key)
	return &tokenCipher{aead: aead, keyID: "v1:" + hex.EncodeToString(digest[:8]) + ":"}, nil
}

func (c *tokenCipher) encrypt(token string) string {
	//nolint:gosec // NewGCMWithRandomNonce generates and prepends a random nonce; its nonce argument must be nil.
	return c.keyID + base64.RawURLEncoding.EncodeToString(c.aead.Seal(nil, nil, []byte(token), []byte(c.keyID)))
}

func (c *tokenCipher) decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, c.keyID) {
		return "", errors.New("token key is unavailable")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, c.keyID))
	if err != nil || len(raw) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", errors.New("invalid encrypted token")
	}
	plaintext, err := c.aead.Open(nil, nil, raw, []byte(c.keyID))
	if err != nil {
		return "", errors.New("invalid encrypted token")
	}
	return string(plaintext), nil
}
