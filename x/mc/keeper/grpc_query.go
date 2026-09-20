package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// MaxLogEntries caps one Log response. The gateway polls faster than blocks
// commit, so it only ever asks for a handful; the cap is there for a client
// that fell a long way behind.
const MaxLogEntries = 512

var _ types.QueryServer = queryServer{}

type queryServer struct {
	k *Keeper
}

// NewQueryServerImpl returns a QueryServer for the mc keeper.
func NewQueryServerImpl(k *Keeper) types.QueryServer {
	return queryServer{k: k}
}

func (q queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

func (q queryServer) State(ctx context.Context, _ *types.QueryStateRequest) (*types.QueryStateResponse, error) {
	gs, err := q.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	player, err := q.k.Player.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return nil, err
		}
		params, perr := q.k.Params.Get(ctx)
		if perr != nil {
			return nil, perr
		}
		player = params.Spawn()
	}

	return &types.QueryStateResponse{State: gs, Player: player}, nil
}

// Log hands back what happened from a tick onwards. This is the whole interface
// between consensus and the gateway: the gateway turns these into packets and
// never decides anything itself.
func (q queryServer) Log(ctx context.Context, req *types.QueryLogRequest) (*types.QueryLogResponse, error) {
	gs, err := q.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	rng := new(collections.Range[uint64]).StartInclusive(req.FromTick)

	iter, err := q.k.Log.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	res := &types.QueryLogResponse{Tick: gs.Tick}

	for ; iter.Valid() && len(res.Entries) < MaxLogEntries; iter.Next() {
		kv, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		res.Entries = append(res.Entries, types.LogEntry{Tick: kv.Key, Actions: kv.Value.Actions})
	}

	return res, nil
}

func (q queryServer) Chunk(ctx context.Context, req *types.QueryChunkRequest) (*types.QueryChunkResponse, error) {
	edits, err := q.k.ChunkEdits(ctx, req.X, req.Z)
	if err != nil {
		return nil, err
	}
	return &types.QueryChunkResponse{Edits: edits}, nil
}
