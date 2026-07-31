package telemetry

import (
	"context"
	"log/slog"
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

func (m *memSender) TrySendJSON(v any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, v)
	return true
}

func TestCollectorPublishes(t *testing.T) {
	sender := &memSender{}
	c := New(&config.Config{TelemetryInterval: 50 * time.Millisecond}, slog.Default(), sender)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c.Run(ctx)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.frames) == 0 {
		t.Fatal("expected telemetry frames")
	}
	frame, ok := sender.frames[0].(protocol.Telemetry)
	if !ok {
		t.Fatalf("unexpected type %T", sender.frames[0])
	}
	if frame.Type != protocol.TypeTelemetry {
		t.Fatalf("type=%s", frame.Type)
	}
	if frame.RAMTotalBytes == 0 {
		t.Fatal("expected ram_total_bytes")
	}
}
