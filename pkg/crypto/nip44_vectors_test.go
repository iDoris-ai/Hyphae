package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
	"github.com/stretchr/testify/require"
)

const (
	nip44VectorSourceCommit = "671a1f04bcfacaf125b0db68adc45bc9ce0e763b"
	nip44VectorSourceSHA256 = "269ed0f69e4c192512cc779e78c555090cebc7c785b609e338a62afc3ce25040"
	nip44SpecSourceCommit   = "0046368a747c5c25ae2bec28bae0e537744c8f10"
	nip44SpecSourceSHA256   = "b5f89374e4e1dbdee7881e8573b4313b6a89430a9c8d518471599ae0660cf0e2"
)

type nip44ReferenceFixture struct {
	Vectors nip44VectorsSource `json:"paulmillr_nip44_vectors"`
	Spec    nip44SpecSource    `json:"nostr_protocol_nip44_spec"`
}

type sourceMetadata struct {
	Source             string `json:"source"`
	SourceVersion      string `json:"source_version"`
	OriginalFile       string `json:"original_file"`
	OriginalFileBytes  int    `json:"original_file_bytes"`
	OriginalFileSHA256 string `json:"original_file_sha256"`
}

type nip44VectorsSource struct {
	sourceMetadata
	Short   []shortEncryptVector   `json:"v2.valid.encrypt_decrypt"`
	Long    []longEncryptVector    `json:"v2.valid.encrypt_decrypt_long_msg"`
	Invalid []invalidDecryptVector `json:"v2.invalid.decrypt"`
}

type shortEncryptVector struct {
	Index           int    `json:"index"`
	SecretKey1      string `json:"sec1"`
	SecretKey2      string `json:"sec2"`
	ConversationKey string `json:"conversation_key"`
	Nonce           string `json:"nonce"`
	Plaintext       string `json:"plaintext"`
	Payload         string `json:"payload"`
}

type longEncryptVector struct {
	Index           int    `json:"index"`
	ConversationKey string `json:"conversation_key"`
	Nonce           string `json:"nonce"`
	Pattern         string `json:"pattern"`
	Repeat          int    `json:"repeat"`
	PlaintextSHA256 string `json:"plaintext_sha256"`
	PayloadSHA256   string `json:"payload_sha256"`
}

type invalidDecryptVector struct {
	Index           int    `json:"index"`
	ConversationKey string `json:"conversation_key"`
	Nonce           string `json:"nonce"`
	Note            string `json:"note"`
	Payload         string `json:"payload"`
	Plaintext       string `json:"plaintext"`
}

type nip44SpecSource struct {
	sourceMetadata
	ConversationKey string                 `json:"conversation_key"`
	Nonce           string                 `json:"nonce"`
	Pattern         string                 `json:"pattern"`
	Extended        []extendedLengthVector `json:"extended_length_prefix"`
}

type extendedLengthVector struct {
	Index           int    `json:"index"`
	Length          int    `json:"length"`
	Prefix          string `json:"prefix"`
	PaddedLength    int    `json:"padded_length"`
	PlaintextSHA256 string `json:"plaintext_sha256"`
	PayloadSHA256   string `json:"payload_sha256"`
}

func TestNIP44ReferenceVectorSources(t *testing.T) {
	fixture := loadNIP44ReferenceFixture(t)
	require.Equal(t, "https://github.com/paulmillr/nip44", fixture.Vectors.Source)
	require.Equal(t, nip44VectorSourceCommit, fixture.Vectors.SourceVersion)
	require.Equal(t, "nip44.vectors.json", fixture.Vectors.OriginalFile)
	require.Equal(t, 37630, fixture.Vectors.OriginalFileBytes)
	require.Equal(t, nip44VectorSourceSHA256, fixture.Vectors.OriginalFileSHA256)
	require.Len(t, fixture.Vectors.Short, 10)
	require.Len(t, fixture.Vectors.Long, 3)
	require.Len(t, fixture.Vectors.Invalid, 12)

	require.Equal(t, "https://github.com/nostr-protocol/nips", fixture.Spec.Source)
	require.Equal(t, nip44SpecSourceCommit, fixture.Spec.SourceVersion)
	require.Equal(t, "44.md", fixture.Spec.OriginalFile)
	require.Equal(t, 19610, fixture.Spec.OriginalFileBytes)
	require.Equal(t, nip44SpecSourceSHA256, fixture.Spec.OriginalFileSHA256)
	require.Len(t, fixture.Spec.Extended, 3)
}

