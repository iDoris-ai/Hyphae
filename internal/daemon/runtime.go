package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type daemonLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func newDaemonLogger(w io.Writer) *daemonLogger {
	return &daemonLogger{w: w}
}

func (l *daemonLogger) printf(format string, args ...any) {
	if l == nil {
		fmt.Printf(format, args...)
		return
	}
	if l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, format, args...)
}

func (l *daemonLogger) println(args ...any) {
	if l == nil {
		fmt.Println(args...)
		return
	}
	if l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintln(l.w, args...)
}

type daemonLoggerContextKey struct{}

func withDaemonLogger(ctx context.Context, logger *daemonLogger) context.Context {
	return context.WithValue(ctx, daemonLoggerContextKey{}, logger)
}

func daemonLoggerForContext(ctx context.Context) *daemonLogger {
	logger, _ := ctx.Value(daemonLoggerContextKey{}).(*daemonLogger)
	return logger
}

func logDaemonf(ctx context.Context, format string, args ...any) {
	if logger, ok := ctx.Value(daemonLoggerContextKey{}).(*daemonLogger); ok && logger != nil {
		logger.printf(format, args...)
		return
	}
	fmt.Printf(format, args...)
}

func newDaemonStatusGeneration() (string, error) {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", fmt.Errorf("generate daemon status generation: %w", err)
	}
	return hex.EncodeToString(randomBytes[:]), nil
}

type daemonRuntimeWork struct {
	processOutbox func() error
	scanInbox     func() error
	cleanup       func()
}

func runDaemonRuntime(
	ctx context.Context,
	logger *daemonLogger,
	status *StatusStream,
	retryInterval, watchInterval, cleanupInterval time.Duration,
	work daemonRuntimeWork,
) error {
	if logger == nil {
		logger = newDaemonLogger(os.Stdout)
	}
	if status != nil {
		if err := status.SetProcessState(StatusProcessRunning, time.Now()); err != nil {
			return err
		}
	}
	retryTicker := time.NewTicker(retryInterval)
	watchTicker := time.NewTicker(watchInterval)
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer retryTicker.Stop()
	defer watchTicker.Stop()
	defer cleanupTicker.Stop()

	runOutbox := func() error {
		if ctx.Err() != nil || work.processOutbox == nil {
			return nil
		}
		if err := work.processOutbox(); err != nil {
			if errors.Is(err, errStatusOutput) || errors.Is(err, errStatusTransition) {
				return err
			}
			logger.printf("[%s] ⚠️  Outbox processing failed: %v\n", time.Now().Format("15:04:05"), err)
		}
		return nil
	}
	runScan := func() error {
		if ctx.Err() != nil || work.scanInbox == nil {
			return nil
		}
		if err := work.scanInbox(); err != nil {
			if errors.Is(err, errStatusOutput) || errors.Is(err, errStatusTransition) {
				return err
			}
			if ctx.Err() == nil {
				logger.printf("[%s] ⚠️  Inbox scan incomplete: %v\n", time.Now().Format("15:04:05"), err)
			}
		}
		return nil
	}
	stop := func() error {
		logger.println("\n👋 Stopping daemon...")
		if status == nil {
			return nil
		}
		if err := status.SetProcessState(StatusProcessStopping, time.Now()); err != nil {
			return err
		}
		return status.SetProcessState(StatusProcessStopped, time.Now())
	}

	if ctx.Err() != nil {
		return stop()
	}
	if err := runOutbox(); err != nil {
		return err
	}
	if err := runScan(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return stop()
	}

	for {
		select {
		case <-retryTicker.C:
			if err := runOutbox(); err != nil {
				return err
			}
		case <-watchTicker.C:
			if err := runScan(); err != nil {
				return err
			}
		case <-cleanupTicker.C:
			if ctx.Err() == nil && work.cleanup != nil {
				work.cleanup()
			}
		case <-ctx.Done():
			return stop()
		}
	}
}
