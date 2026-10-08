//go:build e2e && !linux

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func testConsole(*application.WebviewWindow) {}
