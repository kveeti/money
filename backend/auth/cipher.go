package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// TokenCipher encrypts provider tokens before they are stored in a session.
type TokenCipher struct {
	aead cipher.AEAD
}

func NewTokenCipher(key [32]byte) *TokenCipher {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &TokenCipher{aead: aead}
}

func (c *TokenCipher) Encrypt(plaintext, associatedData string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate token nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(associatedData))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *TokenCipher) Decrypt(value, associatedData string) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	if len(sealed) < c.aead.NonceSize() {
		return "", fmt.Errorf("encrypted token is too short")
	}
	nonce, ciphertext := sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(associatedData))
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plaintext), nil
}
