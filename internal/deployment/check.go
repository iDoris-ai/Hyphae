// Package deployment provides pure deployment validation predicates ONLY.
//
// PREDICATE ONLY — NOT V7 PROOF:
// Pure evaluation of manifest and observation tuples does not verify
// OS-level quiescence, process termination, or store protection.
package deployment

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

func Validate(m *Manifest, obs *Observation) error {
	if m == nil || obs == nil {
		return closedErr("DEPLOYMENT_CLOSED", "nil manifest or observation")
	}
	if m.SchemaVersion != SupportedSchemaVersion {
		return closedErr("UNSUPPORTED_VERSION", fmt.Sprintf("version %q unsupported", m.SchemaVersion))
	}
	if m.State != "active" {
		return closedErr("DEPLOYMENT_CLOSED", fmt.Sprintf("state %q not active", m.State))
	}
	for _, p := range []string{m.CanonicalHome, m.BinaryPath, m.LauncherPath} {
		if !isCleanAbs(p) {
			return closedErr("PATH_UNSAFE", fmt.Sprintf("unsafe path %q", p))
		}
	}
	for _, p := range m.Stores.all() {
		if !isCleanAbs(p) || !strings.HasPrefix(p, m.CanonicalHome+"/") {
			return closedErr("PATH_ESCAPED", fmt.Sprintf("store path %q escapes canonical home", p))
		}
	}
	tempDir := filepath.Dir(m.Stores.OutboxTempPrefix)
	if tempDir != filepath.Dir(m.Stores.OutboxJSON) || tempDir != filepath.Dir(m.Stores.OutboxLock) ||
		!strings.HasPrefix(filepath.Base(m.Stores.OutboxTempPrefix), ".outbox-") {
		return closedErr("PATH_ESCAPED", "outbox temp prefix is not the designated outbox-sibling namespace")
	}
	if obs.CanonicalHome != m.CanonicalHome {
		return closedErr("FOREIGN_HOME", "observed home does not match canonical home")
	}
	if m.ControlOwnerUID < 0 || obs.ControlOwnerUID < 0 || obs.ControlOwnerUID != m.ControlOwnerUID {
		return closedErr("WRONG_OWNER", "observed uid does not match control owner")
	}
	exp, obsM := m.ExpectedModes, ModeConfig{DirMode: obs.ObservedDirMode, FileMode: obs.ObservedFileMode}
	if !isOwnerOnly(exp.DirMode) || !isOwnerOnly(exp.FileMode) || !isOwnerOnly(obsM.DirMode) || !isOwnerOnly(obsM.FileMode) || exp != obsM {
		return closedErr("WRONG_MODE", "insecure or mismatched permission modes")
	}
	if !isSHA256Hex(m.BinarySHA256) || !isSHA256Hex(m.LauncherSHA256) ||
		obs.BinaryPath != m.BinaryPath || obs.BinarySHA256 != m.BinarySHA256 ||
		obs.LauncherPath != m.LauncherPath || obs.LauncherSHA256 != m.LauncherSHA256 {
		return closedErr("HASH_MISMATCH", "executable identity or hash mismatch")
	}
	if !isSHA256Hex(m.InventoryDigest) || obs.InventoryDigest != m.InventoryDigest {
		return closedErr("DRIFT", "inventory digest drift detected")
	}
	if err := validateEntryInventory(m.AllowedEntries); err != nil {
		return err
	}
	entry, ok := m.AllowedEntries[obs.EntryID]
	if !ok {
		return closedErr("UNKNOWN_ENTRY", fmt.Sprintf("entry %q unknown", obs.EntryID))
	}
	return checkSessionMode(obs.SessionMode, entry.CommandClass)
}

func isKnownClass(c string) bool {
	return c == "dm" || c == "group-store" || c == "group-write" || c == "maintenance" || c == "probe"
}

func isOwnerOnly(mode uint32) bool { return mode != 0 && (mode&^uint32(0700)) == 0 }

func isCleanAbs(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }

func isSHA256Hex(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil
}

func checkSessionMode(mode SessionMode, class string) error {
	switch {
	case mode == SessionModeRuntime && class != "maintenance" && class != "probe":
		return nil
	case mode == SessionModeMaintenance && class == "maintenance", mode == SessionModeProbe && class == "probe":
		return nil
	case mode != SessionModeRuntime && mode != SessionModeMaintenance && mode != SessionModeProbe:
		return closedErr("INVALID_SESSION_MODE", fmt.Sprintf("unknown session mode %q", mode))
	default:
		return closedErr("MODE_MISMATCH", fmt.Sprintf("class %q incompatible with mode %q", class, mode))
	}
}
