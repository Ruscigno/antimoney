package plaid

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

// encrypt seals plaintext bound to aad (additional authenticated data). The
// ciphertext only decrypts with the same aad, so a row swapped to another
// book/item by an attacker with DB access fails authentication instead of
// decrypting successfully.
func encrypt(key []byte, plaintext string, aad []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), aad)
	return ciphertext, nonce, nil
}

func decrypt(key []byte, ciphertext, nonce, aad []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", errors.New("decrypt failed")
	}
	return string(plain), nil
}
