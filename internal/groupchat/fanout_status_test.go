package groupchat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

func TestInspectFanoutBookkeepingClasses(t *testing.T) {
	cases := map[string]struct {
		arrange func(t *testing.T, f failureFixture)
		want    FanoutBookkeeping
	}{
		"normal queued": {func(*testing.T, failureFixture) {}, BookkeepingNormal},
		"pending failure": {func(t *testing.T, f failureFixture) {
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
		}, BookkeepingPendingFailure},
		"pending recovery": {func(t *testing.T, f failureFixture) {
			f.crashD6BeforeT2(t)
		}, BookkeepingPendingRecovery},
		"cleanup pending": {func(t *testing.T, f failureFixture) {
			token := f.startAttempt(t, 1)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=4`)
			require.NoError(t, err)
			require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, time.Now().Unix()), ErrFanoutCleanupPending)
		}, BookkeepingCleanupPending},
		"accepted and cleaned": {func(t *testing.T, f failureFixture) {
			require.NoError(t, f.member.store.commitFanoutAccepted(f.startAttempt(t, 1), time.Now().Unix()))
		}, BookkeepingNormal},
		"queue missing": {func(t *testing.T, f failureFixture) {
			require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
		}, BookkeepingQueueMissing},
		"queue conflict": {func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
			require.NoError(t, err)
		}, BookkeepingQueueConflict},
		"bookkeeping conflict": {func(t *testing.T, f failureFixture) {
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
			require.NoError(t, err)
		}, BookkeepingConflict},
		"file corrupt": {func(t *testing.T, f failureFixture) {
			data, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(f.outboxPath, append(data, '{'), 0600))
		}, BookkeepingFileCorrupt},
		"failed with exhausted entry": {func(t *testing.T, f failureFixture) {
			f.exhaust(t)
		}, BookkeepingNormal},
		"failed, entry cleaned": {func(t *testing.T, f failureFixture) {
			f.exhaust(t)
			require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
		}, BookkeepingNormal},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			c.arrange(t, f)
			unchanged := f.snapshot(t)
			got, err := f.member.store.inspectFanoutBookkeeping(f.key, f.recipient)
			require.NoError(t, err)
			require.Equal(t, c.want, got)
			unchanged()
		})
	}
}

func TestInspectFanoutBookkeepingMissingRow(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.store.inspectFanoutBookkeeping(f.key, "npub1nobody")
	require.Error(t, err)
	_, err = f.member.store.inspectFanoutBookkeeping(FanoutKey{LocalNpub: "bad"}, f.recipient)
	require.Error(t, err)
}

func TestInspectFanoutBookkeepingByKey(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	_, err := f.member.store.enqueuePreparedRaw(f.key, 1)
	require.NoError(t, err)
	unchanged := f.snapshot(t)

	reports, err := f.member.store.InspectFanoutBookkeeping(f.key)
	require.NoError(t, err)
	require.Len(t, reports, 2)
	require.True(t, reports[0].RecipientNpub < reports[1].RecipientNpub, "npub order")
	for _, r := range reports {
		require.Contains(t, npubs, r.RecipientNpub)
		require.Equal(t, BookkeepingNormal, r.Bookkeeping)
	}
	unchanged()

	// One recipient left with an unconsumed O3: only that recipient changes class.
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	unchanged = f.snapshot(t)
	reports, err = f.member.store.InspectFanoutBookkeeping(f.key)
	require.NoError(t, err)
	classes := map[string]FanoutBookkeeping{}
	for _, r := range reports {
		classes[r.RecipientNpub] = r.Bookkeeping
	}
	require.Equal(t, BookkeepingPendingFailure, classes[f.recipient])
	for _, n := range npubs {
		if n != f.recipient {
			require.Equal(t, BookkeepingNormal, classes[n])
		}
	}
	unchanged()

	// Unknown key: empty, not an error. Bad npub: error.
	empty, err := f.member.store.InspectFanoutBookkeeping(FanoutKey{LocalNpub: f.key.LocalNpub, GroupID: f.key.GroupID, EnvelopeType: f.key.EnvelopeType, SendKey: "nope"})
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NotNil(t, empty)
	_, err = f.member.store.InspectFanoutBookkeeping(FanoutKey{LocalNpub: "bad"})
	require.Error(t, err)
}

var errSentinelIterator = errors.New("sentinel iterator error")

type faultRows struct {
	rows                     driver.Rows
	remainingRowsBeforeError int
	err                      error
	inject                   bool
}

func (r *faultRows) Columns() []string { return r.rows.Columns() }
func (r *faultRows) Close() error      { return r.rows.Close() }
func (r *faultRows) Next(dest []driver.Value) error {
	if r.inject {
		if r.remainingRowsBeforeError > 0 {
			r.remainingRowsBeforeError--
			return r.rows.Next(dest)
		}
		return r.err
	}
	return r.rows.Next(dest)
}

type faultStmt struct {
	stmt      driver.Stmt
	inject    bool
	err       error
	afterRows int
}

func (s *faultStmt) Close() error                                    { return s.stmt.Close() }
func (s *faultStmt) NumInput() int                                   { return s.stmt.NumInput() }
func (s *faultStmt) Exec(args []driver.Value) (driver.Result, error) { return s.stmt.Exec(args) }
func (s *faultStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := s.stmt.Query(args)
	if err != nil {
		return nil, err
	}
	return &faultRows{rows: rows, remainingRowsBeforeError: s.afterRows, err: s.err, inject: s.inject}, nil
}
func (s *faultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if qc, ok := s.stmt.(driver.StmtQueryContext); ok {
		rows, err := qc.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}
		return &faultRows{rows: rows, remainingRowsBeforeError: s.afterRows, err: s.err, inject: s.inject}, nil
	}
	return nil, errors.New("stmt query context not supported")
}
func (s *faultStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if ec, ok := s.stmt.(driver.StmtExecContext); ok {
		return ec.ExecContext(ctx, args)
	}
	return nil, errors.New("stmt exec context not supported")
}

type faultConn struct {
	conn      driver.Conn
	inject    bool
	targetSub string
	err       error
	afterRows int
}

func (c *faultConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &faultStmt{
		stmt:      stmt,
		inject:    c.inject && (c.targetSub == "" || strings.Contains(query, c.targetSub)),
		err:       c.err,
		afterRows: c.afterRows,
	}, nil
}

func (c *faultConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if pc, ok := c.conn.(driver.ConnPrepareContext); ok {
		stmt, err = pc.PrepareContext(ctx, query)
	} else {
		stmt, err = c.conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &faultStmt{
		stmt:      stmt,
		inject:    c.inject && (c.targetSub == "" || strings.Contains(query, c.targetSub)),
		err:       c.err,
		afterRows: c.afterRows,
	}, nil
}

func (c *faultConn) Close() error              { return c.conn.Close() }
func (c *faultConn) Begin() (driver.Tx, error) { return c.conn.Begin() }
func (c *faultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if bt, ok := c.conn.(driver.ConnBeginTx); ok {
		return bt.BeginTx(ctx, opts)
	}
	return c.conn.Begin()
}
func (c *faultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if ec, ok := c.conn.(driver.ExecerContext); ok {
		return ec.ExecContext(ctx, query, args)
	}
	return nil, errors.New("execer context not supported")
}
func (c *faultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, errors.New("queryer context not supported")
	}
	rows, err := qc.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	shouldInject := c.inject && (c.targetSub == "" || strings.Contains(query, c.targetSub))
	return &faultRows{
		rows:                     rows,
		remainingRowsBeforeError: c.afterRows,
		err:                      c.err,
		inject:                   shouldInject,
	}, nil
}
func (c *faultConn) ResetSession(ctx context.Context) error {
	if rs, ok := c.conn.(driver.SessionResetter); ok {
		return rs.ResetSession(ctx)
	}
	return nil
}

type faultConnector struct {
	base      driver.Connector
	inject    bool
	targetSub string
	err       error
	afterRows int
}

func (c *faultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &faultConn{
		conn:      conn,
		inject:    c.inject,
		targetSub: c.targetSub,
		err:       c.err,
		afterRows: c.afterRows,
	}, nil
}

func (c *faultConnector) Driver() driver.Driver { return c.base.Driver() }

func newFaultDB(t *testing.T, dbPath string, inject bool, afterRows int) *sql.DB {
	t.Helper()
	baseConn, err := sqlite.NewConnector(dbPath)
	require.NoError(t, err)
	db := sql.OpenDB(&faultConnector{
		base:      baseConn,
		inject:    inject,
		targetSub: "recipient_npub FROM groupchat_fanout",
		err:       errSentinelIterator,
		afterRows: afterRows,
	})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInspectFanoutBookkeepingRowsIteratorDriverSeam(t *testing.T) {
	t.Run("fault injection yields sentinel error and no mutation", func(t *testing.T) {
		f, _ := newPreparedFixture(t, 2)
		_, err := f.member.store.enqueuePreparedRaw(f.key, 1)
		require.NoError(t, err)
		unchanged := f.snapshot(t)

		faultDB := newFaultDB(t, f.member.dbPath, true, 1)
		faultStore := &Store{db: faultDB}

		reports, err := faultStore.InspectFanoutBookkeeping(f.key)
		require.Error(t, err)
		require.ErrorIs(t, err, errSentinelIterator)
		require.Nil(t, reports)
		unchanged()
	})

	t.Run("full enumeration control remains valid", func(t *testing.T) {
		f, npubs := newPreparedFixture(t, 2)
		_, err := f.member.store.enqueuePreparedRaw(f.key, 1)
		require.NoError(t, err)
		unchanged := f.snapshot(t)

		controlDB := newFaultDB(t, f.member.dbPath, false, 0)
		controlStore := &Store{db: controlDB}

		reports, err := controlStore.InspectFanoutBookkeeping(f.key)
		require.NoError(t, err)
		require.Len(t, reports, 2)
		require.True(t, reports[0].RecipientNpub < reports[1].RecipientNpub, "npub order")
		for _, r := range reports {
			require.Contains(t, npubs, r.RecipientNpub)
			require.Equal(t, BookkeepingNormal, r.Bookkeeping)
		}
		unchanged()
	})
}
