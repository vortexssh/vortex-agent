// Package config loads agent settings from the environment.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"vortex-agent/internal/version"
)

const (
	envCoreURL            = "VORTEX_CORE_URL"
	envSecretToken        = "VORTEX_SECRET_TOKEN"
	envLogLevel           = "VORTEX_LOG_LEVEL"
	envAgentID            = "VORTEX_AGENT_ID"
	envDialTimeout        = "VORTEX_DIAL_TIMEOUT_SEC"
	envHeartbeat          = "VORTEX_HEARTBEAT_INTERVAL_SEC"
	envTelemetryInterval  = "VORTEX_TELEMETRY_INTERVAL"
	envTaskTimeout        = "VORTEX_TASK_TIMEOUT_SEC"
	envSSHAddr            = "VORTEX_SSH_ADDR"
	envMaxSessions        = "VORTEX_MAX_SESSIONS"
)

// Config holds runtime settings for the Vortex Agent.
type Config struct {
	// CoreURL is the Vortex Core base (wss://host) or full /ws/agent URL.
	CoreURL string
	// SecretToken authenticates the agent to Vortex Core (query param secret).
	SecretToken string
	// AgentID is the agent UUID from Core (required).
	AgentID string
	// Version reported on connect.
	Version string
	// LogLevel is a slog level name: debug, info, warn, error.
	LogLevel string
	// DialTimeout for the initial WebSocket handshake.
	DialTimeout time.Duration
	// BackoffInitial is the first reconnect delay.
	BackoffInitial time.Duration
	// BackoffMax caps the exponential backoff.
	BackoffMax time.Duration
	// HeartbeatInterval for application-level heartbeat frames.
	HeartbeatInterval time.Duration
	// TelemetryInterval between metric samples.
	TelemetryInterval time.Duration
	// TaskTimeout kills long-running task_run commands.
	TaskTimeout time.Duration
	// SSHAddr is the local sshd address for TCP proxy (must be loopback).
	SSHAddr string
	// MaxSessions caps concurrent PTY + proxy sessions.
	MaxSessions int
}

// Load reads configuration from environment variables.
// Required: VORTEX_CORE_URL, VORTEX_SECRET_TOKEN, VORTEX_AGENT_ID.
func Load() (*Config, error) {
	cfg := &Config{
		CoreURL:           strings.TrimSpace(os.Getenv(envCoreURL)),
		SecretToken:       os.Getenv(envSecretToken),
		AgentID:           strings.TrimSpace(os.Getenv(envAgentID)),
		Version:           version.Version,
		LogLevel:          getenvDefault(envLogLevel, "info"),
		DialTimeout:       15 * time.Second,
		BackoffInitial:    1 * time.Second,
		BackoffMax:        60 * time.Second,
		HeartbeatInterval: 30 * time.Second,
		TelemetryInterval: 5 * time.Second,
		TaskTimeout:       5 * time.Minute,
		SSHAddr:           getenvDefault(envSSHAddr, "127.0.0.1:22"),
		MaxSessions:       32,
	}

	var err error
	if cfg.DialTimeout, err = durationSeconds(envDialTimeout, cfg.DialTimeout); err != nil {
		return nil, err
	}
	if cfg.HeartbeatInterval, err = durationSeconds(envHeartbeat, cfg.HeartbeatInterval); err != nil {
		return nil, err
	}
	if cfg.TelemetryInterval, err = durationSeconds(envTelemetryInterval, cfg.TelemetryInterval); err != nil {
		return nil, err
	}
	if cfg.TaskTimeout, err = durationSeconds(envTaskTimeout, cfg.TaskTimeout); err != nil {
		return nil, err
	}
	if v := os.Getenv(envMaxSessions); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid %s: %q", envMaxSessions, v)
		}
		cfg.MaxSessions = n
	}

	if cfg.CoreURL == "" {
		return nil, fmt.Errorf("%s is required (e.g. wss://core.example.com)", envCoreURL)
	}
	if cfg.SecretToken == "" {
		return nil, fmt.Errorf("%s is required", envSecretToken)
	}
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("%s is required (UUID from Core)", envAgentID)
	}
	if err := validateLoopbackAddr(cfg.SSHAddr); err != nil {
		return nil, err
	}

	return cfg, nil
}

// AgentWSURL builds the Core agent WebSocket URL with auth query params.
// Secrets are included in the returned URL — do not log it.
func (c *Config) AgentWSURL() (string, error) {
	u, err := url.Parse(c.CoreURL)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", envCoreURL, err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("%s must use ws:// or wss:// scheme", envCoreURL)
	}

	path := strings.TrimSuffix(u.Path, "/")
	switch {
	case path == "" || path == "/":
		u.Path = "/ws/agent"
	case strings.HasSuffix(path, "/ws/agent"):
		u.Path = path
	default:
		u.Path = path + "/ws/agent"
	}

	q := u.Query()
	q.Set("agent_id", c.AgentID)
	q.Set("secret", c.SecretToken)
	if c.Version != "" {
		q.Set("version", c.Version)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CoreURLForLog returns CoreURL without credentials for safe logging.
func (c *Config) CoreURLForLog() string {
	u, err := url.Parse(c.CoreURL)
	if err != nil {
		return c.CoreURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func durationSeconds(env string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(env)
	if v == "" {
		return fallback, nil
	}
	sec, err := strconv.Atoi(v)
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("invalid %s: %q", env, v)
	}
	return time.Duration(sec) * time.Second, nil
}

func validateLoopbackAddr(addr string) error {
	host, _, err := splitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", envSSHAddr, err)
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("%s must be a loopback address (got %q)", envSSHAddr, host)
	}
}

func splitHostPort(addr string) (host, port string, err error) {
	// net.SplitHostPort requires brackets for IPv6; accept host:port.
	uhost, uport, err := parseHostPort(addr)
	return uhost, uport, err
}

func parseHostPort(addr string) (string, string, error) {
	if strings.HasPrefix(addr, "[") {
		// [::<host>]:port
		end := strings.Index(addr, "]")
		if end < 0 {
			return "", "", fmt.Errorf("missing ] in address")
		}
		host := addr[1:end]
		rest := addr[end+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", "", fmt.Errorf("missing port")
		}
		return host, rest[1:], nil
	}
	host, port, ok := strings.Cut(addr, ":")
	if !ok || host == "" || port == "" {
		return "", "", fmt.Errorf("want host:port")
	}
	return host, port, nil
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
