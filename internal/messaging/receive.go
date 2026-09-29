package messaging

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/klauspost/compress/zstd"
)

const (
	maxIncomingContentBytes = 2 << 20
	maxIncomingBodyBytes    = 1 << 20
	maxIncomingWindowBytes  = 8 << 20
)

var zstdFrameMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// DecodeMessageContent validates the enc/z tags, boundedly decompresses
// content, and decrypts NIP-44 when marked. The caller must verify the event
// signature, kind, and filters first. Encoded content is limited to 2 MiB;
// decompressed/ciphertext/plaintext bodies are limited to 1 MiB and zstd
// decoder windows to 8 MiB.
func DecodeMessageContent(event *nostr.Event, recipientSK nostr.SecretKey) (plaintext string, isEncrypted bool, err error) {
	body, isEncrypted, err := decodeIncomingMessageBody(event)
	if err != nil {
		return "", isEncrypted, err
	}
	if !isEncrypted {
		return string(body), false, nil
	}
	plaintext, err = crypto.DecryptMessage(string(body), recipientSK, event.PubKey)
	if err != nil {
		return "", true, fmt.Errorf("decrypt NIP-44 content: %w", err)
	}
	if len(plaintext) > maxIncomingBodyBytes {
		return "", true, fmt.Errorf("decrypted message exceeds 1 MiB limit")
	}
	return plaintext, true, nil
}

// decodeInboxContent validates tags and decompresses content, while allowing
// encrypted messages to be displayed without requiring a key or validating
// the ciphertext when the user disabled decryption.
func decodeInboxContent(event *nostr.Event, recipientSK nostr.SecretKey, decrypt bool) (content string, encrypted, decrypted bool, err error) {
	body, encrypted, err := decodeIncomingMessageBody(event)
	if err != nil {
		return "", encrypted, false, err
	}
	if !encrypted {
		return string(body), false, false, nil
	}
	if !decrypt {
		return "[encrypted message]", true, false, nil
	}
	content, err = crypto.DecryptMessage(string(body), recipientSK, event.PubKey)
	if err != nil {
		return "", true, false, fmt.Errorf("decrypt NIP-44 content: %w", err)
	}
	if len(content) > maxIncomingBodyBytes {
		return "", true, false, fmt.Errorf("decrypted message exceeds 1 MiB limit")
	}
	return content, true, true, nil
}

func decodeIncomingMessageBody(event *nostr.Event) ([]byte, bool, error) {
	if event == nil {
		return nil, false, fmt.Errorf("event is required")
	}
	if len(event.Content) > maxIncomingContentBytes {
		return nil, false, fmt.Errorf("encoded content exceeds 2 MiB limit")
	}
	encryption, hasEncryption, err := outboxTagValue(event.Tags, "enc")
	if err != nil {
		return nil, false, err
	}
	compression, hasCompression, err := outboxTagValue(event.Tags, "z")
	if err != nil {
		return nil, false, err
	}
	if hasEncryption && encryption != "nip44" {
		return nil, false, fmt.Errorf("unsupported encryption tag")
	}
	if hasCompression && compression != CompressTag {
		return nil, false, fmt.Errorf("unsupported compression tag")
	}

	body, err := decodeIncomingBody(event.Content, hasCompression)
	if err != nil {
		return nil, hasEncryption, err
	}
	if len(body) > maxIncomingBodyBytes {
		return nil, hasEncryption, fmt.Errorf("message body exceeds 1 MiB limit")
	}
	return body, hasEncryption, nil
}

func decodeIncomingBody(content string, taggedCompressed bool) ([]byte, error) {
	compressed, decodeErr := base64.StdEncoding.DecodeString(content)
	isZstdFrame := decodeErr == nil && isZstdFrame(compressed)
	if taggedCompressed && decodeErr != nil {
		return nil, fmt.Errorf("decode compressed content: invalid base64")
	}
	if taggedCompressed {
		return boundedZstdDecode(compressed)
	}
	if isZstdFrame {
		return boundedZstdDecode(compressed)
	}
	if len(content) > maxIncomingBodyBytes {
		return nil, fmt.Errorf("message body exceeds 1 MiB limit")
	}
	return []byte(content), nil
}

func isZstdFrame(data []byte) bool {
	if bytes.HasPrefix(data, zstdFrameMagic) {
		return true
	}
	return len(data) >= 4 && data[0] >= 0x50 && data[0] <= 0x5f && data[1] == 0x2a && data[2] == 0x4d && data[3] == 0x18
}

func boundedZstdDecode(frame []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxIncomingWindowBytes),
		zstd.WithDecoderMaxWindow(maxIncomingWindowBytes),
		zstd.WithDecodeAllCapLimit(true),
	)
	if err != nil {
		return nil, fmt.Errorf("create bounded zstd decoder: %w", err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(frame, make([]byte, 0, maxIncomingBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decompress zstd content: %w", err)
	}
	if len(decoded) > maxIncomingBodyBytes {
		return nil, fmt.Errorf("decompressed content exceeds 1 MiB limit")
	}
	return decoded, nil
}
