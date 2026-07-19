package admin

import "embed"

// Assets contains the built admin SPA. The first implementation is a compact
// embedded shell; it can be replaced by vue-pure-admin build output without
// changing the Go server.
//
//go:embed dist/*
var Assets embed.FS
