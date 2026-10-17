package deployment_test

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

// Structured metamorphic seeds for the pure deployment predicate.
//
// PREDICATE ONLY — NOT V7 PROOF. Each Observation is synthetic tuple data
// built in this process. Validate does not consult the filesystem, a live
// session, or a process table, and a matching digest or path is not OS proof.
//
// JSON presence and a typed Manifest are different contracts. Parse rejects a
// missing control_owner_uid key, but a direct Manifest whose UID field is 0
// is a legal explicit zero: the struct cannot prove the key was absent. An
// empty digest on a typed value is DRIFT, not a missing-key parse error.
// Fuzz []byte values only select mutations of the known-good tuple.

const (
	altDigest = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	cyrState  = "st\u0430te"
)

var classValues = []string{"dm", "group-store", "group-write", "maintenance", "probe"}

var structuralNames = []string{
	"stores", "expected_modes", "inventory_digest", "schema_version",
	"State", "allowed_entries", "outbox_json", "command_class",
}

type verdict struct {
	pass   bool
	code   string
	sub    string
	notSub string
	phase  string // "parse" or "validate"
}

type seed struct {
	name       string
	cat        string
	body       []byte
	directOnly bool
	obs        func(*deployment.Observation)
	man        func(*deployment.Manifest)
	v          verdict
}

func passV() verdict { return verdict{pass: true, phase: "validate"} }

func parseFail(code, sub string) verdict {
	return verdict{code: code, sub: sub, phase: "parse"}
}

func valFail(code, sub string) verdict {
	return verdict{code: code, sub: sub, phase: "validate"}
}

var (
	controlOnce     sync.Once
	controlJSON     []byte
	catalogOnce     sync.Once
	catalogCache    []seed
	keyOnce         sync.Once
	keyCache        []seed
	structuralOnce  sync.Once
	structuralCache []seed
	modeOnce        sync.Once
	modeCache       []seed
	omitOnce        sync.Once
	omitCache       []seed
	nullOnce        sync.Once
	nullCache       []seed
	emptyOnce       sync.Once
	emptyCache      []seed
	doubledOnce     sync.Once
	doubledJSON     []byte
	renamedOnce     sync.Once
	renamedJSON     []byte
)

// controlBody is immutable. Callers must not append to the returned slice.
func controlBody() []byte {
	controlOnce.Do(func() { controlJSON = raw(fresh()) })
	return controlJSON
}

func doc() map[string]any { return fresh() }

func descend(d map[string]any, path []string) map[string]any {
	m := d
	for _, p := range path[:len(path)-1] {
		m = m[p].(map[string]any)
	}
	return m
}

