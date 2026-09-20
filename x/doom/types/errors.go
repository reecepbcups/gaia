package types

import "cosmossdk.io/errors"

var (
	// ErrEngineUnavailable is returned when the node has no game running, which
	// usually means it was started without an IWAD.
	ErrEngineUnavailable = errors.Register(ModuleName, 2, "doom engine unavailable")

	// ErrNotController is returned when params name a controller and somebody
	// else tries to play.
	ErrNotController = errors.Register(ModuleName, 3, "sender is not the controller")

	// ErrUnknownButton is returned for button bits the engine does not map.
	ErrUnknownButton = errors.Register(ModuleName, 4, "unknown button bit")

	// ErrWADMismatch is returned when the node's IWAD is not the one params
	// commit to.
	ErrWADMismatch = errors.Register(ModuleName, 5, "wad does not match params")

	// ErrFrameMismatch is returned when a pushed frame is not the one the
	// engine drew. It usually just means the frame missed its block.
	ErrFrameMismatch = errors.Register(ModuleName, 6, "frame does not match what the engine rendered")
)
