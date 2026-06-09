package plaid

import (
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := "access-sandbox-abc123-def456"

	ciphertext, nonce, err := encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatal("expected non-empty ciphertext and nonce")
	}

	got, err := decrypt(key, ciphertext, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, nonce, err := encrypt(key1, "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = decrypt(key2, ciphertext, nonce)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}
