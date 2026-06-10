package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type ShellManager struct {
	workdir string
	limit   int
	mu      sync.Mutex
	runs    map[string]*shellRun
	ended   map[string]string
}

type shellRun struct {
	id        string
	command   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	output    *safeBuffer
	started   time.Time
	done      chan struct{}
	mu        sync.Mutex
	err       error
	completed bool
}

type ShellSnapshot struct {
	ID        string
	Command   string
	Status    string
	PID       int
	Age       time.Duration
	StartedAt time.Time
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
		ended:   map[string]string{},
	}
}

func (m *ShellManager) SetLimit(limit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limit = limit
}

func (m *ShellManager) RunBlocking(ctx context.Context, command string, timeoutSec int) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	before := snapFiles(m.workdir)
	run, err := m.start(command, false)
	if err != nil {
		return "", err
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	select {
	case <-run.done:
		out := run.output.String()
		if err := run.Err(); err != nil {
			return truncate(out, m.limit), fmt.Errorf("command failed: %w", err)
		}
		out = truncate(out, m.limit)
		after := snapFiles(m.workdir)
		if diff := computeFileDiff(before, after); diff != "" {
			out += diff
		}
		return out, nil
	case <-waitCtx.Done():
		m.killRun(run)
		out := run.output.String()
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += fmt.Sprintf("<TIMEOUT after %d seconds>", timeoutSec)
		out = truncate(out, m.limit)
		return out, nil
	}
}

func (m *ShellManager) StartAsync(command string, interactive bool) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	run, err := m.start(command, interactive)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.runs[run.id] = run
	m.mu.Unlock()
	mode := "background command"
	if interactive {
		mode = "background terminal accepting stdin"
	}
	return fmt.Sprintf("shell_id=%s\nstarted %s", run.id, mode), nil
}

func (m *ShellManager) Status(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" {
		run := m.runs[id]
		if run == nil {
			if status := m.ended[id]; status != "" {
				return status
			}
			return "shell not found or terminated"
		}
		return truncate(m.describeWithOutput(run), m.limit)
	}
	rows := []string{}
	for _, run := range m.runs {
		rows = append(rows, m.describe(run))
	}
	sort.Strings(rows)
	if len(rows) == 0 {
		return "no running shells"
	}
	return truncate(strings.Join(rows, "\n\n"), m.limit)
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
	m.killRun(run)
	status := m.describe(run)
	m.mu.Lock()
	delete(m.runs, id)
	m.ended[id] = status + "\nterminated"
	m.mu.Unlock()
	return "killed", nil
}

func (m *ShellManager) Snapshots() []ShellSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ShellSnapshot{}
	for _, run := range m.runs {
		status := "running"
		if run.Completed() {
			status = "completed"
			if err := run.Err(); err != nil {
				status = "failed"
			}
		}
		pid := 0
		if run.cmd.Process != nil {
			pid = run.cmd.Process.Pid
		}
		out = append(out, ShellSnapshot{
			ID:        run.id,
			Command:   run.command,
			Status:    status,
			PID:       pid,
			Age:       time.Since(run.started).Truncate(time.Second),
			StartedAt: run.started,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *ShellManager) KillAll() {
	m.mu.Lock()
	runs := make([]*shellRun, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.runs = map[string]*shellRun{}
	m.ended = map[string]string{}
	m.mu.Unlock()

	for _, run := range runs {
		m.killRun(run)
	}
}

func (m *ShellManager) get(id string) *shellRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[id]
}

func (m *ShellManager) describe(run *shellRun) string {
	status := "running"
	if run.Completed() {
		status = "completed"
		if err := run.Err(); err != nil {
			status = "failed: " + err.Error()
		}
	}
	short := run.id
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s %s pid=%d age=%s cmd=%s", short, status, run.cmd.Process.Pid, time.Since(run.started).Truncate(time.Second), run.command)
}

func (m *ShellManager) describeWithOutput(run *shellRun) string {
	return m.describe(run) + "\n" + run.output.String()
}

func (r *shellRun) Finish(err error) {
	r.mu.Lock()
	r.err = err
	r.completed = true
	r.mu.Unlock()
	close(r.done)
}

func (r *shellRun) Completed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed
}

func (r *shellRun) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
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
