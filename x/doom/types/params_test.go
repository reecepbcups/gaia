package types_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

func TestParamsValidate(t *testing.T) {
	t.Parallel()

	valid := types.DefaultParams()

	testCases := []struct {
		name    string
		mutate  func(*types.Params)
		expErr  string
		wantErr bool
	}{
		{
			name:   "default params are inert but valid",
			mutate: func(*types.Params) {},
		},
		{
			name:   "a real wad hash",
			mutate: func(p *types.Params) { p.WadHash = types.SharewareWADHash },
		},
		{
			name:    "wad hash is not hex",
			mutate:  func(p *types.Params) { p.WadHash = strings.Repeat("z", 64) },
			expErr:  "not hex",
			wantErr: true,
		},
		{
			name:    "wad hash is the wrong length",
			mutate:  func(p *types.Params) { p.WadHash = "abcd" },
			expErr:  "must be 32 bytes",
			wantErr: true,
		},
		{
			name:    "zero tics per block would stall the game",
			mutate:  func(p *types.Params) { p.TicsPerBlock = 0 },
			expErr:  "tics_per_block",
			wantErr: true,
		},
		{
			name:    "too many tics per block",
			mutate:  func(p *types.Params) { p.TicsPerBlock = types.MaxTicsPerBlock + 1 },
			expErr:  "tics_per_block",
			wantErr: true,
		},
		{
			name:   "pruning the input log is allowed",
			mutate: func(p *types.Params) { p.InputHistory = 100_000 },
		},
		{
			name:    "controller must be bech32",
			mutate:  func(p *types.Params) { p.Controller = "not-an-address" },
			expErr:  "invalid controller",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := valid
			tc.mutate(&params)

			err := params.Validate()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expErr)
		})
	}
}

func TestDefaultGenesisIsValid(t *testing.T) {
	t.Parallel()

	require.NoError(t, types.DefaultGenesis().Validate())
}
