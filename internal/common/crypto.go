// Package common 提供共享的加密和编码工具
package common

import (
	"encoding/hex"
	"fmt"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// ParseSecretKey 解析 nsec 或 hex 私钥
func ParseSecretKey(key string) (nostr.SecretKey, error) {
	key = strings.TrimSpace(key)

	if strings.HasPrefix(key, "nsec1") {
		hrp, data, err := bech32.Decode(key)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("invalid nsec: %w", err)
		}
		if hrp != "nsec" {
			return nostr.SecretKey{}, fmt.Errorf("invalid nsec prefix: %s", hrp)
		}
		converted, err := bech32.ConvertBits(data, 5, 8, false)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("failed to convert bits: %w", err)
		}
		if len(converted) != 32 {
			return nostr.SecretKey{}, fmt.Errorf("invalid key length: %d", len(converted))
		}
		var sk nostr.SecretKey
		copy(sk[:], converted)
		return sk, nil
	}

	sk, err := nostr.SecretKeyFromHex(key)
	if err != nil {
		return nostr.SecretKey{}, fmt.Errorf("invalid hex key: %w", err)
	}
	return sk, nil
}

// ParsePublicKey 解析 npub 或 hex 公钥
func ParsePublicKey(key string) (nostr.PubKey, error) {
	key = strings.TrimSpace(key)

	if strings.HasPrefix(key, "npub1") {
		hrp, data, err := bech32.Decode(key)
		if err != nil {
			return nostr.PubKey{}, fmt.Errorf("invalid npub: %w", err)
		}
		if hrp != "npub" {
			return nostr.PubKey{}, fmt.Errorf("invalid npub prefix: %s", hrp)
		}
		converted, err := bech32.ConvertBits(data, 5, 8, false)
		if err != nil {
			return nostr.PubKey{}, fmt.Errorf("failed to convert bits: %w", err)
		}
		if len(converted) != 32 {
			return nostr.PubKey{}, fmt.Errorf("invalid key length: %d", len(converted))
		}
		var pk nostr.PubKey
		copy(pk[:], converted)
		return pk, nil
	}

	pk, err := nostr.PubKeyFromHex(key)
	if err != nil {
		return nostr.PubKey{}, fmt.Errorf("invalid hex key: %w", err)
	}
	return pk, nil
}

// PubKeyToHex 将公钥转为 hex
func PubKeyToHex(pk nostr.PubKey) string {
	return hex.EncodeToString(pk[:])
}

// EncodeNpub 编码公钥为 npub
func EncodeNpub(pk nostr.PubKey) string {
	data, err := bech32.ConvertBits(pk[:], 8, 5, true)
	if err != nil {
		return ""
	}
	encoded, err := bech32.Encode("npub", data)
	if err != nil {
		return ""
	}
	return encoded
}

// EncodeNsec 编码私钥为 nsec
func EncodeNsec(sk nostr.SecretKey) string {
	data, err := bech32.ConvertBits(sk[:], 8, 5, true)
	if err != nil {
		return ""
	}
	encoded, err := bech32.Encode("nsec", data)
	if err != nil {
		return ""
	}
	return encoded
}
