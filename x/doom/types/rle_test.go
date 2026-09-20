package types_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

func TestRunLengthRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []byte
	}{
		{name: "empty", in: []byte{}},
		{name: "one byte", in: []byte{7}},
		{name: "short run", in: []byte{1, 1}},
		{name: "run worth encoding", in: []byte{2, 2, 2, 2, 2}},
		{name: "literals", in: []byte{1, 2, 3, 4, 5}},
		{name: "run then literals", in: bytes.Join([][]byte{bytes.Repeat([]byte{9}, 40), {1, 2, 3}}, nil)},
		{name: "longer than one control byte", in: bytes.Repeat([]byte{4}, 300)},
		{name: "literal block overflow", in: seqBytes(400)},
		{name: "alternating, the worst case", in: bytes.Repeat([]byte{0, 255}, 200)},
		{name: "a whole blank screen", in: make([]byte, 320*200)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := types.RunLengthDecode(types.RunLengthEncode(tc.in), len(tc.in))
			require.NoError(t, err)
			require.Equal(t, tc.in, got)
		})
	}
}

func TestRunLengthDecodeRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []byte
		size int
	}{
		{name: "reserved control byte", in: []byte{128, 1}, size: 1},
		{name: "literal past the end", in: []byte{5, 1, 2}, size: 6},
		{name: "run with no value", in: []byte{200}, size: 57},
		{name: "short of the frame size", in: types.RunLengthEncode([]byte{1, 2, 3}), size: 10},
		{name: "longer than the frame size", in: types.RunLengthEncode([]byte{1, 2, 3}), size: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := types.RunLengthDecode(tc.in, tc.size)
			require.Error(t, err)
		})
	}
}

// TestRunLengthRandom is the property the chain depends on: whatever the
// screen looks like, it survives the round trip.
func TestRunLengthRandom(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(1))

	for i := 0; i < 200; i++ {
		in := make([]byte, rng.Intn(2000))
		for j := range in {
			// Biased towards repeats, like a real frame.
			if j > 0 && rng.Intn(3) > 0 {
				in[j] = in[j-1]
				continue
			}
			in[j] = byte(rng.Intn(256))
		}

		got, err := types.RunLengthDecode(types.RunLengthEncode(in), len(in))
		require.NoError(t, err)
		require.Equal(t, in, got)
	}
}

func seqBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}