func mustReplace(s, old, new string) string {
	if !strings.Contains(s, old) {
		panic("fixture missing " + old)
	}
	return strings.Replace(s, old, new, 1)
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func checkSeed(t *testing.T, s seed) {
	t.Helper()
	if s.directOnly {
		m, obs := good(t)
		if s.man != nil {
			s.man(m)
		}
		if s.obs != nil {
			s.obs(&obs)
		}
		assertVerdict(t, deployment.Validate(m, &obs), s.v)
		return
	}
	body := s.body
	if body == nil {
		body = controlBody()
	}
	m, err := deployment.ParseManifestJSON(body)
	if s.v.phase == "parse" {
		assertVerdict(t, err, s.v)
		return
	}
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	obs := validObservation()
	if s.obs != nil {
		s.obs(&obs)
	}
	if s.man != nil {
		s.man(m)
	}
	assertVerdict(t, deployment.Validate(m, &obs), s.v)
}

func assertVerdict(t *testing.T, err error, v verdict) {
	t.Helper()
	if v.pass {
		if err != nil {
			t.Fatalf("expected pass, got %v", err)
		}
		return
	}
	assertErrorCode(t, err, v.code)
	if v.sub != "" && !strings.Contains(err.Error(), v.sub) {
		t.Fatalf("error %q missing %q", err.Error(), v.sub)
	}
	if v.notSub != "" && strings.Contains(err.Error(), v.notSub) {
		t.Fatalf("error %q unexpectedly contains %q", err.Error(), v.notSub)
	}
}

func seedControl() seed {
	return seed{name: "valid-control", cat: "control", body: controlBody(), v: passV()}
}

var omitPaths = [][]string{
	{"schema_version"},
	{"state"},
	{"canonical_home"},
	{"control_owner_uid"},
	{"expected_modes"},
	{"stores"},
	{"binary_path"},
	{"binary_sha256"},
	{"launcher_path"},
	{"launcher_sha256"},
	{"allowed_entries"},
	{"inventory_digest"},
	{"expected_modes", "dir_mode"},
	{"expected_modes", "file_mode"},
	{"stores", "outbox_json"},
	{"stores", "outbox_lock"},
	{"stores", "outbox_temp_prefix"},
	{"stores", "messages_db"},
	{"stores", "messages_db_wal"},
	{"stores", "messages_db_shm"},
	{"stores", "daemon_lock"},
	{"allowed_entries", "dm", "id"},
	{"allowed_entries", "dm", "command_class"},
}

func seedOmit(path []string) seed {
	d := doc()
	delete(descend(d, path), path[len(path)-1])
	last := path[len(path)-1]
	return seed{
		name: "omit/" + strings.Join(path, "."),
		cat:  "presence",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", `missing required key "`+last+`"`),
	}
}

var nullPaths = [][]string{
	{"canonical_home"},
	{"control_owner_uid"},
	{"inventory_digest"},
	{"state"},
	{"stores", "outbox_temp_prefix"},
	{"stores", "outbox_json"},
	{"expected_modes", "dir_mode"},
	{"allowed_entries", "dm", "id"},
	{"allowed_entries"},
	{"binary_sha256"},
}

func seedNull(path []string) seed {
	d := doc()
	last := path[len(path)-1]
	descend(d, path)[last] = nil
	return seed{
		name: "null/" + strings.Join(path, "."),
		cat:  "presence",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", `null value for key "`+last+`" rejected`),
	}
}

var emptyPaths = [][]string{
	{"state"},
	{"canonical_home"},
	{"binary_path"},
	{"binary_sha256"},
	{"launcher_path"},
	{"launcher_sha256"},
	{"inventory_digest"},
	{"stores", "outbox_json"},
	{"stores", "outbox_temp_prefix"},
	{"stores", "daemon_lock"},
}

func seedEmpty(path []string) seed {
	d := doc()
	last := path[len(path)-1]
	descend(d, path)[last] = ""
	return seed{
		name: "empty/" + strings.Join(path, "."),
		cat:  "presence",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", "missing required manifest/store field"),
	}
}

func seedUIDBoth(v int, name string) seed {
	d := doc()
	d["control_owner_uid"] = v
	s := seed{
		name: name,
		cat:  "presence",
		body: raw(d),
		obs: func(o *deployment.Observation) {
			o.ControlOwnerUID = v
		},
	}
	if v >= 0 {
		s.v = passV()
	} else {
		s.v = valFail("WRONG_OWNER", "observed uid does not match control owner")
	}
	return s
}

func seedBadUID(i int) seed {
	base := string(controlBody())
	old := `"control_owner_uid":1000`
	name, token, sub := "uid/bad-float", "1000.5", "cannot unmarshal number"
	switch i % 4 {
	case 1:
		name, token, sub = "uid/bad-string", `"1000"`, "cannot unmarshal string"
	case 2:
		name, token, sub = "uid/bad-bool", "true", "cannot unmarshal bool"
	case 3:
		name, token, sub = "uid/bad-overflow", "9223372036854775808", "cannot unmarshal number"
	}
	return seed{
		name: name,
		cat:  "presence",
		body: []byte(mustReplace(base, old, `"control_owner_uid":`+token)),
		v:    parseFail("MALFORMED_MANIFEST", sub),
	}
}

func seedNegZero() seed {
	body := mustReplace(string(controlBody()), `"control_owner_uid":1000`, `"control_owner_uid":-0`)
	return seed{
		name: "uid/explicit-negzero",
		cat:  "presence",
		body: []byte(body),
		obs:  func(o *deployment.Observation) { o.ControlOwnerUID = 0 },
		v:    passV(),
	}
}

func seedTopNull() seed {
	return seed{
		name: "presence/top-level-null",
		cat:  "presence",
		body: []byte("null"),
		v:    parseFail("UNSUPPORTED_VERSION", "unsupported"),
	}
}

func seedMode(dirExp, fileExp, dirObs, fileObs uint32, name string) seed {
	d := doc()
	modes := d["expected_modes"].(map[string]any)
	modes["dir_mode"] = dirExp
	modes["file_mode"] = fileExp
	pass := dirExp != 0 && fileExp != 0 && dirExp&^0700 == 0 && fileExp&^0700 == 0 && dirExp == dirObs && fileExp == fileObs
	s := seed{
		name: name,
		cat:  "presence",
		body: raw(d),
		obs: func(o *deployment.Observation) {
			o.ObservedDirMode = dirObs
			o.ObservedFileMode = fileObs
		},
	}
	if pass {
		s.v = passV()
	} else {
		s.v = valFail("WRONG_MODE", "insecure or mismatched permission modes")
	}
	return s
}

func modeSeeds() []seed {
	modeOnce.Do(func() {
		modeCache = []seed{
			seedMode(0, 0600, 0, 0600, "mode/dir-explicit-0"),
			seedMode(0700, 0, 0700, 0, "mode/file-explicit-0"),
			seedMode(0755, 0600, 0755, 0600, "mode/dir-0755"),
			seedMode(0500, 0600, 0500, 0600, "mode/dir-0500"),
			seedMode(0500, 0600, 0700, 0600, "mode/owner-only-mismatch"),
			seedMode(0700, 0600, 0700, 0660, "mode/obs-group-write"),
		}
	})
	return modeCache
}

func splice(old, repl string) []byte {
	return []byte(mustReplace(string(controlBody()), old, repl))
}

func keyIdentitySeeds() []seed {
	keyOnce.Do(func() { keyCache = buildKeyIdentitySeeds() })
	return keyCache
}

func buildKeyIdentitySeeds() []seed {
	return []seed{
		{name: "key/equiv-state", cat: "key", body: splice(`"state"`, `"sta\u0074e"`), v: passV()},
		{name: "key/dup-state-escape", cat: "key", body: splice(`"state":"active"`, `"state":"active","sta\u0074e":"active"`), v: parseFail("MALFORMED_MANIFEST", "duplicate key: state")},
		{name: "key/add-State", cat: "key", body: splice(`"state":"active"`, `"state":"active","State":"active"`), v: parseFail("MALFORMED_MANIFEST", `unknown field "State"`)},
		{name: "key/replace-State", cat: "key", body: splice(`"state":"active"`, `"State":"active"`), v: parseFail("MALFORMED_MANIFEST", `missing required key "state"`)},
		{name: "key/lookalike-add", cat: "key", body: splice(`"state":"active"`, `"state":"active","`+cyrState+`":"active"`), v: parseFail("MALFORMED_MANIFEST", fmt.Sprintf("unknown field %q", cyrState))},
		{name: "key/lookalike-replace", cat: "key", body: splice(`"state":"active"`, `"`+cyrState+`":"active"`), v: parseFail("MALFORMED_MANIFEST", `missing required key "state"`)},
		{name: "key/equiv-outbox", cat: "key", body: splice(`"outbox_json"`, `"outbox_\u006ason"`), v: passV()},
		{name: "key/dup-outbox-escape", cat: "key", body: splice(`"outbox_json":"`+h+`/.hyphae/outbox.json"`, `"outbox_json":"`+h+`/.hyphae/outbox.json","outbox_\u006ason":"`+h+`/.hyphae/outbox.json"`), v: parseFail("MALFORMED_MANIFEST", "duplicate key: outbox_json")},
		{name: "key/equiv-schema", cat: "key", body: splice(`"schema_version"`, `"s\u0063hema_version"`), v: passV()},
		{name: "key/equiv-id", cat: "key", body: splice(`"id":"dm"`, `"\u0069d":"dm"`), v: passV()},
		{name: "key/dup-id-escape", cat: "key", body: splice(`"id":"dm"`, `"id":"dm","\u0069d":"dm"`), v: parseFail("MALFORMED_MANIFEST", "duplicate key: id")},
		{name: "key/add-Dir_mode", cat: "key", body: splice(`"dir_mode":448`, `"dir_mode":448,"Dir_mode":448`), v: parseFail("MALFORMED_MANIFEST", `unknown field "Dir_mode"`)},
		{name: "key/ws-document", cat: "key", body: []byte(" \n\t" + string(controlBody()) + " \n"), v: passV()},
		{name: "key/trailing", cat: "key", body: append(append([]byte(nil), controlBody()...), []byte(" {}")...), v: parseFail("MALFORMED_MANIFEST", "unexpected trailing data")},
		{name: "key/empty-input", cat: "key", body: []byte{}, v: parseFail("MALFORMED_MANIFEST", "EOF")},
	}
}

func seedStructural(name string) seed {
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	ent[name] = map[string]any{"id": name, "command_class": "dm"}
	return seed{name: "struct-key-id/" + name, cat: "key", body: raw(d), v: passV()}
}

func structuralSeeds() []seed {
	structuralOnce.Do(func() {
		structuralCache = make([]seed, 0, len(structuralNames))
		for _, name := range structuralNames {
			structuralCache = append(structuralCache, seedStructural(name))
		}
	})
	return structuralCache
}

var wsIDs = []struct{ name, id string }{
	{"space", " "},
	{"tab", "\t"},
	{"lf", "\n"},
	{"cr", "\r"},
	{"mixed", " \t "},
	{"nbsp", "\u00a0"},
	{"emsp", "\u2003"},
}

func seedWhitespace(i int) seed {
	w := wsIDs[i%len(wsIDs)]
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	delete(ent, "dm")
	ent[w.id] = map[string]any{"id": w.id, "command_class": "dm"}
	return seed{
		name: "entry/ws/" + w.name,
		cat:  "entry",
		body: raw(d),
		v:    parseFail("UNKNOWN_ENTRY", fmt.Sprintf("invalid entry %q", w.id)),
	}
}

var passIDs = []struct{ name, id string }{
	{"my-worker", "my worker"},
	{"padded-dm", "dm "},
	{"lead-space", " dm"},
	{"dotted", "entry.1"},
	{"cafe", "café"},
}

func seedPassID(i int) seed {
	p := passIDs[i%len(passIDs)]
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	delete(ent, "dm")
	ent[p.id] = map[string]any{"id": p.id, "command_class": "dm"}
	return seed{
		name: "entry/pass-id/" + p.name,
		cat:  "entry",
		body: raw(d),
		obs:  func(o *deployment.Observation) { o.EntryID = p.id },
		v:    passV(),
	}
}

func seedIDMismatch() seed {
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	dm := ent["dm"].(map[string]any)
	dm["id"] = "other"
	return seed{
		name: "entry/id-mismatch",
		cat:  "entry",
		body: raw(d),
		v:    parseFail("UNKNOWN_ENTRY", `invalid entry "dm"`),
	}
}

func seedDropClass(i int) seed {
	class := classValues[i%len(classValues)]
	d := doc()
	delete(d["allowed_entries"].(map[string]any), class)
	return seed{
		name: "entry/drop/" + class,
		cat:  "entry",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", fmt.Sprintf("missing class %q", class)),
	}
}

func seedUnknownClass() seed {
	d := doc()
	dm := d["allowed_entries"].(map[string]any)["dm"].(map[string]any)
	dm["command_class"] = "DM"
	return seed{
		name: "entry/unknown-class-DM",
		cat:  "entry",
		body: raw(d),
		v:    parseFail("UNKNOWN_ENTRY", `invalid entry "dm"`),
	}
}

func seedEmptyTable() seed {
	d := doc()
	d["allowed_entries"] = map[string]any{}
	return seed{
		name: "entry/empty-table",
		cat:  "entry",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", "allowed_entries must not be empty"),
	}
}

func seedExtraMissingProbe() seed {
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	ent["extra-dm"] = map[string]any{"id": "extra-dm", "command_class": "dm"}
	delete(ent, "probe")
	return seed{
		name: "entry/extra-dm-missing-probe",
		cat:  "entry",
		body: raw(d),
		v:    parseFail("MALFORMED_MANIFEST", `missing class "probe"`),
	}
}

func doubledBody() []byte {
	doubledOnce.Do(func() {
		d := doc()
		ent := map[string]any{}
		for _, c := range classValues {
			for _, pre := range []string{"a-", "b-"} {
				id := pre + c
				ent[id] = map[string]any{"id": id, "command_class": c}
			}
		}
		d["allowed_entries"] = ent
		doubledJSON = raw(d)
	})
	return doubledJSON
}

func seedMulti(name, id string, mode deployment.SessionMode) seed {
	return seed{
		name: name,
		cat:  "entry",
		body: doubledBody(),
		obs: func(o *deployment.Observation) {
			o.EntryID = id
			o.SessionMode = mode
		},
		v: passV(),
	}
}

func seedExtraID(id string) seed {
	d := doc()
	ent := d["allowed_entries"].(map[string]any)
	ent[id] = map[string]any{"id": id, "command_class": "dm"}
	return seed{name: "entry/extra/" + id, cat: "entry", body: raw(d), v: passV()}
}

func copyEntries(m *deployment.Manifest) map[string]deployment.EntryDef {
	cp := make(map[string]deployment.EntryDef, len(m.AllowedEntries))
	for k, v := range m.AllowedEntries {
		cp[k] = v
	}
	return cp
}

func directSeeds() []seed {
	return []seed{
		{
			name: "direct/uid-0-not-json-presence", cat: "presence", directOnly: true,
			man: func(m *deployment.Manifest) { m.ControlOwnerUID = 0 },
			obs: func(o *deployment.Observation) { o.ControlOwnerUID = 0 },
			v:   passV(),
		},
		{
			name: "direct/uid-both-negative", cat: "presence", directOnly: true,
			man: func(m *deployment.Manifest) { m.ControlOwnerUID = -1 },
			obs: func(o *deployment.Observation) { o.ControlOwnerUID = -1 },
			v:   valFail("WRONG_OWNER", "observed uid does not match control owner"),
		},
		{
			name: "direct/id-mismatch", cat: "entry", directOnly: true,
			man: func(m *deployment.Manifest) {
				cp := copyEntries(m)
				cp["dm"] = deployment.EntryDef{ID: "other", CommandClass: "dm"}
				m.AllowedEntries = cp
			},
			v: valFail("UNKNOWN_ENTRY", `invalid entry "dm"`),
		},
		{
			name: "direct/ws-space", cat: "entry", directOnly: true,
			man: func(m *deployment.Manifest) {
				cp := copyEntries(m)
				delete(cp, "dm")
				cp[" "] = deployment.EntryDef{ID: " ", CommandClass: "dm"}
				m.AllowedEntries = cp
			},
			v: valFail("UNKNOWN_ENTRY", `invalid entry " "`),
		},
		{
			name: "direct/drop-probe", cat: "entry", directOnly: true,
			man: func(m *deployment.Manifest) {
				cp := copyEntries(m)
				delete(cp, "probe")
				m.AllowedEntries = cp
			},
			v: valFail("MALFORMED_MANIFEST", `missing class "probe"`),
		},
		{
			name: "direct/multi-id", cat: "entry", directOnly: true,
			man: func(m *deployment.Manifest) {
				m.AllowedEntries = map[string]deployment.EntryDef{
					"alpha": {ID: "alpha", CommandClass: "dm"},
					"beta":  {ID: "beta", CommandClass: "dm"},
					"gs1":   {ID: "gs1", CommandClass: "group-store"},
					"gs2":   {ID: "gs2", CommandClass: "group-store"},
					"gw":    {ID: "gw", CommandClass: "group-write"},
					"mnt":   {ID: "mnt", CommandClass: "maintenance"},
					"prn":   {ID: "prn", CommandClass: "probe"},
				}
			},
			obs: func(o *deployment.Observation) {
				o.EntryID = "beta"
				o.SessionMode = deployment.SessionModeRuntime
			},
			v: passV(),
		},
		{
			name: "direct/empty-digest-is-drift", cat: "digest", directOnly: true,
			man: func(m *deployment.Manifest) { m.InventoryDigest = "" },
			obs: func(o *deployment.Observation) { o.InventoryDigest = "" },
			v: verdict{
				code: "DRIFT", sub: "inventory digest drift", notSub: "missing required key", phase: "validate",
			},
		},
	}
}

type sessRow struct {
	name  string
	entry string
	mode  deployment.SessionMode
	code  string
}

func sessionMatrix() []sessRow {
	rt, mn, pr := deployment.SessionModeRuntime, deployment.SessionModeMaintenance, deployment.SessionModeProbe
	return []sessRow{
		{"runtime-dm", "dm", rt, ""},
		{"runtime-group-store", "group-store", rt, ""},
		{"runtime-group-write", "group-write", rt, ""},
		{"runtime-maintenance", "maintenance", rt, "MODE_MISMATCH"},
		{"runtime-probe", "probe", rt, "MODE_MISMATCH"},
		{"maintenance-dm", "dm", mn, "MODE_MISMATCH"},
		{"maintenance-group-store", "group-store", mn, "MODE_MISMATCH"},
		{"maintenance-group-write", "group-write", mn, "MODE_MISMATCH"},
		{"maintenance-maintenance", "maintenance", mn, ""},
		{"maintenance-probe", "probe", mn, "MODE_MISMATCH"},
		{"probe-dm", "dm", pr, "MODE_MISMATCH"},
		{"probe-group-store", "group-store", pr, "MODE_MISMATCH"},
		{"probe-group-write", "group-write", pr, "MODE_MISMATCH"},
		{"probe-maintenance", "maintenance", pr, "MODE_MISMATCH"},
		{"probe-probe", "probe", pr, ""},
		{"invalid-empty", "dm", "", "INVALID_SESSION_MODE"},
		{"invalid-bogus", "dm", "bogus_mode", "INVALID_SESSION_MODE"},
		{"invalid-Runtime", "dm", "Runtime", "INVALID_SESSION_MODE"},
	}
}

func seedSession(row sessRow) seed {
	s := seed{
		name: "session/" + row.name,
		cat:  "session",
		body: controlBody(),
		obs: func(o *deployment.Observation) {
			o.EntryID = row.entry
			o.SessionMode = row.mode
		},
	}
	if row.code == "" {
		s.v = passV()
	} else if row.code == "INVALID_SESSION_MODE" {
		s.v = valFail(row.code, "unknown session mode")
	} else {
		s.v = valFail(row.code, "incompatible with mode")
	}
	return s
}

func renamedBody() []byte {
	renamedOnce.Do(func() {
		d := doc()
		d["allowed_entries"] = map[string]any{
			"send":   map[string]any{"id": "send", "command_class": "dm"},
			"daemon": map[string]any{"id": "daemon", "command_class": "group-store"},
			"tui":    map[string]any{"id": "tui", "command_class": "group-write"},
			"owner":  map[string]any{"id": "owner", "command_class": "maintenance"},
			"health": map[string]any{"id": "health", "command_class": "probe"},
		}
		renamedJSON = raw(d)
	})
	return renamedJSON
}

func renamedMatrix() []sessRow {
	rt, mn, pr := deployment.SessionModeRuntime, deployment.SessionModeMaintenance, deployment.SessionModeProbe
	return []sessRow{
		{"renamed-runtime-send", "send", rt, ""},
		{"renamed-runtime-daemon", "daemon", rt, ""},
		{"renamed-runtime-tui", "tui", rt, ""},
		{"renamed-runtime-owner", "owner", rt, "MODE_MISMATCH"},
		{"renamed-maintenance-owner", "owner", mn, ""},
		{"renamed-maintenance-send", "send", mn, "MODE_MISMATCH"},
		{"renamed-probe-health", "health", pr, ""},
		{"renamed-probe-daemon", "daemon", pr, "MODE_MISMATCH"},
	}
}

func seedRenamed(row sessRow) seed {
	s := seedSession(row)
	s.name = "session/" + row.name
	s.body = renamedBody()
	return s
}

func seedDigestSides(man, obsSum, name string) seed {
	d := doc()
	d["inventory_digest"] = man
	s := seed{
		name: name,
		cat:  "digest",
		body: raw(d),
		obs:  func(o *deployment.Observation) { o.InventoryDigest = obsSum },
	}
	if man == obsSum && isHex64(man) {
		s.v = passV()
	} else {
		s.v = valFail("DRIFT", "inventory digest drift")
	}
	return s
}

func digestHex(rest []byte) string {
	buf := make([]byte, 32)
	copy(buf, rest)
	return hex.EncodeToString(buf)
}

type nsCase struct {
	name string
	pass bool
	code string
	sub  string
	edit func(map[string]any)
}

func namespaceCases() []nsCase {
	stores := func(d map[string]any) map[string]any { return d["stores"].(map[string]any) }
	return []nsCase{
		{name: "ns/temp-alt", pass: true, edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/.outbox-alt"
		}},
		{name: "ns/temp-worker", pass: true, edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/.outbox-worker"
		}},
		{name: "ns/temp-case", code: "PATH_ESCAPED", sub: "outbox-sibling namespace", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/.Outbox-"
		}},
		{name: "ns/temp-nested", code: "PATH_ESCAPED", sub: "outbox-sibling namespace", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/nested/.outbox-"
		}},
		{name: "ns/temp-nodot", code: "PATH_ESCAPED", sub: "outbox-sibling namespace", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/outbox-"
		}},
		{name: "ns/lock-other-dir", code: "PATH_ESCAPED", sub: "outbox-sibling namespace", edit: func(d map[string]any) {
			stores(d)["outbox_lock"] = h + "/.hyphae/locks/outbox.json.lock"
		}},
		{name: "ns/outside-home", code: "PATH_ESCAPED", sub: "escapes canonical home", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = "/foreign/home/.outbox-"
		}},
		{name: "ns/dotdot-unclean", code: "PATH_ESCAPED", sub: "escapes canonical home", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae/../.hyphae/.outbox-"
		}},
		{name: "ns/double-slash", code: "PATH_ESCAPED", sub: "escapes canonical home", edit: func(d map[string]any) {
			stores(d)["outbox_temp_prefix"] = h + "/.hyphae//.outbox-"
		}},
	}
}

