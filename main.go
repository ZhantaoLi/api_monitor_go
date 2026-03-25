package main

import (
	"embed"

	"api_monitor/internal/bootstrap"
)

//go:embed web/*
var webFS embed.FS

func main() {
	bootstrap.Start(webFS)
}
