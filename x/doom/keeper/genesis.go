package keeper

import (
	"context"
	"fmt"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

// InitGenesis seeds the params and the input log. A non-empty log is replayed
// on the first block, which is how a game survives an export/import.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}

	for tic, buttons := range gs.Inputs {
		if err := k.Inputs.Set(ctx, uint64(tic), buttons); err != nil {
			return err
		}
	}

	return k.GameState.Set(ctx, types.GameState{Tic: uint64(len(gs.Inputs))})
}

// ExportGenesis dumps the params and the whole input log. The game itself is
// not exported: replaying the log rebuilds it exactly.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	gs, err := k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	inputs := make([]uint32, 0, gs.Tic)
	for tic := uint64(0); tic < gs.Tic; tic++ {
		buttons, err := k.Inputs.Get(ctx, tic)
		if err != nil {
			if errIsNotFound(err) {
				return nil, fmt.Errorf("input log was pruned past tic %d, cannot export a replayable game", tic)
			}
			return nil, err
		}
		inputs = append(inputs, buttons)
	}

	return &types.GenesisState{Params: params, Inputs: inputs}, nil
}
