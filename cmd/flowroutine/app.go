package main

import (
	"context"

	"flowroutine/internal/bridge"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

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

func (a *App) shutdown(ctx context.Context) {
	_ = a.controller.Stop()
}

func (a *App) Emit(ctx context.Context, eventName string, data any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, eventName, data)
}

func (a *App) StartLoad(req bridge.StartRequest) (bridge.StartResponse, error) {
	return a.controller.Start(a.ctx, req)
}

func (a *App) StopLoad() error {
	return a.controller.Stop()
}

func (a *App) Snapshot() (bridge.SnapshotResponse, error) {
	return a.controller.Snapshot()
}
