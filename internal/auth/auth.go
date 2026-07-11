// Package auth handles the local single-user login and the encryption of
// AWS secrets at rest. There is no server, no OAuth, nothing remote: a
// password hash and a random salt live in SQLite, and the AES key used to
// encrypt/decrypt stored AWS secret keys is derived from the password with
// scrypt and kept only in server memory for the life of a session — never
// written to disk. Restarting the process (or logging out) forgets the key,
// so the encrypted secrets in billkaat.db are unreadable until someone logs
// back in with the password.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

const (
	saltLen = 16
	keyLen  = 32 // AES-256
)

// NewSalt returns fresh random bytes for scrypt's salt parameter.
func NewSalt() ([]byte, error) {
	b := make([]byte, saltLen)
	_, err := rand.Read(b)
	return b, err
}

// HashPassword returns a bcrypt hash suitable for storing and later
// verifying a login password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword reports whether password matches a hash from HashPassword.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// DeriveKey turns a password + salt into a 256-bit AES key. This key is
// never stored; it is recomputed at login time and kept in memory only.
func DeriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, keyLen)
}

// NewSessionToken returns a random, URL-safe session token for the login
// cookie.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Encrypt seals plaintext with AES-256-GCM under key, prefixing the nonce.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// ErrDecrypt is returned when a ciphertext can't be opened — almost always
// because the wrong key (wrong password) was used.
var ErrDecrypt = errors.New("could not decrypt — wrong password?")

// Decrypt opens a ciphertext produced by Encrypt.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("%w: ciphertext too short", ErrDecrypt)
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
