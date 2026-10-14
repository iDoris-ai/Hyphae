package groupchat

// Independent review probe for #230 (test-only). It instruments the real
// database/sql.Rows lifecycle on top of the modernc sqlite driver to prove
// that the injected iterator fault is observed through rows.Next/rows.Err,
// that database/sql auto-closes the driver rows exactly once, that at least
// one recipient row really streamed before the fault, and that no DB rows,
// DB/WAL bytes, or outbox bytes/mtime change.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

type probeCounters struct {
	realRows   atomic.Int32 // rows streamed by the real sqlite driver
	faultsSent atomic.Int32
	closes     atomic.Int32
	closeAfter atomic.Bool // Close arrived after the fault was returned
}

type probeRows struct {
	driver.Rows
	c       *probeCounters
	allow   int
	faulted bool
}

func (r *probeRows) Next(dest []driver.Value) error {
	if r.allow == 0 {
		r.faulted = true
		r.c.faultsSent.Add(1)
		return errSentinelIterator
	}
	r.allow--
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	r.c.realRows.Add(1)
	return nil
}

func (r *probeRows) Close() error {
	r.c.closes.Add(1)
	if r.faulted {
		r.c.closeAfter.Store(true)
	}
	return r.Rows.Close()
}

type probeConn struct {
	driver.Conn
	c     *probeCounters
	allow int
}

func (c *probeConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	if strings.Contains(q, "SELECT recipient_npub FROM groupchat_fanout") {
		return &probeRows{Rows: rows, c: c.c, allow: c.allow}, nil
	}
	return rows, nil
}

func (c *probeConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func (c *probeConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

type probeConnector struct {
	base  driver.Connector
	c     *probeCounters
	allow int
}

func (p *probeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := p.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &probeConn{Conn: conn, c: p.c, allow: p.allow}, nil
}
func (p *probeConnector) Driver() driver.Driver { return p.base.Driver() }

func probeFileDigest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	require.NoError(t, err)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func probeFanoutDump(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT recipient_npub, ` + fanoutFailureColumns + ` FROM groupchat_fanout ORDER BY recipient_npub`)
	require.NoError(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		out = append(out, fmt.Sprint(vals...))
	}
	require.NoError(t, rows.Err())
	return out
}

func TestReview230ProbeIteratorErrorRefusesPartialReport(t *testing.T) {
	for _, recipients := range []int{2, 3} {
		t.Run(fmt.Sprintf("recipients=%d", recipients), func(t *testing.T) {
			f, _ := newPreparedFixture(t, recipients)
			_, err := f.member.store.enqueuePreparedRaw(f.key, 1)
			require.NoError(t, err)
			unchanged := f.snapshot(t)
			dumpBefore := probeFanoutDump(t, f.member.db)
			require.Len(t, dumpBefore, recipients)
			dbBefore := probeFileDigest(t, f.member.dbPath)
			walBefore := probeFileDigest(t, f.member.dbPath+"-wal")

			base, err := sqlite.NewConnector(f.member.dbPath)
			require.NoError(t, err)
			c := &probeCounters{}
			db := sql.OpenDB(&probeConnector{base: base, c: c, allow: 1})
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = db.Close() })

			reports, err := (&Store{db: db}).InspectFanoutBookkeeping(f.key)
			require.ErrorIs(t, err, errSentinelIterator, "iterator error must surface")
			require.Nil(t, reports, "no partial report")
			require.EqualValues(t, 1, c.realRows.Load(), "exactly one real recipient streamed before the fault")
			require.EqualValues(t, 1, c.faultsSent.Load(), "fault delivered via Rows.Next")
			require.EqualValues(t, 1, c.closes.Load(), "driver rows closed exactly once")
			require.True(t, c.closeAfter.Load(), "database/sql auto-closed after the Next error")

			unchanged()
			require.Equal(t, dumpBefore, probeFanoutDump(t, f.member.db), "fanout rows changed")
			require.Equal(t, dbBefore, probeFileDigest(t, f.member.dbPath), "db bytes changed")
			require.Equal(t, walBefore, probeFileDigest(t, f.member.dbPath+"-wal"), "wal bytes changed")
		})
	}
}
