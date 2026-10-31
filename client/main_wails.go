//go:build wails

// Wails entry point. Build with:
//
//	cd frontend && npm install && npm run build
//	cd .. && wails build -tags wails
//
// or `make client` from the repo root. The `wails` tag is required so this file
// replaces the headless stub in main.go.
package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	defer app.Close()

	err := wails.Run(&options.App{
		Title:  "stackman",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			app.SetEmitter(wailsEmitter{ctx: ctx})
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}

type wailsEmitter struct {
	ctx context.Context
}

func (e wailsEmitter) Emit(event string, data any) {
	runtime.EventsEmit(e.ctx, event, data)
}