func TestNIP44ReferenceShortEncryptDecryptVectors(t *testing.T) {
	fixture := loadNIP44ReferenceFixture(t)
	require.Len(t, fixture.Vectors.Short, 10, "short-vector loop must not pass vacuously")
	for i, vector := range fixture.Vectors.Short {
		vector := vector
		t.Run(vectorName(vector.Index), func(t *testing.T) {
			require.Equal(t, i, vector.Index)
			secret1, err := nostr.SecretKeyFromHex(vector.SecretKey1)
			require.NoError(t, err)
			secret2, err := nostr.SecretKeyFromHex(vector.SecretKey2)
			require.NoError(t, err)
			expectedKey := mustNIP44Key(t, vector.ConversationKey)
			nonce := mustNIP44Nonce(t, vector.Nonce)

			forwardKey, err := nip44.GenerateConversationKey(secret2.Public(), secret1)
			require.NoError(t, err)
			require.Equal(t, expectedKey, forwardKey, "secret1 to public key 2")
			reverseKey, err := nip44.GenerateConversationKey(secret1.Public(), secret2)
			require.NoError(t, err)
			require.Equal(t, expectedKey, reverseKey, "secret2 to public key 1")

			payload, err := nip44.Encrypt(vector.Plaintext, forwardKey, nip44.WithCustomNonce(nonce[:]))
			require.NoError(t, err)
			require.Equal(t, vector.Payload, payload)

			wrapperPlaintext, err := DecryptMessage(vector.Payload, secret2, secret1.Public())
			require.NoError(t, err)
			require.Equal(t, vector.Plaintext, wrapperPlaintext)
			sdkPlaintext, err := nip44.Decrypt(vector.Payload, reverseKey)
			require.NoError(t, err)
			require.Equal(t, vector.Plaintext, sdkPlaintext)
		})
	}
}

func TestNIP44ReferenceLongMessageVectors(t *testing.T) {
	fixture := loadNIP44ReferenceFixture(t)
	require.Len(t, fixture.Vectors.Long, 3, "long-vector loop must not pass vacuously")
	for i, vector := range fixture.Vectors.Long {
		vector := vector
		t.Run(vectorName(vector.Index), func(t *testing.T) {
			require.Equal(t, i, vector.Index)
			plaintext := strings.Repeat(vector.Pattern, vector.Repeat)
			require.Equal(t, vector.PlaintextSHA256, sha256Hex([]byte(plaintext)))
			key := mustNIP44Key(t, vector.ConversationKey)
			nonce := mustNIP44Nonce(t, vector.Nonce)

			payload, err := nip44.Encrypt(plaintext, key, nip44.WithCustomNonce(nonce[:]))
			require.NoError(t, err)
			require.Equal(t, vector.PayloadSHA256, sha256Hex([]byte(payload)))
			decrypted, err := nip44.Decrypt(payload, key)
			require.NoError(t, err)
			require.Equal(t, plaintext, decrypted)
		})
	}
}

func TestNIP44ReferenceInvalidDecryptVectors(t *testing.T) {
	fixture := loadNIP44ReferenceFixture(t)
	require.Len(t, fixture.Vectors.Invalid, 12, "invalid-vector loop must not pass vacuously")
	for i, vector := range fixture.Vectors.Invalid {
		vector := vector
		t.Run(vectorName(vector.Index), func(t *testing.T) {
			require.Equal(t, i, vector.Index)
			key := mustNIP44Key(t, vector.ConversationKey)
			_, err := nip44.Decrypt(vector.Payload, key)
			require.Error(t, err, "invalid vector was accepted; note=%q", vector.Note)
		})
	}
}

func TestNIP44ReferenceOfficialExtendedLengthPrefixVectors(t *testing.T) {
	fixture := loadNIP44ReferenceFixture(t)
	require.Len(t, fixture.Spec.Extended, 3, "extended-prefix loop must not pass vacuously")
	key := mustNIP44Key(t, fixture.Spec.ConversationKey)
	nonce := mustNIP44Nonce(t, fixture.Spec.Nonce)
	for i, vector := range fixture.Spec.Extended {
		vector := vector
		t.Run(vectorName(vector.Index), func(t *testing.T) {
			require.Equal(t, i, vector.Index)
			plaintext := strings.Repeat(fixture.Spec.Pattern, vector.Length)
			require.Len(t, []byte(plaintext), vector.Length)
			require.Equal(t, vector.PlaintextSHA256, sha256Hex([]byte(plaintext)))
			payload, err := nip44.Encrypt(plaintext, key, nip44.WithCustomNonce(nonce[:]))
			require.NoError(t, err)
			require.Equal(t, vector.PayloadSHA256, sha256Hex([]byte(payload)))
			decrypted, err := nip44.Decrypt(payload, key)
			require.NoError(t, err)
			require.Equal(t, plaintext, decrypted)
		})
	}
}

func loadNIP44ReferenceFixture(t *testing.T) nip44ReferenceFixture {
	t.Helper()
	path := filepath.Join("testdata", "nip44-reference.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture nip44ReferenceFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.NotEmpty(t, fixture.Vectors.Short, "reference vector fixture is missing")
	return fixture
}

func mustNIP44Key(t *testing.T, encoded string) [32]byte {
	t.Helper()
	decoded, err := hex.DecodeString(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
	var key [32]byte
	copy(key[:], decoded)
	return key
}

func mustNIP44Nonce(t *testing.T, encoded string) [32]byte {
	t.Helper()
	decoded, err := hex.DecodeString(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
	var nonce [32]byte
	copy(nonce[:], decoded)
	return nonce
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func vectorName(index int) string {
	return "index_" + strconv.Itoa(index)
}
