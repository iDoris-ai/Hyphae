// Package crypto 提供加密和解密功能
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	NIP44Version = 0x01
)

// ComputeSharedSecret 计算 ECDH 共享密钥
func ComputeSharedSecret(privateKey nostr.SecretKey, publicKey nostr.PubKey) ([32]byte, error) {
	privKey, _ := btcec.PrivKeyFromBytes(privateKey[:])

	compressedPub := make([]byte, 33)
	compressedPub[0] = 0x02
	copy(compressedPub[1:], publicKey[:])

	pubKey, err := btcec.ParsePubKey(compressedPub)
	if err != nil {
		compressedPub[0] = 0x03
		pubKey, err = btcec.ParsePubKey(compressedPub)
		if err != nil {
			return [32]byte{}, fmt.Errorf("failed to parse public key: %w", err)
		}
	}

	secret := btcec.GenerateSharedSecret(privKey, pubKey)
	if len(secret) != 32 {
		return [32]byte{}, fmt.Errorf("unexpected secret length: %d", len(secret))
	}

	var result [32]byte
	copy(result[:], secret)
	return result, nil
}

// DeriveConversationKey 从共享密钥派生会话密钥
func DeriveConversationKey(sharedSecret [32]byte) [32]byte {
	hash := sha256.Sum256(append([]byte("nip44-v1"), sharedSecret[:]...))
	return hash
}

// EncryptMessage 加密消息 (NIP-44 格式)
func EncryptMessage(plaintext string, senderSK nostr.SecretKey, recipientPK nostr.PubKey) (string, error) {
	sharedSecret, err := ComputeSharedSecret(senderSK, recipientPK)
	if err != nil {
		return "", fmt.Errorf("failed to compute shared secret: %w", err)
	}

	convKey := DeriveConversationKey(sharedSecret)

	aead, err := chacha20poly1305.NewX(convKey[:])
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, []byte(plaintext), nil)

	result := make([]byte, 1+len(nonce)+len(ciphertext))
	result[0] = NIP44Version
	copy(result[1:], nonce)
	copy(result[1+len(nonce):], ciphertext)

	return base64.StdEncoding.EncodeToString(result), nil
}

// DecryptMessage 解密消息
func DecryptMessage(ciphertext string, recipientSK nostr.SecretKey, senderPK nostr.PubKey) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}

	if len(data) < 1 || data[0] != NIP44Version {
		return "", fmt.Errorf("unsupported version")
	}

	sharedSecret, err := ComputeSharedSecret(recipientSK, senderPK)
	if err != nil {
		return "", fmt.Errorf("failed to compute shared secret: %w", err)
	}

	convKey := DeriveConversationKey(sharedSecret)

	aead, err := chacha20poly1305.NewX(convKey[:])
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	if len(data) < 1+aead.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce := data[1 : 1+aead.NonceSize()]
	encrypted := data[1+aead.NonceSize():]

	plaintext, err := aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}
