package tunnel_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
	"vortex-agent/internal/tunnel"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func TestConnectHeartbeatAndReconnect(t *testing.T) {
	var dials atomic.Int32
	var gotHeartbeat atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		if r.URL.Query().Get("agent_id") == "" || r.URL.Query().Get("secret") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]string{"type": "connected", "agent_id": r.URL.Query().Get("agent_id")})

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		_ = json.Unmarshal(data, &env)
		if env.Type == protocol.TypeHeartbeat {
			gotHeartbeat.Store(true)
		}
		// Drop connection to exercise reconnect.
		_ = conn.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	cfg := &config.Config{
		CoreURL:           wsURL,
		SecretToken:       "test-secret",
		AgentID:           "11111111-1111-1111-1111-111111111111",
		Version:           "test",
		DialTimeout:       2 * time.Second,
		BackoffInitial:    50 * time.Millisecond,
		BackoffMax:        200 * time.Millisecond,
		HeartbeatInterval: 100 * time.Millisecond,
	}

	client := tunnel.New(cfg, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- client.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gotHeartbeat.Load() && dials.Load() >= 2 {
			cancel()
			<-errCh
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-errCh
	t.Fatalf("heartbeat=%v dials=%d", gotHeartbeat.Load(), dials.Load())
}
