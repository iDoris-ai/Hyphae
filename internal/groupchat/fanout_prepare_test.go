package groupchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func prepareEnvelope(g *threeMemberGroup, id string) Envelope {
	return Envelope{Version: Version, Type: EnvelopeMessage, GroupID: g.draft.Group.ID, LogicalID: id, Body: "private S4 message"}
}

func prepareKey(g *threeMemberGroup, e Envelope) FanoutKey {
	return FanoutKey{LocalNpub: g.alice.npub, GroupID: e.GroupID, EnvelopeType: e.Type, SendKey: e.LogicalID}
}

func TestPrepareMessageFanoutFrozenReentry(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	e := prepareEnvelope(g, mustOpaque(t))
	report, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 1234, 7)
	require.NoError(t, err)
	require.Len(t, report.Recipients, 2)
	require.EqualValues(t, 1234, report.CreatedAt)
	require.Equal(t, 2, report.Queued)
	encoded, err := Encode(e)
	require.NoError(t, err)
	rows, err := g.alice.db.Query(`SELECT recipient_npub,event_json FROM groupchat_fanout ORDER BY recipient_npub`)
	require.NoError(t, err)
	events := map[string]string{}
	ids := map[string]bool{}
	contents := map[string]bool{}
	ds := map[string]bool{}
	for rows.Next() {
		var recipient, raw string
		require.NoError(t, rows.Scan(&recipient, &raw))
		events[recipient] = raw
		require.NotContains(t, raw, e.Body)
		require.NotContains(t, raw, g.draft.Group.Name)
		var event nostr.Event
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		member := g.member(recipient)
		v, err := VerifyIncoming(event, member.sk)
		require.NoError(t, err)
		require.Equal(t, encoded, v.Plaintext())
		require.Equal(t, g.alice.npub, v.SenderNpub())
		require.EqualValues(t, 1234, v.CreatedAt())
		_, err = VerifyIncoming(event, g.alice.sk)
		require.Error(t, err)
		require.False(t, ids[event.ID.Hex()])
		ids[event.ID.Hex()] = true
		require.False(t, contents[event.Content])
		contents[event.Content] = true
		for _, tag := range event.Tags {
			if len(tag) == 2 && tag[0] == "d" {
				require.False(t, ds[tag[1]])
				ds[tag[1]] = true
			}
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Len(t, ds, 2)
	// Reentry with a different requested time must never call encryption/signing.
	tx, err := g.alice.store.beginImmediate()
	require.NoError(t, err)
	calls := 0
	err = g.alice.store.prepareFanoutTx(tx, g.alice.sk, e, 9999, 7, func(nostr.SecretKey, nostr.PubKey, string, nostr.Timestamp) (*nostr.Event, error) {
		calls++
		return nil, errors.New("must not sign")
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Zero(t, calls)
	replay, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 9999, 7)
	require.NoError(t, err)
	require.Equal(t, report, replay)
	for recipient, raw := range events {
		var got string
		require.NoError(t, g.alice.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE recipient_npub=?`, recipient).Scan(&got))
		require.Equal(t, raw, got)
	}
	history, err := g.alice.store.GroupMessages(g.alice.npub, e.GroupID, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Empty(t, history[0].EventID)
	require.EqualValues(t, 1234, history[0].CreatedAt)
	for _, r := range report.Recipients {
		require.Empty(t, r.QueueID)
		require.Nil(t, r.RetryCount)
		require.Zero(t, r.Attempts)
		require.Equal(t, "native", r.AccountingOrigin)
		require.Equal(t, "idle", r.AttemptPhase)
	}
	_, err = g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, "different", 1234, 7)
	require.ErrorIs(t, err, ErrLogicalIDConflict)
}

func TestPrepareMessageFanoutRollback(t *testing.T) {
	for _, cut := range []string{"history", "metadata", "first recipient", "second recipient", "commit"} {
		t.Run(cut, func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			_, err := g.alice.db.Exec(`UPDATE groupchat_groups SET updated_at=1`)
			require.NoError(t, err)
			statement := ""
			switch cut {
			case "history":
				statement = `CREATE TRIGGER cut BEFORE INSERT ON groupchat_messages BEGIN SELECT RAISE(ABORT,'history cut'); END`
			case "metadata":
				statement = `CREATE TRIGGER cut BEFORE UPDATE ON groupchat_groups BEGIN SELECT RAISE(ABORT,'metadata cut'); END`
			case "first recipient":
				statement = `CREATE TRIGGER cut BEFORE INSERT ON groupchat_fanout BEGIN SELECT RAISE(ABORT,'fanout cut'); END`
			case "second recipient":
				statement = `CREATE TRIGGER cut BEFORE INSERT ON groupchat_fanout WHEN (SELECT COUNT(*) FROM groupchat_fanout)=1 BEGIN SELECT RAISE(ABORT,'fanout cut'); END`
			case "commit":
				_, err = g.alice.db.Exec(`PRAGMA foreign_keys=ON`)
				require.NoError(t, err)
				_, err = g.alice.db.Exec(`CREATE TABLE commit_parent(id INTEGER PRIMARY KEY)`)
				require.NoError(t, err)
				_, err = g.alice.db.Exec(`CREATE TABLE commit_child(id INTEGER REFERENCES commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
				require.NoError(t, err)
				statement = `CREATE TRIGGER cut AFTER INSERT ON groupchat_fanout BEGIN INSERT INTO commit_child VALUES(1); END`
			}
			_, err = g.alice.db.Exec(statement)
			require.NoError(t, err)
			_, err = g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 1234, 7)
			require.Error(t, err)
			for _, table := range []string{"groupchat_messages", "groupchat_fanout"} {
				var n int
				require.NoError(t, g.alice.db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
				require.Zero(t, n)
			}
			var updated int
			require.NoError(t, g.alice.db.QueryRow(`SELECT updated_at FROM groupchat_groups`).Scan(&updated))
			require.Equal(t, 1, updated)
		})
	}
}

func TestPrepareMessageFanoutSigningRollback(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			tx, err := g.alice.store.beginImmediate()
			require.NoError(t, err)
			calls := 0
			err = g.alice.store.prepareFanoutTx(tx, g.alice.sk, e, 1234, 7, func(sk nostr.SecretKey, pk nostr.PubKey, text string, at nostr.Timestamp) (*nostr.Event, error) {
				calls++
				if calls == failAt {
					return nil, errors.New("crypto cut")
				}
				return messaging.BuildAgentMessageEvent(sk, pk, text, at)
			})
			require.ErrorContains(t, err, "crypto cut")
			require.NoError(t, tx.Rollback())
			var n int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&n))
			require.Zero(t, n)
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
			require.Zero(t, n)
		})
	}
}

