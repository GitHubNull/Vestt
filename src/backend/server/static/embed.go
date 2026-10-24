package static

import "embed"

// Dist embeds the frontend build output. Before building, the Makefile syncs
// src/frontend/dist into this directory (see `make web`). The committed
// .gitkeep placeholder keeps the embed pattern valid in a fresh checkout.
//
//go:embed all:dist
var Dist embed.FS
