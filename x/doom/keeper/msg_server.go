package keeper

import (
	"bytes"
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

var _ types.MsgServer = msgServer{}

type msgServer struct {
	k *Keeper
}

// NewMsgServerImpl returns a types.MsgServer backed by the given keeper.
func NewMsgServerImpl(k *Keeper) types.MsgServer {
	return msgServer{k: k}
}

// buttonMask is every bit the engine knows how to map. Rejecting the rest keeps
// a typo from looking like it worked.
const buttonMask uint32 = (1 << 22) - 1

// Input records the buttons held down for this block. The last one to land wins,
// which is all single-player needs: a client sends about 35 of these a second
// and whichever made it into the block is what the tic sees.
func (s msgServer) Input(ctx context.Context, msg *types.MsgInput) (*types.MsgInputResponse, error) {
	params, err := s.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if params.WadHash == "" {
		return nil, types.ErrEngineUnavailable
	}

	if params.Controller != "" && params.Controller != msg.Player {
		return nil, errorsmod.Wrapf(types.ErrNotController, "game is controlled by %s", params.Controller)
	}

	if msg.Buttons&^buttonMask != 0 {
		return nil, errorsmod.Wrapf(types.ErrUnknownButton, "buttons 0x%x has bits above %d", msg.Buttons, 21)
	}

	if err := s.k.Pending.Set(ctx, msg.Buttons); err != nil {
		return nil, err
	}

	gs, err := s.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	return &types.MsgInputResponse{Tic: gs.Tic}, nil
}

// UpdateParams updates the module parameters.
func (s msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if s.k.GetAuthority() != msg.Authority {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized,
			"invalid authority; expected %s, got %s", s.k.GetAuthority(), msg.Authority)
	}

	if err := msg.Params.Validate(); err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}

	current, err := s.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	// Swapping the WAD out from under a running game would rewrite history: the
	// stored input log only reproduces the sim for the WAD it was recorded on.
	if current.WadHash != "" && msg.Params.WadHash != current.WadHash {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest,
			fmt.Sprintf("cannot change wad_hash on a running game (%s)", current.WadHash))
	}

	if err := s.k.Params.Set(ctx, msg.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}

// Frame accepts a screen into block data.
//
// The frame is checked against the one the engine is holding, which is the
// last tic of the previous block: by the time this runs the engine has not
// advanced yet, and every node agrees on what it drew. So the message cannot
// carry a picture the sim never produced, and it cannot carry an intermediate
// tic either, because that frame is already overwritten.
func (s msgServer) Frame(ctx context.Context, msg *types.MsgFrame) (*types.MsgFrameResponse, error) {
	params, err := s.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	if params.WadHash == "" {
		return nil, types.ErrEngineUnavailable
	}

	eng := s.k.Engine()
	if eng == nil {
		return nil, types.ErrEngineUnavailable
	}

	gs, err := s.k.GameState.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return nil, err
	}

	// A frame that missed its block is stale, not wrong. Saying so plainly
	// beats letting it through and showing the player an old screen.
	if msg.Tic != gs.Tic {
		return nil, errorsmod.Wrapf(types.ErrFrameMismatch, "frame is for tic %d, chain is on %d", msg.Tic, gs.Tic)
	}

	pixels, err := types.RunLengthDecode(msg.Pixels, engine.FrameSize)
	if err != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "decode pixels: %s", err)
	}

	want, palette, err := eng.Frame(ctx)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(pixels, want) {
		return nil, errorsmod.Wrap(types.ErrFrameMismatch, "pixels")
	}
	if !bytes.Equal(msg.Palette, palette) {
		return nil, errorsmod.Wrap(types.ErrFrameMismatch, "palette")
	}

	return &types.MsgFrameResponse{BlockBytes: uint64(len(msg.Pixels) + len(msg.Palette))}, nil
}
