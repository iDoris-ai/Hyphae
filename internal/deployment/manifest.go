// Package deployment provides pure passive deployment manifest validation.
//
// PREDICATE ONLY — NOT V7 PROOF:
// Validation of manifest and observation tuples is a necessary condition,
// but explicitly NOT proof of V7 closure or OS process retirement.
package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type SessionMode string

const (
	SupportedSchemaVersion             = "1"
	SessionModeRuntime     SessionMode = "runtime"
	SessionModeMaintenance SessionMode = "maintenance"
	SessionModeProbe       SessionMode = "probe"
)

var RequiredClasses = []string{"dm", "group-store", "group-write", "maintenance", "probe"}

type (
	StorePaths struct {
		OutboxJSON       string `json:"outbox_json"`
		OutboxLock       string `json:"outbox_lock"`
		OutboxTempPrefix string `json:"outbox_temp_prefix"`
		MessagesDB       string `json:"messages_db"`
		MessagesDBWAL    string `json:"messages_db_wal"`
		MessagesDBSHM    string `json:"messages_db_shm"`
		DaemonLock       string `json:"daemon_lock"`
	}
	EntryDef struct {
		ID           string `json:"id"`
		CommandClass string `json:"command_class"`
	}
	ModeConfig struct {
		DirMode  uint32 `json:"dir_mode"`
		FileMode uint32 `json:"file_mode"`
	}
	Manifest struct {
		SchemaVersion   string              `json:"schema_version"`
		State           string              `json:"state"`
		CanonicalHome   string              `json:"canonical_home"`
		ControlOwnerUID int                 `json:"control_owner_uid"`
		ExpectedModes   ModeConfig          `json:"expected_modes"`
		Stores          StorePaths          `json:"stores"`
		BinaryPath      string              `json:"binary_path"`
		BinarySHA256    string              `json:"binary_sha256"`
		LauncherPath    string              `json:"launcher_path"`
		LauncherSHA256  string              `json:"launcher_sha256"`
		AllowedEntries  map[string]EntryDef `json:"allowed_entries"`
		InventoryDigest string              `json:"inventory_digest"`
	}
	Observation struct {
		CanonicalHome, BinaryPath, BinarySHA256 string
		LauncherPath, LauncherSHA256, EntryID   string
		InventoryDigest                         string
		ControlOwnerUID                         int
		ObservedDirMode, ObservedFileMode       uint32
		SessionMode                             SessionMode
	}
	DeploymentClosedError struct {
		Code, Reason string
	}
)

func (e *DeploymentClosedError) Error() string {
	return fmt.Sprintf("deployment closed (%s): %s", e.Code, e.Reason)
}
func (e *DeploymentClosedError) ExitCode() int { return 4 }

func closedErr(code, reason string) *DeploymentClosedError {
	return &DeploymentClosedError{Code: code, Reason: reason}
}

func (s *StorePaths) all() []string {
	return []string{s.OutboxJSON, s.OutboxLock, s.OutboxTempPrefix, s.MessagesDB, s.MessagesDBWAL, s.MessagesDBSHM, s.DaemonLock}
}

func ParseManifestJSON(data []byte) (*Manifest, error) {
	if err := scanJSONStrict(data); err != nil {
		return nil, closedErr("MALFORMED_MANIFEST", err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, closedErr("MALFORMED_MANIFEST", err.Error())
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, closedErr("MALFORMED_MANIFEST", "unexpected trailing data")
	}
	if m.SchemaVersion != SupportedSchemaVersion {
		return nil, closedErr("UNSUPPORTED_VERSION", fmt.Sprintf("version %q unsupported", m.SchemaVersion))
	}
	for _, req := range append(m.Stores.all(), m.State, m.CanonicalHome, m.BinaryPath, m.BinarySHA256, m.LauncherPath, m.LauncherSHA256, m.InventoryDigest) {
		if req == "" {
			return nil, closedErr("MALFORMED_MANIFEST", "missing required manifest/store field")
		}
	}
	return &m, nil
}

type jsonScope struct {
	isObj, inVal bool
	seen         map[string]bool
	key, kind    string
}

func scanJSONStrict(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var stack []jsonScope
	var docs int
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if len(stack) == 0 {
			if docs++; docs > 1 {
				return fmt.Errorf("unexpected trailing data after JSON document")
			}
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, jsonScope{isObj: true, seen: make(map[string]bool), kind: childKind(stack)})
			case '[':
				stack = append(stack, jsonScope{kind: "other"})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("unexpected delimiter %q", t)
				}
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stack[len(stack)-1].inVal = false
				}
				if t == '}' {
					if err := checkKeys(top.kind, top.seen); err != nil {
						return err
					}
				}
			}
		default:
			if len(stack) > 0 && stack[len(stack)-1].isObj {
				top := &stack[len(stack)-1]
				if !top.inVal {
					k, ok := t.(string)
					if !ok {
						return fmt.Errorf("expected string key, got %T", t)
					}
					if top.seen[k] {
						return fmt.Errorf("duplicate key: %s", k)
					}
					top.seen[k], top.inVal, top.key = true, true, k
					continue
				}
				if t == nil {
					return fmt.Errorf("null value for key %q rejected", top.key)
				}
				top.inVal = false
			}
		}
	}
	if docs != 1 || len(stack) != 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// scopeSchema is keyed by nesting path/type, not by key name, so an entry
// named e.g. "stores" is never mistaken for the root stores object.
var scopeSchema = map[string][]string{
	"root":   {"schema_version", "state", "canonical_home", "control_owner_uid", "expected_modes", "stores", "binary_path", "binary_sha256", "launcher_path", "launcher_sha256", "allowed_entries", "inventory_digest"},
	"modes":  {"dir_mode", "file_mode"},
	"stores": {"outbox_json", "outbox_lock", "outbox_temp_prefix", "messages_db", "messages_db_wal", "messages_db_shm", "daemon_lock"},
	"entry":  {"id", "command_class"},
}

func childKind(stack []jsonScope) string {
	if len(stack) == 0 {
		return "root"
	}
	switch p := stack[len(stack)-1]; {
	case p.kind == "entries":
		return "entry"
	case p.kind != "root":
		return "other"
	case p.key == "expected_modes":
		return "modes"
	case p.key == "stores":
		return "stores"
	case p.key == "allowed_entries":
		return "entries"
	}
	return "other"
}

func checkKeys(kind string, seen map[string]bool) error {
	if kind == "entries" {
		if len(seen) == 0 {
			return fmt.Errorf("allowed_entries must not be empty")
		}
		return nil
	}
	req, ok := scopeSchema[kind]
	if !ok {
		return nil
	}
	allowed := make(map[string]bool, len(req))
	for _, k := range req {
		allowed[k] = true
		if !seen[k] {
			return fmt.Errorf("missing required key %q", k)
		}
	}
	for k := range seen {
		if !allowed[k] {
			return fmt.Errorf("unknown field %q", k)
		}
	}
	return nil
}
