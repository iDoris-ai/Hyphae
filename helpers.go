package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"syscall"

	"fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"golang.org/x/term"
)

// readSecretKey prompts for a secret key securely
func readSecretKey(prompt string) (string, error) {
	if prompt == "" {
		prompt = "Secret key (nsec or hex): "
	}
	fmt.Fprint(os.Stderr, prompt)

	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}

	secKey := strings.TrimSpace(string(bytePassword))
	if secKey == "" {
		return "", fmt.Errorf("secret key is required")
	}

	return secKey, nil
}

// parseSecretKey parses nsec or hex secret key
func parseSecretKey(key string) (nostr.SecretKey, error) {
	key = strings.TrimSpace(key)

	if strings.HasPrefix(key, "nsec1") {
		// Decode bech32 nsec
		hrp, data, err := bech32.Decode(key)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("invalid nsec: %w", err)
		}
		if hrp != "nsec" {
			return nostr.SecretKey{}, fmt.Errorf("invalid nsec prefix: %s", hrp)
		}
		// Convert 5-bit to 8-bit
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

	// Assume hex
	sk, err := nostr.SecretKeyFromHex(key)
	if err != nil {
		return nostr.SecretKey{}, fmt.Errorf("invalid hex key: %w", err)
	}
	return sk, nil
}

// parsePublicKey parses npub or hex public key
func parsePublicKey(key string) (nostr.PubKey, error) {
	key = strings.TrimSpace(key)

	if strings.HasPrefix(key, "npub1") {
		// Decode bech32 npub
		hrp, data, err := bech32.Decode(key)
		if err != nil {
			return nostr.PubKey{}, fmt.Errorf("invalid npub: %w", err)
		}
		if hrp != "npub" {
			return nostr.PubKey{}, fmt.Errorf("invalid npub prefix: %s", hrp)
		}
		// Convert 5-bit to 8-bit
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

	// Assume hex
	pk, err := nostr.PubKeyFromHex(key)
	if err != nil {
		return nostr.PubKey{}, fmt.Errorf("invalid hex key: %w", err)
	}
	return pk, nil
}

// pubKeyToHex converts PubKey to hex string
func pubKeyToHex(pk nostr.PubKey) string {
	return hex.EncodeToString(pk[:])
}

// encodeNpub encodes PubKey to npub
func encodeNpub(pk nostr.PubKey) string {
	// Convert 8-bit to 5-bit
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

// encodeNsec encodes SecretKey to nsec
func encodeNsec(sk nostr.SecretKey) string {
	// Convert 8-bit to 5-bit
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

// normalizeKey is kept for compatibility
func normalizeKey(key string) (string, error) {
	sk, err := parseSecretKey(key)
	if err != nil {
		return "", err
	}
	return sk.Hex(), nil
}
