// Package compress provides compression utilities for Agent Nostr CLI
package compress

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Compress compresses data using zstd and returns base64 encoded string
func Compress(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	encoder, err := zstd.NewWriter(&buf)
	if err != nil {
		return "", fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	defer encoder.Close()

	_, err = encoder.Write(data)
	if err != nil {
		return "", fmt.Errorf("failed to compress data: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("failed to close encoder: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Decompress decompresses base64 encoded zstd data
func Decompress(data string) ([]byte, error) {
	if data == "" {
		return []byte{}, nil
	}

	compressed, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}
	defer decoder.Close()

	result, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress data: %w", err)
	}

	return result, nil
}

// CompressWithPrefix compresses data and adds agent marker prefix
func CompressWithPrefix(data []byte, version string) (string, error) {
	compressed, err := Compress(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("agent:%s:zstd:%s", version, compressed), nil
}

// DecompressWithPrefix decompresses data with agent marker prefix
func DecompressWithPrefix(data string) ([]byte, string, error) {
	// Check for prefix
	const prefix = "agent:"
	if !strings.HasPrefix(data, prefix) {
		// No prefix, try direct decompress
		result, err := Decompress(data)
		return result, "", err
	}

	// Parse: agent:VERSION:zstd:DATA
	parts := strings.SplitN(data, ":", 4)
	if len(parts) != 4 || parts[2] != "zstd" {
		// Invalid format, try direct decompress
		result, err := Decompress(data)
		return result, "", err
	}

	version := parts[1]
	compressed := parts[3]
	
	result, err := Decompress(compressed)
	return result, version, err
}
