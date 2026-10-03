//go:build !e2e

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func testBrowserArgs() []string { return nil }

func testHook(*application.WebviewWindow) {}
