package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/asayn/asayn/internal/subscription"
	"github.com/asayn/asayn/internal/app"
	"github.com/asayn/asayn/internal/tui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "subscription" {
		if err := subscriptionCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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

func subscriptionCommand(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: asayn subscription <login|models|logout> [account-label]")
	}
	account := "default"
	if len(args) == 2 {
		account = args[1]
	}
	path, err := subscription.DefaultPath(account)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch args[0] {
	case "login":
		return subscription.Login(ctx, path, os.Stdout)
	case "logout":
		return subscription.Logout(ctx, path)
	case "models":
		models, err := subscription.Models(ctx, path)
		if err != nil {
			return err
		}
		for _, m := range models {
			fmt.Printf("%s\t%s\n", m.Slug, m.DisplayName)
		}
		return nil
	default:
		return fmt.Errorf("usage: asayn subscription <login|models|logout> [account-label]")
	}
}
