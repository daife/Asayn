package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

func TestBlockingShellUnlimitedWait(t *testing.T) {
	for _, timeout := range []int{0, -1} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			manager := NewShellManager(t.TempDir(), 2000, false)
			out, err := manager.RunBlocking(context.Background(), "echo completed", timeout)
			if err != nil || !strings.Contains(out, "completed") {
				t.Fatalf("out=%q err=%v", out, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			out, err = manager.RunBlocking(ctx, "sleep 5", timeout)
			if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(out, "TIMEOUT") {
				t.Fatalf("parent deadline lost: out=%q err=%v", out, err)
			}
		})
	}
}
