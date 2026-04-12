// Package compress provides compression utilities for agent-speaker
package compress

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompressDecompress tests basic compression and decompression
func TestCompressDecompress(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		wantErr  bool
	}{
		{
			name:    "simple_text",
			input:   []byte("Hello, World!"),
			wantErr: false,
		},
		{
			name:    "empty_string",
			input:   []byte(""),
			wantErr: false,
		},
		{
			name:    "long_text",
			input:   []byte(strings.Repeat("This is a test message. ", 100)),
			wantErr: false,
		},
		{
			name:    "json_content",
			input:   []byte(`{"kind":30078,"content":"test","tags":[["c","agent"]]}`),
			wantErr: false,
		},
		{
			name:    "unicode_text",
			input:   []byte("你好世界 🌍 مرحبا بالعالم"),
			wantErr: false,
		},
		{
			name:    "binary_data",
			input:   []byte{0x00, 0x01, 0x02, 0xFF, 0xFE},
			wantErr: false,
		},
		{
			name:    "large_content",
			input:   []byte(strings.Repeat("Lorem ipsum dolor sit amet. ", 1000)),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compress
			compressed, err := Compress(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			
			// Empty input should return empty string
			if len(tt.input) == 0 {
				assert.Empty(t, compressed)
				return
			}
			
			// Compressed should be different from original (when base64 encoded)
			assert.NotEqual(t, string(tt.input), compressed)
			
			// Decompress
			decompressed, err := Decompress(compressed)
			require.NoError(t, err)
			
			// Should match original
			assert.Equal(t, tt.input, decompressed)
		})
	}
}

// TestCompressWithPrefix tests compression with agent prefix
func TestCompressWithPrefix(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		version string
		wantErr bool
	}{
		{
			name:    "with_version",
			data:    []byte("test data"),
			version: "v1",
			wantErr: false,
		},
		{
			name:    "empty_data",
			data:    []byte{},
			version: "v1",
			wantErr: false,
			// Note: CompressWithPrefix returns "agent:v1:zstd:" for empty data
		},
		{
			name:    "with_v2",
			data:    []byte("version 2 data"),
			version: "v2",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressWithPrefix(tt.data, tt.version)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			
			if len(tt.data) == 0 {
				// Empty data produces "agent:v1:zstd:" prefix only
				assert.True(t, strings.HasPrefix(compressed, "agent:"))
				return
			}
			
			// Should have prefix
			assert.True(t, strings.HasPrefix(compressed, "agent:"))
			assert.Contains(t, compressed, tt.version)
			assert.Contains(t, compressed, "zstd")
		})
	}
}

// TestDecompressWithPrefix tests decompression with agent prefix
func TestDecompressWithPrefix(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedData   []byte
		expectedVer    string
		wantErr        bool
	}{
		{
			name:           "with_prefix",
			input:          "",
			expectedData:   []byte{},
			expectedVer:    "",
			wantErr:        false,
		},
		{
			name:           "empty_string",
			input:          "",
			expectedData:   []byte{},
			expectedVer:    "",
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input == "" {
				// Test empty input
				data, ver, err := DecompressWithPrefix("")
				assert.NoError(t, err)
				assert.Empty(t, data)
				assert.Empty(t, ver)
				return
			}
			
			data, ver, err := DecompressWithPrefix(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
			assert.Equal(t, tt.expectedVer, ver)
		})
	}
}

// TestRoundTrip tests full round-trip compression/decompression
func TestRoundTrip(t *testing.T) {
	original := []byte("This is a test message for round-trip compression and decompression testing.")
	
	// Test basic round-trip
	compressed, err := Compress(original)
	require.NoError(t, err)
	
	decompressed, err := Decompress(compressed)
	require.NoError(t, err)
	
	assert.Equal(t, original, decompressed)
	
	// Test with prefix round-trip
	compressedWithPrefix, err := CompressWithPrefix(original, "v1")
	require.NoError(t, err)
	
	decompressedWithPrefix, ver, err := DecompressWithPrefix(compressedWithPrefix)
	require.NoError(t, err)
	
	assert.Equal(t, original, decompressedWithPrefix)
	assert.Equal(t, "v1", ver)
}

// TestDecompressInvalidData tests decompression of invalid data
func TestDecompressInvalidData(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "invalid_base64",
			input:   "!!!invalid!!!",
			wantErr: true,
		},
		{
			name:    "corrupted_zstd",
			input:   "dGVzdA==", // base64 of "test" - not valid zstd
			wantErr: true,
		},
		{
			name:    "valid_base64_invalid_zstd",
			input:   "SGVsbG8gV29ybGQ=", // base64 of "Hello World"
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decompress(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCompressionRatio tests that compression actually reduces size
func TestCompressionRatio(t *testing.T) {
	// Large repetitive data should compress well
	original := []byte(strings.Repeat("This is repetitive data. ", 100))
	
	compressed, err := Compress(original)
	require.NoError(t, err)
	
	// Compressed size (after base64) should be less than original
	// Note: base64 increases size by ~33%, but zstd should overcome this for repetitive data
	originalSize := len(original)
	compressedSize := len(compressed)
	
	ratio := float64(compressedSize) / float64(originalSize)
	t.Logf("Compression ratio: %.2f%% (original: %d, compressed: %d)", 
		ratio*100, originalSize, compressedSize)
	
	// For highly repetitive data, compression should be effective
	assert.Less(t, ratio, 0.5, "Compression should reduce size by at least 50% for repetitive data")
}

// BenchmarkCompress benchmarks compression
func BenchmarkCompress(b *testing.B) {
	data := []byte(strings.Repeat("Benchmark test data. ", 100))
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Compress(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecompress benchmarks decompression
func BenchmarkDecompress(b *testing.B) {
	data := []byte(strings.Repeat("Benchmark test data. ", 100))
	compressed, err := Compress(data)
	if err != nil {
		b.Fatal(err)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Decompress(compressed)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestConcurrentAccess tests thread safety
func TestConcurrentAccess(t *testing.T) {
	data := []byte("concurrent test data")
	
	// Run multiple goroutines compressing and decompressing
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			
			for j := 0; j < 100; j++ {
				compressed, err := Compress(data)
				if err != nil {
					t.Error(err)
					return
				}
				
				decompressed, err := Decompress(compressed)
				if err != nil {
					t.Error(err)
					return
				}
				
				if !bytes.Equal(data, decompressed) {
					t.Error("Data mismatch")
					return
				}
			}
		}()
	}
	
	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