func TestPrepareMessageFanoutSecurity(t *testing.T) {
	for _, mutation := range []string{"pending group", "left group", "cancelled group", "missing member", "inactive recipient", "inactive sender", "roster hash", "unsorted roster", "duplicate roster", "extra active member", "zero key", "negative time", "zero budget", "blank body", "invalid logical ID"} {
		t.Run(mutation, func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			sk := g.alice.sk
			at := nostr.Timestamp(1234)
			budget := 7
			var err error
			switch mutation {
			case "pending group", "left group", "cancelled group":
				state := map[string]GroupState{"pending group": StatePending, "left group": StateLeft, "cancelled group": StateCancelled}[mutation]
				_, err = g.alice.db.Exec(`UPDATE groupchat_groups SET state=?`, state)
			case "missing member":
				_, err = g.alice.db.Exec(`DELETE FROM groupchat_members WHERE npub=?`, g.bob.npub)
			case "inactive recipient":
				_, err = g.alice.db.Exec(`UPDATE groupchat_members SET state='accepted' WHERE npub=?`, g.bob.npub)
			case "inactive sender":
				_, err = g.alice.db.Exec(`UPDATE groupchat_members SET state='accepted' WHERE npub=?`, g.alice.npub)
			case "roster hash":
				_, err = g.alice.db.Exec(`UPDATE groupchat_groups SET roster_hash=?`, mustOpaque(t)+mustOpaque(t))
			case "unsorted roster", "duplicate roster":
				roster := append([]string(nil), g.draft.Group.Roster...)
				if mutation == "unsorted roster" {
					roster[0], roster[1] = roster[1], roster[0]
				} else {
					roster[1] = roster[0]
				}
				raw, _ := json.Marshal(roster)
				_, err = g.alice.db.Exec(`UPDATE groupchat_groups SET roster_json=?`, string(raw))
			case "extra active member":
				_, err = g.alice.db.Exec(`INSERT INTO groupchat_members(local_npub,group_id,npub,state) VALUES(?,?,?,'active')`, g.alice.npub, e.GroupID, newMember(t).npub)
			case "zero key":
				sk = nostr.SecretKey{}
			case "negative time":
				at = -1
			case "zero budget":
				budget = 0
			case "blank body":
				e.Body = "  "
			case "invalid logical ID":
				e.LogicalID = "not-an-id"
			}
			require.NoError(t, err)
			_, err = g.alice.store.PrepareMessageFanout(sk, e.GroupID, e.LogicalID, e.Body, at, budget)
			require.Error(t, err)
			var n int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&n))
			require.Zero(t, n)
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
			require.Zero(t, n)
		})
	}
}

