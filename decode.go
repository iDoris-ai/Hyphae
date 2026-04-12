package main

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/urfave/cli/v3"
)

var decodeCmd = &cli.Command{
	Name:  "decode",
	Usage: "Decode bech32 encoded keys or IDs",
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name: "input",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		input := c.String("input")
		if input == "" {
			return fmt.Errorf("input is required")
		}

		hrp, data, err := bech32.Decode(input)
		if err != nil {
			return fmt.Errorf("failed to decode: %w", err)
		}

		// Convert 5-bit to 8-bit
		converted, err := bech32.ConvertBits(data, 5, 8, false)
		if err != nil {
			return fmt.Errorf("failed to convert bits: %w", err)
		}

		fmt.Printf("Prefix: %s\n", hrp)
		fmt.Printf("Hex:    %s\n", hex.EncodeToString(converted))

		return nil
	},
}
