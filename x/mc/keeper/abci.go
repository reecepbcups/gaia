package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// EndBlocker advances the world.
//
// Everything the player did while the block was open is applied here, in the
// order it arrived, and the block is the only thing that moves the clock. If
// the chain stalls the world freezes; if blocks speed up so does the day.
func (k *Keeper) EndBlocker(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	pending, err := k.Pending.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return err
		}
		pending = types.TickLog{}
	} else if err := k.Pending.Remove(ctx); err != nil {
		return err
	}

	gs, err := k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return err
	}

	player, err := k.Player.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return err
		}
		player = params.Spawn()
	}

	for _, action := range pending.Actions {
		if err := k.applyAction(ctx, &player, action); err != nil {
			return err
		}
	}

	// The block's actions are logged against the tick the block started on. The
	// remaining ticks of the block are empty ones that still move the clock,
	// which is what a Minecraft server does on a tick where nothing happened.
	if len(pending.Actions) > 0 {
		if err := k.Log.Set(ctx, gs.Tick, pending); err != nil {
			return err
		}
	}

	gs.Tick += uint64(params.TicksPerBlock)
	gs.StateHash = commit(gs.StateHash, gs.Tick, &player, &pending)

	if err := k.Player.Set(ctx, player); err != nil {
		return err
	}

	return k.GameState.Set(ctx, gs)
}

// commit chains the previous commitment with this block's input and the state
// it produced.
//
// Rolling it forward rather than hashing the whole world keeps the EndBlocker
// off the size of the world, and it still catches a node that applied the log
// differently: the player it derived goes into the hash next to the actions it
// derived them from.
func commit(prev []byte, tick uint64, player *types.Player, log *types.TickLog) []byte {
	h := sha256.New()
	h.Write(prev)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], tick)
	h.Write(buf[:])

	// Marshal cannot fail for these, they are generated types over fixed fields.
	playerBz, _ := player.Marshal()
	h.Write(playerBz)

	logBz, _ := log.Marshal()
	h.Write(logBz)

	return h.Sum(nil)
}
