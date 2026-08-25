// Package web embeds the compiled React SPA so the panel ships as a single
// binary with zero Node.js runtime dependency.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distEmbed embed.FS

// DistFS returns the embedded build rooted at the dist directory.
func DistFS() (fs.FS, error) {
	return fs.Sub(distEmbed, "dist")
}