func seedNamespace(i int) seed {
	cases := namespaceCases()
	c := cases[i%len(cases)]
	d := doc()
	c.edit(d)
	s := seed{name: c.name, cat: "namespace", body: raw(d)}
	if c.pass {
		s.v = passV()
	} else {
		s.v = valFail(c.code, c.sub)
	}
	return s
}

func buildStructuredCatalog() []seed {
	var out []seed
	out = append(out, seedControl())
	for _, p := range omitPaths {
		out = append(out, seedOmit(p))
	}
	for _, p := range nullPaths {
		out = append(out, seedNull(p))
	}
	for _, p := range emptyPaths {
		out = append(out, seedEmpty(p))
	}
	out = append(out,
		seedUIDBoth(0, "uid/explicit-0"),
		seedUIDBoth(1, "uid/both-1"),
		seedUIDBoth(-1, "uid/both-negative"),
		seedUIDBoth(int(math.MaxInt32), "uid/max-int32"),
		seedUIDBoth(math.MinInt32, "uid/min-int32"),
		seedUIDBoth(math.MaxInt, "uid/max-int"),
		seedNegZero(),
	)
	mismatch := seed{name: "uid/obs-1001", cat: "presence", body: controlBody(), obs: func(o *deployment.Observation) { o.ControlOwnerUID = 1001 }, v: valFail("WRONG_OWNER", "observed uid does not match control owner")}
	out = append(out, mismatch)
	for i := 0; i < 4; i++ {
		out = append(out, seedBadUID(i))
	}
	out = append(out, seedTopNull())
	out = append(out, modeSeeds()...)
	out = append(out, keyIdentitySeeds()...)
	out = append(out, structuralSeeds()...)
	out = append(out, seedIDMismatch(), seedUnknownClass(), seedEmptyTable(), seedExtraMissingProbe())
	for i := range wsIDs {
		out = append(out, seedWhitespace(i))
	}
	for i := range passIDs {
		out = append(out, seedPassID(i))
	}
	for i := range classValues {
		out = append(out, seedDropClass(i))
	}
	out = append(out,
		seedMulti("entry/multi/runtime-a-dm", "a-dm", deployment.SessionModeRuntime),
		seedMulti("entry/multi/maint-b", "b-maintenance", deployment.SessionModeMaintenance),
		seedMulti("entry/multi/probe-a", "a-probe", deployment.SessionModeProbe),
		seedExtraID("e-fixed"),
	)
	out = append(out, directSeeds()...)
	for _, row := range sessionMatrix() {
		out = append(out, seedSession(row))
	}
	for _, row := range renamedMatrix() {
		out = append(out, seedRenamed(row))
	}
	out = append(out,
		seedDigestSides(strings.Repeat("0", 64), strings.Repeat("0", 64), "digest/both-zero"),
		seedDigestSides(altDigest, altDigest, "digest/both-alt"),
		seedDigestSides(strings.ToUpper(testValidDigest), strings.ToUpper(testValidDigest), "digest/both-upper"),
		seedDigestSides(altDigest, testValidDigest, "digest/manifest-changed"),
		seedDigestSides(testValidDigest[:63]+"g", testValidDigest[:63]+"g", "digest/invalid-equal"),
		seedDigestSides("aa", "aa", "digest/short-equal"),
	)
	for i := range namespaceCases() {
		out = append(out, seedNamespace(i))
	}
	return out
}

