package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// These tests drive the real binary against an isolated HOME for the offline
// commands that had no Go-level coverage (they were only touched by shell
// scripts that are not run in CI): identity export, storage audit-log/migrate
// and the legacy local `group` roster commands.

func surfaceCLI(t *testing.T, home string, args ...string) storageInfoCLIResult {
	t.Helper()
	return runStorageInfoCLI(t, home, nil, args...)
}

func mustSurfaceCLI(t *testing.T, home string, args ...string) string {
	t.Helper()
	got := surfaceCLI(t, home, args...)
	require.Equal(t, 0, got.code, "hyphae %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), got.stdout, got.stderr)
	return got.stdout
}

func TestIdentityExportPrintsKeyPairAndRejectsUnknownNickname(t *testing.T) {
	home := t.TempDir()
	created := mustSurfaceCLI(t, home, "--json", "identity", "create", "--nickname", "alice")
	require.Contains(t, created, `"nickname":"alice"`)

	out := mustSurfaceCLI(t, home, "identity", "export", "--nickname", "alice")
	assert.Contains(t, out, "Identity: alice")
	assert.Contains(t, out, "Npub:     npub1")
	assert.Contains(t, out, "Nsec:     nsec1")
	// the npub printed by export is the one create reported
	npub := out[strings.Index(out, "npub1"):]
	npub = strings.Fields(npub)[0]
	assert.Contains(t, created, npub)

	missing := surfaceCLI(t, home, "identity", "export", "--nickname", "nobody")
	assert.NotEqual(t, 0, missing.code)
	assert.NotContains(t, missing.stdout, "nsec1", "an unknown identity must not leak any key material")
}

func TestStorageAuditLogListsNewestFirstAndVerifiesChain(t *testing.T) {
	home := t.TempDir()
	mustSurfaceCLI(t, home, "identity", "create", "--nickname", "alice")
	mustSurfaceCLI(t, home, "identity", "create", "--nickname", "bob")

	all := mustSurfaceCLI(t, home, "storage", "audit-log")
	assert.Contains(t, all, "Audit Log (2 entries)")
	assert.Less(t, strings.Index(all, "actor=bob"), strings.Index(all, "actor=alice"), "most recent entry first")

	limited := mustSurfaceCLI(t, home, "storage", "audit-log", "--limit", "1")
	assert.Contains(t, limited, "Audit Log (1 entries)")
	assert.Contains(t, limited, "actor=bob")
	assert.NotContains(t, limited, "actor=alice")

	assert.Contains(t, mustSurfaceCLI(t, home, "storage", "audit-log", "--verify"), "Audit chain intact")
}

func TestStorageAuditLogVerifyFailsAfterTampering(t *testing.T) {
	home := t.TempDir()
	for _, n := range []string{"alice", "bob", "carol"} {
		mustSurfaceCLI(t, home, "identity", "create", "--nickname", n)
	}

	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE audit_log SET details = '{"tampered":true}' WHERE seq = 2`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	got := surfaceCLI(t, home, "storage", "audit-log", "--verify")
	assert.NotEqual(t, 0, got.code, "a broken chain must not look like success\nstdout: %s", got.stdout)
	assert.NotContains(t, got.stdout, "Audit chain intact")
}

func TestStorageMigrateIsIdempotent(t *testing.T) {
	home := t.TempDir()
	mustSurfaceCLI(t, home, "identity", "create", "--nickname", "alice")
	for i := 0; i < 2; i++ {
		assert.Contains(t, mustSurfaceCLI(t, home, "storage", "migrate"), "Migration complete")
	}
	// migrating twice must not lose or duplicate the audit trail
	assert.Contains(t, mustSurfaceCLI(t, home, "storage", "audit-log"), "Audit Log (1 entries)")
}

func TestLegacyGroupRosterLifecycle(t *testing.T) {
	home := t.TempDir()
	for _, n := range []string{"alice", "bob", "carol"} {
		mustSurfaceCLI(t, home, "identity", "create", "--nickname", n)
	}

	assert.Contains(t, mustSurfaceCLI(t, home, "group", "create", "--name", "team", "--members", "bob"), "Members: 2")
	assert.Contains(t, mustSurfaceCLI(t, home, "group", "list"), "team  2")

	// add-member is idempotent: adding the same nickname twice keeps 3 members
	for i := 0; i < 2; i++ {
		mustSurfaceCLI(t, home, "group", "add-member", "--name", "team", "--user", "carol")
	}
	assert.Contains(t, mustSurfaceCLI(t, home, "group", "list"), "team  3")

	unknown := surfaceCLI(t, home, "group", "add-member", "--name", "team", "--user", "nobody")
	assert.NotEqual(t, 0, unknown.code)
	assert.Contains(t, mustSurfaceCLI(t, home, "group", "list"), "team  3", "a failed add must not change the roster")

	// remove-member and delete are not implemented: they must say so with a
	// failing exit code and leave the roster alone, never report success.
	for _, args := range [][]string{
		{"group", "remove-member", "--name", "team", "--user", "carol"},
		{"group", "delete", "--name", "team", "--force"},
	} {
		got := surfaceCLI(t, home, args...)
		assert.NotEqual(t, 0, got.code, "hyphae %s must not exit 0", strings.Join(args, " "))
		assert.Empty(t, got.stdout)
		assert.Contains(t, got.stderr, "not implemented")
	}
	assert.Contains(t, mustSurfaceCLI(t, home, "group", "list"), "team  3")

	assert.Contains(t, mustSurfaceCLI(t, home, "group", "leave", "--name", "team"), "You left group 'team'")
	assert.Contains(t, mustSurfaceCLI(t, home, "group", "list"), "No groups found")
}
