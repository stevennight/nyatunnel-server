// Package webdist embeds a small fallback page that is served when the real web
// build (NYATUNNEL_WEB_DIR, default .tmp-webdist) is not present, e.g. during `go run`.
package webdist

import "embed"

//go:embed static
var FS embed.FS
