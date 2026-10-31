// Command stackman is the per-VPS agent: a CLI and a daemon in one binary.
//
// Dispatch rule: `stackman daemon` runs the RPC server; anything else is a
// one-shot CLI command. Both faces share the service layer under internal/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/stack-man/agent/internal/cli"
	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/daemon"
	"github.com/example/stack-man/agent/internal/service"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon(os.Args[2:])
		return
	}
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runDaemon serves the private RPC API bound to the subnet IP.
func runDaemon(args []string) {
	configPath := config.DefaultConfigPath
	if len(args) > 0 {
		configPath = args[0]
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	svc, err := service.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer svc.Close()

	srv, err := daemon.New(svc, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runCLI dispatches one-shot commands (config, install, stack, git, ...).
func runCLI(args []string) error {
	return cli.Execute(args)
}
