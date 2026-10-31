//go:build !wails

// stackman-desktop: the Wails desktop client backend (docs/client.md).
//
// The Wails entry point and webview are provided by the `wails` build tag in
// main_wails.go (generated/kept alongside `wails.json`). This default build is
// a headless backend smoke test so `go build ./client/...` works on hosts
// without the Wails toolchain. Build the desktop app with `make client` or
// `wails build -tags wails`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	listAgents := flag.Bool("list-agents", false, "scan for agents and print JSON")
	flag.Parse()
	if !*listAgents {
		fmt.Println("stackman-desktop: backend built without Wails; use `wails build` (see docs/client.md)")
		return
	}
	app := NewApp()
	defer app.Close()
	agents, err := app.ListAgents()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(agents)
}
