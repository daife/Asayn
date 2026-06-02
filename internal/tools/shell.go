package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ShellManager struct {
	workdir string
	limit   int
	mu      sync.Mutex
	runs    map[string]*shellRun
}

type shellRun struct {
	id      string
	command string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	output  *safeBuffer
	started time.Time
	done    chan error
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func NewShellManager(workdir string, limit int) *ShellManager {
	return &ShellManager{
		workdir: workdir,
		limit:   limit,
		runs:    map[string]*shellRun{},
	}
}

func (m *ShellManager) Run(ctx context.Context, command string, timeoutSec int, interactive bool) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	run, err := m.start(command)
	if err != nil {
		return "", err
	}
	if interactive {
		m.mu.Lock()
		m.runs[run.id] = run
		m.mu.Unlock()
		return fmt.Sprintf("shell_id=%s\nstarted interactive command; use shell_read/shell_write/shell_kill", run.id), nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	select {
	case err := <-run.done:
		out := run.output.String()
		if err != nil {
			return truncate(out, m.limit), fmt.Errorf("command failed: %w", err)
		}
		return truncate(out, m.limit), nil
	case <-waitCtx.Done():
		out := run.output.String()
		m.mu.Lock()
		m.runs[run.id] = run
		m.mu.Unlock()
		return truncate(fmt.Sprintf("shell_id=%s\ncommand still running after %ds\n%s", run.id, timeoutSec, out), m.limit), nil
	}
}

func (m *ShellManager) Read(id string) string {
	run := m.get(id)
	if run == nil {
		return "shell not found"
	}
	select {
	case err := <-run.done:
		status := "completed"
		if err != nil {
			status = "failed: " + err.Error()
		}
		m.mu.Lock()
		delete(m.runs, id)
		m.mu.Unlock()
		return truncate(status+"\n"+run.output.String(), m.limit)
	default:
		return truncate("running\n"+run.output.String(), m.limit)
	}
}

func (m *ShellManager) Write(id, input string) (string, error) {
	run := m.get(id)
	if run == nil {
		return "", fmt.Errorf("shell not found")
	}
	if _, err := run.stdin.Write([]byte(input)); err != nil {
		return "", err
	}
	return "input sent", nil
}

func (m *ShellManager) Kill(id string) (string, error) {
	run := m.get(id)
	if run == nil {
		return "", fmt.Errorf("shell not found")
	}
	if run.cmd.Process != nil {
		_ = run.cmd.Process.Kill()
	}
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
	return "killed", nil
}

func (m *ShellManager) start(command string) (*shellRun, error) {
	shell := "sh"
	args := []string{"-lc", command}
	if runtime.GOOS == "windows" {
		shell = "powershell"
		args = []string{"-NoProfile", "-Command", command}
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = m.workdir
	out := &safeBuffer{}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = out
	cmd.Stderr = out
	run := &shellRun{
		id:      uuid.NewString(),
		command: command,
		cmd:     cmd,
		stdin:   stdin,
		output:  out,
		started: time.Now(),
		done:    make(chan error, 1),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		run.done <- cmd.Wait()
	}()
	return run, nil
}

func (m *ShellManager) get(id string) *shellRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[id]
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
