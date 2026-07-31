package tasks

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
	mu    sync.Mutex
	frames []any
}

func (m *memSender) SendJSON(v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, v)
	return nil
}

func (m *memSender) last() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.frames) == 0 {
		return nil
	}
	return m.frames[len(m.frames)-1]
}

func TestRunSuccess(t *testing.T) {
	sender := &memSender{}
	ex := New(&config.Config{TaskTimeout: time.Minute}, slog.Default(), sender)
	ctx := context.Background()
	ex.HandleTaskRun(ctx, []byte(`{"type":"task_run","task_id":"t1","command":"echo hello"}`))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := sender.last().(protocol.TaskResult); ok {
			if r.Status != protocol.StatusSuccess {
				t.Fatalf("status=%s", r.Status)
			}
			if !contains(r.Stdout, "hello") {
				t.Fatalf("stdout=%q", r.Stdout)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for result")
}

func TestRunTimeout(t *testing.T) {
	sender := &memSender{}
	ex := New(&config.Config{TaskTimeout: 200 * time.Millisecond}, slog.Default(), sender)
	ex.HandleTaskRun(context.Background(), []byte(`{"type":"task_run","task_id":"t2","command":"sleep 10"}`))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := sender.last().(protocol.TaskResult); ok {
			if r.Status != protocol.StatusTimeout {
				t.Fatalf("status=%s", r.Status)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for TIMEOUT status")
}

func TestCloseAllCancels(t *testing.T) {
	sender := &memSender{}
	ex := New(&config.Config{TaskTimeout: time.Minute}, slog.Default(), sender)
	ex.HandleTaskRun(context.Background(), []byte(`{"type":"task_run","task_id":"t3","command":"sleep 30"}`))
	time.Sleep(50 * time.Millisecond)
	ex.CloseAll()
	// Process should be cancelled; result may or may not be sent depending on race.
	// Just ensure CloseAll does not panic and map is cleared.
	ex.mu.Lock()
	n := len(ex.running)
	ex.mu.Unlock()
	if n != 0 {
		t.Fatalf("running=%d", n)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
