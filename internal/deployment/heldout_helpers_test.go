package deployment_test

import (
	"encoding/json"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

// h, fresh, raw, and good adapt the independent heldout overlay
// (/tmp/hyphae-v7-737-heldout_test.go) onto this package's own fixtures
// without altering any of its assertions.
const h = "/canonical/home"

func fresh() map[string]any {
	return map[string]any{
		"schema_version":    "1",
		"state":             "active",
		"canonical_home":    h,
		"control_owner_uid": 1000,
		"expected_modes": map[string]any{
			"dir_mode":  448,
			"file_mode": 384,
		},
		"stores": map[string]any{
			"outbox_json":        h + "/.hyphae/outbox.json",
			"outbox_lock":        h + "/.hyphae/outbox.json.lock",
			"outbox_temp_prefix": h + "/.hyphae/.outbox-",
			"messages_db":        h + "/.hyphae/messages.db",
			"messages_db_wal":    h + "/.hyphae/messages.db-wal",
			"messages_db_shm":    h + "/.hyphae/messages.db-shm",
			"daemon_lock":        h + "/.hyphae/daemon.lock",
		},
		"binary_path":     "/canonical/bin/hyphae",
		"binary_sha256":   testValidSHA256A,
		"launcher_path":   "/canonical/bin/hyphae-managed",
		"launcher_sha256": testValidSHA256B,
		"allowed_entries": map[string]any{
			"dm":          map[string]any{"id": "dm", "command_class": "dm"},
			"group-store": map[string]any{"id": "group-store", "command_class": "group-store"},
			"group-write": map[string]any{"id": "group-write", "command_class": "group-write"},
			"maintenance": map[string]any{"id": "maintenance", "command_class": "maintenance"},
			"probe":       map[string]any{"id": "probe", "command_class": "probe"},
		},
		"inventory_digest": testValidDigest,
	}
}

func raw(v map[string]any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func good(t *testing.T) (*deployment.Manifest, deployment.Observation) {
	t.Helper()
	m, err := deployment.ParseManifestJSON(raw(fresh()))
	if err != nil {
		t.Fatalf("fresh() fixture rejected: %v", err)
	}
	return m, validObservation()
}
