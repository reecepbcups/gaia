package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of the mc module.
	ModuleName = "mc"

	// StoreKey is the primary module store key.
	StoreKey = ModuleName

	// RouterKey is the message route for the mc module.
	RouterKey = ModuleName
)

var (
	// ParamsKey holds the module parameters.
	ParamsKey = collections.NewPrefix(0)

	// GameStateKey holds the tick counter and the state commitment.
	GameStateKey = collections.NewPrefix(1)

	// PlayerKey holds where the player is.
	PlayerKey = collections.NewPrefix(2)

	// PendingKey holds the actions sent during the current block, consumed by
	// the EndBlocker. It lives in the store rather than in memory so a node
	// that restarts mid-block still agrees on what was pressed.
	PendingKey = collections.NewPrefix(3)

	// BlocksKey holds the blocks that differ from the generated world, keyed by
	// chunk so a client can ask for one chunk's worth.
	BlocksKey = collections.NewPrefix(4)

	// LogKey holds the input log, keyed by tick. Replaying it from tick 0
	// reproduces the world, which is why it and not the world is what the chain
	// actually stores.
	LogKey = collections.NewPrefix(5)
)
