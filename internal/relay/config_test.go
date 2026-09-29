package relay

import (
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name, listen, dataDir string
		port                  int
		public                bool
		emptyDataDir          bool
		wantErr               string
	}{
		{name: "loopback", listen: "127.0.0.1", port: 3334},
		{name: "ipv6", listen: "::1", port: 443},
		{name: "port zero", listen: "127.0.0.1", port: 0, wantErr: "port"},
		{name: "port too large", listen: "127.0.0.1", port: 65536, wantErr: "port"},
		{name: "public requires opt in", listen: "0.0.0.0", port: 3334, wantErr: "--public"},
		{name: "public explicitly allowed", listen: "0.0.0.0", port: 3334, public: true},
		{name: "hostnames rejected", listen: "localhost", port: 3334, wantErr: "IP address"},
		{name: "empty data path", listen: "127.0.0.1", port: 3334, emptyDataDir: true, wantErr: "data directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := tt.dataDir
			if !tt.emptyDataDir {
				dataDir = t.TempDir()
			}
			cfg, err := ParseConfig(tt.listen, dataDir, tt.port, tt.public)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseConfig() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConfig() error = %v", err)
			}
			if cfg.Port != tt.port || cfg.Address == "" {
				t.Fatalf("unexpected config: %+v", cfg)
			}
		})
	}
}
