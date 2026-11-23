// Package webassets embeds the server-rendered templates and vendored static
// files. It lives outside internal/web because go:embed cannot reach parent
// directories.
package webassets

import "embed"

// FS holds templates/ and static/. The all: prefix keeps underscore-prefixed
// partials such as templates/_flash.html, which go:embed otherwise skips.
//
//go:embed all:templates all:static
var FS embed.FS
