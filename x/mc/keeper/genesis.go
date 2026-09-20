package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// InitGenesis writes the world into state.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}

	if err := k.GameState.Set(ctx, gs.State); err != nil {
		return err
	}

	player := gs.Player
	if !player.Joined && player.X == 0 && player.Y == 0 && player.Z == 0 {
		player = gs.Params.Spawn()
	}
	if err := k.Player.Set(ctx, player); err != nil {
		return err
	}

	for _, edit := range gs.Edits {
		if err := k.setBlock(ctx, edit.Pos, edit.State); err != nil {
			return err
		}
	}

	for _, entry := range gs.Log {
		if err := k.Log.Set(ctx, entry.Tick, types.TickLog{Actions: entry.Actions}); err != nil {
			return err
		}
	}

	return nil
}

// ExportGenesis reads the world back out.
//
// The log goes with it. Without it an exported world is just a picture of one,
// and the point of keeping the input history is that anyone can replay it from
// tick 0 and land on the same commitment.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	gs, err := k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	player, err := k.Player.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return nil, err
		}
		player = params.Spawn()
	}

	out := &types.GenesisState{Params: params, State: gs, Player: player}

	blocks, err := k.Blocks.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer blocks.Close()

	for ; blocks.Valid(); blocks.Next() {
		kv, err := blocks.KeyValue()
		if err != nil {
			return nil, err
		}
		out.Edits = append(out.Edits, types.BlockEdit{
			Pos:   types.UnpackLocal(kv.Key.K1(), kv.Key.K2(), kv.Key.K3()),
			State: kv.Value,
		})
	}

	log, err := k.Log.Iterate(ctx, new(collections.Range[uint64]))
	if err != nil {
		return nil, err
	}
	defer log.Close()

	for ; log.Valid(); log.Next() {
		kv, err := log.KeyValue()
		if err != nil {
			return nil, err
		}
		out.Log = append(out.Log, types.LogEntry{Tick: kv.Key, Actions: kv.Value.Actions})
	}

	return out, nil
}
