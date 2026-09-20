package engine_test

import (
	"context"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/doom/engine"
)

// loadWAD returns the IWAD to test against, or skips. Tests cannot ship one:
// the shareware WAD is 4MB of somebody else's copyright.
func loadWAD(t *testing.T) []byte {
	t.Helper()

	path := os.Getenv("GAIA_DOOM_WAD")
	if path == "" {
		path = os.Getenv("HOME") + "/.gaia-doom/doom.wad"
	}

	wad, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no iwad at %s, set GAIA_DOOM_WAD to run this", path)
	}

	return wad
}

func newEngine(t *testing.T, wad []byte) *engine.Engine {
	t.Helper()

	e, err := engine.New(context.Background(), wad, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, e.Close(context.Background())) })

	return e
}

// TestDeterminism is the property the whole module rests on: the same inputs
// against the same WAD have to produce the same memory, every time, on every
// node.
func TestDeterminism(t *testing.T) {
	t.Parallel()

	wad := loadWAD(t)
	ctx := context.Background()

	// Something with menu input in it, so the run is not just the attract loop.
	const (
		escape = 1 << 10
		enter  = 1 << 11
		fire   = 1 << 6
	)

	inputs := make([]uint32, 120)
	for i := range inputs {
		switch {
		case i >= 10 && i < 14:
			inputs[i] = escape
		case i >= 20 && i < 24:
			inputs[i] = enter
		case i >= 60 && i < 90:
			inputs[i] = fire
		}
	}

	a, b := newEngine(t, wad), newEngine(t, wad)

	require.NoError(t, a.Replay(ctx, inputs))
	require.NoError(t, b.Replay(ctx, inputs))

	hashA, err := a.StateHash(ctx)
	require.NoError(t, err)
	hashB, err := b.StateHash(ctx)
	require.NoError(t, err)

	require.Equal(t, hex.EncodeToString(hashA), hex.EncodeToString(hashB),
		"two engines fed the same inputs disagreed")
	require.Equal(t, uint64(len(inputs)), a.Tics())
}

// TestInputChangesState guards against the opposite failure: an engine that is
// deterministic because it ignores what it is told.
func TestInputChangesState(t *testing.T) {
	t.Parallel()

	wad := loadWAD(t)
	ctx := context.Background()

	idle := make([]uint32, 60)
	pressed := make([]uint32, 60)
	for i := 20; i < 30; i++ {
		pressed[i] = 1 << 10 // escape, which opens the menu
	}

	a, b := newEngine(t, wad), newEngine(t, wad)

	require.NoError(t, a.Replay(ctx, idle))
	require.NoError(t, b.Replay(ctx, pressed))

	hashA, err := a.StateHash(ctx)
	require.NoError(t, err)
	hashB, err := b.StateHash(ctx)
	require.NoError(t, err)

	require.NotEqual(t, hashA, hashB, "input made no difference to the game state")
}

func TestFrame(t *testing.T) {
	t.Parallel()

	wad := loadWAD(t)
	ctx := context.Background()

	e := newEngine(t, wad)
	require.NoError(t, e.Replay(ctx, make([]uint32, 100)))

	pixels, palette, err := e.Frame(ctx)
	require.NoError(t, err)
	require.Len(t, pixels, engine.FrameSize)
	require.Len(t, palette, engine.PaletteSize)

	// The attract loop is rendering a level, so the screen cannot be one colour.
	seen := map[byte]struct{}{}
	for _, p := range pixels {
		seen[p] = struct{}{}
	}
	require.Greater(t, len(seen), 16, "framebuffer looks blank")
}

func TestRejectsEmptyWAD(t *testing.T) {
	t.Parallel()

	_, err := engine.New(context.Background(), nil, nil)
	require.Error(t, err)
}
