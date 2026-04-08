// Package view: SSE streaming I/O for the embeddable session browser.
//
// The patterns here mirror memory/pkg/tui/view/io.go and
// flow/pkg/tui/status/io.go: every piece of stream lifecycle state lives
// on the Model (via a *streamLifecycle pointer) so multiple instances of
// the panel can coexist inside a host multiplexer without sharing
// channels or cancel funcs through package globals.
//
// The flow status fix from earlier this session is the canonical
// reference for the WaitGroup-based teardown — Close() must Wait() for
// the in-flight read goroutine to exit after cancelling the stream
// context, otherwise host workspace switches leak listener goroutines.
package view

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

// streamLifecycle owns the SSE stream channel + cancel func + waitgroup
// for one Model instance. It is referenced by pointer from Model so that
// the bubbletea value-receiver Update path doesn't copy the sync
// primitives (which would be undefined behavior).
type streamLifecycle struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	ch        <-chan daemon.StateUpdate
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func newStreamLifecycle() *streamLifecycle {
	return &streamLifecycle{}
}

// daemonStreamConnectedMsg carries the SSE stream channel + cancel func
// back to the Model so it can store them and start consuming updates.
type daemonStreamConnectedMsg struct {
	ch     <-chan daemon.StateUpdate
	cancel context.CancelFunc
}

// daemonStreamErrorMsg is dispatched when the initial stream subscription
// fails. The Model treats it as non-fatal — the panel just stays on its
// most recent fetched session list without live updates.
type daemonStreamErrorMsg struct {
	err error
}

// daemonStateUpdateMsg wraps a single SSE update for Update() dispatch.
type daemonStateUpdateMsg struct {
	update daemon.StateUpdate
}

// sessionsRefetchedMsg is the result of an async session refetch kicked
// off after an SSE update arrives that doesn't carry the full session
// list (e.g., a single-session lifecycle event). Carrying it through a
// dedicated message keeps the daemon RPC off the bubbletea event loop.
type sessionsRefetchedMsg struct {
	sessions []*models.Session
	err      error
}

// subscribeToDaemonCmd opens an SSE stream against the shared daemon
// client and returns a daemonStreamConnectedMsg carrying the channel +
// cancel so the Model owns the lifecycle and can tear it down via
// Close(). Returns nil if the client is unavailable so the model
// gracefully degrades to a static initial-fetch view.
func subscribeToDaemonCmd(client daemon.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil || !client.IsRunning() {
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := client.StreamState(ctx)
		if err != nil {
			cancel()
			return daemonStreamErrorMsg{err: err}
		}
		return daemonStreamConnectedMsg{ch: ch, cancel: cancel}
	}
}

// readDaemonStreamCmd pulls one update from the channel and returns it
// as a daemonStateUpdateMsg. If the channel is closed, the command
// returns nil and the Update loop stops chaining reads.
//
// The read goroutine is tracked by the Model's streamLifecycle.wg so
// Close() can wait for it to exit after cancelling the stream context.
// wg.Add is called synchronously (outside the returned closure) so
// Close()'s Wait() cannot race past an Add that has not yet fired.
func (s *streamLifecycle) readDaemonStreamCmd(ch <-chan daemon.StateUpdate) tea.Cmd {
	if ch == nil || s == nil {
		return nil
	}
	s.wg.Add(1)
	return func() tea.Msg {
		defer s.wg.Done()
		u, ok := <-ch
		if !ok {
			return nil
		}
		return daemonStateUpdateMsg{update: u}
	}
}

// fetchSessionsCmd asynchronously refetches the session list via the
// configured loader. Used when an SSE update lands but doesn't carry the
// full Sessions slice (e.g., single-session lifecycle deltas).
func fetchSessionsCmd(load GetAllSessionsFunc, client daemon.Client, hideCompleted bool) tea.Cmd {
	if load == nil || client == nil {
		return nil
	}
	return func() tea.Msg {
		s, err := load(client, hideCompleted)
		return sessionsRefetchedMsg{sessions: s, err: err}
	}
}

// close cancels the stream context (closing the SSE channel) and waits
// for any in-flight read goroutine to exit. Idempotent thanks to
// closeOnce.
func (s *streamLifecycle) close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		c := s.cancel
		s.cancel = nil
		s.mu.Unlock()
		if c != nil {
			c()
		}
	})
	s.wg.Wait()
}

// store records the connected channel/cancel for later teardown.
func (s *streamLifecycle) store(ch <-chan daemon.StateUpdate, cancel context.CancelFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ch = ch
	s.cancel = cancel
}
