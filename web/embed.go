// Package web embeds the dashboard files into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// Static returns the dashboard files, rooted so that index.html is at "/".
func Static() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}
