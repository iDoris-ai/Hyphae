package main

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

var keyCmd = &cli.Command{
	Name:  "key",
	Usage: "Key management commands",
	Commands: []*cli.Command{
		{
			Name:  "generate",
			Usage: "Generate a new key pair",
			Action: func(ctx context.Context, c *cli.Command) error {
				sk := nostr.Generate()
				pk := sk.Public()

				nsec := encodeNsec(sk)
				npub := encodeNpub(pk)

				green := color.New(color.FgGreen).SprintFunc()
				yellow := color.New(color.FgYellow).SprintFunc()

				fmt.Println("✅ Generated new key pair")
				fmt.Println()
				fmt.Printf("Private key (hex): %s\n", green(sk.Hex()))
				fmt.Printf("Private key (nsec): %s\n", green(nsec))
				fmt.Println()
				fmt.Printf("Public key (hex):  %s\n", yellow(pk.Hex()))
				fmt.Printf("Public key (npub): %s\n", yellow(npub))
				fmt.Println()
				fmt.Println("⚠️  Save your private key securely. Never share it with anyone!")

				return nil
			},
		},
		{
			Name:  "public",
			Usage: "Get public key from secret key",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "sec",
					Aliases:  []string{"s"},
					Usage:    "Secret key (hex or nsec)",
					Required: true,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				secKeyStr := c.String("sec")
				secKey, err := parseSecretKey(secKeyStr)
				if err != nil {
					return fmt.Errorf("invalid secret key: %w", err)
				}

				pubKey := secKey.Public()

				fmt.Printf("Public key (hex):  %s\n", pubKey.Hex())
				fmt.Printf("Public key (npub): %s\n", encodeNpub(pubKey))

				return nil
			},
		},
	},
}
