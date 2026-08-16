//go:build embedweb

package main

import (
	"embed"
	"io/fs"
)

// The built SvelteKit app. Present only in a release build, which runs the frontend
// build first and then compiles with `-tags embedweb`.
//
//go:embed all:webbuild
var webFS embed.FS

func embeddedWeb() (fs.FS, error) { return fs.Sub(webFS, "webbuild") }
