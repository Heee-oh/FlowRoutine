package main

import "flowroutine/internal/desktop"

// App keeps the public Wails namespace at main.App while sharing one backend.
type App struct {
	*desktop.App
}
