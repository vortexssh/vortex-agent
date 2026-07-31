// Package version exposes the agent build version (overridable via ldflags).
package version

// Version is the agent semver reported to Vortex Core on connect.
// Override at build time:
//
//	go build -ldflags "-X vortex-agent/internal/version.Version=1.2.3"
var Version = "0.1.0-dev"
