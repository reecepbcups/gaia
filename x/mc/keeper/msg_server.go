package keeper

import (
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// MaxActionsPerTick caps a single transaction. A client sends about twenty
// position reports a second, so a block's worth is small; anything much larger
// is somebody trying to write the world in one go.
const MaxActionsPerTick = 256

var _ types.MsgServer = msgServer{}

type msgServer struct {
	k *Keeper
}

// NewMsgServerImpl returns a MsgServer for the mc keeper.
func NewMsgServerImpl(k *Keeper) types.MsgServer {
	return msgServer{k: k}
}

// Tick queues a block's worth of play. Nothing is applied here; the EndBlocker
// is the only thing that moves the world, so every transaction in the block
// lands on the same tick regardless of where it sat in the block.
func (s msgServer) Tick(ctx context.Context, msg *types.MsgTick) (*types.MsgTickResponse, error) {
	if _, err := sdk.AccAddressFromBech32(msg.Player); err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidAddress, "player: %s", err)
	}

	if len(msg.Actions) == 0 {
		return nil, types.ErrEmptyAction.Wrap("no actions")
	}
	if len(msg.Actions) > MaxActionsPerTick {
		return nil, fmt.Errorf("too many actions: %d, max %d", len(msg.Actions), MaxActionsPerTick)
	}

	params, err := s.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if params.Controller != "" && params.Controller != msg.Player {
		return nil, types.ErrNotController.Wrap(msg.Player)
	}

	player, err := s.k.Player.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return nil, err
		}
		player = params.Spawn()
	}

	// One seat. Whoever sends the first transaction keeps it.
	switch {
	case !player.Joined:
		player.Address = msg.Player
		player.Joined = true
		if err := s.k.Player.Set(ctx, player); err != nil {
			return nil, err
		}
	case player.Address != msg.Player:
		return nil, types.ErrSeatTaken.Wrapf("seat is held by %s", player.Address)
	}

	pending, err := s.k.Pending.Get(ctx)
	if err != nil {
		if !errIsNotFound(err) {
			return nil, err
		}
		pending = types.TickLog{}
	}

	for _, action := range msg.Actions {
		if action.Kind == nil {
			return nil, types.ErrEmptyAction
		}
	}

	pending.Actions = append(pending.Actions, msg.Actions...)
	if len(pending.Actions) > MaxActionsPerTick {
		return nil, fmt.Errorf("block already holds %d actions", len(pending.Actions))
	}

	if err := s.k.Pending.Set(ctx, pending); err != nil {
		return nil, err
	}

	gs, err := s.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	return &types.MsgTickResponse{Tick: gs.Tick}, nil
}

// UpdateParams updates the module parameters.
func (s msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if msg.Authority != s.k.GetAuthority() {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized,
			"invalid authority; expected %s, got %s", s.k.GetAuthority(), msg.Authority)
	}

	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}

	if err := s.k.Params.Set(ctx, msg.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}
