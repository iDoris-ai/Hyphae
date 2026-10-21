package deployment

import (
	"fmt"
	"os"
	"path/filepath"
)

// ManagedEntry is one registered surface and the store roles it names.
type ManagedEntry struct {
	ID           string
	CommandClass string
	Stores       []string
}

type entrySpec struct {
	id, class string
	roles     []string
}

// CLI, daemon, TUI, maintenance, and probe. Role names match StorePaths JSON.
var managedSpecs = []entrySpec{
	{id: "cli", class: "dm", roles: []string{"outbox_json", "outbox_lock", "outbox_temp_prefix"}},
	{id: "daemon", class: "group-store", roles: []string{"messages_db", "messages_db_wal", "messages_db_shm", "daemon_lock"}},
	{id: "tui", class: "group-write", roles: []string{"outbox_json", "outbox_lock", "outbox_temp_prefix", "messages_db", "messages_db_wal", "messages_db_shm"}},
	{id: "maintenance", class: "maintenance", roles: []string{
		"outbox_json", "outbox_lock", "outbox_temp_prefix",
		"messages_db", "messages_db_wal", "messages_db_shm", "daemon_lock",
	}},
	{id: "probe", class: "probe"},
}

var storeRoles = []string{
	"outbox_json", "outbox_lock", "outbox_temp_prefix",
	"messages_db", "messages_db_wal", "messages_db_shm", "daemon_lock",
}

// ManagedEntries returns the registered surfaces in inventory order.
func ManagedEntries() []ManagedEntry {
	out := make([]ManagedEntry, len(managedSpecs))
	for i, spec := range managedSpecs {
		out[i] = ManagedEntry{
			ID: spec.id, CommandClass: spec.class,
			Stores: append([]string(nil), spec.roles...),
		}
	}
	return out
}

// ManagedEntryInventory returns the entry map a managed manifest records.
func ManagedEntryInventory() map[string]EntryDef {
	out := make(map[string]EntryDef, len(managedSpecs))
	for _, spec := range managedSpecs {
		out[spec.id] = EntryDef{ID: spec.id, CommandClass: spec.class}
	}
	return out
}

// CanonicalStores returns the store paths bound under home.
func CanonicalStores(home string) (StorePaths, error) {
	if !isCleanAbs(home) {
		return StorePaths{}, closedErr("PATH_UNSAFE", fmt.Sprintf("unsafe path %q", home))
	}
	dir := filepath.Join(home, ".hyphae")
	return StorePaths{
		OutboxJSON:       filepath.Join(dir, "outbox.json"),
		OutboxLock:       filepath.Join(dir, "outbox.json.lock"),
		OutboxTempPrefix: filepath.Join(dir, ".outbox-"),
		MessagesDB:       filepath.Join(dir, "messages.db"),
		MessagesDBWAL:    filepath.Join(dir, "messages.db-wal"),
		MessagesDBSHM:    filepath.Join(dir, "messages.db-shm"),
		DaemonLock:       filepath.Join(dir, "daemon.lock"),
	}, nil
}

// PlanManagedEntry returns the pinned binary for a registered entry.
// An unknown entry returns before the manifest is read. Inventory and store
// checks run on the parsed snapshot that also supplies the pinned path.
func PlanManagedEntry(req LaunchRequest) (string, error) {
	if err := gateRegistered(req.EntryID, req.Session); err != nil {
		return "", err
	}
	var planned string
	err := launch(req, launchEnv{
		executable: os.Executable,
		execFn: func(path string, argv []string, _ []string) error {
			if !isCleanAbs(path) || len(argv) == 0 || argv[0] != path {
				return closedErr("PATH_UNSAFE", "refusing a non-pinned executable")
			}
			planned = path
			return nil
		},
		open: openNoFollowOS,
		onManifest: func(m *Manifest) error {
			if err := sameInventory(m.AllowedEntries); err != nil {
				return err
			}
			return storesMatchCanonical(m)
		},
	})
	if err != nil {
		return "", err
	}
	if planned == "" {
		return "", closedErr("DEPLOYMENT_CLOSED", "pinned binary missing")
	}
	return planned, nil
}

func gateRegistered(id string, mode SessionMode) error {
	for _, spec := range managedSpecs {
		if spec.id != id {
			continue
		}
		return checkSessionMode(mode, spec.class)
	}
	return closedErr("UNKNOWN_ENTRY", fmt.Sprintf("entry %q unknown", id))
}

func sameInventory(got map[string]EntryDef) error {
	want := ManagedEntryInventory()
	if len(got) != len(want) {
		return closedErr("DRIFT", "registered inventory mismatch")
	}
	for id, spec := range want {
		have, ok := got[id]
		if !ok || have != spec {
			return closedErr("DRIFT", "registered inventory mismatch")
		}
	}
	return nil
}

func storesMatchCanonical(m *Manifest) error {
	want, err := CanonicalStores(m.CanonicalHome)
	if err != nil {
		return err
	}
	covered := make(map[string]struct{}, len(storeRoles))
	for _, spec := range managedSpecs {
		for _, role := range spec.roles {
			got, ok := storeByRole(m.Stores, role)
			need, okNeed := storeByRole(want, role)
			if !ok || !okNeed || got == "" || got != need {
				return closedErr("PATH_ESCAPED", "store path is not canonical")
			}
			covered[role] = struct{}{}
		}
	}
	for _, role := range storeRoles {
		if _, ok := covered[role]; !ok {
			return closedErr("MALFORMED_MANIFEST", "store role unbound")
		}
	}
	return nil
}

func storeByRole(s StorePaths, role string) (string, bool) {
	switch role {
	case "outbox_json":
		return s.OutboxJSON, true
	case "outbox_lock":
		return s.OutboxLock, true
	case "outbox_temp_prefix":
		return s.OutboxTempPrefix, true
	case "messages_db":
		return s.MessagesDB, true
	case "messages_db_wal":
		return s.MessagesDBWAL, true
	case "messages_db_shm":
		return s.MessagesDBSHM, true
	case "daemon_lock":
		return s.DaemonLock, true
	default:
		return "", false
	}
}
