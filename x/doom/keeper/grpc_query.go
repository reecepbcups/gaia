package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	k *Keeper
}

// NewQueryServerImpl returns a types.QueryServer backed by the given keeper.
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
	return &types.QueryStateResponse{State: gs}, nil
}

// Frame reads the screen straight out of the engine. It is node-local by
// nature: the framebuffer is derived from the sim, never stored, so this answers
// from whatever this node has rendered rather than from committed state.
func (q queryServer) Frame(ctx context.Context, _ *types.QueryFrameRequest) (*types.QueryFrameResponse, error) {
	eng := q.k.Engine()
	if eng == nil {
		return nil, status.Error(codes.Unavailable, "no game running on this node")
	}

	pixels, palette, err := eng.Frame(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// The commitment comes out of the store rather than off the engine: hashing
	// 7MB per query would cost more than rendering. The tic has to come from
	// the same read, or a caller polling across a commit sees a tic and a hash
	// from different blocks.
	gs, err := q.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryFrameResponse{
		Tic:       gs.Tic,
		Width:     engine.ScreenWidth,
		Height:    engine.ScreenHeight,
		Pixels:    pixels,
		Palette:   palette,
		StateHash: gs.StateHash,
	}, nil
}
