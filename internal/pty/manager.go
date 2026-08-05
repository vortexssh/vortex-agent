// Package pty manages local pseudo-terminals for Web SSH sessions.
// stdin/stdout are proxied over the outbound WebSocket tunnel.
package pty

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	creackpty "github.com/creack/pty"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

// Sender publishes pty frames upstream.
type Sender interface {
	SendJSON(v any) error
}

// Manager tracks active PTY sessions.
type Manager struct {
	cfg    *config.Config
	log    *slog.Logger
	sender Sender

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id     string
	cmd    *exec.Cmd
	ptmx   *os.File
	cancel context.CancelFunc
}

// New creates a PTY session manager.
func New(cfg *config.Config, log *slog.Logger, sender Sender) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		cfg:      cfg,
		log:      log.With("component", "pty"),
		sender:   sender,
		sessions: make(map[string]*session),
	}
}

// HandleOpen spawns a shell in a PTY.
func (m *Manager) HandleOpen(parent context.Context, raw []byte) {
	var msg protocol.SessionOpen
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		m.log.Warn("bad pty_open", "error", err)
		return
	}
	if msg.SessionID == "" {
		m.log.Warn("pty_open missing session_id")
		return
	}

	cols, rows := msg.Cols, msg.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	m.mu.Lock()
	if len(m.sessions) >= m.cfg.MaxSessions {
		m.mu.Unlock()
		_ = m.sender.SendJSON(protocol.NewSessionError(protocol.TypePTYError, msg.SessionID, "max sessions reached"))
		return
	}
	if _, exists := m.sessions[msg.SessionID]; exists {
		m.mu.Unlock()
		m.log.Warn("pty session already open", "session_id", msg.SessionID)
		return
	}
	m.mu.Unlock()

	shell := resolveShell()
	cmd := exec.CommandContext(parent, shell)
	cmd.Env = append(
		os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
	)

	ptmx, err := creackpty.Start(cmd)
	if err != nil {
		m.log.Warn("pty start failed", "error", err)
		_ = m.sender.SendJSON(protocol.NewSessionError(protocol.TypePTYError, msg.SessionID, err.Error()))
		return
	}

	_ = creackpty.Setsize(ptmx, &creackpty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})

	ctx, cancel := context.WithCancel(parent)
	s := &session{id: msg.SessionID, cmd: cmd, ptmx: ptmx, cancel: cancel}

	m.mu.Lock()
	m.sessions[msg.SessionID] = s
	m.mu.Unlock()

	m.log.Info("pty session opened", "session_id", msg.SessionID, "shell", shell, "cols", cols, "rows", rows)
	go m.pumpUpstream(ctx, s)
	go m.waitExit(s)
}

// HandleData writes Core→agent bytes into the PTY.
func (m *Manager) HandleData(raw []byte) {
	var msg protocol.SessionData
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		m.log.Warn("bad pty_data", "error", err)
		return
	}
	payload, err := protocol.DecodeBase64(msg.Data)
	if err != nil {
		m.log.Warn("pty_data decode", "error", err)
		return
	}

	m.mu.Lock()
	s := m.sessions[msg.SessionID]
	m.mu.Unlock()
	if s == nil {
		return
	}
	if _, err := s.ptmx.Write(payload); err != nil {
		m.log.Debug("pty write failed", "session_id", msg.SessionID, "error", err)
		m.closeSession(msg.SessionID, true)
	}
}

// HandleClose closes a session at Core's request.
func (m *Manager) HandleClose(raw []byte) {
	var msg protocol.SessionClose
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		return
	}
	m.closeSession(msg.SessionID, false)
}

// HandleResize updates PTY window size if Core sends pty_resize.
func (m *Manager) HandleResize(raw []byte) {
	var msg protocol.SessionResize
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		return
	}
	m.mu.Lock()
	s := m.sessions[msg.SessionID]
	m.mu.Unlock()
	if s == nil || msg.Cols <= 0 || msg.Rows <= 0 {
		return
	}
	_ = creackpty.Setsize(s.ptmx, &creackpty.Winsize{
		Rows: uint16(msg.Rows),
		Cols: uint16(msg.Cols),
	})
}

// CloseAll tears down every PTY session.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.closeSession(id, false)
	}
}

func (m *Manager) pumpUpstream(ctx context.Context, s *session) {
	defer m.closeSession(s.id, true)

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := s.ptmx.Read(buf)
		if n > 0 {
			frame := protocol.NewSessionData(protocol.TypePTYData, s.id, buf[:n])
			if sendErr := m.sender.SendJSON(frame); sendErr != nil {
				m.log.Debug("pty upstream send failed", "session_id", s.id, "error", sendErr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				m.log.Debug("pty read ended", "session_id", s.id, "error", err)
			}
			return
		}
	}
}

func (m *Manager) waitExit(s *session) {
	_ = s.cmd.Wait()
	m.closeSession(s.id, true)
}

func (m *Manager) closeSession(id string, notify bool) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	s.cancel()
	_ = s.ptmx.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if notify {
		_ = m.sender.SendJSON(protocol.NewSessionClose(protocol.TypePTYClose, id))
	}
	m.log.Info("pty session closed", "session_id", id)
}

// ActiveCount returns open PTY sessions (for tests).
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func resolveShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	for _, candidate := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "/bin/sh"
}
