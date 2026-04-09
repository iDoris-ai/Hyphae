package compress

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressDecompress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "simple text",
			input:   "Hello, World!",
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: false,
		},
		{
			name:    "long text",
			input:   "This is a very long text that should be compressed efficiently by zstd algorithm. " +
						"It contains repeated patterns and should achieve good compression ratio. " +
						"The more repetitive the content is, the better compression we get.",
			wantErr: false,
		},
		{
			name:    "json content",
			input:   `{"kind":30078,"content":"test","tags":[["c","agent-v1"]]}`,
			wantErr: false,
		},
		{
			name:    "unicode text",
			input:   "你好世界 🌍 こんにちは",
			wantErr: false,
		},
		{
			name:    "large content",
			input:   string(make([]byte, 10000)),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test Compress
			compressed, err := Compress([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("Compress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Test Decompress
			decompressed, err := Decompress(compressed)
			if (err != nil) != tt.wantErr {
				t.Errorf("Decompress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Verify round-trip
			if !bytes.Equal(decompressed, []byte(tt.input)) {
				t.Errorf("Round-trip failed: got %q, want %q", string(decompressed), tt.input)
			}
		})
	}
}

func TestCompressWithPrefix(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		version string
	}{
		{
			name:    "with version",
			data:    "test data",
			version: "v1",
		},
		{
			name:    "empty data",
			data:    "x",
			version: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressWithPrefix([]byte(tt.data), tt.version)
			if err != nil {
				t.Errorf("CompressWithPrefix() error = %v", err)
				return
			}

			// Verify it contains the prefix
			expectedPrefix := "agent:" + tt.version + ":zstd:"
			if !strings.HasPrefix(compressed, expectedPrefix) {
				t.Errorf("Compressed data missing prefix, got: %s", compressed)
				return
			}
		})
	}
}

func TestDecompressWithPrefix(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		version string
	}{
		{
			name:    "with prefix",
			data:    "test message for prefix",
			version: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First compress with prefix
			compressed, err := CompressWithPrefix([]byte(tt.data), tt.version)
			if err != nil {
				t.Errorf("CompressWithPrefix() error = %v", err)
				return
			}

			// Then decompress
			decompressed, version, err := DecompressWithPrefix(compressed)
			if err != nil {
				t.Errorf("DecompressWithPrefix() error = %v", err)
				return
			}

			if version != tt.version {
				t.Errorf("Version mismatch: got %q, want %q", version, tt.version)
			}

			if string(decompressed) != tt.data {
				t.Errorf("Data mismatch: got %q, want %q", string(decompressed), tt.data)
			}
		})
	}
}

func TestDecompressInvalidData(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "invalid base64",
			data: "!!!invalid!!!",
		},
		{
			name: "corrupted zstd",
			data: "dGVzdA==", // base64 of "test" but not zstd
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decompress(tt.data)
			if err == nil {
				t.Error("Decompress() expected error for invalid data")
			}
		})
	}
}

func BenchmarkCompress(b *testing.B) {
	data := []byte("This is a benchmark test for zstd compression. " +
		"It should measure the performance of our compression implementation.")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Compress(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompress(b *testing.B) {
	data := []byte("This is a benchmark test for zstd decompression.")
	compressed, _ := Compress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Decompress(compressed)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestCompressionRatio(t *testing.T) {
	// Test that compression actually reduces size for repetitive data
	data := bytes.Repeat([]byte("Hello World "), 100)
	
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	// For highly repetitive data, compression should be significant
	compressionRatio := float64(len(compressed)) / float64(len(data))
	t.Logf("Compression ratio: %.2f%%", compressionRatio*100)
	
	// Should achieve at least 50% compression for repetitive data
	if compressionRatio > 0.5 {
		t.Errorf("Compression ratio too high: %.2f%%, expected < 50%%", compressionRatio*100)
	}
}
