package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

type memSender struct {
	mu     sync.Mutex
	frames []any
}

func (m *memSender) SendJSON(v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, v)
	return nil
}

func (m *memSender) hasType(t string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.frames {
		switch v := f.(type) {
		case protocol.SessionError:
			if v.Type == t {
				return true
			}
		case protocol.SessionClose:
			if v.Type == t {
				return true
			}
		case protocol.SessionData:
			if v.Type == t {
				return true
			}
		}
	}
	return false
}

func TestDialFailureSendsError(t *testing.T) {
	sender := &memSender{}
	mgr := New(&config.Config{
		SSHAddr:     "127.0.0.1:1",
		MaxSessions: 8,
	}, slog.Default(), sender)

	raw, _ := json.Marshal(protocol.SessionOpen{
		Type:      protocol.TypeProxyOpen,
		SessionID: "s1",
	})
	mgr.HandleOpen(context.Background(), raw)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sender.hasType(protocol.TypeProxyError) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected proxy_error")
}

func TestProxyEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	sender := &memSender{}
	mgr := New(&config.Config{
		SSHAddr:     ln.Addr().String(),
		MaxSessions: 8,
	}, slog.Default(), sender)

	sid := "echo-1"
	raw, _ := json.Marshal(protocol.SessionOpen{Type: protocol.TypeProxyOpen, SessionID: sid})
	mgr.HandleOpen(context.Background(), raw)

	deadline := time.Now().Add(2 * time.Second)
	for mgr.ActiveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if mgr.ActiveCount() != 1 {
		t.Fatal("session not opened")
	}

	payload := protocol.NewSessionData(protocol.TypeProxyData, sid, []byte("ping"))
	dataRaw, _ := json.Marshal(payload)
	mgr.HandleData(dataRaw)

	for time.Now().Before(deadline) {
		if sender.hasType(protocol.TypeProxyData) {
			mgr.CloseAll()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected proxied data upstream")
}
