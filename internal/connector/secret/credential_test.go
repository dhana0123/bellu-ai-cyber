package secret

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func key32(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestNewRejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, n)); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("New(%d-byte key) err = %v, want ErrInvalidKey", n, err)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s, err := New(key32(t))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("glpat-super-secret-token")
	ct, err := s.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("ciphertext contains the plaintext in the clear")
	}
	got, err := s.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

func TestEncryptIsNondeterministic(t *testing.T) {
	s, _ := New(key32(t))
	a, _ := s.Encrypt([]byte("same"))
	b, _ := s.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext are identical (nonce reuse?)")
	}
}

func TestDecryptDetectsTampering(t *testing.T) {
	s, _ := New(key32(t))
	ct, _ := s.Encrypt([]byte("secret"))
	ct[len(ct)-1] ^= 0xFF // flip a tag byte
	if _, err := s.Decrypt(ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered ciphertext err = %v, want ErrDecrypt", err)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	a, _ := New(key32(t))
	b, _ := New(key32(t))
	ct, _ := a.Encrypt([]byte("secret"))
	if _, err := b.Decrypt(ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong-key decrypt err = %v, want ErrDecrypt", err)
	}
}

func TestDecryptRejectsTooShort(t *testing.T) {
	s, _ := New(key32(t))
	if _, err := s.Decrypt([]byte{0x01}); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short ciphertext err = %v, want ErrDecrypt", err)
	}
}
