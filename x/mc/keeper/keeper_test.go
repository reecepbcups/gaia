package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	"github.com/cosmos/gaia/v29/x/mc"
	"github.com/cosmos/gaia/v29/x/mc/keeper"
	"github.com/cosmos/gaia/v29/x/mc/types"
)

const authority = "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn"

func setup(t *testing.T) (sdk.Context, *keeper.Keeper) {
	t.Helper()

	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient")).Ctx
	encCfg := moduletestutil.MakeTestEncodingConfig(mc.AppModuleBasic{})

	k := keeper.NewKeeper(encCfg.Codec, runtime.NewKVStoreService(key), authority)
	require.NoError(t, k.InitGenesis(ctx, types.DefaultGenesis()))

	return ctx, k
}

func move(x, y, z int64) types.Action {
	return types.Action{Kind: &types.Action_Move{Move: &types.Move{X: x, Y: y, Z: z}}}
}

func dig(x, y, z int32) types.Action {
	return types.Action{Kind: &types.Action_Dig{Dig: &types.Dig{
		Pos: types.BlockPos{X: x, Y: y, Z: z},
	}}}
}

func place(x, y, z int32, state uint32) types.Action {
	return types.Action{Kind: &types.Action_Place{Place: &types.Place{
		Pos:   types.BlockPos{X: x, Y: y, Z: z},
		State: state,
	}}}
}

// The block is the clock and nothing else moves it, so an empty block still
// has to advance the world.
func TestEndBlockerAdvancesTheClock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		ticksPerBlock uint32
		blocks        int
		wantTick      uint64
	}{
		{"one a block", 1, 3, 3},
		{"two a block", 2, 3, 6},
		{"idle", 1, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, k := setup(t)

			params := types.DefaultParams()
			params.TicksPerBlock = tc.ticksPerBlock
			require.NoError(t, k.Params.Set(ctx, params))

			for i := 0; i < tc.blocks; i++ {
				require.NoError(t, k.EndBlocker(ctx))
			}

			gs, err := k.GameState.Get(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantTick, gs.Tick)
		})
	}
}

func TestEndBlockerAppliesActions(t *testing.T) {
	t.Parallel()

	ctx, k := setup(t)

	require.NoError(t, k.Pending.Set(ctx, types.TickLog{Actions: []types.Action{
		move(1024, 2048, 3072),
		dig(8, 64, 8),
		place(9, 65, 9, 42),
	}}))

	require.NoError(t, k.EndBlocker(ctx))

	player, err := k.Player.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1024), player.X)
	require.Equal(t, int64(2048), player.Y)
	require.Equal(t, int64(3072), player.Z)

	edits, err := k.ChunkEdits(ctx, 0, 0)
	require.NoError(t, err)
	require.Equal(t, []types.BlockEdit{
		{Pos: types.BlockPos{X: 8, Y: 64, Z: 8}, State: types.StateAir},
		{Pos: types.BlockPos{X: 9, Y: 65, Z: 9}, State: 42},
	}, edits)

	// The log is keyed by the tick the block started on, which is what a client
	// reading the world back has to line up with.
	logged, err := k.Log.Get(ctx, 0)
	require.NoError(t, err)
	require.Len(t, logged.Actions, 3)

	// Nothing happened in the next block, so nothing is logged for it.
	require.NoError(t, k.EndBlocker(ctx))
	_, err = k.Log.Get(ctx, 1)
	require.Error(t, err)
}

// The commitment is the whole point of putting the world in consensus: two
// nodes fed the same log have to land on the same hash, and a different log has
// to land somewhere else.
func TestCommitmentFollowsTheLog(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, actions []types.Action) []byte {
		t.Helper()

		ctx, k := setup(t)
		require.NoError(t, k.Pending.Set(ctx, types.TickLog{Actions: actions}))
		require.NoError(t, k.EndBlocker(ctx))

		gs, err := k.GameState.Get(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, gs.StateHash)

		return gs.StateHash
	}

	played := []types.Action{move(1, 2, 3), dig(0, 64, 0)}

	require.Equal(t, run(t, played), run(t, played))
	require.NotEqual(t, run(t, played), run(t, []types.Action{move(1, 2, 4), dig(0, 64, 0)}))
}

func TestSetBlockOutsideTheWorld(t *testing.T) {
	t.Parallel()

	ctx, k := setup(t)

	require.NoError(t, k.Pending.Set(ctx, types.TickLog{Actions: []types.Action{
		place(0, types.MinY-1, 0, 1),
	}}))

	require.ErrorIs(t, k.EndBlocker(ctx), types.ErrOutOfWorld)
}
