package desktop

import (
	"fmt"
	"io/fs"
	"os"

	"flowroutine/internal/system"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

const MinimumFileDescriptorLimit uint64 = 100_000

type BindingFactory func(*App) any

func Run(assets fs.FS, bindingFactory BindingFactory) error {
	limit, err := system.MaximizeFileDescriptorLimit(MinimumFileDescriptorLimit)
	if err != nil {
		return fmt.Errorf("file descriptor limit check failed: %w", err)
	}
	if limit.Current < MinimumFileDescriptorLimit {
		fmt.Fprintf(
			os.Stderr,
			"warning: file descriptor limit is %d; target high-concurrency limit is %d\n",
			limit.Current,
			MinimumFileDescriptorLimit,
		)
	}

	return wails.Run(NewOptions(assets, bindingFactory))
}

func NewOptions(assets fs.FS, bindingFactory BindingFactory) *options.App {
	app := NewApp()
	return &options.App{
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
			bindingFactory(app),
		},
	}
}
