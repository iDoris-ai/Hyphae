package deployment_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

// PREDICATE ONLY — NOT V7 PROOF:
// These tests verify pure data-structure validation predicates ONLY.
// They explicitly do NOT verify OS-level process retirement or CLI preAccess gates.

const (
	testValidSHA256A = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testValidSHA256B = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	testValidDigest  = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
)

func validManifestJSON() string {
	return `{
		"schema_version": "1",
		"state": "active",
		"canonical_home": "/canonical/home",
		"control_owner_uid": 1000,
		"expected_modes": {
			"dir_mode": 448,
			"file_mode": 384
		},
		"stores": {
			"outbox_json": "/canonical/home/.hyphae/outbox.json",
			"outbox_lock": "/canonical/home/.hyphae/outbox.json.lock",
			"outbox_temp_prefix": "/canonical/home/.hyphae/.outbox-",
			"messages_db": "/canonical/home/.hyphae/messages.db",
			"messages_db_wal": "/canonical/home/.hyphae/messages.db-wal",
			"messages_db_shm": "/canonical/home/.hyphae/messages.db-shm",
			"daemon_lock": "/canonical/home/.hyphae/daemon.lock"
		},
		"binary_path": "/canonical/bin/hyphae",
		"binary_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"launcher_path": "/canonical/bin/hyphae-managed",
		"launcher_sha256": "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		"allowed_entries": {
			"dm": {"id": "dm", "command_class": "dm"},
			"group-store": {"id": "group-store", "command_class": "group-store"},
			"group-write": {"id": "group-write", "command_class": "group-write"},
			"maintenance": {"id": "maintenance", "command_class": "maintenance"},
			"probe": {"id": "probe", "command_class": "probe"}
		},
		"inventory_digest": "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	}`
}

func validObservation() deployment.Observation {
	return deployment.Observation{
		CanonicalHome:    "/canonical/home",
		ControlOwnerUID:  1000,
		ObservedDirMode:  0700,
		ObservedFileMode: 0600,
		BinaryPath:       "/canonical/bin/hyphae",
		BinarySHA256:     testValidSHA256A,
		LauncherPath:     "/canonical/bin/hyphae-managed",
		LauncherSHA256:   testValidSHA256B,
		EntryID:          "dm",
		SessionMode:      deployment.SessionModeRuntime,
		InventoryDigest:  testValidDigest,
	}
}

func TestParseManifestJSON_MalformedAndStrictRejection(t *testing.T) {
	tests := []struct {
		name      string
		rawJSON   string
		errSubstr string
	}{
		{
			name:      "unknown field rejection",
			rawJSON:   strings.Replace(validManifestJSON(), `"state": "active",`, `"state": "active", "unexpected_field": true,`, 1),
			errSubstr: "unknown field",
		},
		{
			name:      "duplicate key rejection",
			rawJSON:   strings.Replace(validManifestJSON(), `"state": "active",`, `"state": "active", "state": "active",`, 1),
			errSubstr: "duplicate key",
		},
		{
			name:      "required null rejection canonical_home",
			rawJSON:   strings.Replace(validManifestJSON(), `"canonical_home": "/canonical/home",`, `"canonical_home": null,`, 1),
			errSubstr: "null value for key \"canonical_home\" rejected",
		},
		{
			name:      "required null rejection nested store",
			rawJSON:   strings.Replace(validManifestJSON(), `"outbox_json": "/canonical/home/.hyphae/outbox.json",`, `"outbox_json": null,`, 1),
			errSubstr: "null value for key \"outbox_json\" rejected",
		},
		{
			name:      "unsupported schema version",
			rawJSON:   strings.Replace(validManifestJSON(), `"schema_version": "1"`, `"schema_version": "2"`, 1),
			errSubstr: "unsupported",
		},
		{
			name:      "missing binary_path",
			rawJSON:   strings.Replace(validManifestJSON(), `"binary_path": "/canonical/bin/hyphae",`, `"binary_path": "",`, 1),
			errSubstr: "missing required",
		},
		{
			name:      "empty allowed_entries",
			rawJSON:   func() string {
				raw := validManifestJSON()
				start := strings.Index(raw, `"allowed_entries": {`)
				end := strings.Index(raw[start:], "},\n\t\t\"inventory_digest\"") + start + 1
				return raw[:start] + `"allowed_entries": {}` + raw[end:]
			}(),
			errSubstr: "allowed_entries must not be empty",
		},
		{
			name:      "syntax error",
			rawJSON:   `{ broken json `,
			errSubstr: "MALFORMED_MANIFEST",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := deployment.ParseManifestJSON([]byte(tc.rawJSON))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil manifest: %+v", tc.errSubstr, m)
			}
			closedErr, ok := err.(*deployment.DeploymentClosedError)
			if !ok {
				t.Fatalf("expected *DeploymentClosedError, got %T: %v", err, err)
			}
			if closedErr.ExitCode() != 4 {
				t.Fatalf("expected ExitCode 4, got %d", closedErr.ExitCode())
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
			}
		})
	}
}

