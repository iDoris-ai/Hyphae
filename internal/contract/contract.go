// Package contract implements `hyphae contract`, the
// machine-readable component interface consumed by Agent24.
package contract

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

// ContractVersion is "major.minor". Removing, renaming or changing the
// semantics of a command, flag, error code or `hyphae contract` output key
// bumps major; only ADDING a subcommand, flag or error code bumps minor. This is
// a release policy for maintainers; no code enforces it.
const ContractVersion = "1.0"

// provides is the capability table; keep sorted.
var provides = []string{"nostr-dm", "nostr-group", "nostr-identity", "nostr-relay"}

type commandInfo struct {
	Path  string   `json:"path"`
	Flags []string `json:"flags"`
}

type errorCodeInfo struct {
	Code string `json:"code"`
	Exit int    `json:"exit"`
}

type contractData struct {
	Name            string          `json:"name"`
	ContractVersion string          `json:"contract_version"`
	BinaryVersion   string          `json:"binary_version"`
	Provides        []string        `json:"provides"`
	Commands        []commandInfo   `json:"commands"`
	ErrorCodes      []errorCodeInfo `json:"error_codes"`
}

// Cmd is `hyphae contract`.
var Cmd = &cli.Command{
	Name:  "contract",
	Usage: "Print the machine-readable component contract (commands, flags, error codes)",
	Action: func(_ context.Context, c *cli.Command) error {
		d := buildContract(c.Root())
		common.Emit(common.JSONMode(c), d, func() {
			fmt.Printf("%s contract %s (binary %s)\n", d.Name, d.ContractVersion, d.BinaryVersion)
			fmt.Printf("provides: %s\n", strings.Join(d.Provides, ", "))
			for _, ci := range d.Commands {
				fmt.Printf("  %s %s\n", ci.Path, strings.Join(ci.Flags, " "))
			}
			for _, e := range d.ErrorCodes {
				fmt.Printf("  error %s -> exit %d\n", e.Code, e.Exit)
			}
		})
		return nil
	},
}

func buildContract(root *cli.Command) contractData {
	codes := make([]errorCodeInfo, 0, len(common.StableErrorCodes))
	for _, code := range common.StableErrorCodes {
		codes = append(codes, errorCodeInfo{Code: code, Exit: common.ExitCodeFor(code)})
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].Code < codes[j].Code })
	return contractData{
		Name: "hyphae-cli", ContractVersion: ContractVersion, BinaryVersion: root.Version,
		Provides: provides, Commands: walk(root), ErrorCodes: codes,
	}
}

// walk lists every visible command path with its visible flags. The root is
// listed itself; root-level (global) flags are not repeated.
func walk(root *cli.Command) []commandInfo {
	out := []commandInfo{}
	var rec func(cmd *cli.Command, path string)
	rec = func(cmd *cli.Command, path string) {
		if path != "" {
			out = append(out, commandInfo{Path: path, Flags: flagNames(cmd)})
		}
		for _, sub := range cmd.Commands {
			if sub.Hidden || sub.Name == "help" {
				continue
			}
			p := sub.Name
			if path != "" {
				p = path + " " + sub.Name
			}
			rec(sub, p)
		}
	}
	rec(root, "")
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func flagNames(cmd *cli.Command) []string {
	names := []string{}
	for _, f := range cmd.Flags {
		if vf, ok := f.(cli.VisibleFlag); ok && !vf.IsVisible() {
			continue
		}
		n := f.Names()[0]
		if n == "help" {
			continue
		}
		names = append(names, "--"+n)
	}
	sort.Strings(names)
	return names
}
