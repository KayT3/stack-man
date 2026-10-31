// Command stackman is the per-VPS agent: a CLI and a daemon in one binary.
//
// Dispatch rule: `stackman daemon` runs the RPC server; anything else is a
// one-shot CLI command. Both faces share the service layer under internal/.
package main

import (
	"fmt"
	"os"

	"github.com/example/stack-man/agent/internal/cli"
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
// TODO(phase 2): see docs/agent.md §5 and docs/api.md.
func runDaemon(_ []string) {
	fmt.Fprintln(os.Stderr, "stackman daemon: not implemented yet")
	os.Exit(1)
}

// runCLI dispatches one-shot commands (config, install, stack, git, ...).
func runCLI(args []string) error {
	return cli.Execute(args)
}
