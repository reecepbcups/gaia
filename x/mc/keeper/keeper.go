package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// Keeper owns the world.
//
// There is no game server here and nothing derived: the blocks the player broke
// and the position they last reported are the state, and the tick counter is
// driven by the EndBlocker. The gateway that speaks Minecraft to a real client
// is a reader of this, the same way a browser is a reader of x/doom.
type Keeper struct {
	cdc          codec.BinaryCodec
	storeService storetypes.KVStoreService
	authority    string

	Schema    collections.Schema
	Params    collections.Item[types.Params]
	GameState collections.Item[types.GameState]
	Player    collections.Item[types.Player]
	Pending   collections.Item[types.TickLog]

	// Blocks holds only what differs from the generated world, keyed by
	// (chunk x, chunk z, packed position) so one chunk can be fetched at once.
	Blocks collections.Map[collections.Triple[int32, int32, int64], uint32]

	// Log is the input history, keyed by the tick the block landed on.
	Log collections.Map[uint64, types.TickLog]
}

// NewKeeper creates an mc keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	authority string,
) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)

	k := &Keeper{
		cdc:          cdc,
		storeService: storeService,
		authority:    authority,
		Params: collections.NewItem(sb, types.ParamsKey, "params",
			codec.CollValue[types.Params](cdc)),
		GameState: collections.NewItem(sb, types.GameStateKey, "game_state",
			codec.CollValue[types.GameState](cdc)),
		Player: collections.NewItem(sb, types.PlayerKey, "player",
			codec.CollValue[types.Player](cdc)),
		Pending: collections.NewItem(sb, types.PendingKey, "pending",
			codec.CollValue[types.TickLog](cdc)),
		Blocks: collections.NewMap(sb, types.BlocksKey, "blocks",
			collections.TripleKeyCodec(collections.Int32Key, collections.Int32Key, collections.Int64Key),
			collections.Uint32Value),
		Log: collections.NewMap(sb, types.LogKey, "log",
			collections.Uint64Key, codec.CollValue[types.TickLog](cdc)),
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

// GetAuthority returns the x/mc module's authority.
func (k *Keeper) GetAuthority() string {
	return k.authority
}

func errIsNotFound(err error) bool {
	return errors.Is(err, collections.ErrNotFound)
}
