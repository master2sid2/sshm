package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen = 32
	keyLen  = 32

	// Argon2id parameters — tuned for ~100ms on a modern laptop.
	argonTime    = 2
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
)

// deriveKey runs Argon2id to produce a 32-byte AES key.
func deriveKey(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, argonTime, argonMemory, argonThreads, keyLen)
}

// generateSalt returns cryptographically random salt.
func generateSalt() ([]byte, error) {
	s := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, s); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return s, nil
}

// encrypt uses AES-256-GCM with a random nonce.
// Output: [4-byte nonce-len][nonce][ciphertext+tag]
func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Format: [uint32 nonce length][nonce][ciphertext]
	out := make([]byte, 4+len(nonce)+len(ciphertext))
	binary.BigEndian.PutUint32(out[:4], uint32(len(nonce)))
	copy(out[4:], nonce)
	copy(out[4+len(nonce):], ciphertext)
	return out, nil
}

// decrypt reverses encrypt.
func decrypt(key, data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// The length comes from the file: check it, gcm.Open panics on a nonce
	// of the wrong size.
	nonceSize := int(binary.BigEndian.Uint32(data[:4]))
	if nonceSize != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce length %d", nonceSize)
	}
	if len(data) < 4+nonceSize+gcm.Overhead() {
		return nil, fmt.Errorf("ciphertext truncated")
	}
	return gcm.Open(nil, data[4:4+nonceSize], data[4+nonceSize:], nil)
}

// buildFile serialises the on-disk format:
// [4-byte magic][4-byte version][4-byte salt-len][salt][ciphertext...]
func buildFile(salt, ciphertext []byte) []byte {
	// magic: "SSHM"
	out := []byte{'S', 'S', 'H', 'M'}
	versionBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(versionBuf, 1)
	out = append(out, versionBuf...)

	saltLenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(saltLenBuf, uint32(len(salt)))
	out = append(out, saltLenBuf...)
	out = append(out, salt...)
	out = append(out, ciphertext...)
	return out
}

// parseFile extracts salt and ciphertext from the on-disk format.
func parseFile(data []byte) (salt, ciphertext []byte, err error) {
	if len(data) < 12 {
		return nil, nil, fmt.Errorf("file too short")
	}
	magic := data[:4]
	if string(magic) != "SSHM" {
		return nil, nil, fmt.Errorf("invalid magic bytes")
	}
	// data[4:8] = version (reserved for future migration)
	saltSize := int(binary.BigEndian.Uint32(data[8:12]))
	if len(data) < 12+saltSize {
		return nil, nil, fmt.Errorf("file truncated at salt")
	}
	salt = data[12 : 12+saltSize]
	ciphertext = data[12+saltSize:]
	return salt, ciphertext, nil
}
