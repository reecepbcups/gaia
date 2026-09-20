// Package engine runs id Software's DOOM as a deterministic state machine.
//
// The game is compiled to wasm32-wasi (see x/doom/wasmbuild) and driven one tic
// at a time from Go. Nothing inside the sandbox can read a clock, a device or
// an entropy source, so a given WAD plus a given sequence of button masks
// always produces the same linear memory. That is what lets the chain commit to
// a hash of it.
package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"
	"testing/fstest"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	// ScreenWidth and ScreenHeight are DOOM's native resolution. The wasm module
	// is built with DOOMGENERIC_RESX/RESY pinned to these.
	ScreenWidth  = 320
	ScreenHeight = 200

	// FrameSize is one 8-bit paletted frame.
	FrameSize = ScreenWidth * ScreenHeight

	// PaletteSize is 256 BGRA entries.
	PaletteSize = 256 * 4

	// wadPath is where the IWAD is mounted inside the sandbox. It has to match
	// the -iwad argument in doomgeneric_chain.c.
	wadPath = "doom.wad"
)

// Engine is a running game. It is safe for concurrent use: the EndBlocker
// advances it while the frame server reads from it.
type Engine struct {
	mu      sync.Mutex
	runtime wazero.Runtime
	module  api.Module
	memory  api.Memory

	tic         api.Function
	framebuffer api.Function
	palette     api.Function

	fbPtr  uint32
	palPtr uint32

	tics uint64
}

// New boots a game on the given IWAD. The returned engine has already executed
// the boot tic, so it is sitting on the title screen.
func New(ctx context.Context, wad []byte, logs io.Writer) (*Engine, error) {
	if len(wad) == 0 {
		return nil, fmt.Errorf("empty wad")
	}
	if logs == nil {
		logs = io.Discard
	}

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig())

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	// The only thing the sandbox can see is a read-only IWAD. DOOM tries to
	// write a config file and a savegame dir on boot; both fail harmlessly.
	root := fstest.MapFS{wadPath: &fstest.MapFile{Data: wad, Mode: 0o444}}

	cfg := wazero.NewModuleConfig().
		WithName("doom").
		WithStdout(logs).
		WithStderr(logs).
		WithRandSource(zeroSource{}).
		WithFSConfig(wazero.NewFSConfig().WithFSMount(root, "/")).
		WithStartFunctions("_initialize")

	mod, err := rt.InstantiateWithConfig(ctx, doomWasm, cfg)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate doom: %w", err)
	}

	e := &Engine{runtime: rt, module: mod, memory: mod.Memory()}

	lookup := func(name string) (api.Function, error) {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			return nil, fmt.Errorf("wasm module is missing export %q", name)
		}
		return fn, nil
	}

	init, err := lookup("chain_init")
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if e.tic, err = lookup("chain_tic"); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if e.framebuffer, err = lookup("chain_framebuffer"); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if e.palette, err = lookup("chain_palette"); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}

	res, err := init.Call(ctx)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("chain_init: %w", err)
	}
	if res[0] != 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("chain_init returned %d", res[0])
	}

	if e.fbPtr, err = e.callPtr(ctx, e.framebuffer); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if e.palPtr, err = e.callPtr(ctx, e.palette); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}

	return e, nil
}

func (e *Engine) callPtr(ctx context.Context, fn api.Function) (uint32, error) {
	res, err := fn.Call(ctx)
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
}

// Tic advances the game by one tic with the given buttons held down.
func (e *Engine) Tic(ctx context.Context, buttons uint32) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, err := e.tic.Call(ctx, uint64(buttons)); err != nil {
		return fmt.Errorf("tic %d: %w", e.tics, err)
	}
	e.tics++
	return nil
}

// Replay advances the game through a whole input log. Used to rebuild the sim
// after a restart, so it skips the per-tic bookkeeping the EndBlocker does.
func (e *Engine) Replay(ctx context.Context, inputs []uint32) error {
	for i, buttons := range inputs {
		if err := e.Tic(ctx, buttons); err != nil {
			return fmt.Errorf("replay input %d: %w", i, err)
		}
	}
	return nil
}

// Tics reports how many tics have been executed since boot.
func (e *Engine) Tics() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tics
}

// StateHash is sha256 over the engine's whole linear memory. Two nodes that
// have fed the engine the same inputs must agree on it.
func (e *Engine) StateHash(ctx context.Context) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mem, ok := e.memory.Read(0, e.memory.Size())
	if !ok {
		return nil, fmt.Errorf("read linear memory: %d bytes", e.memory.Size())
	}

	sum := sha256.Sum256(mem)
	return sum[:], nil
}

// Frame copies out the current screen and its palette. The framebuffer is
// re-derived from the sim rather than stored, so it never touches consensus.
func (e *Engine) Frame(ctx context.Context) (pixels, palette []byte, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	fb, ok := e.memory.Read(e.fbPtr, FrameSize)
	if !ok {
		return nil, nil, fmt.Errorf("read framebuffer at 0x%x", e.fbPtr)
	}
	pal, ok := e.memory.Read(e.palPtr, PaletteSize)
	if !ok {
		return nil, nil, fmt.Errorf("read palette at 0x%x", e.palPtr)
	}

	// Read returns a view into linear memory, so copy before unlocking.
	pixels = make([]byte, FrameSize)
	copy(pixels, fb)
	palette = make([]byte, PaletteSize)
	copy(palette, pal)

	return pixels, palette, nil
}

// Close tears down the wasm runtime.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runtime.Close(ctx)
}

// zeroSource stands in for the host entropy wazero would otherwise wire up.
// DOOM has its own fixed random table and never needs real randomness, but WASI
// exposes random_get regardless and it must not vary between nodes.
type zeroSource struct{}

func (zeroSource) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
