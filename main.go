package main

import (
	"embed"
	"log"

	"flowroutine/internal/desktop"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := desktop.Run(assets, func(app *desktop.App) any { return &App{App: app} }); err != nil {
		log.Fatal(err)
	}
}
