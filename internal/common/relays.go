package common

import (
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/urfave/cli/v3"
)

// ResolveRelays applies the shared explicit flag > local config > old default rule.
func ResolveRelays(c *cli.Command) ([]string, error) {
	r, err := relayconfig.New()
	if err != nil {
		return nil, err
	}
	explicit := c.StringSlice("relay")
	resolved, err := r.Resolve(explicit)
	if err != nil {
		if len(explicit) != 0 {
			return nil, NewExitError(ErrCodeUser, err)
		}
		return nil, NewExitError(ErrCodeOther, fmt.Errorf("relay configuration: %w", err))
	}
	return resolved.Relays, nil
}
