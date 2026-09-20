package gateway

import (
	"testing"

	"github.com/Tnze/go-mc/data/item"
	"github.com/Tnze/go-mc/level/block"
	"github.com/stretchr/testify/require"
)

func TestStateForItem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		item      item.Item
		placeable bool
		want      block.Block
	}{
		{"plain block", item.Stone, true, block.Stone{}},
		{"block with properties", item.OakLog, true, nil},
		{"block whose item name matches", item.DiamondBlock, true, block.DiamondBlock{}},
		{"not a block", item.DiamondSword, false, nil},
		{"food", item.Apple, false, nil},
		{"air", item.Air, false, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			state, ok := stateForItem(uint32(tc.item.ID))
			require.Equal(t, tc.placeable, ok)

			if !tc.placeable {
				return
			}

			// Whatever came back has to be a state of the block the item names,
			// even where the block has properties and this is only one of them.
			require.Equal(t, "minecraft:"+tc.item.Name, block.StateList[state].ID())

			if tc.want != nil {
				require.Equal(t, tc.want, block.StateList[state])
			}
		})
	}
}

// The hotbar the gateway hands over has to be placeable through the same path
// the client's own picks go through.
func TestHotbarItemsResolve(t *testing.T) {
	t.Parallel()

	for i, slot := range hotbar {
		state, ok := stateForItem(uint32(slot.item))
		require.True(t, ok, "slot %d", i)
		require.Equal(t, stateID(slot.block), state, "slot %d", i)
	}
}
