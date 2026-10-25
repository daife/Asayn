package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBlockingShellCancellationIsNotReportedAsTimeout(t *testing.T) {
	manager := NewShellManager(t.TempDir(), 2000, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := manager.RunBlocking(ctx, "echo interrupted", 60)
	if !errors.Is(err, context.Canceled) || strings.Contains(out, "TIMEOUT") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
