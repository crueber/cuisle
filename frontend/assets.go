// Package frontend embeds the built UI (frontend/dist, produced by `vite build`).
// dist/.keep is committed so the package compiles before the first build.
package frontend

import "embed"

//go:embed all:dist
var Dist embed.FS
