package groupchat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// TestFanoutAttemptHandlerReadsShareImmediateTx fails when Reserve's event
// lookup or Failed's state read runs outside the BEGIN IMMEDIATE transaction
// that mutates the same row.
func TestFanoutAttemptHandlerReadsShareImmediateTx(t *testing.T) {
	f := newFailureFixture(t)
	trace := attachFanoutSQLTrace(t, f.member.store, f.member.dbPath)
	handler := NewFanoutAttemptHandler(f.member.store)

	token, err := handler.Reserve(f.typedEntry(t))
	require.NoError(t, err)
	trace.requireReadSharesImmediateTx(t,
		"SELECT local_npub,group_id,envelope_type,send_key,recipient_npub FROM groupchat_fanout WHERE event_id=?",
		"attempt_phase='reserved'",
	)

	trace.reset()
	require.NoError(t, handler.Start(token, 1, time.Now().Unix()))
	trace.reset()
	exhausted, err := handler.Failed(token)
	require.NoError(t, err)
	require.False(t, exhausted, "first failure still has retry budget")
	trace.requireReadSharesImmediateTx(t,
		"SELECT state FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?",
		"attempt_phase='failed'",
	)
}

type tracedSQL struct {
	conn  int
	inTx  bool
	query string
}

type fanoutSQLTrace struct {
	db     *sql.DB
	mu     sync.Mutex
	events []tracedSQL
}

func attachFanoutSQLTrace(t *testing.T, store *Store, dbPath string) *fanoutSQLTrace {
	t.Helper()
	trace := &fanoutSQLTrace{}
	base, err := sqlite.NewConnector(dbPath)
	require.NoError(t, err)
	trace.db = sql.OpenDB(&fanoutTraceConnector{base: base, trace: trace})
	t.Cleanup(func() { _ = trace.db.Close() })
	store.db = trace.db
	return trace
}

func (l *fanoutSQLTrace) reset() {
	l.mu.Lock()
	l.events = nil
	l.mu.Unlock()
}

func (l *fanoutSQLTrace) add(ev tracedSQL) {
	l.mu.Lock()
	l.events = append(l.events, ev)
	l.mu.Unlock()
}

func (l *fanoutSQLTrace) snapshot() []tracedSQL {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]tracedSQL, len(l.events))
	copy(out, l.events)
	return out
}

// requireReadSharesImmediateTx fails unless every read ran inside a BEGIN
// IMMEDIATE transaction on the same connection as mutation, with no commit
// between them.
func (l *fanoutSQLTrace) requireReadSharesImmediateTx(t *testing.T, read, mutation string) {
	t.Helper()
	events := l.snapshot()
	found := false
	for i, ev := range events {
		if !strings.Contains(ev.query, read) {
			continue
		}
		found = true
		require.Truef(t, ev.inTx, "read outside BEGIN IMMEDIATE:\n%s\ntrace:\n%s", ev.query, formatFanoutSQLTrace(events))
		span := sameImmediateSpan(events, i)
		require.Truef(t, spanHasPrefix(span, "BEGIN"), "read has no BEGIN IMMEDIATE on its connection:\n%s", formatFanoutSQLTrace(events))
		require.Truef(t, spanContains(span, mutation), "read is not in the same BEGIN IMMEDIATE transaction as %s\ntrace:\n%s", mutation, formatFanoutSQLTrace(span))
	}
	require.Truef(t, found, "read not observed: %s\ntrace:\n%s", read, formatFanoutSQLTrace(events))
}

func sameImmediateSpan(events []tracedSQL, idx int) []tracedSQL {
	conn := events[idx].conn
	start := idx
	for i := idx - 1; i >= 0; i-- {
		if events[i].conn != conn {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(events[i].query))
		if strings.HasPrefix(upper, "COMMIT") || strings.HasPrefix(upper, "ROLLBACK") || strings.HasPrefix(upper, "END") {
			break
		}
		start = i
		if strings.HasPrefix(upper, "BEGIN") {
			break
		}
	}
	end := idx
	for i := idx + 1; i < len(events); i++ {
		if events[i].conn != conn {
			continue
		}
		end = i
		upper := strings.ToUpper(strings.TrimSpace(events[i].query))
		if strings.HasPrefix(upper, "COMMIT") || strings.HasPrefix(upper, "ROLLBACK") || strings.HasPrefix(upper, "END") {
			break
		}
	}
	span := make([]tracedSQL, 0, end-start+1)
	for i := start; i <= end; i++ {
		if events[i].conn == conn {
			span = append(span, events[i])
		}
	}
	return span
}

func spanHasPrefix(span []tracedSQL, prefix string) bool {
	for _, ev := range span {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(ev.query)), prefix) {
			return true
		}
	}
	return false
}

func spanContains(span []tracedSQL, needle string) bool {
	for _, ev := range span {
		if strings.Contains(ev.query, needle) && ev.inTx {
			return true
		}
	}
	return false
}

func formatFanoutSQLTrace(events []tracedSQL) string {
	var b strings.Builder
	for _, ev := range events {
		state := "autocommit"
		if ev.inTx {
			state = "in-tx"
		}
		fmt.Fprintf(&b, "%s conn=%d %s\n", state, ev.conn, strings.TrimSpace(ev.query))
	}
	return b.String()
}

var fanoutTraceConnSeq atomic.Int64

type fanoutTraceConnector struct {
	base  driver.Connector
	trace *fanoutSQLTrace
}

func (c *fanoutTraceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &fanoutTraceConn{conn: conn, trace: c.trace, id: int(fanoutTraceConnSeq.Add(1))}, nil
}

func (c *fanoutTraceConnector) Driver() driver.Driver { return c.base.Driver() }

type fanoutTraceConn struct {
	conn  driver.Conn
	trace *fanoutSQLTrace
	id    int
	inTx  bool
}

func (c *fanoutTraceConn) Prepare(query string) (driver.Stmt, error) { return c.conn.Prepare(query) }
func (c *fanoutTraceConn) Close() error                              { return c.conn.Close() }
func (c *fanoutTraceConn) Begin() (driver.Tx, error)                 { return c.conn.Begin() }

func (c *fanoutTraceConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, query)
	}
	return c.conn.Prepare(query)
}

func (c *fanoutTraceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if bt, ok := c.conn.(driver.ConnBeginTx); ok {
		return bt.BeginTx(ctx, opts)
	}
	return c.conn.Begin()
}

func (c *fanoutTraceConn) ResetSession(ctx context.Context) error {
	if rs, ok := c.conn.(driver.SessionResetter); ok {
		return rs.ResetSession(ctx)
	}
	return nil
}

func (c *fanoutTraceConn) Ping(ctx context.Context) error {
	if p, ok := c.conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *fanoutTraceConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *fanoutTraceConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := ec.ExecContext(ctx, query, args)
	c.observe(query, err)
	return res, err
}

func (c *fanoutTraceConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := qc.QueryContext(ctx, query, args)
	c.observe(query, err)
	return rows, err
}

func (c *fanoutTraceConn) observe(query string, err error) {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if err == nil && strings.HasPrefix(upper, "BEGIN") {
		c.inTx = true
	}
	c.trace.add(tracedSQL{conn: c.id, inTx: c.inTx, query: query})
	if err == nil && (strings.HasPrefix(upper, "COMMIT") || strings.HasPrefix(upper, "ROLLBACK") || strings.HasPrefix(upper, "END")) {
		c.inTx = false
	}
}
