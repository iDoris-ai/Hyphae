package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// staleTempMinAge is a grace period on top of the lock argument below. Hyphae
// before v0.26.1 (and any external tool) wrote the same temp name without the
// flock; a file touched within this window may belong to such a live writer
// (which finishes in milliseconds) and is left alone. A real orphan only
// lingers this much longer before the next write removes it.
const staleTempMinAge = 5 * time.Minute

// syncDirFn is a test seam around syncDir.
var syncDirFn = syncDir

// beforeRenameHook is a test seam: it runs after the temp keystore is written
// and synced but before it is renamed into place. Always nil in production.
var beforeRenameHook func()

// tempKeystoreName matches exactly what os.CreateTemp(dir, KeyStoreFile+".*.tmp")
// produces: the literal prefix, a decimal random string, the literal suffix.
var tempKeystoreName = regexp.MustCompile(`^` + regexp.QuoteMeta(KeyStoreFile) + `\.[0-9]+\.tmp$`)

// removeStaleTempKeystores deletes temp keystores left behind by writers that
// died between CreateTemp and rename (for an unencrypted keystore such a file
// holds the plaintext nsec).
//
// It MUST only be called from saveKeyStoreLocked, i.e. while holding the
// keystore flock. Removal is safe because every code path that creates a
// keystore temp file does so inside withKeyStoreLock and finishes (rename or
// remove) before releasing it; flock is released by the kernel when a process
// dies. So while we hold the lock no live writer can own a matching temp file,
// and every match written by this code is an orphan; staleTempMinAge covers
// writers that predate or bypass the lock. Only plain regular files with the exact
// generated name directly inside the keystore dir are removed: symlinks,
// directories and anything else are left untouched, and nothing is followed.
// Errors are ignored: cleanup is best effort and must not fail the real write.
func removeStaleTempKeystores(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !tempKeystoreName.MatchString(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleTempMinAge {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// syncDir fsyncs the directory so the preceding rename survives power loss.
// It is best effort: by the time it runs the rename has already replaced the
// keystore, so reporting failure would tell the caller the write failed when
// the new content is in place (and ks.StoreVersion would go stale). Some
// filesystems reject fsync on a directory (EINVAL); we do not treat that as
// an error either.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