func TestValidate_PositiveAndClosedCases(t *testing.T) {
	manifest, err := deployment.ParseManifestJSON([]byte(validManifestJSON()))
	if err != nil {
		t.Fatalf("failed to parse base valid manifest: %v", err)
	}

	t.Run("complete observed positive runtime", func(t *testing.T) {
		obs := validObservation()
		if err := deployment.Validate(manifest, &obs); err != nil {
			t.Fatalf("expected valid validation, got: %v", err)
		}
	})

	t.Run("complete observed positive maintenance", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "maintenance"
		obs.SessionMode = deployment.SessionModeMaintenance
		if err := deployment.Validate(manifest, &obs); err != nil {
			t.Fatalf("expected valid maintenance validation, got: %v", err)
		}
	})

	t.Run("complete observed positive probe", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "probe"
		obs.SessionMode = deployment.SessionModeProbe
		if err := deployment.Validate(manifest, &obs); err != nil {
			t.Fatalf("expected valid probe validation, got: %v", err)
		}
	})

	t.Run("nil manifest or observation", func(t *testing.T) {
		if err := deployment.Validate(nil, nil); err == nil {
			t.Fatal("expected error on nil inputs")
		}
	})

	t.Run("manifest state closed", func(t *testing.T) {
		m := *manifest
		m.State = "closed"
		obs := validObservation()
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "DEPLOYMENT_CLOSED")
	})

	t.Run("unsafe canonical home with dotdot", func(t *testing.T) {
		m := *manifest
		m.CanonicalHome = "/canonical/home/../escaped"
		obs := validObservation()
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "PATH_UNSAFE")
	})

	t.Run("relative binary path", func(t *testing.T) {
		m := *manifest
		m.BinaryPath = "bin/hyphae"
		obs := validObservation()
		obs.BinaryPath = "bin/hyphae"
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "PATH_UNSAFE")
	})

	t.Run("store path escapes canonical home", func(t *testing.T) {
		m := *manifest
		m.Stores.MessagesDB = "/foreign/messages.db"
		obs := validObservation()
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "PATH_ESCAPED")
	})

	t.Run("escaped outbox temp prefix", func(t *testing.T) {
		m := *manifest
		m.Stores.OutboxTempPrefix = "/escaped/.outbox-"
		obs := validObservation()
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "PATH_ESCAPED")
	})

	t.Run("foreign home mismatch", func(t *testing.T) {
		obs := validObservation()
		obs.CanonicalHome = "/other/home"
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "FOREIGN_HOME")
	})

	t.Run("wrong owner UID", func(t *testing.T) {
		obs := validObservation()
		obs.ControlOwnerUID = 1001
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "WRONG_OWNER")
	})

	t.Run("insecure modes group write", func(t *testing.T) {
		obs := validObservation()
		obs.ObservedDirMode = 0770 // group writable
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "WRONG_MODE")
	})

	t.Run("binary hash mismatch", func(t *testing.T) {
		obs := validObservation()
		obs.BinarySHA256 = testValidSHA256B
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "HASH_MISMATCH")
	})

	t.Run("invalid sha256 hex format", func(t *testing.T) {
		m := *manifest
		m.BinarySHA256 = "not-a-valid-hex-length-and-string"
		obs := validObservation()
		obs.BinarySHA256 = m.BinarySHA256
		err := deployment.Validate(&m, &obs)
		assertErrorCode(t, err, "HASH_MISMATCH")
	})

	t.Run("launcher path mismatch", func(t *testing.T) {
		obs := validObservation()
		obs.LauncherPath = "/different/launcher"
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "HASH_MISMATCH")
	})

	t.Run("inventory digest drift", func(t *testing.T) {
		obs := validObservation()
		obs.InventoryDigest = "0000000000000000000000000000000000000000000000000000000000000000"
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "DRIFT")
	})

	t.Run("unknown entry ID", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "unregistered_worker"
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "UNKNOWN_ENTRY")
	})

	t.Run("unknown session mode", func(t *testing.T) {
		obs := validObservation()
		obs.SessionMode = "bogus_mode"
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "INVALID_SESSION_MODE")
	})

	t.Run("runtime mode denies maintenance entry", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "maintenance"
		obs.SessionMode = deployment.SessionModeRuntime
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "MODE_MISMATCH")
	})

	t.Run("maintenance mode denies non-maintenance entry (no blanket bypass)", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "dm"
		obs.SessionMode = deployment.SessionModeMaintenance
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "MODE_MISMATCH")
	})

	t.Run("probe mode denies non-probe entry", func(t *testing.T) {
		obs := validObservation()
		obs.EntryID = "dm"
		obs.SessionMode = deployment.SessionModeProbe
		err := deployment.Validate(manifest, &obs)
		assertErrorCode(t, err, "MODE_MISMATCH")
	})
}

