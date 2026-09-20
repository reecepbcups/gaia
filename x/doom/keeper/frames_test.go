package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

func tics(frames []types.Frame) []uint64 {
	out := make([]uint64, len(frames))
	for i, f := range frames {
		out[i] = f.Tic
	}
	return out
}

func push(r *frameRing, from, to uint64) {
	for tic := from; tic <= to; tic++ {
		r.push(types.Frame{Tic: tic})
	}
}

func TestFrameRing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		last  uint64
		after uint64
		want  []uint64
	}{
		{name: "empty", last: 0, after: 0},
		{name: "all of a short ring", last: 3, after: 0, want: []uint64{1, 2, 3}},
		{name: "only what the caller is missing", last: 3, after: 1, want: []uint64{2, 3}},
		{name: "caught up", last: 3, after: 3},
		{name: "ahead of the node", last: 3, after: 9},
		// A caller further behind than the ring is deep gets what is left, not
		// an error: the newest frames are what it wants anyway.
		{name: "dropped the oldest", last: frameRingSize + 2, after: 0, want: seq(3, frameRingSize+2)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var r frameRing
			push(&r, 1, tc.last)

			require.Equal(t, tc.want, nonEmpty(tics(r.since(tc.after))))
		})
	}
}

func seq(from, to uint64) []uint64 {
	out := make([]uint64, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// nonEmpty normalises the empty result so the table can leave want unset.
func nonEmpty(got []uint64) []uint64 {
	if len(got) == 0 {
		return nil
	}
	return got
}
