//go:build windows
// +build windows

package tui

import (
	"fmt"
	"os/exec"
	"strings"
)

func copyToClipboard(text string) error {
	cmd := exec.Command("clip")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard copy via clip failed: %w", err)
	}
	return nil
}
