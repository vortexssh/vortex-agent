// Package protocol defines the JSON frame contract between Vortex Agent and Vortex Core.
package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Frame types (agent ↔ core).
const (
	TypeConnected  = "connected"
	TypeHeartbeat  = "heartbeat"
	TypeTelemetry  = "telemetry"
	TypeTaskRun    = "task_run"
	TypeTaskResult = "task_result"

	TypeProxyOpen  = "proxy_open"
	TypeProxyData  = "proxy_data"
	TypeProxyClose = "proxy_close"
	TypeProxyError = "proxy_error"

	TypePTYOpen  = "pty_open"
	TypePTYData  = "pty_data"
	TypePTYClose = "pty_close"
	TypePTYError = "pty_error"
	TypePTYResize = "pty_resize"

	EncodingBase64 = "base64"

	StatusSuccess = "SUCCESS"
	StatusFailed  = "FAILED"
	StatusTimeout = "TIMEOUT"
)

// Envelope is the minimal discriminator present on every frame.
type Envelope struct {
	Type string `json:"type"`
}

// Connected is sent by Core after a successful agent handshake.
type Connected struct {
	Type    string `json:"type"`
	AgentID string `json:"agent_id"`
}

// Heartbeat is sent by the agent to refresh Redis presence.
type Heartbeat struct {
	Type string `json:"type"`
}

// Telemetry carries host metrics.
type Telemetry struct {
	Type          string   `json:"type"`
	CPUPercent    float64  `json:"cpu_percent"`
	RAMPercent    float64  `json:"ram_percent"`
	RAMUsedBytes  uint64   `json:"ram_used_bytes"`
	RAMTotalBytes uint64   `json:"ram_total_bytes"`
	NetBytesSent  uint64   `json:"net_bytes_sent"`
	NetBytesRecv  uint64   `json:"net_bytes_recv"`
	UptimeSeconds uint64   `json:"uptime_seconds"`
}

// TaskRun is dispatched by Core (manual or cron).
type TaskRun struct {
	Type    string `json:"type"`
	TaskID  string `json:"task_id"`
	Command string `json:"command"`
}

// TaskResult is returned by the agent after execution.
type TaskResult struct {
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// SessionOpen is shared by proxy_open / pty_open.
type SessionOpen struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	HostID    string `json:"host_id,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

// SessionData carries base64 payload for proxy/pty streams.
type SessionData struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
}

// SessionClose closes a proxy/pty session.
type SessionClose struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

// SessionError reports a proxy/pty failure.
type SessionError struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Error     string `json:"error,omitempty"`
}

// SessionResize optionally adjusts PTY geometry.
type SessionResize struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// DecodeType extracts the frame type from raw JSON.
func DecodeType(raw []byte) (string, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	if env.Type == "" {
		return "", fmt.Errorf("missing type")
	}
	return env.Type, nil
}

// DecodeJSON unmarshals raw into dst.
func DecodeJSON(raw []byte, dst any) error {
	return json.Unmarshal(raw, dst)
}

// EncodeBase64 encodes binary payload for *_data frames.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeBase64 decodes *_data payloads.
func DecodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// NewHeartbeat builds a heartbeat frame.
func NewHeartbeat() Heartbeat {
	return Heartbeat{Type: TypeHeartbeat}
}

// NewTelemetry builds a telemetry frame from metric fields.
func NewTelemetry(m Telemetry) Telemetry {
	m.Type = TypeTelemetry
	return m
}

// NewTaskResult builds a task_result frame.
func NewTaskResult(taskID, status string, exitCode *int, stdout, stderr string) TaskResult {
	return TaskResult{
		Type:     TypeTaskResult,
		TaskID:   taskID,
		Status:   status,
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	}
}

// NewSessionData builds a proxy_data or pty_data frame.
func NewSessionData(frameType, sessionID string, payload []byte) SessionData {
	return SessionData{
		Type:      frameType,
		SessionID: sessionID,
		Encoding:  EncodingBase64,
		Data:      EncodeBase64(payload),
	}
}

// NewSessionClose builds a proxy_close or pty_close frame.
func NewSessionClose(frameType, sessionID string) SessionClose {
	return SessionClose{Type: frameType, SessionID: sessionID}
}

// NewSessionError builds a proxy_error or pty_error frame.
func NewSessionError(frameType, sessionID, errMsg string) SessionError {
	return SessionError{Type: frameType, SessionID: sessionID, Error: errMsg}
}
