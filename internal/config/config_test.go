package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresFields(t *testing.T) {
	t.Setenv("VORTEX_CORE_URL", "")
	t.Setenv("VORTEX_SECRET_TOKEN", "")
	t.Setenv("VORTEX_AGENT_ID", "")
	t.Setenv("VORTEX_SSH_ADDR", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when required env is missing")
	}

	t.Setenv("VORTEX_CORE_URL", "wss://core.example.com")
	t.Setenv("VORTEX_SECRET_TOKEN", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when agent id is missing")
	}

	t.Setenv("VORTEX_AGENT_ID", "11111111-1111-1111-1111-111111111111")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BackoffInitial != time.Second {
		t.Fatalf("BackoffInitial = %v", cfg.BackoffInitial)
	}
	if cfg.TelemetryInterval != 5*time.Second {
		t.Fatalf("TelemetryInterval = %v", cfg.TelemetryInterval)
	}
	if cfg.SSHAddr != "127.0.0.1:22" {
		t.Fatalf("SSHAddr = %q", cfg.SSHAddr)
	}
}

func TestAgentWSURL(t *testing.T) {
	cfg := &Config{
		CoreURL:     "wss://core.example.com",
		SecretToken: "sekrit",
		AgentID:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Version:     "1.2.3",
	}
	got, err := cfg.AgentWSURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "wss://core.example.com/ws/agent?") {
		t.Fatalf("url = %q", got)
	}
	for _, part := range []string{
		"agent_id=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"secret=sekrit",
		"version=1.2.3",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %s", part, got)
		}
	}

	cfg.CoreURL = "ws://localhost:8000/ws/agent"
	got, err = cfg.AgentWSURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "ws://localhost:8000/ws/agent?") {
		t.Fatalf("full path url = %q", got)
	}
}

func TestRejectNonLoopbackSSH(t *testing.T) {
	t.Setenv("VORTEX_CORE_URL", "wss://core.example.com")
	t.Setenv("VORTEX_SECRET_TOKEN", "secret")
	t.Setenv("VORTEX_AGENT_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("VORTEX_SSH_ADDR", "192.168.1.1:22")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-loopback SSH addr")
	}
}
