package groupchat

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// groupchatTableShape is the bounded physical-shape contract NewStore checks
// before the daemon/TUI are allowed to dial relays: the authoritative columns
// and primary key every documented groupchat_* DDL/migration guarantees.
// Extra columns or indexes are never checked — only their absence matters.
type groupchatTableShape struct {
	columns    []string
	primaryKey []string
}

// requiredGroupchatShapes covers every table NewStore's migrate creates.
// groupchat_fanout lists only its pre-rev7 columns: migrateFanoutAccounting
// adds the accounting columns afterward, so a supported legacy fanout table
// that hasn't been through that migration yet must still pass here.
var requiredGroupchatShapes = []struct {
	table string
	shape groupchatTableShape
}{
	{"groupchat_groups", groupchatTableShape{
		columns:    []string{"local_npub", "group_id", "name", "creator_npub", "roster_json", "roster_hash", "state", "created_at", "updated_at"},
		primaryKey: []string{"local_npub", "group_id"},
	}},
	{"groupchat_members", groupchatTableShape{
		columns:    []string{"local_npub", "group_id", "npub", "state", "is_creator"},
		primaryKey: []string{"local_npub", "group_id", "npub"},
	}},
	{"groupchat_invites", groupchatTableShape{
		columns:    []string{"local_npub", "invite_id", "group_id", "creator_npub", "invitee_npub", "name", "roster_json", "roster_hash", "state"},
		primaryKey: []string{"local_npub", "invite_id"},
	}},
	{"groupchat_messages", groupchatTableShape{
		columns:    []string{"local_npub", "group_id", "logical_id", "sender_npub", "body", "created_at"},
		primaryKey: []string{"local_npub", "group_id", "logical_id"},
	}},
	{"groupchat_fanout", groupchatTableShape{
		columns:    []string{"local_npub", "group_id", "envelope_type", "send_key", "recipient_npub", "event_id", "event_json", "state", "max_retries"},
		primaryKey: []string{"local_npub", "group_id", "envelope_type", "send_key", "recipient_npub"},
	}},
}

// validateGroupchatSchema runs bounded, read-only physical-schema checks on
// every required groupchat_* name right after CREATE TABLE IF NOT EXISTS.
// That statement silently no-ops when the name is already occupied by a VIEW
// or a wrong-shape TABLE, so without this check NewStore would falsely
// succeed and the daemon would dial relays against a store that cannot
// actually hold group state. It never DROPs, rebuilds, or autofixes
// anything — a bad shape is a hard startup error, not a repair target.
func validateGroupchatSchema(db *sql.DB) error {
	for _, req := range requiredGroupchatShapes {
		if err := validateTableShape(db, req.table, req.shape); err != nil {
			return err
		}
	}
	return nil
}

func validateTableShape(db *sql.DB, table string, shape groupchatTableShape) error {
	var sqlType string
	err := db.QueryRow(`SELECT type FROM sqlite_master WHERE lower(name) = lower(?)`, table).Scan(&sqlType)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("groupchat schema %s: missing after create", table)
	case err != nil:
		return fmt.Errorf("groupchat schema %s: %w", table, err)
	case sqlType != "table":
		return fmt.Errorf("groupchat schema %s: expected a table, found a %s occupying that name", table, sqlType)
	}

	// Table identifiers here always come from the fixed list above, never
	// from external input, so building the PRAGMA string is safe: SQLite's
	// PRAGMA table_info does not accept a bound parameter for the name.
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("groupchat schema %s: %w", table, err)
	}
	defer rows.Close()

	present := map[string]bool{}
	pkColumns := map[string]bool{}
	pkCount := 0
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			return fmt.Errorf("groupchat schema %s: %w", table, err)
		}
		lower := strings.ToLower(name)
		present[lower] = true
		if pk > 0 {
			pkColumns[lower] = true
			pkCount++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("groupchat schema %s: %w", table, err)
	}
	if len(present) == 0 {
		return fmt.Errorf("groupchat schema %s: missing after create", table)
	}

	var missing []string
	for _, column := range shape.columns {
		if !present[strings.ToLower(column)] {
			missing = append(missing, column)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("groupchat schema %s: missing required column(s) %s", table, strings.Join(missing, ", "))
	}

	if pkCount != len(shape.primaryKey) {
		return fmt.Errorf("groupchat schema %s: expected primary key (%s), found %d key column(s)", table, strings.Join(shape.primaryKey, ", "), pkCount)
	}
	for _, column := range shape.primaryKey {
		if !pkColumns[strings.ToLower(column)] {
			return fmt.Errorf("groupchat schema %s: expected primary key (%s), column %s is not part of the key", table, strings.Join(shape.primaryKey, ", "), column)
		}
	}
	return nil
}
