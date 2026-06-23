//go:build windows
// +build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

const gitBashDefaultPath = "C:\\Program Files\\Git\\bin\\bash.exe"

func (m *ShellManager) start(command string, interactive bool) (*shellRun, error) {
	useGitBash := m.usesGitBash()
	var cmd *exec.Cmd
	if useGitBash {
		bash, err := GitBashPath()
		if err != nil {
			return nil, err
		}
		cmd = exec.Command(bash, "-lc", command)
	} else {
		args := []string{"-NoLogo", "-NoProfile"}
		if !interactive {
			args = append(args, "-NonInteractive")
		}
		args = append(args, "-ExecutionPolicy", "Bypass", "-Command", "[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command)
		cmd = exec.Command("powershell.exe", args...)
	}
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
		done:    make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		run.Finish(cmd.Wait())
	}()
	return run, nil
}

func (m *ShellManager) environmentName() string {
	if m.usesGitBash() {
		return "Git Bash"
	}
	return "PowerShell"
}

func (m *ShellManager) usesGitBash() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.useGitBash
}

func GitBashPath() (string, error) {
	if _, err := os.Stat(gitBashDefaultPath); err == nil {
		out, err := exec.Command(gitBashDefaultPath, "-lc", "uname -o").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("Git Bash check failed for %s: %w", gitBashDefaultPath, err)
		}
		env := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(env, "msys") || strings.Contains(env, "mingw") {
			return gitBashDefaultPath, nil
		}
		return "", fmt.Errorf("%s is not Git Bash. Please install Git for Windows from https://git-scm.com/download/win using default settings", gitBashDefaultPath)
	}
	return "", fmt.Errorf("Git Bash not found at %s. Please install Git for Windows from https://git-scm.com/download/win using the default installation path (C:\\Program Files\\Git)", gitBashDefaultPath)
}

func GitBashAvailable() error {
	_, err := GitBashPath()
	return err
}

func (m *ShellManager) killRun(run *shellRun) {
	if run == nil || run.cmd.Process == nil {
		return
	}
	_ = run.cmd.Process.Kill()
}