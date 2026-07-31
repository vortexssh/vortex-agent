package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
	"vortex-agent/internal/proxy"
	agentpty "vortex-agent/internal/pty"
	"vortex-agent/internal/tasks"
	"vortex-agent/internal/telemetry"
	"vortex-agent/internal/tunnel"
	"vortex-agent/internal/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	log.Info("vortex-agent starting",
		"version", version.Version,
		"core_url", cfg.CoreURLForLog(),
		"agent_id", cfg.AgentID,
		"log_level", cfg.LogLevel,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := tunnel.New(cfg, log)
	tel := telemetry.New(cfg, log, client)
	taskExec := tasks.New(cfg, log, client)
	proxyMgr := proxy.New(cfg, log, client)
	ptyMgr := agentpty.New(cfg, log, client)

	client.OnSessionStart(func(sessionCtx context.Context) {
		go tel.Run(sessionCtx)
	})
	client.OnSessionEnd(func(context.Context) {
		taskExec.CloseAll()
		proxyMgr.CloseAll()
		ptyMgr.CloseAll()
	})
	client.OnMessage(func(sessionCtx context.Context, msgType string, raw []byte) {
		switch msgType {
		case protocol.TypeTaskRun:
			taskExec.HandleTaskRun(sessionCtx, raw)
		case protocol.TypeProxyOpen:
			proxyMgr.HandleOpen(sessionCtx, raw)
		case protocol.TypeProxyData:
			proxyMgr.HandleData(raw)
		case protocol.TypeProxyClose:
			proxyMgr.HandleClose(raw)
		case protocol.TypePTYOpen:
			ptyMgr.HandleOpen(sessionCtx, raw)
		case protocol.TypePTYData:
			ptyMgr.HandleData(raw)
		case protocol.TypePTYClose:
			ptyMgr.HandleClose(raw)
		case protocol.TypePTYResize:
			ptyMgr.HandleResize(raw)
		case protocol.TypeConnected:
			// already consumed during handshake; ignore if re-sent
		default:
			log.Debug("unhandled frame", "frame_type", msgType)
		}
	})

	if err := client.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("tunnel exited with error", "error", err)
		os.Exit(1)
	}

	log.Info("vortex-agent stopped")
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