func TestPrepareMessageFanoutRejectsBuilderOutput(t *testing.T) {
	for _, mutation := range []string{"nil", "wrong kind", "wrong author", "wrong time", "wrong recipient", "duplicate p", "plaintext", "wrong encryption", "bad ID", "bad signature", "wrong envelope", "bad ciphertext"} {
		t.Run(mutation, func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			tx, err := g.alice.store.beginImmediate()
			require.NoError(t, err)
			err = g.alice.store.prepareFanoutTx(tx, g.alice.sk, e, 1234, 7, func(sk nostr.SecretKey, pk nostr.PubKey, text string, at nostr.Timestamp) (*nostr.Event, error) {
				if mutation == "nil" {
					return nil, nil
				}
				if mutation == "wrong envelope" {
					text += "changed"
				}
				event, err := messaging.BuildAgentMessageEvent(sk, pk, text, at)
				if err != nil {
					return nil, err
				}
				switch mutation {
				case "wrong kind":
					event.Kind = 1
				case "wrong author":
					sk = g.bob.sk
					event.PubKey = sk.Public()
				case "wrong time":
					event.CreatedAt++
				case "wrong recipient":
					event.Tags[0][1] = g.alice.sk.Public().Hex()
				case "duplicate p":
					event.Tags = append(event.Tags, nostr.Tag{"p", pk.Hex()})
				case "plaintext":
					event.Content = text
					event.Tags = event.Tags[:len(event.Tags)-1]
				case "wrong encryption":
					event.Tags[len(event.Tags)-1][1] = "other"
				case "bad ciphertext":
					event.Content = "invalid"
				}
				if err = event.Sign(sk); err != nil {
					return nil, err
				}
				if mutation == "bad ID" {
					event.ID[0] ^= 1
				}
				if mutation == "bad signature" {
					event.Sig[0] ^= 1
				}
				return event, nil
			})
			require.Error(t, err)
			require.NoError(t, tx.Rollback())
			var n int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&n))
			require.Zero(t, n)
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
			require.Zero(t, n)
		})
	}
}

func TestPrepareMessageFanoutHistoryAndQueueIdentity(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	e := prepareEnvelope(g, mustOpaque(t))
	_, err := g.alice.store.StoreLocalMessage(g.alice.npub, e.GroupID, e.LogicalID, e.Body, 1000)
	require.NoError(t, err)
	report, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 2000, 7)
	require.NoError(t, err)
	require.EqualValues(t, 1000, report.CreatedAt)
	key := prepareKey(g, e)
	tx, err := g.alice.store.beginImmediate()
	require.NoError(t, err)
	ok, err := transitionFanoutTx(tx, key, report.Recipients[0].RecipientNpub, RecipientPrepared, RecipientQueued, map[string]any{"queue_id": "existing-queue"})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, tx.Commit())
	before, err := g.alice.store.LoadFanoutReport(key)
	require.NoError(t, err)
	after, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 3000, 7)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, "existing-queue", after.Recipients[0].QueueID)
}

