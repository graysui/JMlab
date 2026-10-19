package securevalue

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// Encrypt protects a secret at rest with a key derived from the application
// master key. The version prefix allows the envelope format to evolve.
func Encrypt(masterKey string, plaintext []byte) (string, error) {
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
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
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return "v1:" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a secret written by Encrypt.
func Decrypt(masterKey, value string) ([]byte, error) {
	version, encoded, ok := strings.Cut(value, ":")
	if !ok || version != "v1" {
		return nil, errors.New("密文版本无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("密文无效")
	}
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, errors.New("密文无效")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return nil, errors.New("密文无效")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("密文无效")
	}
	return plain, nil
}
