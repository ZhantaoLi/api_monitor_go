package main

import (
	"embed"

	"api_monitor/internal/app"
)

//go:embed web/*
var webFS embed.FS

func main() {
	app.Start(webFS)
}