// TestAuthorIndependentRegressionCases tests all 6 blocker regressions
// identified by Codex review of commit 9230cce.
func TestAuthorIndependentRegressionCases(t *testing.T) {
	t.Run("Blocker 1: trailing JSON rejected", func(t *testing.T) {
		for _, trailing := range []string{" {}", " true", " 123", ` "string"`} {
			_, err := deployment.ParseManifestJSON([]byte(validManifestJSON() + trailing))
			if err == nil {
				t.Fatalf("expected error on trailing data %q, got nil", trailing)
			}
		}
	})

	t.Run("Blocker 2: required fields presence and explicit root UID 0 allowed", func(t *testing.T) {
		mJSON := strings.Replace(validManifestJSON(), `"control_owner_uid": 1000,`, `"control_owner_uid": 0,`, 1)
		m, err := deployment.ParseManifestJSON([]byte(mJSON))
		if err != nil {
			t.Fatalf("explicit root UID 0 rejected: %v", err)
		}
		obs := validObservation()
		obs.ControlOwnerUID = 0
		if err := deployment.Validate(m, &obs); err != nil {
			t.Fatalf("explicit root UID 0 validation rejected: %v", err)
		}

		// Missing numeric UID
		badJSON := strings.Replace(validManifestJSON(), `"control_owner_uid": 1000,`, "", 1)
		if _, err := deployment.ParseManifestJSON([]byte(badJSON)); err == nil {
			t.Fatal("expected error on missing control_owner_uid")
		}

		// Missing expected modes
		badJSON = strings.Replace(validManifestJSON(), "\"expected_modes\": {\n\t\t\t\"dir_mode\": 448,\n\t\t\t\"file_mode\": 384\n\t\t},", "", 1)
		if _, err := deployment.ParseManifestJSON([]byte(badJSON)); err == nil {
			t.Fatal("expected error on missing expected_modes")
		}
	})

	t.Run("Blocker 3: complete known vocabulary and all good classes", func(t *testing.T) {
		m, err := deployment.ParseManifestJSON([]byte(validManifestJSON()))
		if err != nil {
			t.Fatalf("base manifest failed: %v", err)
		}
		goodClasses := []struct {
			entry string
			mode  deployment.SessionMode
		}{
			{"dm", deployment.SessionModeRuntime},
			{"group-store", deployment.SessionModeRuntime},
			{"group-write", deployment.SessionModeRuntime},
			{"maintenance", deployment.SessionModeMaintenance},
			{"probe", deployment.SessionModeProbe},
		}
		for _, tc := range goodClasses {
			obs := validObservation()
			obs.EntryID = tc.entry
			obs.SessionMode = tc.mode
			if err := deployment.Validate(m, &obs); err != nil {
				t.Errorf("class %s mode %s rejected: %v", tc.entry, tc.mode, err)
			}
		}

		// Unknown class
		mUnknown := *m
		mUnknown.AllowedEntries = map[string]deployment.EntryDef{
			"dm":          {ID: "dm", CommandClass: "unauthorized_class"},
			"group-store": {ID: "group-store", CommandClass: "group-store"},
			"group-write": {ID: "group-write", CommandClass: "group-write"},
			"maintenance": {ID: "maintenance", CommandClass: "maintenance"},
			"probe":       {ID: "probe", CommandClass: "probe"},
		}
		obs := validObservation()
		if err := deployment.Validate(&mUnknown, &obs); err == nil {
			t.Fatal("expected error on unknown command class")
		}

		// Mismatched embedded ID
		mMismatch := *m
		mMismatch.AllowedEntries = map[string]deployment.EntryDef{
			"dm":          {ID: "other_id", CommandClass: "dm"},
			"group-store": {ID: "group-store", CommandClass: "group-store"},
			"group-write": {ID: "group-write", CommandClass: "group-write"},
			"maintenance": {ID: "maintenance", CommandClass: "maintenance"},
			"probe":       {ID: "probe", CommandClass: "probe"},
		}
		if err := deployment.Validate(&mMismatch, &obs); err == nil {
			t.Fatal("expected error on mismatched entry ID")
		}
	})

	t.Run("Blocker 4: owner-only modes non-zero", func(t *testing.T) {
		m, _ := deployment.ParseManifestJSON([]byte(validManifestJSON()))
		obs := validObservation()

		// 0755 non-owner readable directory
		m755 := *m
		m755.ExpectedModes.DirMode = 0755
		obs755 := obs
		obs755.ObservedDirMode = 0755
		if err := deployment.Validate(&m755, &obs755); err == nil {
			t.Fatal("expected error on non-owner readable mode 0755")
		}

		// zero expected mode
		mZero := *m
		mZero.ExpectedModes = deployment.ModeConfig{}
		if err := deployment.Validate(&mZero, &obs); err == nil {
			t.Fatal("expected error on zero expected modes")
		}

		// legitimate restrictive owner-only mode
		m500 := *m
		m500.ExpectedModes.DirMode = 0500
		obs500 := obs
		obs500.ObservedDirMode = 0500
		if err := deployment.Validate(&m500, &obs500); err != nil {
			t.Fatalf("expected success on 0500 owner mode: %v", err)
		}
	})

	t.Run("Blocker 5: canonical absolute temp prefix and legitimate dotdot filename", func(t *testing.T) {
		m, _ := deployment.ParseManifestJSON([]byte(validManifestJSON()))
		obs := validObservation()

		// escaped temp parent
		mEsc := *m
		mEsc.Stores.OutboxTempPrefix = "/canonical/home/.hyphae/../escape"
		if err := deployment.Validate(&mEsc, &obs); err == nil {
			t.Fatal("expected error on escaped temp prefix")
		}

		// double slash
		mDouble := *m
		mDouble.Stores.OutboxTempPrefix = "/canonical/home/.hyphae//tmp"
		if err := deployment.Validate(&mDouble, &obs); err == nil {
			t.Fatal("expected error on double slash temp prefix")
		}

		// legitimate dotdot in filename
		mDot := *m
		mDot.BinaryPath = "/canonical/bin/hyphae..backup"
		obsDot := obs
		obsDot.BinaryPath = mDot.BinaryPath
		if err := deployment.Validate(&mDot, &obsDot); err != nil {
			t.Fatalf("legitimate dotdot filename rejected: %v", err)
		}
	})

	t.Run("Blocker 6: direct Validate invariants", func(t *testing.T) {
		m, _ := deployment.ParseManifestJSON([]byte(validManifestJSON()))
		obs := validObservation()

		// Unsupported schema version
		mBadVer := *m
		mBadVer.SchemaVersion = "2"
		if err := deployment.Validate(&mBadVer, &obs); err == nil {
			t.Fatal("expected error on unsupported schema version via direct Validate")
		}

		// Empty inventory digest
		mEmptyDigest := *m
		mEmptyDigest.InventoryDigest = ""
		obsEmpty := obs
		obsEmpty.InventoryDigest = ""
		if err := deployment.Validate(&mEmptyDigest, &obsEmpty); err == nil {
			t.Fatal("expected error on empty inventory digest via direct Validate")
		}
	})
}

