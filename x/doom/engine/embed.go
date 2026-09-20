package engine

import _ "embed"

// doomWasm is the game itself: doomgeneric built for wasm32-wasi by
// x/doom/wasmbuild/Makefile. Rebuild with `make -C x/doom/wasmbuild`.
//
//go:embed doom.wasm
var doomWasm []byte
