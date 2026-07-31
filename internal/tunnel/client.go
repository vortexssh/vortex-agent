// Package tunnel implements the outbound WebSocket connection to Vortex Core.
// The agent never binds listening ports — all traffic is multiplexed over this tunnel.
package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// MessageHandler processes an inbound Core frame within a live session.
type MessageHandler func(ctx context.Context, msgType string, raw []byte)

// SessionHook is called when a tunnel session becomes ready (after connected)
// or ends. The provided context is cancelled when the WebSocket drops.
type SessionHook func(ctx context.Context)

// Client maintains a resilient outbound WebSocket session to Vortex Core.
type Client struct {
	cfg  *config.Config
	log  *slog.Logger
	dial *websocket.Dialer

	mu   sync.Mutex
	conn *websocket.Conn

	onMessage MessageHandler
	onStart   SessionHook
	onEnd     SessionHook
}

// New creates a tunnel client from agent configuration.
func New(cfg *config.Config, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		cfg: cfg,
		log: log.With("component", "tunnel"),
		dial: &websocket.Dialer{
			HandshakeTimeout: cfg.DialTimeout,
			Proxy:            http.ProxyFromEnvironment,
		},
	}
}

// OnMessage registers the inbound frame dispatcher.
func (c *Client) OnMessage(h MessageHandler) { c.onMessage = h }

// OnSessionStart registers a hook invoked after Core sends "connected".
func (c *Client) OnSessionStart(h SessionHook) { c.onStart = h }

// OnSessionEnd registers a hook invoked when a session tears down.
func (c *Client) OnSessionEnd(h SessionHook) { c.onEnd = h }

// Run connects to Vortex Core and reconnects with exponential backoff until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	initial := c.cfg.BackoffInitial
	if initial <= 0 {
		initial = time.Second
	}
	maxBackoff := c.cfg.BackoffMax
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}
	backoff := initial

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.log.Info("connecting to Vortex Core",
			"url", c.cfg.CoreURLForLog(),
			"agent_id", c.cfg.AgentID,
			"version", c.cfg.Version,
		)

		authed, err := c.session(ctx)
		if ctx.Err() != nil {
			c.log.Info("tunnel stopped", "reason", ctx.Err())
			return ctx.Err()
		}

		if authed {
			backoff = initial
		}

		c.log.Warn("connection lost, will reconnect",
			"error", err,
			"backoff", backoff.String(),
		)

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) session(ctx context.Context) (authed bool, err error) {
	wsURL, err := c.cfg.AgentWSURL()
	if err != nil {
		return false, err
	}

	conn, resp, err := c.dial.DialContext(ctx, wsURL, nil)
	if err != nil {
		if resp != nil {
			return false, fmt.Errorf("websocket dial: %w (http %s)", err, resp.Status)
		}
		return false, fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	c.setConn(conn)
	defer c.setConn(nil)

	if err := c.waitConnected(conn); err != nil {
		return false, fmt.Errorf("handshake: %w", err)
	}

	c.log.Info("connected and authenticated")
	return true, c.serve(ctx, conn)
}

func (c *Client) waitConnected(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.DialTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	_, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	msgType, err := protocol.DecodeType(data)
	if err != nil {
		return err
	}
	if msgType != protocol.TypeConnected {
		return fmt.Errorf("expected %q, got %q", protocol.TypeConnected, msgType)
	}
	var frame protocol.Connected
	if err := protocol.DecodeJSON(data, &frame); err != nil {
		return err
	}
	c.log.Debug("core handshake ok", "agent_id", frame.AgentID)
	return nil
}

func (c *Client) serve(ctx context.Context, conn *websocket.Conn) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if c.onStart != nil {
		c.onStart(sessionCtx)
	}
	defer func() {
		if c.onEnd != nil {
			c.onEnd(sessionCtx)
		}
	}()

	errCh := make(chan error, 3)

	go func() { errCh <- c.readLoop(sessionCtx, conn) }()
	go func() { errCh <- c.pingLoop(sessionCtx, conn) }()
	go func() { errCh <- c.heartbeatLoop(sessionCtx) }()

	select {
	case <-ctx.Done():
		c.mu.Lock()
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"),
			time.Now().Add(writeWait),
		)
		c.mu.Unlock()
		return ctx.Err()
	case err := <-errCh:
		cancel()
		return err
	}
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		msgType, err := protocol.DecodeType(data)
		if err != nil {
			c.log.Warn("invalid frame", "error", err)
			continue
		}
		c.log.Debug("frame received", "frame_type", msgType, "bytes", len(data))
		if c.onMessage != nil {
			c.onMessage(ctx, msgType, data)
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) error {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.mu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
			c.mu.Unlock()
			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) error {
	interval := c.cfg.HeartbeatInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Immediate heartbeat so presence TTL starts fresh.
	if err := c.SendJSON(protocol.NewHeartbeat()); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.SendJSON(protocol.NewHeartbeat()); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
		}
	}
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

// SendJSON writes a JSON payload on the active connection (if any).
func (c *Client) SendJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("tunnel: not connected")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteJSON(v)
}

// TrySendJSON attempts a non-blocking-ish send: if not connected, returns false.
// Still serializes writes via the mutex (gorilla Write is not concurrent-safe).
func (c *Client) TrySendJSON(v any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := c.conn.WriteJSON(v); err != nil {
		c.log.Debug("try send failed", "error", err)
		return false
	}
	return true
}

// Connected reports whether a live WebSocket is held.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}
