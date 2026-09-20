package gateway

import (
	"github.com/Tnze/go-mc/data/item"
	"github.com/Tnze/go-mc/level"
	"github.com/Tnze/go-mc/level/block"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// The world the chain does not store.
//
// Only the blocks that differ from this are consensus state, so a chunk nobody
// has touched costs nothing and the chain never holds a copy of a flat world.
// Both ends have to generate the same thing, which is why ground level is a
// parameter rather than a constant in here.

// hotbarSlot is one entry of the fixed creative palette.
//
// The client is told what is in the hotbar and the gateway decides what a click
// places, so there is no item to block resolution to get wrong. Opening the
// creative inventory and picking something else does nothing, which is the
// honest limit of a proof of concept.
type hotbarSlot struct {
	block block.Block
	item  item.ID
}

var hotbar = [9]hotbarSlot{
	{block.Stone{}, item.Stone.ID},
	{block.Cobblestone{}, item.Cobblestone.ID},
	{block.Dirt{}, item.Dirt.ID},
	{block.OakPlanks{}, item.OakPlanks.ID},
	{block.Bricks{}, item.Bricks.ID},
	{block.Glass{}, item.Glass.ID},
	{block.Sand{}, item.Sand.ID},
	{block.Glowstone{}, item.Glowstone.ID},
	{block.GoldBlock{}, item.GoldBlock.ID},
}

// stateID looks a block up in the global palette the client shares.
func stateID(b block.Block) uint32 {
	return uint32(block.ToStateID[b])
}

// generated is the block a superflat world has at a height: bedrock on the
// floor, a few of dirt under a grass layer, stone in between.
func generated(groundLevel, y int32) block.Block {
	switch {
	case y > groundLevel || y < types.MinY:
		return block.Air{}
	case y == groundLevel:
		return block.GrassBlock{}
	case y > groundLevel-4:
		return block.Dirt{}
	case y > types.MinY:
		return block.Stone{}
	default:
		return block.Bedrock{}
	}
}

// sectionIndex splits an absolute Y into its section and its offset inside it.
func sectionIndex(y int32, lx, lz int32) (sec, idx int) {
	rel := y - types.MinY
	return int(rel >> 4), int((rel&15)*256 + lz*16 + lx)
}

// buildChunk renders one chunk for the client: the generated world with the
// chain's edits laid over it.
func buildChunk(groundLevel int32, cx, cz int32, edits []types.BlockEdit) *level.Chunk {
	c := level.EmptyChunk(types.Sections)

	// Fill the generated layers. Only the ones that are not air are worth
	// touching, so a chunk costs the height of the ground rather than the
	// height of the world.
	for y := types.MinY; y <= int(groundLevel); y++ {
		state := level.BlocksState(block.ToStateID[generated(groundLevel, int32(y))])
		for lz := int32(0); lz < 16; lz++ {
			for lx := int32(0); lx < 16; lx++ {
				sec, idx := sectionIndex(int32(y), lx, lz)
				c.Sections[sec].SetBlock(idx, state)
			}
		}
	}

	// A flat world's surface is the same everywhere until something is built
	// on it, so start from that and only fix up the columns an edit touched.
	// Heightmap values count blocks above the floor, so the surface is one more
	// than the ground's offset.
	surface := int(groundLevel-types.MinY) + 1
	for i := 0; i < 16*16; i++ {
		c.HeightMaps.MotionBlocking.Set(i, surface)
		c.HeightMaps.WorldSurface.Set(i, surface)
	}

	touched := make(map[int]bool)

	for _, e := range edits {
		if !e.Pos.InWorld() {
			continue
		}
		lx, lz := e.Pos.X&15, e.Pos.Z&15
		sec, idx := sectionIndex(e.Pos.Y, lx, lz)
		c.Sections[sec].SetBlock(idx, level.BlocksState(e.State))
		touched[int(lz*16+lx)] = true
	}

	for col := range touched {
		lx, lz := int32(col%16), int32(col/16)
		h := 0
		for y := types.MinY + types.WorldHeight - 1; y >= types.MinY; y-- {
			sec, idx := sectionIndex(int32(y), lx, lz)
			if !block.IsAir(c.Sections[sec].GetBlock(idx)) {
				h = y - types.MinY + 1
				break
			}
		}
		c.HeightMaps.MotionBlocking.Set(col, h)
		c.HeightMaps.WorldSurface.Set(col, h)
	}

	// Full daylight everywhere. There is no lighting engine here and nothing
	// below the surface is ever visible, so a lit world beats a black one.
	for i := range c.Sections {
		sky := make([]byte, 2048)
		for j := range sky {
			sky[j] = 0xFF
		}
		c.Sections[i].SkyLight = sky
	}

	c.Status = level.StatusFull

	return c
}
