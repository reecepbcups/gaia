package gateway

import (
	"testing"

	"github.com/Tnze/go-mc/level/block"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// Consensus stores air as 0 without knowing anything about Minecraft's palette.
// If that ever stopped being true, every dig would write the wrong block.
func TestAirIsZero(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint32(types.StateAir), stateID(block.Air{}))
}

// Every hotbar entry has to resolve, or placing from that slot writes a state
// id the client will not recognise.
func TestHotbarResolves(t *testing.T) {
	t.Parallel()

	for i, slot := range hotbar {
		id, ok := block.ToStateID[slot.block]
		require.True(t, ok, "slot %d has no state id", i)
		require.NotZero(t, id, "slot %d is air", i)
		require.NotZero(t, slot.item, "slot %d has no item", i)
	}
}

func TestBuildChunk(t *testing.T) {
	t.Parallel()

	const ground = 64

	edits := []types.BlockEdit{
		// Dig out the grass at the chunk's origin column.
		{Pos: types.BlockPos{X: 0, Y: ground, Z: 0}, State: types.StateAir},
		// Stack a stone block two above it.
		{Pos: types.BlockPos{X: 0, Y: ground + 2, Z: 0}, State: stateID(block.Stone{})},
	}

	c := buildChunk(ground, 0, 0, edits)
	require.Len(t, c.Sections, types.Sections)

	at := func(x, y, z int32) block.Block {
		sec, idx := sectionIndex(y, x, z)
		return block.StateList[c.Sections[sec].GetBlock(idx)]
	}

	cases := []struct {
		name    string
		x, y, z int32
		want    block.Block
	}{
		{"floor", 1, types.MinY, 1, block.Bedrock{}},
		{"stone", 1, 0, 1, block.Stone{}},
		{"dirt", 1, ground - 1, 1, block.Dirt{}},
		{"surface", 1, ground, 1, block.GrassBlock{}},
		{"sky", 1, ground + 1, 1, block.Air{}},
		{"dug out", 0, ground, 0, block.Air{}},
		{"built on", 0, ground + 2, 0, block.Stone{}},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, at(tc.x, tc.y, tc.z), tc.name)
	}

	// The heightmap has to follow what was built, or the client lights the
	// column as if the block were not there.
	require.Equal(t, ground+2-types.MinY+1, c.HeightMaps.MotionBlocking.Get(0))
	require.Equal(t, ground-types.MinY+1, c.HeightMaps.MotionBlocking.Get(1))
}
