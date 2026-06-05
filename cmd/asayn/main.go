package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/tui"
)

func main() {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" && os.Getenv("ASAYN_ALLOW_NON_LINUX") != "1" {
		fmt.Fprintln(os.Stderr, "Asayn runs on Linux, macOS, and Windows. Set ASAYN_ALLOW_NON_LINUX=1 for unsupported platforms.")
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, err := app.Bootstrap(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := tui.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
