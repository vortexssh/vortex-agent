package pty

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
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

func (m *memSender) dataPayloads() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for _, f := range m.frames {
		if d, ok := f.(protocol.SessionData); ok {
			raw, err := protocol.DecodeBase64(d.Data)
			if err == nil {
				b.Write(raw)
			}
		}
	}
	return b.String()
}

func TestPTYEcho(t *testing.T) {
	sender := &memSender{}
	mgr := New(&config.Config{MaxSessions: 4}, slog.Default(), sender)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	open, _ := json.Marshal(protocol.SessionOpen{
		Type:      protocol.TypePTYOpen,
		SessionID: "pty-1",
		Cols:      80,
		Rows:      24,
	})
	mgr.HandleOpen(ctx, open)

	deadline := time.Now().Add(3 * time.Second)
	for mgr.ActiveCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if mgr.ActiveCount() != 1 {
		t.Fatal("pty session not opened")
	}

	// Type a command that produces unique output.
	cmd := "echo VORTEX_PTY_OK\n"
	data, _ := json.Marshal(protocol.NewSessionData(protocol.TypePTYData, "pty-1", []byte(cmd)))
	mgr.HandleData(data)

	for time.Now().Before(deadline) {
		if strings.Contains(sender.dataPayloads(), "VORTEX_PTY_OK") {
			mgr.CloseAll()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pty output missing marker; got %q", sender.dataPayloads())
}
