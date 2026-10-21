package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

const (
	phaseClosed, phaseBackedUp, phaseMigrating   = "closed", "backed-up", "migrating"
	phaseMigrated, phaseActive, phaseRolling     = "migrated", "active", "rolling-back"
	opClose, opBackup, opStage, opCommit, absent = "close", "backup", "stage", "commit", "-"
)

type RunningWriter struct {
	EntryID string
	Alive   bool
}
type upgradeJournal struct {
	Phase, PreviousBinary, PreviousSHA256, NextBinary, NextSHA256, BackupDir string
	StoreSHA256                                                              map[string]string
}

func CloseEntries(p string, w []RunningWriter) error     { return run(p, w, opClose, "", "", "") }
func BackupInstall(p, d string, w []RunningWriter) error { return run(p, w, opBackup, d, "", "") }
func StageMigrate(p, b, h string) error                  { return run(p, nil, opStage, "", b, h) }
func CommitMigrate(p string) error                       { return run(p, nil, opCommit, "", "", "") }
func RollbackInstall(p string) error                     { return run(p, nil, "rollback", "", "", "") }
func ColdStart(manifestPath string) (string, error) {
	m, path, err := loadManifest(manifestPath)
	if err != nil {
		return "", err
	}
	j, err := loadJournal(path)
	if err != nil {
		return "", deny("restart refused")
	}
	if err = hashMatches(m.BinaryPath, m.BinarySHA256); err != nil {
		return "", err
	}
	if j.Phase == phaseMigrated {
		m.State, j.Phase = "active", phaseActive
		if err = writeBoth(path, m, j); err != nil {
			return "", err
		}
		return m.BinaryPath, nil
	}
	if j.Phase == phaseActive && m.State == "active" {
		return m.BinaryPath, nil
	}
	return "", deny("restart refused")
}
func run(manifestPath string, writers []RunningWriter, op, dir, next, sha string) error {
	for _, w := range writers {
		if w.Alive {
			return deny("writer still running")
		}
	}
	m, path, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	switch op {
	case opClose:
		return markClosed(m, path)
	case opBackup:
		return copyBackup(m, path, dir)
	case opStage:
		return noteNext(m, path, next, sha, true)
	case opCommit:
		return noteNext(m, path, "", "", false)
	default:
		return restoreBackup(m, path)
	}
}
func markClosed(m *Manifest, path string) error {
	if m.State == "closed" {
		j, err := loadJournal(path)
		if err != nil {
			return err
		}
		if j.Phase == phaseClosed {
			return nil
		}
		if j.Phase != phaseActive {
			return deny("state refuses maintenance")
		}
	} else if m.State != "active" {
		return deny("state refuses maintenance")
	}
	m.State = "closed"
	return writeBoth(path, m, &upgradeJournal{Phase: phaseClosed, PreviousBinary: m.BinaryPath, PreviousSHA256: m.BinarySHA256})
}
func copyBackup(m *Manifest, path, backupDir string) error {
	j, err := need(m, path, phaseClosed, phaseBackedUp)
	if err != nil {
		return err
	}
	if err = refuseStore(m, backupDir); err != nil {
		return err
	}
	if err = os.MkdirAll(backupDir, 0700); err != nil {
		return deny("backup unavailable")
	}
	sums, err := copyRoles(m.Stores, backupDir, true)
	if err != nil {
		return err
	}
	j.Phase, j.BackupDir, j.StoreSHA256 = phaseBackedUp, backupDir, sums
	return saveJSON(path+".maintain.json", j, 0600)
}
func noteNext(m *Manifest, path, next, sha string, stage bool) error {
	j, err := need(m, path, map[bool][]string{true: {phaseBackedUp, phaseMigrating}, false: {phaseMigrating}}[stage]...)
	if err != nil {
		return err
	}
	if err = verifyBackup(j); err != nil {
		return err
	}
	if stage {
		if err = refuseStore(m, next); err != nil {
			return err
		}
	} else {
		next, sha = j.NextBinary, j.NextSHA256
	}
	if err = hashMatches(next, sha); err != nil {
		return err
	}
	if stage {
		j.Phase, j.NextBinary, j.NextSHA256 = phaseMigrating, next, sha
		return saveJSON(path+".maintain.json", j, 0600)
	}
	m.BinaryPath, m.BinarySHA256, m.State = next, sha, "closed"
	j.Phase = phaseMigrated
	return writeBoth(path, m, j)
}
func restoreBackup(m *Manifest, path string) error {
	j, err := loadJournal(path)
	if err != nil || (j.Phase != phaseBackedUp && j.Phase != phaseMigrating && j.Phase != phaseMigrated && j.Phase != phaseRolling) {
		return deny("backup missing")
	}
	if err = verifyBackup(j); err != nil {
		return err
	}
	if m.State == "active" {
		return deny("rollback after activation refused")
	}
	if err = hashMatches(j.PreviousBinary, j.PreviousSHA256); err != nil {
		return err
	}
	j.Phase = phaseRolling
	if err = saveJSON(path+".maintain.json", j, 0600); err != nil {
		return err
	}
	if _, err = copyRoles(m.Stores, j.BackupDir, false); err != nil {
		return err
	}
	m.BinaryPath, m.BinarySHA256, m.State = j.PreviousBinary, j.PreviousSHA256, "closed"
	j.Phase, j.NextBinary, j.NextSHA256 = phaseClosed, "", ""
	return writeBoth(path, m, j)
}
func need(m *Manifest, path string, phases ...string) (*upgradeJournal, error) {
	if m.State != "closed" {
		return nil, deny("entries are open")
	}
	j, err := loadJournal(path)
	if err == nil {
		for _, item := range phases {
			if j.Phase == item {
				return j, nil
			}
		}
	}
	return nil, deny("backup missing")
}
func verifyBackup(j *upgradeJournal) error {
	if j == nil || j.BackupDir == "" || len(j.StoreSHA256) != len(storeRoles) {
		return deny("backup missing")
	}
	for _, role := range storeRoles {
		want := j.StoreSHA256[role]
		if want == absent {
			continue
		}
		_, got, err := readSum(filepath.Join(j.BackupDir, role))
		if err != nil || want == "" {
			return deny("backup missing")
		}
		if got != want {
			return closedErr("HASH_MISMATCH", "backup hash mismatch")
		}
	}
	return nil
}
func copyRoles(stores StorePaths, dir string, intoBackup bool) (map[string]string, error) {
	sums := make(map[string]string, len(storeRoles))
	for _, role := range storeRoles {
		live, ok := storeByRole(stores, role)
		if !ok {
			return nil, closedErr("MALFORMED_MANIFEST", "store role unbound")
		}
		src, dst := live, filepath.Join(dir, role)
		if !intoBackup {
			src, dst = dst, live
		}
		info, statErr := os.Lstat(src)
		if statErr != nil || !info.Mode().IsRegular() {
			if role == "outbox_json" || role == "messages_db" {
				return nil, deny("path unavailable")
			}
			if !intoBackup {
				_ = os.Remove(dst)
			}
			sums[role] = absent
			continue
		}
		body, sum, err := readSum(src)
		if err != nil {
			return nil, err
		}
		if err = writeExclusive(dst, body, 0600, "backup unavailable"); err != nil {
			return nil, err
		}
		sums[role] = sum
	}
	return sums, nil
}
func hashMatches(path, want string) error {
	_, sum, err := readSum(path)
	if err != nil {
		return err
	}
	if sum != want || !isSHA256Hex(want) {
		return closedErr("HASH_MISMATCH", "executable hash mismatch")
	}
	return nil
}
func loadManifest(manifestPath string) (*Manifest, string, error) {
	path, err := canonicalPath(manifestPath)
	if err != nil {
		return nil, "", err
	}
	raw, err := (launchEnv{open: openNoFollowOS}).readManifest(path)
	if err != nil {
		return nil, "", err
	}
	m, err := ParseManifestJSON(raw)
	return m, path, err
}
func loadJournal(manifestPath string) (*upgradeJournal, error) {
	raw, err := (launchEnv{open: openNoFollowOS}).readManifest(manifestPath + ".maintain.json")
	if err != nil {
		return nil, err
	}
	var j upgradeJournal
	if json.Unmarshal(raw, &j) != nil {
		return nil, deny("upgrade record refused")
	}
	return &j, nil
}
func deny(reason string) error { return closedErr("DEPLOYMENT_CLOSED", reason) }
func writeBoth(path string, m *Manifest, j *upgradeJournal) error {
	if err := saveJSON(path, m, os.FileMode(m.ExpectedModes.FileMode)); err != nil {
		return err
	}
	return saveJSON(path+".maintain.json", j, 0600)
}
func saveJSON(path string, v any, mode os.FileMode) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return deny("upgrade record refused")
	}
	return writeExclusive(path, append(body, '\n'), mode, "upgrade record refused")
}
func readSum(path string) ([]byte, string, error) {
	body, err := (launchEnv{open: openNoFollowOS}).readManifest(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}
func writeExclusive(path string, body []byte, mode os.FileMode, fail string) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return deny(fail)
	}
	_, werr := f.Write(body)
	serr, cerr := f.Sync(), f.Close()
	if werr != nil || serr != nil || cerr != nil || os.Rename(tmp, path) != nil {
		return deny(fail)
	}
	return nil
}
