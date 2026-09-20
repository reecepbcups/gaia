package types

import "fmt"

// PackBits, the run length encoding from the Macintosh toolbox, applied to a
// paletted DOOM screen.
//
// The scheme is worth about 3x on a DOOM frame, which matters because with
// MsgFrame the picture is block data and block data is the whole cost. It is
// byte oriented and has no tables or dictionaries, so the encoder is
// obviously deterministic, which a zstd or flate dependency would not be: a
// version bump that changed a single output byte would fork the chain.
//
// A control byte says what follows:
//
//	0..127    the next c+1 bytes are literal
//	129..255  the next byte repeats 257-c times, so 2..128
//	128       unused, so encoders cannot disagree about it
const (
	maxLiteral = 128
	maxRun     = 128
)

// RunLengthEncode compresses palette indices.
func RunLengthEncode(src []byte) []byte {
	out := make([]byte, 0, len(src)/2)

	for i := 0; i < len(src); {
		run := 1
		for i+run < len(src) && run < maxRun && src[i+run] == src[i] {
			run++
		}

		// A run of two is a wash and a run of one costs a byte, so short runs
		// go in a literal block with whatever is around them.
		if run > 2 {
			out = append(out, byte(257-run), src[i])
			i += run
			continue
		}

		start := i
		for i < len(src) && i-start < maxLiteral {
			// Stop the literal block once a run worth encoding starts.
			ahead := 1
			for i+ahead < len(src) && ahead < 3 && src[i+ahead] == src[i] {
				ahead++
			}
			if ahead >= 3 {
				break
			}
			i++
		}

		out = append(out, byte(i-start-1))
		out = append(out, src[start:i]...)
	}

	return out
}

// RunLengthDecode expands what RunLengthEncode produced into a buffer of
// exactly size bytes. Anything that does not decode to that length is
// rejected, because a frame is a fixed size and a client should not be made to
// guess.
func RunLengthDecode(src []byte, size int) ([]byte, error) {
	out := make([]byte, 0, size)

	for i := 0; i < len(src); {
		c := int(src[i])
		i++

		switch {
		case c < maxLiteral:
			n := c + 1
			if i+n > len(src) {
				return nil, fmt.Errorf("literal of %d bytes runs past the end at %d", n, i)
			}
			out = append(out, src[i:i+n]...)
			i += n

		case c == maxLiteral:
			return nil, fmt.Errorf("reserved control byte 128 at %d", i-1)

		default:
			if i >= len(src) {
				return nil, fmt.Errorf("run at %d has no value", i-1)
			}
			n := 257 - c
			for j := 0; j < n; j++ {
				out = append(out, src[i])
			}
			i++
		}

		if len(out) > size {
			return nil, fmt.Errorf("decodes to more than %d bytes", size)
		}
	}

	if len(out) != size {
		return nil, fmt.Errorf("decodes to %d bytes, want %d", len(out), size)
	}

	return out, nil
}
