package gui

import "embed"

// Assets is the frontend (plain HTML, CSS and JS; no build step).
//
//go:embed assets
var Assets embed.FS
