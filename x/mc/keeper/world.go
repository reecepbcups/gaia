package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// applyAction folds one thing the player did into the world.
//
// Movement is taken at face value. Minecraft has always been client
// authoritative about where you are, so the chain is recording a claim rather
// than simulating physics, and a cheating client is out of scope for a world
// with one seat in it.
func (k *Keeper) applyAction(ctx context.Context, player *types.Player, action types.Action) error {
	switch kind := action.Kind.(type) {
	case *types.Action_Move:
		m := kind.Move
		player.X, player.Y, player.Z = m.X, m.Y, m.Z
		player.Yaw, player.Pitch = m.Yaw, m.Pitch
		player.OnGround = m.OnGround

	case *types.Action_Dig:
		return k.setBlock(ctx, kind.Dig.Pos, types.StateAir)

	case *types.Action_Place:
		return k.setBlock(ctx, kind.Place.Pos, kind.Place.State)

	case *types.Action_Hotbar:
		player.Hotbar = kind.Hotbar.Slot % 9

	case *types.Action_Chat:
		// Chat is state only in the sense that it is in the log, which is where
		// the gateway reads it back out of.

	default:
		return types.ErrEmptyAction
	}

	return nil
}

// setBlock writes one block edit.
func (k *Keeper) setBlock(ctx context.Context, pos types.BlockPos, state uint32) error {
	if !pos.InWorld() {
		return types.ErrOutOfWorld.Wrapf("y %d", pos.Y)
	}

	cx, cz := types.ChunkOf(pos.X, pos.Z)
	return k.Blocks.Set(ctx, collections.Join3(cx, cz, types.PackLocal(pos)), state)
}

// ChunkEdits returns everything in one chunk that differs from the generated
// world, in key order.
func (k *Keeper) ChunkEdits(ctx context.Context, cx, cz int32) ([]types.BlockEdit, error) {
	rng := collections.NewSuperPrefixedTripleRange[int32, int32, int64](cx, cz)

	iter, err := k.Blocks.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var edits []types.BlockEdit
	for ; iter.Valid(); iter.Next() {
		kv, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		edits = append(edits, types.BlockEdit{
			Pos:   types.UnpackLocal(kv.Key.K1(), kv.Key.K2(), kv.Key.K3()),
			State: kv.Value,
		})
	}

	return edits, nil
}
