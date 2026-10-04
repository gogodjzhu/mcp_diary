package sync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const ciphertextPrefix = "s1:"

type Codec struct {
	gcm cipher.AEAD
}

func NewCodec(key string) (*Codec, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return &Codec{}, nil
	}
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Codec{gcm: gcm}, nil
}

func (c *Codec) Enabled() bool {
	return c != nil && c.gcm != nil
}

func (c *Codec) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if !c.Enabled() {
		return "", ErrNoEncryptionKey
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertextPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *Codec) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if !c.Enabled() {
		return "", ErrNoEncryptionKey
	}
	raw, ok := strings.CutPrefix(ciphertext, ciphertextPrefix)
	if !ok {
		return "", ErrInvalidCiphertext
	}
	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	nonceSize := c.gcm.NonceSize()
	if len(sealed) < nonceSize {
		return "", ErrInvalidCiphertext
	}
	plain, err := c.gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(plain), nil
}

func decodeKey(key string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if raw, err := hex.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	return nil, ErrInvalidKey
}
