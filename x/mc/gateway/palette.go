package gateway

import (
	"github.com/Tnze/go-mc/data/item"
	"github.com/Tnze/go-mc/level/block"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// What the client is allowed to put down.
//
// The chain takes an opaque state id and hands it straight back, so the only
// question here is which one an item in the hotbar means. That mapping is the
// client's, not the chain's, which is why it lives next to the socket.

// defaultStates maps a block's name to the state to place for it.
//
// A block with properties has many states and this takes the first, so an oak
// log goes down on one axis and a stair faces one way. Getting that right means
// working out placement context from where the player is standing and which
// face they clicked, which is a bigger job than being able to place the block
// at all.
var defaultStates = buildDefaultStates()

func buildDefaultStates() map[string]uint32 {
	out := make(map[string]uint32, len(block.StateList))

	for i, b := range block.StateList {
		if _, seen := out[b.ID()]; !seen {
			out[b.ID()] = uint32(i)
		}
	}

	return out
}

// stateForItem returns the block an item puts down.
//
// Most of the creative inventory is not a block. A sword or an apple resolves
// to nothing and a click holding one places nothing, same as vanilla.
func stateForItem(id uint32) (uint32, bool) {
	it, ok := item.ByID[item.ID(id)]
	if !ok {
		return 0, false
	}

	state, ok := defaultStates["minecraft:"+it.Name]
	if !ok || state == types.StateAir {
		return 0, false
	}

	return state, true
}
