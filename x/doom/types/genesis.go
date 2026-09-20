package types

// DefaultGenesis returns a fresh game: default params and an empty input log,
// so the chain boots into DOOM's attract loop.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
	}
}

// Validate checks the genesis state is usable.
func (gs GenesisState) Validate() error {
	return gs.Params.Validate()
}
