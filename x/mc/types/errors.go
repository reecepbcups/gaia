package types

import "cosmossdk.io/errors"

var (
	// ErrNotController is returned when params name a controller and somebody
	// else tries to play.
	ErrNotController = errors.Register(ModuleName, 2, "sender is not the controller")

	// ErrSeatTaken is returned when a second address tries to play. This is a
	// single player world.
	ErrSeatTaken = errors.Register(ModuleName, 3, "another player already holds the seat")

	// ErrEmptyAction is returned for an action with no kind set, which is a
	// client bug rather than a move.
	ErrEmptyAction = errors.Register(ModuleName, 4, "action has no kind")

	// ErrOutOfWorld is returned for a block edit above the ceiling or below the
	// floor.
	ErrOutOfWorld = errors.Register(ModuleName, 5, "block position is outside the world")
)
