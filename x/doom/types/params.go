package types

import (
	"encoding/hex"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// SharewareWADHash is sha256 of the v1.9 shareware DOOM1.WAD, the IWAD a chain
// gets by default. Override it in params to run a different one.
const SharewareWADHash = "1d7d43be501e67d927e415e0b8f3e29c3bf33075e859721816f652a526cac771"

// MaxTicsPerBlock caps how far a single block may advance the game. A block
// that runs a second of DOOM is already stretching what "live" means, and the
// cap keeps a bad param from stalling the chain.
const MaxTicsPerBlock = 35

// DefaultParams returns an inert module: DOOM runs at its native tic rate and
// is open to anybody, but only once genesis names a WAD.
func DefaultParams() Params {
	return Params{
		// Empty means the module is inert: a plain gaia chain does not run DOOM.
		// A doom chain sets this in genesis, see x/doom/README.md.
		WadHash:      "",
		TicsPerBlock: 1,
		InputHistory: 0,
		Controller:   "",
	}
}

// Validate checks the params are usable.
func (p Params) Validate() error {
	if p.WadHash != "" {
		h, err := hex.DecodeString(p.WadHash)
		if err != nil {
			return fmt.Errorf("wad_hash is not hex: %w", err)
		}
		if len(h) != 32 {
			return fmt.Errorf("wad_hash must be 32 bytes, got %d", len(h))
		}
	}

	if p.TicsPerBlock == 0 || p.TicsPerBlock > MaxTicsPerBlock {
		return fmt.Errorf("tics_per_block must be between 1 and %d, got %d", MaxTicsPerBlock, p.TicsPerBlock)
	}

	if p.Controller != "" {
		if _, err := sdk.AccAddressFromBech32(p.Controller); err != nil {
			return fmt.Errorf("invalid controller %q: %w", p.Controller, err)
		}
	}

	return nil
}
