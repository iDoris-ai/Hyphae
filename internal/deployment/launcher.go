package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LaunchRequest is the only caller input. The executable comes from the manifest.
type LaunchRequest struct {
	ManifestPath, EntryID string
	Session               SessionMode
	Args, Env             []string
}

type launchEnv struct {
	executable func() (string, error)
	execFn     func(string, []string, []string) error
	open       func(string) (*os.File, error)
	onManifest func(*Manifest) error
}

type pathFact struct {
	uid  int
	mode uint32
	sum  string
}

func EntryInventoryDigest(entries map[string]EntryDef) string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		e := entries[key]
		_, _ = io.WriteString(h, key+"\x00"+e.ID+"\x00"+e.CommandClass+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Launch execs the pinned binary only. It does not open store paths and is not V7 closure.
func Launch(req LaunchRequest) error {
	return launch(req, launchEnv{executable: os.Executable, execFn: execBinary, open: openNoFollowOS})
}

func launch(req LaunchRequest, env launchEnv) error {
	if err := rejectLegacy(req.Args, false); err != nil {
		return err
	}
	base := req.Env
	if base == nil {
		base = os.Environ()
	}
	if err := rejectLegacy(base, true); err != nil {
		return err
	}
	manifestPath, err := canonicalPath(req.ManifestPath)
	if err != nil {
		return err
	}
	raw, err := env.readManifest(manifestPath)
	if err != nil {
		return err
	}
	m, err := ParseManifestJSON(raw)
	if err != nil {
		return err
	}
	if m.State != "active" {
		return closedErr("DEPLOYMENT_CLOSED", fmt.Sprintf("state %q not active", m.State))
	}
	if err := refuseStore(m, manifestPath); err != nil {
		return err
	}
	if env.onManifest != nil {
		if err := env.onManifest(m); err != nil {
			return err
		}
	}
	child, err := childEnv(base, m.CanonicalHome, req.EntryID, string(req.Session))
	if err != nil {
		return err
	}
	self, err := env.executable()
	if err != nil || self == "" {
		return closedErr("PATH_UNSAFE", "launcher path unavailable")
	}
	if self, err = canonicalPath(self); err != nil {
		return err
	}
	if self != m.LauncherPath {
		return closedErr("PATH_UNSAFE", "running path is not the registered launcher")
	}
	home, err := env.observe(m, m.CanonicalHome, true, false)
	if err != nil {
		return err
	}
	manifest, err := env.observe(m, manifestPath, false, false)
	if err != nil {
		return err
	}
	launcher, err := env.observe(m, m.LauncherPath, false, true)
	if err != nil {
		return err
	}
	binary, err := env.observe(m, m.BinaryPath, false, true)
	if err != nil {
		return err
	}
	uid, err := currentUID()
	if err != nil {
		return err
	}
	if uid != m.ControlOwnerUID {
		return closedErr("WRONG_OWNER", "process uid does not match control owner")
	}
	for _, fact := range []pathFact{home, manifest, launcher, binary} {
		if fact.uid != uid {
			return closedErr("WRONG_OWNER", "path owner does not match control owner")
		}
	}
	if home.mode != m.ExpectedModes.DirMode || manifest.mode != m.ExpectedModes.FileMode || launcher.mode != m.ExpectedModes.FileMode || binary.mode != m.ExpectedModes.FileMode {
		return closedErr("WRONG_MODE", "permission mode does not match the manifest")
	}
	if err := Validate(m, &Observation{
		CanonicalHome: m.CanonicalHome, ControlOwnerUID: uid,
		ObservedDirMode: home.mode, ObservedFileMode: binary.mode,
		BinaryPath: m.BinaryPath, BinarySHA256: binary.sum,
		LauncherPath: m.LauncherPath, LauncherSHA256: launcher.sum,
		EntryID: req.EntryID, SessionMode: req.Session,
		InventoryDigest: EntryInventoryDigest(m.AllowedEntries),
	}); err != nil {
		return err
	}
	argv := append([]string{m.BinaryPath}, req.Args...)
	if err := env.execFn(m.BinaryPath, argv, child); err != nil {
		return closedErr("DEPLOYMENT_CLOSED", "pinned binary exec failed")
	}
	return nil
}

func rejectLegacy(list []string, env bool) error {
	for _, item := range list {
		base, _, ok := strings.Cut(item, "=")
		bad := base == "--binary" || base == "--exec" || base == "--legacy"
		if env {
			bad = base == "HYPHAE_BINARY" || base == "HYPHAE_EXEC" || base == "HYPHAE_VERSION_PATH"
		}
		if strings.ContainsRune(item, 0) || (env && (!ok || base == "")) {
			return closedErr("DEPLOYMENT_CLOSED", "malformed request")
		}
		if bad {
			return closedErr("DEPLOYMENT_CLOSED", "legacy version request rejected")
		}
	}
	return nil
}

func childEnv(base []string, home, entry, session string) ([]string, error) {
	out := make([]string, 0, len(base)+3)
	var sawHome bool
	for _, kv := range base {
		key, val, _ := strings.Cut(kv, "=")
		switch key {
		case "HOME":
			if sawHome || val != home {
				return nil, closedErr("FOREIGN_HOME", "HOME override rejected")
			}
			sawHome = true
		case "HYPHAE_ENTRY_ID":
			if val != entry {
				return nil, closedErr("DEPLOYMENT_CLOSED", "entry override rejected")
			}
		case "HYPHAE_SESSION_MODE":
			if val != session {
				return nil, closedErr("DEPLOYMENT_CLOSED", "session override rejected")
			}
		default:
			out = append(out, kv)
		}
	}
	return append(out, "HOME="+home, "HYPHAE_ENTRY_ID="+entry, "HYPHAE_SESSION_MODE="+session), nil
}

func canonicalPath(path string) (string, error) {
	if !isCleanAbs(path) {
		return "", closedErr("PATH_UNSAFE", fmt.Sprintf("unsafe path %q", path))
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return "", closedErr("PATH_UNSAFE", fmt.Sprintf("path alias %q", path))
	}
	return path, nil
}

func refuseStore(m *Manifest, path string) error {
	for _, store := range m.Stores.all() {
		if path == store {
			return closedErr("PATH_UNSAFE", "refusing a store path")
		}
	}
	return nil
}

func (e launchEnv) observe(m *Manifest, path string, dir, hash bool) (pathFact, error) {
	if err := refuseStore(m, path); err != nil {
		return pathFact{}, err
	}
	if _, err := canonicalPath(path); err != nil {
		return pathFact{}, err
	}
	fact, err := inspect(path, dir)
	if err != nil {
		return pathFact{}, err
	}
	if !hash {
		return fact, nil
	}
	f, err := e.open(path)
	if err != nil {
		return pathFact{}, closedErr("DEPLOYMENT_CLOSED", "executable unreadable")
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return pathFact{}, closedErr("DEPLOYMENT_CLOSED", "executable unreadable")
	}
	fact.sum = hex.EncodeToString(sum.Sum(nil))
	return fact, nil
}

func (e launchEnv) readManifest(path string) ([]byte, error) {
	if _, err := inspect(path, false); err != nil {
		return nil, err
	}
	f, err := e.open(path)
	if err != nil {
		return nil, closedErr("DEPLOYMENT_CLOSED", "manifest unreadable")
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, closedErr("DEPLOYMENT_CLOSED", "manifest unreadable")
	}
	return b, nil
}

func inspect(path string, dir bool) (pathFact, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return pathFact{}, closedErr("DEPLOYMENT_CLOSED", "path unavailable")
	}
	mode := info.Mode()
	switch {
	case dir && !info.IsDir():
		return pathFact{}, closedErr("PATH_UNSAFE", "expected a directory")
	case !dir && !mode.IsRegular():
		return pathFact{}, closedErr("PATH_UNSAFE", "expected a regular file")
	case mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0:
		return pathFact{}, closedErr("WRONG_MODE", "special mode bits rejected")
	}
	uid, nlink, err := fileIdentity(info)
	if err != nil {
		return pathFact{}, err
	}
	if !dir && nlink != 1 {
		return pathFact{}, closedErr("PATH_UNSAFE", "hard link rejected")
	}
	return pathFact{uid: uid, mode: uint32(mode.Perm())}, nil
}
