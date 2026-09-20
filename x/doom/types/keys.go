package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of the doom module.
	ModuleName = "doom"

	// StoreKey is the primary module store key.
	StoreKey = ModuleName

	// RouterKey is the message route for the doom module.
	RouterKey = ModuleName
)

var (
	// ParamsKey holds the module parameters.
	ParamsKey = collections.NewPrefix(0)

	// GameStateKey holds the tic counter and the state commitment.
	GameStateKey = collections.NewPrefix(1)

	// InputsKey holds the input log, keyed by tic. Replaying it from tic 0
	// reproduces the game, which is why it and not the sim is what the chain
	// actually stores.
	InputsKey = collections.NewPrefix(2)
)

// PendingKey holds the button mask sent during the current block, consumed by
// the EndBlocker. It lives in the store rather than in memory so a node that
// restarts mid-block still agrees on what was pressed.
var PendingKey = collections.NewPrefix(3)