func catalog() []seed {
	catalogOnce.Do(func() { catalogCache = buildStructuredCatalog() })
	return catalogCache
}

func TestStructuredValidControlPasses(t *testing.T) {
	s := seedControl()
	if !s.v.pass {
		t.Fatal("control oracle is not a pass")
	}
	checkSeed(t, s)
	m, err := deployment.ParseManifestJSON(controlBody())
	if err != nil {
		t.Fatal(err)
	}
	obs := validObservation()
	if err := deployment.Validate(m, &obs); err != nil {
		t.Fatal(err)
	}
	if m.CanonicalHome != obs.CanonicalHome || m.InventoryDigest != obs.InventoryDigest || m.ControlOwnerUID != obs.ControlOwnerUID {
		t.Fatal("synthetic control tuple is internally inconsistent")
	}
	t.Log("BASELINE_VALID_CONTROL=PASS")
}

func TestStructuredRequiredClassValues(t *testing.T) {
	want := []string{"dm", "group-store", "group-write", "maintenance", "probe"}
	if len(deployment.RequiredClasses) != len(want) {
		t.Fatalf("RequiredClasses=%v", deployment.RequiredClasses)
	}
	for i, c := range want {
		if deployment.RequiredClasses[i] != c || classValues[i] != c {
			t.Fatalf("class[%d]=%s want %s", i, deployment.RequiredClasses[i], c)
		}
	}
}