func TestPrepareMessageFanoutPartialIntentFailsClosed(t *testing.T) {
	for _, mutation := range []string{"missing recipient", "missing history", "changed event", "changed time", "changed budget", "different requested budget", "foreign history event"} {
		t.Run(mutation, func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			report, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 1234, 7)
			require.NoError(t, err)
			recipient := report.Recipients[0].RecipientNpub
			budget := 7
			switch mutation {
			case "missing recipient":
				_, err = g.alice.db.Exec(`DELETE FROM groupchat_fanout WHERE recipient_npub=?`, recipient)
			case "missing history":
				_, err = g.alice.db.Exec(`DELETE FROM groupchat_messages`)
			case "changed event":
				_, err = g.alice.db.Exec(`UPDATE groupchat_fanout SET event_json='{}' WHERE recipient_npub=?`, recipient)
			case "changed time":
				_, err = g.alice.db.Exec(`UPDATE groupchat_fanout SET created_at=999 WHERE recipient_npub=?`, recipient)
			case "changed budget":
				_, err = g.alice.db.Exec(`UPDATE groupchat_fanout SET max_retries=8 WHERE recipient_npub=?`, recipient)
			case "different requested budget":
				budget = 8
			case "foreign history event":
				_, err = g.alice.db.Exec(`UPDATE groupchat_messages SET event_id=?`, mustOpaque(t)+mustOpaque(t))
			}
			require.NoError(t, err)
			var before int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&before))
			_, err = g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, 9999, budget)
			require.Error(t, err)
			var after int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&after))
			require.Equal(t, before, after)
		})
	}
}

func TestPrepareMessageFanoutCallerRollbackAndKind(t *testing.T) {
	for _, kind := range []EnvelopeType{EnvelopeMessage, EnvelopeInvite, EnvelopeActivate, EnvelopeAccept, EnvelopeCancel, EnvelopeDecline} {
		t.Run(string(kind), func(t *testing.T) {
			g := newThreeMemberGroup(t)
			g.activate()
			e := prepareEnvelope(g, mustOpaque(t))
			e.Type = kind
			tx, err := g.alice.store.beginImmediate()
			require.NoError(t, err)
			err = g.alice.store.prepareFanoutTx(tx, g.alice.sk, e, 1234, 7, messaging.BuildAgentMessageEvent)
			if kind == EnvelopeMessage {
				require.NoError(t, err)
				var n int
				require.NoError(t, tx.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
				require.Equal(t, 2, n)
			} else {
				require.ErrorIs(t, err, ErrProtocolMismatch)
			}
			require.NoError(t, tx.Rollback())
			var n int
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&n))
			require.Zero(t, n)
			require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
			require.Zero(t, n)
		})
	}
}

func TestPrepareMessageFanoutConcurrentReentry(t *testing.T) {
	g := poolGroup(t)
	g.activate()
	e := prepareEnvelope(g, mustOpaque(t))
	type result struct {
		report FanoutReport
		err    error
	}
	results := make(chan result, 8)
	for i := range 8 {
		go func() {
			r, err := g.alice.store.PrepareMessageFanout(g.alice.sk, e.GroupID, e.LogicalID, e.Body, nostr.Timestamp(1234+i), 7)
			results <- result{r, err}
		}()
	}
	first := <-results
	require.NoError(t, first.err)
	for range 7 {
		r := <-results
		require.NoError(t, r.err)
		require.Equal(t, first.report, r.report)
	}
	var n int
	require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&n))
	require.Equal(t, 2, n)
}
