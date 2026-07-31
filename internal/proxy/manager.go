// Package proxy bridges Core proxy_* frames to a local sshd on loopback.
package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

// Sender publishes proxy frames upstream.
type Sender interface {
	SendJSON(v any) error
}

// Manager tracks active TCP proxy sessions.
type Manager struct {
	cfg    *config.Config
	log    *slog.Logger
	sender Sender

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id     string
	conn   net.Conn
	cancel context.CancelFunc
}

// New creates a proxy session manager.
func New(cfg *config.Config, log *slog.Logger, sender Sender) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		cfg:      cfg,
		log:      log.With("component", "proxy"),
		sender:   sender,
		sessions: make(map[string]*session),
	}
}

// HandleOpen dials local sshd and starts bidirectional pump.
func (m *Manager) HandleOpen(parent context.Context, raw []byte) {
	var msg protocol.SessionOpen
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		m.log.Warn("bad proxy_open", "error", err)
		return
	}
	if msg.SessionID == "" {
		m.log.Warn("proxy_open missing session_id")
		return
	}

	m.mu.Lock()
	if len(m.sessions) >= m.cfg.MaxSessions {
		m.mu.Unlock()
		_ = m.sender.SendJSON(protocol.NewSessionError(protocol.TypeProxyError, msg.SessionID, "max sessions reached"))
		return
	}
	if _, exists := m.sessions[msg.SessionID]; exists {
		m.mu.Unlock()
		m.log.Warn("proxy session already open", "session_id", msg.SessionID)
		return
	}
	m.mu.Unlock()

	conn, err := net.Dial("tcp", m.cfg.SSHAddr)
	if err != nil {
		m.log.Warn("dial sshd failed", "addr", m.cfg.SSHAddr, "error", err)
		_ = m.sender.SendJSON(protocol.NewSessionError(protocol.TypeProxyError, msg.SessionID, err.Error()))
		return
	}

	ctx, cancel := context.WithCancel(parent)
	s := &session{id: msg.SessionID, conn: conn, cancel: cancel}

	m.mu.Lock()
	m.sessions[msg.SessionID] = s
	m.mu.Unlock()

	m.log.Info("proxy session opened", "session_id", msg.SessionID, "addr", m.cfg.SSHAddr)
	go m.pumpUpstream(ctx, s)
}

// HandleData writes Core→agent bytes into the local TCP connection.
func (m *Manager) HandleData(raw []byte) {
	var msg protocol.SessionData
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		m.log.Warn("bad proxy_data", "error", err)
		return
	}
	payload, err := protocol.DecodeBase64(msg.Data)
	if err != nil {
		m.log.Warn("proxy_data decode", "error", err)
		return
	}

	m.mu.Lock()
	s := m.sessions[msg.SessionID]
	m.mu.Unlock()
	if s == nil {
		return
	}
	if _, err := s.conn.Write(payload); err != nil {
		m.log.Debug("proxy write failed", "session_id", msg.SessionID, "error", err)
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

// CloseAll tears down every proxy session.
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

		n, err := s.conn.Read(buf)
		if n > 0 {
			frame := protocol.NewSessionData(protocol.TypeProxyData, s.id, buf[:n])
			if sendErr := m.sender.SendJSON(frame); sendErr != nil {
				m.log.Debug("proxy upstream send failed", "session_id", s.id, "error", sendErr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				m.log.Debug("proxy read ended", "session_id", s.id, "error", err)
			}
			return
		}
	}
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
	_ = s.conn.Close()
	if notify {
		_ = m.sender.SendJSON(protocol.NewSessionClose(protocol.TypeProxyClose, id))
	}
	m.log.Info("proxy session closed", "session_id", id)
}

// ActiveCount returns the number of open proxy sessions (for tests).
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