func TestStructuredSeeds(t *testing.T) {
	all := catalog()
	seen := map[string]int{}
	pass, fail := 0, 0
	cats := map[string]int{}
	for _, s := range all {
		seen[s.name]++
		cats[s.cat]++
		if s.v.pass {
			pass++
		} else {
			fail++
		}
		if seen[s.name] > 1 {
			t.Errorf("duplicate seed %s", s.name)
		}
	}
	t.Logf("STRUCTURED_SEED_COUNT=%d STRUCTURED_PASS=%d STRUCTURED_FAIL=%d", len(all), pass, fail)
	keys := make([]string, 0, len(cats))
	for k := range cats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("STRUCTURED_CAT %s=%d", k, cats[k])
	}
	if len(all) < 40 || pass < 12 || fail < 25 {
		t.Fatalf("catalog shape count=%d pass=%d fail=%d", len(all), pass, fail)
	}
	for _, s := range all {
		s := s
		t.Run(s.name, func(t *testing.T) { checkSeed(t, s) })
	}
}

func TestStructuredEntryAPIsAgree(t *testing.T) {
	byName := map[string]seed{}
	for _, s := range catalog() {
		byName[s.name] = s
	}
	pairs := [][2]string{
		{"entry/id-mismatch", "direct/id-mismatch"},
		{"entry/ws/space", "direct/ws-space"},
		{"entry/drop/probe", "direct/drop-probe"},
	}
	for _, p := range pairs {
		a, okA := byName[p[0]]
		b, okB := byName[p[1]]
		if !okA || !okB {
			t.Fatalf("missing pair %s %s", p[0], p[1])
		}
		if a.v.pass || b.v.pass || a.v.code == "" || a.v.code != b.v.code {
			t.Fatalf("%s (%s) vs %s (%s)", p[0], a.v.code, p[1], b.v.code)
		}
	}
	jsonOmit := byName["omit/control_owner_uid"]
	directZero := byName["direct/uid-0-not-json-presence"]
	if jsonOmit.v.phase != "parse" || jsonOmit.v.code != "MALFORMED_MANIFEST" || !directZero.v.pass {
		t.Fatal("UID presence contract drifted")
	}
	directDig := byName["direct/empty-digest-is-drift"]
	if directDig.v.code != "DRIFT" || directDig.v.notSub != "missing required key" {
		t.Fatal("digest presence contract drifted")
	}
}

