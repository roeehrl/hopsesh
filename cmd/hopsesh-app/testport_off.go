//go:build !e2e

package main

import (
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/ui/gui"
)

func testBrowserArgs() []string { return nil }

func testHook(*application.WebviewWindow, *gui.App) {}

func testAssets(next http.Handler) http.Handler { return next }
