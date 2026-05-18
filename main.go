package main

import (
	"embed"
	"fmt"
	"log"
	"os"

	"flowroutine/internal/system"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

const minFileDescriptorLimit uint64 = 100_000

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	limit, err := system.MaximizeFileDescriptorLimit(minFileDescriptorLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "file descriptor limit check failed: %v\n", err)
		os.Exit(1)
	}

	if limit.Current < minFileDescriptorLimit {
		fmt.Fprintf(
			os.Stderr,
			"warning: file descriptor limit is %d; target high-concurrency limit is %d\n",
			limit.Current,
			minFileDescriptorLimit,
		)
	}

	app := NewApp()
	err = wails.Run(&options.App{
		Title:     "FlowRoutine",
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