const (
	presControl = iota
	presOmit
	presNull
	presUID
	presBadUID
	presTopNull
	presEmpty
	presNeg0
	presMode
	presenceOps
)

const (
	keyControl = iota
	keyEquivState
	keyDupState
	keyAddState
	keyReplState
	keyLookAdd
	keyLookRepl
	keyEquivOutbox
	keyDupOutbox
	keyEquivSchema
	keyEquivID
	keyDupID
	keyDirCase
	keyWS
	keyTrailing
	keyEmpty
	keyStructural
	keyOps
)

const (
	entryOpControl = iota
	entryOpMismatch
	entryOpWhitespace
	entryOpPassID
	entryOpDrop
	entryOpDoubled
	entryOpSession
	entryOpRenamed
	entryOpUnknown
	entryOpExtra
	entryOpStructural
	entryOpEmptyTable
	entryOps
)

const (
	digControl = iota
	digEqual
	digMismatch
	digInvalid
	digEmpty
	digNull
	digOmit
	digUpper
	digNS
	digestOps
)

func argOf(b []byte) int {
	if len(b) < 2 {
		return 0
	}
	return int(b[1])
}

func u32Of(b []byte) uint32 {
	buf := make([]byte, 4)
	if len(b) > 1 {
		copy(buf, b[1:])
	}
	return binary.LittleEndian.Uint32(buf)
}

