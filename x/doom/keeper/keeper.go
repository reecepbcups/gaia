package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

// Keeper owns the input log and the running game.
//
// The split matters: the input log is consensus state, the game is not. The
// engine is a materialized view that any node can rebuild by replaying the log,
// and the state hash written every block is what proves two nodes rebuilt the
// same thing.
type Keeper struct {
	cdc          codec.BinaryCodec
	storeService storetypes.KVStoreService
	authority    string

	Schema    collections.Schema
	Params    collections.Item[types.Params]
	GameState collections.Item[types.GameState]
	Inputs    collections.Map[uint64, uint32]
	Pending   collections.Item[uint32]

	// wad is the IWAD this node was started with, or nil.
	wad []byte
	// engine is booted lazily on the first block that needs it.
	engine *engine.Engine
	// frames buffers the screens the engine drew, which are node-local.
	frames frameRing
}

// NewKeeper creates a doom keeper. wad may be nil, in which case the module
// stays inert and any chain whose params name a WAD will halt this node.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	authority string,
	wad []byte,
) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)

	k := &Keeper{
		cdc:          cdc,
		storeService: storeService,
		authority:    authority,
		wad:          wad,
		Params: collections.NewItem(sb, types.ParamsKey, "params",
			codec.CollValue[types.Params](cdc)),
		GameState: collections.NewItem(sb, types.GameStateKey, "game_state",
			codec.CollValue[types.GameState](cdc)),
		Inputs: collections.NewMap(sb, types.InputsKey, "inputs",
			collections.Uint64Key, collections.Uint32Value),
		Pending: collections.NewItem(sb, types.PendingKey, "pending",
			collections.Uint32Value),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// Logger returns a module-specific logger.
func (k *Keeper) Logger(ctx context.Context) log.Logger {
	return sdk.UnwrapSDKContext(ctx).Logger().With("module", "x/"+types.ModuleName)
}

// GetAuthority returns the x/doom module's authority.
func (k *Keeper) GetAuthority() string {
	return k.authority
}

// Engine returns the running game, or nil if this node has none.
func (k *Keeper) Engine() *engine.Engine {
	return k.engine
}

// Close shuts the engine down.
func (k *Keeper) Close(ctx context.Context) error {
	if k.engine == nil {
		return nil
	}
	return k.engine.Close(ctx)
}

// ensureEngine boots the game if it is not running yet and fast-forwards it to
// the tic the chain is already on. Restarting a node therefore costs a replay
// of the stored input log, which is cheap: the engine runs a few hundred tics a
// second faster than realtime.
func (k *Keeper) ensureEngine(ctx context.Context, params types.Params) error {
	if k.engine != nil {
		return nil
	}

	if len(k.wad) == 0 {
		return fmt.Errorf("%w: chain params require wad %s but this node was started without one",
			types.ErrEngineUnavailable, params.WadHash)
	}

	sum := sha256.Sum256(k.wad)
	if got := hex.EncodeToString(sum[:]); got != params.WadHash {
		return fmt.Errorf("%w: node has %s, chain wants %s", types.ErrWADMismatch, got, params.WadHash)
	}

	logger := k.Logger(ctx)

	eng, err := engine.New(ctx, k.wad, engineLogWriter{logger})
	if err != nil {
		return fmt.Errorf("boot doom: %w", err)
	}

	gs, err := k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		_ = eng.Close(ctx)
		return err
	}

	if gs.Tic > 0 {
		logger.Info("replaying doom input log", "tics", gs.Tic)

		inputs := make([]uint32, 0, gs.Tic)
		for tic := uint64(0); tic < gs.Tic; tic++ {
			buttons, err := k.Inputs.Get(ctx, tic)
			if err != nil {
				if errIsNotFound(err) {
					// Pruned. The log no longer reaches back this far, so the sim
					// cannot be rebuilt from it. See input_history in params.
					_ = eng.Close(ctx)
					return fmt.Errorf("input log pruned past tic %d, cannot rebuild game state", tic)
				}
				_ = eng.Close(ctx)
				return err
			}
			inputs = append(inputs, buttons)
		}

		if err := eng.Replay(ctx, inputs); err != nil {
			_ = eng.Close(ctx)
			return err
		}
	}

	k.engine = eng
	return nil
}

func errIsNotFound(err error) bool {
	return errors.Is(err, collections.ErrNotFound)
}

// engineLogWriter forwards DOOM's stdout to the node logger at debug level.
type engineLogWriter struct {
	logger log.Logger
}

func (w engineLogWriter) Write(p []byte) (int, error) {
	w.logger.Debug("doom", "out", string(p))
	return len(p), nil
}