// TestSentinelStoresBytesUnchanged_PurePredicateOnly verifies that pure validation
// does not mutate any sentinel bytes.
func TestSentinelStoresBytesUnchanged_PurePredicateOnly(t *testing.T) {
	sentinelOutbox := []byte(`{"version":1,"entries":[{"id":"sentinel-1"}]}`)
	sentinelDB := []byte("SQLITE_FORMAT_3_SENTINEL_BYTES_PRESERVED")

	origOutbox := append([]byte(nil), sentinelOutbox...)
	origDB := append([]byte(nil), sentinelDB...)

	manifest, err := deployment.ParseManifestJSON([]byte(validManifestJSON()))
	if err != nil {
		t.Fatalf("parse manifest failed: %v", err)
	}

	obs := validObservation()
	if err := deployment.Validate(manifest, &obs); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if !bytes.Equal(sentinelOutbox, origOutbox) {
		t.Fatal("sentinel outbox bytes mutated during pure predicate validation")
	}
	if !bytes.Equal(sentinelDB, origDB) {
		t.Fatal("sentinel database bytes mutated during pure predicate validation")
	}
}

func TestBaselineAPIUnavailableScopedNegative(t *testing.T) {
	manifest, err := deployment.ParseManifestJSON([]byte(validManifestJSON()))
	if err != nil {
		t.Fatalf("manifest parse failed: %v", err)
	}
	obs := validObservation()
	if err := deployment.Validate(manifest, &obs); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func assertErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", wantCode)
	}
	closedErr, ok := err.(*deployment.DeploymentClosedError)
	if !ok {
		t.Fatalf("expected *deployment.DeploymentClosedError, got %T: %v", err, err)
	}
	if closedErr.Code != wantCode {
		t.Fatalf("expected code %q, got %q (err: %v)", wantCode, closedErr.Code, closedErr)
	}
	if closedErr.ExitCode() != 4 {
		t.Fatalf("expected exit code 4, got %d", closedErr.ExitCode())
	}
}
