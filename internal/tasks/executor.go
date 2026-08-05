// Package tasks executes bash scripts on demand using exec.CommandContext.
package tasks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"vortex-agent/internal/config"
	"vortex-agent/internal/protocol"
)

const maxOutputBytes = 1 << 20 // 1 MiB per stream

// Sender publishes task_result frames.
type Sender interface {
	SendJSON(v any) error
}

// Executor runs task_run commands from Core.
type Executor struct {
	cfg    *config.Config
	log    *slog.Logger
	sender Sender

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// New creates a task executor.
func New(cfg *config.Config, log *slog.Logger, sender Sender) *Executor {
	if log == nil {
		log = slog.Default()
	}
	return &Executor{
		cfg:     cfg,
		log:     log.With("component", "tasks"),
		sender:  sender,
		running: make(map[string]context.CancelFunc),
	}
}

// HandleTaskRun starts command execution in a background goroutine.
func (e *Executor) HandleTaskRun(parent context.Context, raw []byte) {
	var msg protocol.TaskRun
	if err := protocol.DecodeJSON(raw, &msg); err != nil {
		e.log.Warn("bad task_run", "error", err)
		return
	}
	if msg.TaskID == "" || msg.Command == "" {
		e.log.Warn("task_run missing fields", "task_id", msg.TaskID)
		return
	}

	timeout := e.cfg.TaskTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)

	e.mu.Lock()
	if prev, ok := e.running[msg.TaskID]; ok {
		prev()
	}
	e.running[msg.TaskID] = cancel
	e.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			e.mu.Lock()
			delete(e.running, msg.TaskID)
			e.mu.Unlock()
		}()

		result := e.run(ctx, msg.TaskID, msg.Command)
		if err := e.sender.SendJSON(result); err != nil {
			e.log.Warn("failed to send task_result", "task_id", msg.TaskID, "error", err)
		}
	}()
}

// CloseAll cancels in-flight tasks (e.g. on tunnel drop).
func (e *Executor) CloseAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, cancel := range e.running {
		cancel()
		delete(e.running, id)
	}
}

func (e *Executor) run(ctx context.Context, taskID, command string) protocol.TaskResult {
	e.log.Info("running task", "task_id", taskID)

	shell, args := resolveShell(command)
	cmd := exec.CommandContext(ctx, shell, args...)
	configureCmd(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{limit: maxOutputBytes, buf: &stdout}
	cmd.Stderr = &limitedWriter{limit: maxOutputBytes, buf: &stderr}

	err := cmd.Run()

	exitCode := 0
	status := protocol.StatusSuccess
	if err != nil {
		var ee *exec.ExitError
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			status = protocol.StatusTimeout
			killProcessGroup(cmd)
			if errors.As(err, &ee) {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
			}
		case errors.As(err, &ee):
			status = protocol.StatusFailed
			exitCode = ee.ExitCode()
		default:
			status = protocol.StatusFailed
			exitCode = -1
			if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
	}

	code := exitCode
	e.log.Info("task finished",
		"task_id", taskID,
		"status", status,
		"exit_code", code,
	)
	return protocol.NewTaskResult(taskID, status, &code, stdout.String(), stderr.String())
}

func resolveShell(command string) (string, []string) {
	if path, err := exec.LookPath("bash"); err == nil {
		return path, []string{"-lc", command}
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return path, []string{"-c", command}
	}
	return "/bin/sh", []string{"-c", command}
}

type limitedWriter struct {
	limit int
	buf   *bytes.Buffer
	full  bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.full {
		return len(p), nil
	}
	remain := w.limit - w.buf.Len()
	if remain <= 0 {
		w.full = true
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = w.buf.Write(p[:remain])
		w.full = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

// Ensure limitedWriter implements io.Writer.
var _ io.Writer = (*limitedWriter)(nil)
