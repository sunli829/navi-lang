// Package navi embeds the standard library sources, so that the navi binary
// is self-contained.
package navi

import (
	"embed"
	"io/fs"
)

//go:embed all:lib/std
var stdFiles embed.FS

// StdFS returns the std crate's files, rooted at the crate directory
// (navi.toml, src/lib.navi, ...).
func StdFS() fs.FS {
	sub, err := fs.Sub(stdFiles, "lib/std")
	if err != nil {
		panic(err)
	}
	return sub
}
