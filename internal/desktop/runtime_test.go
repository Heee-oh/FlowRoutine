package desktop

import (
	"context"
	"testing"
	"testing/fstest"
)

type testBinding struct {
	*App
}

func TestNewOptionsPreservesDesktopContractAndPromotedBindings(t *testing.T) {
	assets := fstest.MapFS{"frontend/dist/index.html": {Data: []byte("ok")}}
	var app *App
	configured := NewOptions(assets, func(value *App) any {
		app = value
		return &testBinding{App: value}
	})

	if configured.Title != "FlowRoutine" || configured.Width != 1280 || configured.Height != 820 ||
		configured.MinWidth != 960 || configured.MinHeight != 640 {
		t.Fatalf("desktop window contract changed: %+v", configured)
	}
	if configured.AssetServer == nil || configured.AssetServer.Assets == nil || len(configured.Bind) != 1 {
		t.Fatal("desktop assets or bindings were not configured")
	}
	if _, ok := configured.Bind[0].(*testBinding); !ok {
		t.Fatalf("unexpected binding adapter: %T", configured.Bind[0])
	}
	ctx := context.Background()
	configured.OnStartup(ctx)
	if app == nil || app.ctx != ctx {
		t.Fatal("startup context was not retained by the shared app")
	}
	if _, err := app.Snapshot(); err == nil {
		t.Fatal("snapshot before start should preserve the controller error")
	}
	configured.OnShutdown(ctx)
}
