package keeper

import (
	"sync"

	"github.com/cosmos/gaia/v29/x/doom/types"
)

// frameRingSize is how many rendered tics the node keeps for the client.
// The framebuffer is not consensus state, so this is pure buffering: deep
// enough that a client polling a few times a block never misses a tic, shallow
// enough to stay under a couple of megabytes.
const frameRingSize = 32

// frameRing holds the most recently rendered tics.
//
// A block draws tics_per_block screens and the engine overwrites its
// framebuffer on every one of them, so the frames have to be copied out as
// they happen or all but the last is lost. The EndBlocker writes here while the
// query reads, hence the mutex.
type frameRing struct {
	mu     sync.Mutex
	frames []types.Frame
}

// push adds a frame, dropping the oldest once the ring is full.
func (r *frameRing) push(frames ...types.Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.frames = append(r.frames, frames...)
	if len(r.frames) > frameRingSize {
		r.frames = append([]types.Frame(nil), r.frames[len(r.frames)-frameRingSize:]...)
	}
}

// since returns every buffered frame drawn after tic, oldest first. A caller
// that has fallen further behind than the ring is deep gets whatever is left:
// showing the newest frames beats replaying stale ones.
func (r *frameRing) since(tic uint64) []types.Frame {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]types.Frame, 0, len(r.frames))
	for _, f := range r.frames {
		if f.Tic > tic {
			out = append(out, f)
		}
	}
	return out
}

// FramesSince returns the tics this node has rendered after tic.
func (k *Keeper) FramesSince(tic uint64) []types.Frame {
	return k.frames.since(tic)
}
