package relay

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
)

// New prepares a persistent Khatru relay and returns its HTTP handler and cleanup.
func New(cfg Config) (http.Handler, func(), error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create relay data directory: %w", err)
	}
	if err := os.Chmod(cfg.DataDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("secure relay data directory: %w", err)
	}
	store := &boltdb.BoltBackend{Path: filepath.Join(cfg.DataDir, "events.db")}
	if err := store.Init(); err != nil {
		return nil, nil, fmt.Errorf("initialize relay event store: %w", err)
	}
	r := khatru.NewRelay()
	r.UseEventstore(store, 500)
	tracked := &connectionTracker{handler: r, conns: make(map[net.Conn]struct{})}
	return tracked, func() {
		tracked.closeConnections()
		tracked.handlers.Wait()
		r.DisableExpirationManager()
		store.Close()
	}, nil
}

type connectionTracker struct {
	handler  http.Handler
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closing  bool
	handlers sync.WaitGroup
}

func (t *connectionTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		http.Error(w, "relay is shutting down", http.StatusServiceUnavailable)
		return
	}
	t.handlers.Add(1)
	t.mu.Unlock()
	defer t.handlers.Done()
	t.handler.ServeHTTP(&trackedResponseWriter{ResponseWriter: w, tracker: t}, r)
}

func (t *connectionTracker) register(conn net.Conn) net.Conn {
	tracked := &trackedConn{Conn: conn, tracker: t}
	t.mu.Lock()
	closing := t.closing
	if !closing {
		t.conns[tracked] = struct{}{}
	}
	t.mu.Unlock()
	if closing {
		_ = tracked.Close()
	}
	return tracked
}

func (t *connectionTracker) remove(conn net.Conn) {
	t.mu.Lock()
	delete(t.conns, conn)
	t.mu.Unlock()
}

func (t *connectionTracker) closeConnections() {
	t.mu.Lock()
	t.closing = true
	conns := make([]net.Conn, 0, len(t.conns))
	for conn := range t.conns {
		conns = append(conns, conn)
	}
	t.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

type trackedResponseWriter struct {
	http.ResponseWriter
	tracker *connectionTracker
}

func (w *trackedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("HTTP response writer does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return w.tracker.register(conn), rw, nil
}

type trackedConn struct {
	net.Conn
	tracker *connectionTracker
	once    sync.Once
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.tracker.remove(c)
	})
	return err
}
