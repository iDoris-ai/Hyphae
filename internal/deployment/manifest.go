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

const SupportedSchemaVersion = "1"

type SessionMode string

const (
	SessionModeRuntime     SessionMode = "runtime"
	SessionModeMaintenance SessionMode = "maintenance"
	SessionModeProbe       SessionMode = "probe"
)

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
		CanonicalHome    string
		ControlOwnerUID  int
		ObservedDirMode  uint32
		ObservedFileMode uint32
		BinaryPath       string
		BinarySHA256     string
		LauncherPath     string
		LauncherSHA256   string
		EntryID          string
		SessionMode      SessionMode
		InventoryDigest  string
	}
	DeploymentClosedError struct {
		Code   string
		Reason string
	}
)

func (e *DeploymentClosedError) Error() string {
	return fmt.Sprintf("deployment closed (%s): %s", e.Code, e.Reason)
}
func (e *DeploymentClosedError) ExitCode() int { return 4 }

func ParseManifestJSON(data []byte) (*Manifest, error) {
	if err := scanJSONStrict(data); err != nil {
		return nil, &DeploymentClosedError{Code: "MALFORMED_MANIFEST", Reason: err.Error()}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, &DeploymentClosedError{Code: "MALFORMED_MANIFEST", Reason: err.Error()}
	}
	if m.SchemaVersion != SupportedSchemaVersion {
		return nil, &DeploymentClosedError{Code: "UNSUPPORTED_VERSION", Reason: fmt.Sprintf("version %q unsupported", m.SchemaVersion)}
	}
	s := m.Stores
	for _, req := range []string{m.State, m.CanonicalHome, m.BinaryPath, m.BinarySHA256, m.LauncherPath, m.LauncherSHA256, m.InventoryDigest, s.OutboxJSON, s.OutboxLock, s.OutboxTempPrefix, s.MessagesDB, s.MessagesDBWAL, s.MessagesDBSHM, s.DaemonLock} {
		if req == "" {
			return nil, &DeploymentClosedError{Code: "MALFORMED_MANIFEST", Reason: "missing required manifest/store field"}
		}
	}
	if len(m.AllowedEntries) == 0 {
		return nil, &DeploymentClosedError{Code: "MALFORMED_MANIFEST", Reason: "allowed_entries must not be empty"}
	}
	return &m, nil
}

type jsonScope struct {
	isObj, inVal bool
	seen         map[string]bool
	key          string
}

func scanJSONStrict(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var stack []jsonScope
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, jsonScope{isObj: true, seen: make(map[string]bool)})
			case '[':
				stack = append(stack, jsonScope{isObj: false})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("unexpected delimiter %q", t)
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObj {
					stack[len(stack)-1].inVal = false
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
				} else {
					if t == nil {
						return fmt.Errorf("null value for key %q rejected", top.key)
					}
					top.inVal = false
				}
			}
		}
	}
}
