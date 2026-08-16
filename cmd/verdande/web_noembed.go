//go:build !embedweb

package main

import (
	"errors"
	"io/fs"
)

// Without the embedweb tag there is no frontend in the binary. This is the normal
// state during backend development and on a clean checkout with no Node installed:
// the API runs, and anything that is not an API route says so in plain text.
func embeddedWeb() (fs.FS, error) {
	return nil, errors.New("built without -tags embedweb")
}
