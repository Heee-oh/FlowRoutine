package desktop

import (
	"context"

	"flowroutine/internal/bridge"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App owns the desktop bridge implementation. Entrypoint packages embed it in
// a main.App adapter so the generated Wails binding namespace stays stable.
type App struct {
	ctx        context.Context
	controller *bridge.Controller
}

func NewApp() *App {
	app := &App{}
	app.controller = bridge.NewController(app)
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(context.Context) {
	_ = a.controller.Stop()
}

func (a *App) Emit(_ context.Context, eventName string, data any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, eventName, data)
}

func (a *App) StartLoad(request bridge.StartRequest) (bridge.StartResponse, error) {
	return a.controller.Start(a.ctx, request)
}

func (a *App) PreflightLoad(request bridge.StartRequest) (bridge.PreflightResponse, error) {
	return a.controller.Preflight(request)
}

func (a *App) StopLoad() error {
	return a.controller.Stop()
}

func (a *App) Snapshot() (bridge.SnapshotResponse, error) {
	return a.controller.Snapshot()
}

func (a *App) ImportOpenAPI(request bridge.OpenAPIImportRequest) (bridge.OpenAPIImportResponse, error) {
	return a.controller.ImportOpenAPI(a.ctx, request)
}
