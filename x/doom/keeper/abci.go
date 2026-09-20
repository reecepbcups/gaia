package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

// EndBlocker advances the game.
//
// Whatever buttons the block's transactions left in the pending slot are held
// down for every tic this block runs, exactly like a netgame client whose
// packet arrived. A block with no input is a released-keys tic.
func (k *Keeper) EndBlocker(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	// No WAD named in params means this chain is not running DOOM.
	if params.WadHash == "" {
		return nil
	}

	if err := k.ensureEngine(ctx, params); err != nil {
		return err
	}

	buttons, err := k.Pending.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return err
		}
		buttons = 0
	} else if err := k.Pending.Remove(ctx); err != nil {
		return err
	}

	gs, err := k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return err
	}

	// The engine is rebuilt from the log, so it must be sitting exactly where
	// the chain thinks it is. Catching a mismatch here beats silently writing a
	// state hash nobody else can reproduce.
	if got := k.engine.Tics(); got != gs.Tic {
		return fmt.Errorf("engine is at tic %d but chain is at tic %d", got, gs.Tic)
	}

	// Every tic draws, and the next one overwrites the framebuffer, so the
	// screens have to be copied out here or the client only ever sees the last
	// tic of each block.
	drawn := make([]types.Frame, 0, params.TicsPerBlock)

	for i := uint32(0); i < params.TicsPerBlock; i++ {
		if err := k.Inputs.Set(ctx, gs.Tic, buttons); err != nil {
			return err
		}
		if err := k.engine.Tic(ctx, buttons); err != nil {
			return err
		}
		gs.Tic++

		pixels, palette, err := k.engine.Frame(ctx)
		if err != nil {
			return err
		}
		drawn = append(drawn, types.Frame{Tic: gs.Tic, Pixels: pixels, Palette: palette})
	}

	hash, err := k.engine.StateHash(ctx)
	if err != nil {
		return err
	}

	// The commitment covers the block's last tic, so the earlier tics carry the
	// one that follows them. They were still drawn by this block.
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for i := range drawn {
		drawn[i].BlockHeight = sdkCtx.BlockHeight()
		drawn[i].Time = sdkCtx.BlockTime()
		drawn[i].StateHash = hash
	}
	k.frames.push(drawn...)

	gs.StateHash = hash
	gs.Buttons = buttons

	if err := k.GameState.Set(ctx, gs); err != nil {
		return err
	}

	return k.pruneInputs(ctx, params, gs.Tic, uint64(params.TicsPerBlock))
}

// pruneInputs drops the input that just fell out of the history window.
// Pruning gives up the ability to rebuild the game from tic 0, so it is off
// unless asked for.
func (k *Keeper) pruneInputs(ctx context.Context, params types.Params, tic, advanced uint64) error {
	if params.InputHistory == 0 || tic <= params.InputHistory {
		return nil
	}

	// Everything below cutoff is now out of the window, and everything below
	// cutoff-advanced went already on an earlier block.
	cutoff := tic - params.InputHistory
	start := uint64(0)
	if cutoff > advanced {
		start = cutoff - advanced
	}

	for t := start; t < cutoff; t++ {
		if err := k.Inputs.Remove(ctx, t); err != nil {
			return err
		}
	}

	return nil
}
