package deployment_test

import (
	"encoding/json"
	"github.com/iDoris-ai/hyphae/internal/deployment"
	"strings"
	"testing"
)

// Entry IDs name launch surfaces; their command_class fields name capabilities.
func TestHeldoutDistinctEntryIDsAndClasses(t *testing.T) {
	v := fresh()
	v["allowed_entries"] = map[string]any{
		"send":   map[string]any{"id": "send", "command_class": "dm"},
		"daemon": map[string]any{"id": "daemon", "command_class": "group-store"},
		"tui":    map[string]any{"id": "tui", "command_class": "group-write"},
		"owner":  map[string]any{"id": "owner", "command_class": "maintenance"},
		"health": map[string]any{"id": "health", "command_class": "probe"},
	}
	m, err := deployment.ParseManifestJSON(raw(v))
	if err != nil {
		t.Fatalf("valid entry ID/class table rejected: %v", err)
	}
	_, obs := good(t)
	for _, row := range []struct {
		id   string
		mode deployment.SessionMode
	}{
		{"send", deployment.SessionModeRuntime}, {"daemon", deployment.SessionModeRuntime},
		{"tui", deployment.SessionModeRuntime}, {"owner", deployment.SessionModeMaintenance},
		{"health", deployment.SessionModeProbe},
	} {
		obs.EntryID, obs.SessionMode = row.id, row.mode
		if err := deployment.Validate(m, &obs); err != nil {
			t.Errorf("entry %q rejected: %v", row.id, err)
		}
	}
}

func TestHeldoutDistinctDirectManifest(t *testing.T) {
	m, obs := good(t)
	m.AllowedEntries = map[string]deployment.EntryDef{
		"send":   {ID: "send", CommandClass: "dm"},
		"daemon": {ID: "daemon", CommandClass: "group-store"},
		"tui":    {ID: "tui", CommandClass: "group-write"},
		"owner":  {ID: "owner", CommandClass: "maintenance"},
		"health": {ID: "health", CommandClass: "probe"},
	}
	obs.EntryID = "send"
	if err := deployment.Validate(m, &obs); err != nil {
		t.Errorf("valid direct entry ID/class table rejected: %v", err)
	}
}

func TestHeldoutCaseFoldedAliasRejected(t *testing.T) {
	s := string(raw(fresh()))
	s = strings.Replace(s, `"state":"active"`, `"state":"inactive","State":"active"`, 1)
	m, err := deployment.ParseManifestJSON([]byte(s))
	if err == nil {
		_, obs := good(t)
		if deployment.Validate(m, &obs) == nil {
			t.Error("case-folded duplicate state accepted as active")
		}
		t.Error("case-folded duplicate state parsed")
	}
}

func TestHeldoutEscapedKeysWhitespaceAndUIDZero(t *testing.T) {
	v := fresh()
	v["control_owner_uid"] = 0
	s := string(raw(v))
	s = strings.Replace(s, `"state"`, `"sta\u0074e"`, 1)
	s = strings.Replace(s, `"id":"dm"`, `"\u0069d" : "dm"`, 1)
	s = " \n\t" + s + " \n"
	m, err := deployment.ParseManifestJSON([]byte(s))
	if err != nil {
		t.Fatalf("valid escaped keys/UID0 rejected: %v", err)
	}
	_, obs := good(t)
	obs.ControlOwnerUID = 0
	if err := deployment.Validate(m, &obs); err != nil {
		t.Fatalf("valid UID0 tuple rejected: %v", err)
	}
	delete(v, "control_owner_uid")
	if _, err := deployment.ParseManifestJSON(raw(v)); err == nil {
		t.Error("absent UID parsed")
	}
	_ = json.Valid
}

func TestHeldoutTempSiblingNamespace(t *testing.T) {
	m, obs := good(t)
	m.Stores.OutboxTempPrefix = h + "/other/.outbox-"
	if err := deployment.Validate(m, &obs); err == nil {
		t.Error("temp namespace outside outbox parent accepted")
	}
}

func TestHeldoutExactModeRoles(t *testing.T) {
	m, obs := good(t)
	if err := deployment.Validate(m, &obs); err != nil {
		t.Fatalf("0700 dir 0600 file rejected: %v", err)
	}
	m.ExpectedModes.DirMode = 0755
	obs.ObservedDirMode = 0755
	if err := deployment.Validate(m, &obs); err == nil {
		t.Error("0755 dir accepted")
	}
}

func TestHeldoutEntryIDNamedStores(t *testing.T) {
	v := fresh()
	entries := v["allowed_entries"].(map[string]any)
	entries["stores"] = map[string]any{"id": "stores", "command_class": "dm"}
	if _, err := deployment.ParseManifestJSON(raw(v)); err != nil {
		t.Errorf("valid entry ID 'stores' rejected by scope confusion: %v", err)
	}
}
