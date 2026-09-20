package types

// DefaultGenesis returns a fresh superflat world with nobody in it.
func DefaultGenesis() *GenesisState {
	params := DefaultParams()

	return &GenesisState{
		Params: params,
		Player: params.Spawn(),
	}
}

// Validate checks the genesis state is usable.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	for _, e := range gs.Edits {
		if !e.Pos.InWorld() {
			return ErrOutOfWorld.Wrapf("edit at %d,%d,%d", e.Pos.X, e.Pos.Y, e.Pos.Z)
		}
	}

	return nil
}
