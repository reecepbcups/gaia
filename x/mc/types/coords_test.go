package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

func TestPackLocalRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pos  types.BlockPos
	}{
		{"origin", types.BlockPos{X: 0, Y: 0, Z: 0}},
		{"spawn", types.BlockPos{X: 8, Y: 64, Z: 8}},
		{"chunk corner", types.BlockPos{X: 15, Y: 319, Z: 15}},
		{"floor", types.BlockPos{X: 1, Y: types.MinY, Z: 2}},
		{"negative block", types.BlockPos{X: -1, Y: -3, Z: -16}},
		{"far negative", types.BlockPos{X: -1000, Y: -64, Z: -1000}},
		{"far positive", types.BlockPos{X: 1000, Y: 319, Z: 1000}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cx, cz := types.ChunkOf(tc.pos.X, tc.pos.Z)
			got := types.UnpackLocal(cx, cz, types.PackLocal(tc.pos))
			require.Equal(t, tc.pos, got)
		})
	}
}

// The chunk range query relies on every block in a chunk sharing the chunk part
// of the key, so a position and its neighbour must land in the same chunk.
func TestChunkOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		x, z         int32
		wantX, wantZ int32
	}{
		{0, 0, 0, 0},
		{15, 15, 0, 0},
		{16, 16, 1, 1},
		{-1, -1, -1, -1},
		{-16, -16, -1, -1},
		{-17, -17, -2, -2},
	}

	for _, tc := range cases {
		cx, cz := types.ChunkOf(tc.x, tc.z)
		require.Equal(t, tc.wantX, cx, "x %d", tc.x)
		require.Equal(t, tc.wantZ, cz, "z %d", tc.z)
	}
}

func TestParamsValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*types.Params)
		wantErr bool
	}{
		{"default", func(*types.Params) {}, false},
		{"no ticks", func(p *types.Params) { p.TicksPerBlock = 0 }, true},
		{"too many ticks", func(p *types.Params) { p.TicksPerBlock = types.MaxTicksPerBlock + 1 }, true},
		{"ground on the floor", func(p *types.Params) { p.GroundLevel = types.MinY }, true},
		{"ground through the ceiling", func(p *types.Params) { p.GroundLevel = types.MinY + types.WorldHeight }, true},
		{"bad controller", func(p *types.Params) { p.Controller = "not-an-address" }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := types.DefaultParams()
			tc.mutate(&p)

			if tc.wantErr {
				require.Error(t, p.Validate())
				return
			}
			require.NoError(t, p.Validate())
		})
	}
}
