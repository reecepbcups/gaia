package types

// The world is stored in integers only. Go may fuse a multiply-add on one
// architecture and not another, so a float anywhere in consensus is a fork
// waiting for a validator on different hardware.
const (
	// FixedPointScale is how many units of stored position make one block.
	FixedPointScale = 4096

	// AngleScale is how many units of a stored angle make one degree.
	AngleScale = 100
)

// The overworld's shape. It has to match the dimension type the gateway hands
// the client during configuration, or the client drops chunks on the floor.
const (
	MinY = -64
	// WorldHeight is in blocks, so the world is MinY .. MinY+WorldHeight-1.
	WorldHeight = 384
	// Sections is how many 16-block slices that is.
	Sections = WorldHeight / 16
)

// ChunkOf returns the chunk a block column belongs to.
func ChunkOf(x, z int32) (cx, cz int32) {
	return x >> 4, z >> 4
}

// PackLocal squeezes a block position into one key, relative to its chunk.
//
// The Y stays absolute and stays in the high bits, so the keys for a chunk
// come back grouped by layer, which is the order a chunk gets built in anyway.
func PackLocal(pos BlockPos) int64 {
	lx := pos.X & 15
	lz := pos.Z & 15
	return int64(pos.Y)<<8 | int64(lx)<<4 | int64(lz)
}

// UnpackLocal is the inverse of PackLocal, given the chunk the key was under.
func UnpackLocal(cx, cz int32, packed int64) BlockPos {
	return BlockPos{
		X: cx<<4 + int32(packed>>4&15),
		Y: int32(packed >> 8),
		Z: cz<<4 + int32(packed&15),
	}
}

// InWorld reports whether a block position is somewhere the client can render.
func (p BlockPos) InWorld() bool {
	return p.Y >= MinY && p.Y < MinY+WorldHeight
}

// StateAir is the global palette id of minecraft:air. It has been the first
// block state in every version's palette, which is the only block id consensus
// needs to know: everything else the client puts down arrives as an opaque
// number and is handed back to it unchanged.
const StateAir = 0
