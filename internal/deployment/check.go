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
		return &DeploymentClosedError{Code: "DEPLOYMENT_CLOSED", Reason: "nil manifest or observation"}
	}
	if m.State != "active" {
		return &DeploymentClosedError{Code: "DEPLOYMENT_CLOSED", Reason: fmt.Sprintf("state %q not active", m.State)}
	}
	for _, p := range []string{m.CanonicalHome, m.BinaryPath, m.LauncherPath} {
		if err := checkPath(p); err != nil {
			return &DeploymentClosedError{Code: "PATH_UNSAFE", Reason: err.Error()}
		}
	}
	if err := checkStores(m.CanonicalHome, &m.Stores); err != nil {
		return &DeploymentClosedError{Code: "PATH_ESCAPED", Reason: err.Error()}
	}
	if obs.CanonicalHome != m.CanonicalHome {
		return &DeploymentClosedError{Code: "FOREIGN_HOME", Reason: "observed home does not match canonical home"}
	}
	if m.ControlOwnerUID < 0 || obs.ControlOwnerUID != m.ControlOwnerUID {
		return &DeploymentClosedError{Code: "WRONG_OWNER", Reason: "observed uid does not match control owner"}
	}
	if (obs.ObservedDirMode&0022 != 0) || (obs.ObservedFileMode&0022 != 0) ||
		(m.ExpectedModes.DirMode != 0 && obs.ObservedDirMode != m.ExpectedModes.DirMode) ||
		(m.ExpectedModes.FileMode != 0 && obs.ObservedFileMode != m.ExpectedModes.FileMode) {
		return &DeploymentClosedError{Code: "WRONG_MODE", Reason: "insecure or mismatched permission modes"}
	}
	if !isSHA256Hex(m.BinarySHA256) || !isSHA256Hex(m.LauncherSHA256) ||
		obs.BinaryPath != m.BinaryPath || obs.BinarySHA256 != m.BinarySHA256 ||
		obs.LauncherPath != m.LauncherPath || obs.LauncherSHA256 != m.LauncherSHA256 {
		return &DeploymentClosedError{Code: "HASH_MISMATCH", Reason: "executable identity or hash mismatch"}
	}
	if obs.InventoryDigest != m.InventoryDigest {
		return &DeploymentClosedError{Code: "DRIFT", Reason: "inventory digest drift detected"}
	}
	entry, ok := m.AllowedEntries[obs.EntryID]
	if !ok {
		return &DeploymentClosedError{Code: "UNKNOWN_ENTRY", Reason: fmt.Sprintf("entry %q unknown", obs.EntryID)}
	}
	return checkSessionMode(obs.SessionMode, entry.CommandClass)
}

func checkPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "..") {
		return fmt.Errorf("unsafe path %q", p)
	}
	return nil
}

func checkStores(home string, s *StorePaths) error {
	pfx := home + "/"
	for _, p := range []string{s.OutboxJSON, s.OutboxLock, s.MessagesDB, s.MessagesDBWAL, s.MessagesDBSHM, s.DaemonLock} {
		if err := checkPath(p); err != nil || !strings.HasPrefix(p, pfx) {
			return fmt.Errorf("store path %q escapes canonical home", p)
		}
	}
	if strings.Contains(s.OutboxTempPrefix, "/") && !strings.HasPrefix(s.OutboxTempPrefix, pfx) {
		return fmt.Errorf("temp prefix %q escapes canonical home", s.OutboxTempPrefix)
	}
	if !strings.Contains(s.OutboxTempPrefix, "/") && (strings.Contains(s.OutboxTempPrefix, "..") || s.OutboxTempPrefix == "") {
		return fmt.Errorf("invalid temp prefix %q", s.OutboxTempPrefix)
	}
	return nil
}

func isSHA256Hex(s string) bool {
	dst := make([]byte, 32)
	_, err := hex.Decode(dst, []byte(s))
	return len(s) == 64 && err == nil
}

func checkSessionMode(mode SessionMode, class string) error {
	switch {
	case mode == SessionModeRuntime && class != "maintenance" && class != "probe":
		return nil
	case mode == SessionModeMaintenance && class == "maintenance":
		return nil
	case mode == SessionModeProbe && class == "probe":
		return nil
	case mode != SessionModeRuntime && mode != SessionModeMaintenance && mode != SessionModeProbe:
		return &DeploymentClosedError{Code: "INVALID_SESSION_MODE", Reason: fmt.Sprintf("unknown session mode %q", mode)}
	default:
		return &DeploymentClosedError{Code: "MODE_MISMATCH", Reason: fmt.Sprintf("class %q incompatible with mode %q", class, mode)}
	}
}
