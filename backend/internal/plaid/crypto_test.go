package plaid

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := "access-sandbox-abc123-def456"
	aad := tokenAAD("book-1", "item-1")

	ciphertext, nonce, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatal("expected non-empty ciphertext and nonce")
	}

	got, err := decrypt(key, ciphertext, nonce, aad)
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
	aad := tokenAAD("book-1", "item-1")

	ciphertext, nonce, err := encrypt(key1, "secret", aad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decrypt(key2, ciphertext, nonce, aad); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

// TestDecryptWrongAAD proves the ciphertext-swapping defense: a token row
// copied to another book/item fails GCM authentication.
func TestDecryptWrongAAD(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	ciphertext, nonce, err := encrypt(key, "secret", tokenAAD("book-A", "item-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decrypt(key, ciphertext, nonce, tokenAAD("book-B", "item-1")); err == nil {
		t.Fatal("ciphertext swapped across books must fail authentication")
	}
	if _, err := decrypt(key, ciphertext, nonce, tokenAAD("book-A", "item-2")); err == nil {
		t.Fatal("ciphertext swapped across items must fail authentication")
	}
}

// TestEncryptNonceUniqueness: every seal must draw a fresh random nonce —
// nonce reuse under the same GCM key is catastrophic.
func TestEncryptNonceUniqueness(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	aad := tokenAAD("book-1", "item-1")

	seen := make(map[string]bool)
	for i := 0; i < 64; i++ {
		ct, nonce, err := encrypt(key, "same plaintext", aad)
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(nonce)] {
			t.Fatal("nonce reused across encryptions")
		}
		seen[string(nonce)] = true
		if i == 0 {
			continue
		}
		if bytes.Equal(ct, []byte("same plaintext")) {
			t.Fatal("ciphertext equals plaintext")
		}
	}
}
