package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MaxTicksPerBlock caps how far a single block may advance the world. Minecraft
// runs at 20 ticks a second, so a block worth more than a second of them is
// already stretching what "live" means.
const MaxTicksPerBlock = 20

// DefaultParams returns a superflat world open to anybody, advanced one tick
// per block.
func DefaultParams() Params {
	return Params{
		TicksPerBlock: 1,
		Controller:    "",
		GroundLevel:   64,
	}
}

// Validate checks the params are usable.
func (p Params) Validate() error {
	if p.TicksPerBlock == 0 || p.TicksPerBlock > MaxTicksPerBlock {
		return fmt.Errorf("ticks_per_block must be between 1 and %d, got %d", MaxTicksPerBlock, p.TicksPerBlock)
	}

	// Two blocks of headroom under the ceiling so the player has somewhere to
	// spawn, and clear of the floor so there is a world under them.
	if p.GroundLevel <= MinY || p.GroundLevel >= MinY+WorldHeight-2 {
		return fmt.Errorf("ground_level must be between %d and %d, got %d",
			MinY+1, MinY+WorldHeight-3, p.GroundLevel)
	}

	if p.Controller != "" {
		if _, err := sdk.AccAddressFromBech32(p.Controller); err != nil {
			return fmt.Errorf("invalid controller %q: %w", p.Controller, err)
		}
	}

	return nil
}

// Spawn is where a player that has never joined is put: standing on the ground
// at the origin.
func (p Params) Spawn() Player {
	return Player{
		X:     8 * FixedPointScale,
		Y:     int64(p.GroundLevel+1) * FixedPointScale,
		Z:     8 * FixedPointScale,
		Yaw:   0,
		Pitch: 0,
	}
}