func cachedOmits() []seed {
	omitOnce.Do(func() {
		for _, p := range omitPaths {
			omitCache = append(omitCache, seedOmit(p))
		}
	})
	return omitCache
}

func cachedNulls() []seed {
	nullOnce.Do(func() {
		for _, p := range nullPaths {
			nullCache = append(nullCache, seedNull(p))
		}
	})
	return nullCache
}

func cachedEmpties() []seed {
	emptyOnce.Do(func() {
		for _, p := range emptyPaths {
			emptyCache = append(emptyCache, seedEmpty(p))
		}
	})
	return emptyCache
}

func presenceFromBytes(b []byte) seed {
	if len(b) == 0 {
		return seedControl()
	}
	switch int(b[0]) % presenceOps {
	case presOmit:
		s := cachedOmits()
		return s[argOf(b)%len(s)]
	case presNull:
		s := cachedNulls()
		return s[argOf(b)%len(s)]
	case presUID:
		return seedUIDBoth(int(int32(u32Of(b))), "fuzz/uid")
	case presBadUID:
		return seedBadUID(argOf(b))
	case presTopNull:
		return seedTopNull()
	case presEmpty:
		s := cachedEmpties()
		return s[argOf(b)%len(s)]
	case presNeg0:
		return seedNegZero()
	case presMode:
		s := modeSeeds()
		return s[argOf(b)%len(s)]
	default:
		return seedControl()
	}
}

func keyFromBytes(b []byte) seed {
	if len(b) == 0 {
		return seedControl()
	}
	switch int(b[0]) % keyOps {
	case keyEquivState:
		return keyIdentitySeeds()[0]
	case keyDupState:
		return keyIdentitySeeds()[1]
	case keyAddState:
		return keyIdentitySeeds()[2]
	case keyReplState:
		return keyIdentitySeeds()[3]
	case keyLookAdd:
		return keyIdentitySeeds()[4]
	case keyLookRepl:
		return keyIdentitySeeds()[5]
	case keyEquivOutbox:
		return keyIdentitySeeds()[6]
	case keyDupOutbox:
		return keyIdentitySeeds()[7]
	case keyEquivSchema:
		return keyIdentitySeeds()[8]
	case keyEquivID:
		return keyIdentitySeeds()[9]
	case keyDupID:
		return keyIdentitySeeds()[10]
	case keyDirCase:
		return keyIdentitySeeds()[11]
	case keyWS:
		return keyIdentitySeeds()[12]
	case keyTrailing:
		return keyIdentitySeeds()[13]
	case keyEmpty:
		return keyIdentitySeeds()[14]
	case keyStructural:
		return structuralSeeds()[argOf(b)%len(structuralNames)]
	default:
		return seedControl()
	}
}

func entryFromBytes(b []byte) seed {
	if len(b) == 0 {
		return seedControl()
	}
	arg := argOf(b)
	switch int(b[0]) % entryOps {
	case entryOpMismatch:
		return seedIDMismatch()
	case entryOpWhitespace:
		return seedWhitespace(arg)
	case entryOpPassID:
		return seedPassID(arg)
	case entryOpDrop:
		return seedDropClass(arg)
	case entryOpDoubled:
		return seedMulti("fuzz/multi", "a-dm", deployment.SessionModeRuntime)
	case entryOpSession:
		rows := sessionMatrix()
		return seedSession(rows[arg%len(rows)])
	case entryOpRenamed:
		rows := renamedMatrix()
		return seedRenamed(rows[arg%len(rows)])
	case entryOpUnknown:
		return seedUnknownClass()
	case entryOpExtra:
		rest := []byte(nil)
		if len(b) > 1 {
			rest = b[1:]
		}
		if len(rest) > 16 {
			rest = rest[:16]
		}
		return seedExtraID("e-" + hex.EncodeToString(rest))
	case entryOpStructural:
		return structuralSeeds()[arg%len(structuralNames)]
	case entryOpEmptyTable:
		return seedEmptyTable()
	default:
		return seedControl()
	}
}

func digestFromBytes(b []byte) seed {
	if len(b) == 0 {
		return seedControl()
	}
	rest := []byte(nil)
	if len(b) > 1 {
		rest = b[1:]
	}
	sum := digestHex(rest)
	switch int(b[0]) % digestOps {
	case digEqual:
		return seedDigestSides(sum, sum, "fuzz/digest-equal")
	case digMismatch:
		obs := sum
		if obs[len(obs)-1] == '0' {
			obs = obs[:len(obs)-1] + "1"
		} else {
			obs = obs[:len(obs)-1] + "0"
		}
		return seedDigestSides(sum, obs, "fuzz/digest-mismatch")
	case digInvalid:
		bad := sum[:63] + "g"
		return seedDigestSides(bad, bad, "fuzz/digest-invalid")
	case digEmpty:
		return seedEmpty([]string{"inventory_digest"})
	case digNull:
		return seedNull([]string{"inventory_digest"})
	case digOmit:
		return seedOmit([]string{"inventory_digest"})
	case digUpper:
		up := strings.ToUpper(sum)
		return seedDigestSides(up, up, "fuzz/digest-upper")
	case digNS:
		return seedNamespace(argOf(b))
	default:
		return seedControl()
	}
}

