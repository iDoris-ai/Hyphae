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
	phaseClosed, phaseBackedUp, phaseMigrating = "closed", "backed-up", "migrating"
	phaseMigrated, phaseActive                 = "migrated", "active"
	opClose, opBackup, opStage, opCommit       = "close", "backup", "stage", "commit"
)

type RunningWriter struct {
	EntryID string
	Alive   bool
}

type upgradeJournal struct {
	Phase, PreviousBinary, PreviousSHA256, NextBinary, NextSHA256, BackupDir string
	StoreSHA256                                                              map[string]string
}

func CloseEntries(manifestPath string, writers []RunningWriter) error {
	return upkeep(manifestPath, writers, opClose, "", "", "")
}
func BackupInstall(manifestPath, backupDir string, writers []RunningWriter) error {
	return upkeep(manifestPath, writers, opBackup, backupDir, "", "")
}
func StageMigrate(manifestPath, nextBinary, nextSHA string) error {
	return upkeep(manifestPath, nil, opStage, "", nextBinary, nextSHA)
}
func CommitMigrate(manifestPath string) error { return upkeep(manifestPath, nil, opCommit, "", "", "") }
func RollbackInstall(manifestPath string) error {
	return upkeep(manifestPath, nil, "rollback", "", "", "")
}
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
		if m.State != "active" {
			m.State = "active"
			err = saveJSON(path, m)
		}
		if err == nil {
			j.Phase = phaseActive
			err = saveJSON(journalPath(path), j)
		}
		if err != nil {
			return "", err
		}
		return m.BinaryPath, nil
	}
	if j.Phase == phaseActive && m.State == "active" {
		return m.BinaryPath, nil
	}
	return "", deny("restart refused")
}

func upkeep(manifestPath string, writers []RunningWriter, op, dir, next, sha string) error {
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
		if j, err := loadJournal(path); err == nil && j.Phase != "" {
			return nil
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
	return saveJSON(journalPath(path), j)
}
func noteNext(m *Manifest, path, next, sha string, stage bool) error {
	phases := []string{phaseMigrating}
	if stage {
		phases = []string{phaseBackedUp, phaseMigrating}
	}
	j, err := need(m, path, phases...)
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
		return saveJSON(journalPath(path), j)
	}
	m.BinaryPath, m.BinarySHA256, m.State = next, sha, "closed"
	j.Phase = phaseMigrated
	return writeBoth(path, m, j)
}
func restoreBackup(m *Manifest, path string) error {
	j, err := loadJournal(path)
	if err != nil {
		return deny("backup missing")
	}
	if err = verifyBackup(j); err != nil {
		return err
	}
	if m.State == "active" {
		return deny("rollback after activation refused")
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
		_, got, err := readSum(filepath.Join(j.BackupDir, role))
		if err != nil || j.StoreSHA256[role] == "" {
			return deny("backup missing")
		}
		if got != j.StoreSHA256[role] {
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
		sum, err := copyRegular(src, dst)
		if err != nil {
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
	raw, err := (launchEnv{open: openNoFollowOS}).readManifest(journalPath(manifestPath))
	if err != nil {
		return nil, err
	}
	var j upgradeJournal
	err = json.Unmarshal(raw, &j)
	if err != nil {
		err = deny("upgrade record refused")
	}
	return &j, err
}
func journalPath(manifestPath string) string { return manifestPath + ".maintain.json" }
func deny(reason string) error               { return closedErr("DEPLOYMENT_CLOSED", reason) }
func writeBoth(path string, m *Manifest, j *upgradeJournal) error {
	if err := saveJSON(path, m); err != nil {
		return err
	}
	return saveJSON(journalPath(path), j)
}
func saveJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return deny("upgrade record refused")
	}
	return writeExclusive(path, append(body, '\n'), "upgrade record refused")
}
func copyRegular(src, dst string) (string, error) {
	body, sum, err := readSum(src)
	if err != nil {
		return "", err
	}
	return sum, writeExclusive(dst, body, "backup unavailable")
}
func readSum(path string) ([]byte, string, error) {
	body, err := (launchEnv{open: openNoFollowOS}).readManifest(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}
func writeExclusive(path string, body []byte, fail string) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return deny(fail)
	}
	_, werr := f.Write(body)
	if serr, cerr := f.Sync(), f.Close(); werr != nil || serr != nil || cerr != nil {
		return deny(fail)
	}
	if err = os.Rename(tmp, path); err != nil {
		return deny(fail)
	}
	return nil
}
