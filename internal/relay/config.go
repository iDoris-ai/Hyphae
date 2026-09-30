package relay

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Address  string
	DataDir  string
	Port     int
	PublicOK bool
}

func ParseConfig(listen, dataDir string, port int, publicOK bool) (Config, error) {
	if port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("port must be between 1 and 65535: %d", port)
	}
	ip := net.ParseIP(strings.Trim(listen, "[]"))
	if ip == nil {
		return Config{}, fmt.Errorf("listen address must be an IP address: %q", listen)
	}
	if !ip.IsLoopback() && !publicOK {
		return Config{}, errors.New("non-loopback listen address requires --public")
	}
	if strings.TrimSpace(dataDir) == "" {
		return Config{}, errors.New("data directory cannot be empty")
	}
	return Config{
		Address: net.JoinHostPort(ip.String(), strconv.Itoa(port)),
		DataDir: filepath.Clean(dataDir),
		Port:    port, PublicOK: publicOK,
	}, nil
}
