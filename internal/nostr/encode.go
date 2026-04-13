package nostr

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/jason/agent-speaker/internal/common"
	"github.com/urfave/cli/v3"
)

var EncodeCmd = &cli.Command{
	Name:  "encode",
	Usage: "Encode hex to bech32 format",
	Description: `Encode hex keys to bech32 format (npub, nsec, note, etc.).
Example: agent-speaker encode --prefix npub --hex <hex>`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "prefix",
			Aliases:  []string{"p"},
			Usage:    "Prefix (npub, nsec)",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "hex",
			Aliases:  []string{"x"},
			Usage:    "Hex string to encode",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		prefix := c.String("prefix")
		hexStr := c.String("hex")

		if prefix == "" || hexStr == "" {
			return fmt.Errorf("usage: encode <prefix> <hex>")
		}

		data, err := hex.DecodeString(hexStr)
		if err != nil {
			return fmt.Errorf("invalid hex: %w", err)
		}

		var encoded string
		switch strings.ToLower(prefix) {
		case "npub":
			if len(data) != 32 {
				return fmt.Errorf("invalid public key length: %d", len(data))
			}
			var pk nostr.PubKey
			copy(pk[:], data)
			encoded = common.EncodeNpub(pk)
		case "nsec":
			if len(data) != 32 {
				return fmt.Errorf("invalid secret key length: %d", len(data))
			}
			var sk nostr.SecretKey
			copy(sk[:], data)
			encoded = common.EncodeNsec(sk)
		default:
			return fmt.Errorf("unsupported prefix: %s", prefix)
		}

		if encoded == "" {
			return fmt.Errorf("failed to encode")
		}

		fmt.Println(encoded)
		return nil
	},
}
