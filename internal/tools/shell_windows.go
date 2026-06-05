//go:build windows
// +build windows

package tools

import (
	"os/exec"
	"time"

	"github.com/google/uuid"
)

func (m *ShellManager) start(command string) (*shellRun, error) {
	cmd := exec.Command("cmd", "/c", command)
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

func (m *ShellManager) killRun(run *shellRun) {
	if run == nil || run.cmd.Process == nil {
		return
	}
	_ = run.cmd.Process.Kill()
}