func fuzzSeedInputs() map[string][][]byte {
	pres := [][]byte{{0}, {presNeg0}, {presTopNull}}
	for i := range omitPaths {
		pres = append(pres, []byte{presOmit, byte(i)})
	}
	for i := range nullPaths {
		pres = append(pres, []byte{presNull, byte(i)})
	}
	for i := range emptyPaths {
		pres = append(pres, []byte{presEmpty, byte(i)})
	}
	pres = append(pres,
		[]byte{presUID, 0, 0, 0, 0},
		[]byte{presUID, 1, 0, 0, 0},
		[]byte{presUID, 0xff, 0xff, 0xff, 0xff},
		[]byte{presUID, 0xff, 0xff, 0xff, 0x7f},
		[]byte{presUID, 0x00, 0x00, 0x00, 0x80},
	)
	for i := 0; i < 4; i++ {
		pres = append(pres, []byte{presBadUID, byte(i)})
	}
	for i := range modeSeeds() {
		pres = append(pres, []byte{presMode, byte(i)})
	}

	keys := [][]byte{{keyControl}}
	for op := 1; op < keyOps; op++ {
		keys = append(keys, []byte{byte(op), 0}, []byte{byte(op), 3})
	}

	entries := [][]byte{{entryOpControl}}
	for op := 1; op < entryOps; op++ {
		entries = append(entries, []byte{byte(op), 0}, []byte{byte(op), 1, 0xab})
	}

	digs := [][]byte{
		{digControl},
		{digEqual},
		{digEqual, 0xab, 0xcd},
		{digMismatch, 0x11},
		{digInvalid, 0x22},
		{digEmpty},
		{digNull},
		{digOmit},
		{digUpper, 0xff},
	}
	for i := range namespaceCases() {
		digs = append(digs, []byte{digNS, byte(i)})
	}
	return map[string][][]byte{
		"presence": pres,
		"key":      keys,
		"entry":    entries,
		"digest":   digs,
	}
}

func dispatchFuzz(cat string, b []byte) seed {
	switch cat {
	case "presence":
		return presenceFromBytes(b)
	case "key":
		return keyFromBytes(b)
	case "entry":
		return entryFromBytes(b)
	case "digest":
		return digestFromBytes(b)
	default:
		return seedControl()
	}
}

func TestStructuredFuzzCorpusNotVacuous(t *testing.T) {
	for cat, inputs := range fuzzSeedInputs() {
		pass, fail := 0, 0
		names := map[string]int{}
		for _, b := range inputs {
			s := dispatchFuzz(cat, b)
			names[s.name]++
			if s.v.pass {
				pass++
			} else {
				fail++
			}
			checkSeed(t, s)
		}
		t.Logf("FUZZ_CORPUS %s inputs=%d distinct=%d pass=%d fail=%d", cat, len(inputs), len(names), pass, fail)
		if pass == 0 || fail == 0 {
			t.Fatalf("%s fuzz corpus is vacuous", cat)
		}
	}
}

func TestStructuredDigestPropertySamples(t *testing.T) {
	samples := [][]byte{
		{digEqual},
		{digEqual, 0x00, 0xff},
		{digEqual, 0x10, 0x20, 0x30, 0x40},
		append([]byte{digEqual}, bytesRepeat(0x5a, 32)...),
		{digUpper, 0xab},
		append([]byte{digUpper}, bytesRepeat(0x0f, 16)...),
		{digMismatch, 0x01},
		{digInvalid, 0x02},
	}
	for _, b := range samples {
		s := digestFromBytes(b)
		checkSeed(t, s)
		op := int(b[0]) % digestOps
		if op == digEqual || op == digUpper {
			if !s.v.pass {
				t.Fatalf("equal hex digest oracle rejected %x", b)
			}
		}
		if op == digMismatch || op == digInvalid {
			if s.v.pass || s.v.code != "DRIFT" {
				t.Fatalf("digest reject oracle drifted for %x: %+v", b, s.v)
			}
		}
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// FuzzCategoryRequiredPresence selects omissions, nulls, UID bounds, and modes.
func FuzzCategoryRequiredPresence(f *testing.F) {
	for _, b := range fuzzSeedInputs()["presence"] {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, fuzzBytes []byte) {
		checkSeed(t, presenceFromBytes(fuzzBytes))
	})
}

// FuzzCategoryKeyIdentity selects case, escaped duplicates, unicode aliases,
// and legal dynamic entry IDs that reuse structural key names.
func FuzzCategoryKeyIdentity(f *testing.F) {
	for _, b := range fuzzSeedInputs()["key"] {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, fuzzBytes []byte) {
		checkSeed(t, keyFromBytes(fuzzBytes))
	})
}

// FuzzCategoryEntryInventory selects ID binding, whitespace IDs, class coverage,
// arbitrary extra IDs, and the session/class matrix.
func FuzzCategoryEntryInventory(f *testing.F) {
	for _, b := range fuzzSeedInputs()["entry"] {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, fuzzBytes []byte) {
		checkSeed(t, entryFromBytes(fuzzBytes))
	})
}

// FuzzCategoryDigestNamespace selects digest equality over fuzz-chosen bytes
// and canonical outbox temp sibling mutations.
func FuzzCategoryDigestNamespace(f *testing.F) {
	for _, b := range fuzzSeedInputs()["digest"] {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, fuzzBytes []byte) {
		checkSeed(t, digestFromBytes(fuzzBytes))
	})
}
