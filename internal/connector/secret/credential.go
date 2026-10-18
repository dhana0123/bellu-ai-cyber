// Package secret provides a small AES-256-GCM credential store used to encrypt
// connector credentials (GitLab/Bitbucket tokens) at rest in the customer
// Postgres. GitHub does not use it: a GitHub App stores no token (the App JWT
// is minted on demand and exchanged for a short-lived installation token).
//
// The key is 32 bytes (AES-256), supplied via BELLU_CREDENTIAL_KEY (base64) and
// never logged. Each Encrypt uses a fresh random 12-byte nonce prepended to the
// ciphertext, so the same plaintext encrypts differently each time and Decrypt
// recovers the nonce without separate storage. GCM authenticates the ciphertext,
// so any tampering (or a wrong key) is detected on Decrypt.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// CredentialStore encrypts and decrypts credential bytes with AES-256-GCM.
type CredentialStore struct {
	gcm cipher.AEAD
}

// ErrInvalidKey is returned when the supplied key is not 32 bytes.
var ErrInvalidKey = errors.New("secret: key must be 32 bytes for AES-256")

// ErrDecrypt is returned when ciphertext cannot be authenticated/decrypted
// (tampered data, truncated input, or a wrong key). The underlying GCM error is
// not surfaced so no detail about the key leaks.
var ErrDecrypt = errors.New("secret: decrypt failed")

// New builds a CredentialStore from a 32-byte AES-256 key.
func New(key []byte) (*CredentialStore, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &CredentialStore{gcm: gcm}, nil
}

// Encrypt seals plaintext and returns nonce||ciphertext. The nonce is random
// per call so repeated encryptions of the same plaintext differ.
func (s *CredentialStore) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secret: generate nonce: %w", err)
	}
	// Seal appends the ciphertext (and tag) to the nonce prefix, so the result
	// is nonce||ciphertext in one slice.
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens nonce||ciphertext produced by Encrypt. It returns ErrDecrypt if
// the input is too short, has been tampered with, or was sealed with a
// different key.
func (s *CredentialStore) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := s.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, ErrDecrypt
	}
	nonce, sealed := ciphertext[:ns], ciphertext[ns:]
	out, err := s.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
