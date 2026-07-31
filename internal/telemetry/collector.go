// Package telemetry collects host metrics and publishes them through the tunnel.
package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

// Sender publishes telemetry frames to Vortex Core.
type Sender interface {
	TrySendJSON(v any) bool
}

// Collector periodically samples system metrics.
type Collector struct {
	cfg    *config.Config
	log    *slog.Logger
	sender Sender
}

// New creates a telemetry collector.
func New(cfg *config.Config, log *slog.Logger, sender Sender) *Collector {
	if log == nil {
		log = slog.Default()
	}
	return &Collector{
		cfg:    cfg,
		log:    log.With("component", "telemetry"),
		sender: sender,
	}
}

// Run samples metrics until ctx is cancelled. Safe to call once per tunnel session.
func (c *Collector) Run(ctx context.Context) {
	interval := c.cfg.TelemetryInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	// Prime CPU percent (gopsutil needs a prior sample for meaningful delta).
	_, _ = cpu.PercentWithContext(ctx, 0, false)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.publish(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.publish(ctx)
		}
	}
}

func (c *Collector) publish(ctx context.Context) {
	frame, err := c.sample(ctx)
	if err != nil {
		c.log.Warn("sample failed", "error", err)
		return
	}
	if !c.sender.TrySendJSON(frame) {
		c.log.Debug("telemetry dropped (tunnel busy or disconnected)")
	}
}

func (c *Collector) sample(ctx context.Context) (protocol.Telemetry, error) {
	var out protocol.Telemetry

	percents, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return out, err
	}
	if len(percents) > 0 {
		out.CPUPercent = percents[0]
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return out, err
	}
	out.RAMPercent = vm.UsedPercent
	out.RAMUsedBytes = vm.Used
	out.RAMTotalBytes = vm.Total

	counters, err := net.IOCountersWithContext(ctx, false)
	if err != nil {
		return out, err
	}
	if len(counters) > 0 {
		out.NetBytesSent = counters[0].BytesSent
		out.NetBytesRecv = counters[0].BytesRecv
	}

	uptime, err := host.UptimeWithContext(ctx)
	if err != nil {
		return out, err
	}
	out.UptimeSeconds = uptime

	return protocol.NewTelemetry(out), nil
}
