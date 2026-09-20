// Command replay rebuilds the game from an exported input log and prints the
// state commitment it arrives at.
//
// This is the claim the module makes, checked from outside the node: the chain
// stores button presses, not a game, and anyone holding the same WAD and the
// same wasm can turn that log back into the exact state the chain committed.
// A hash that matches `gaia.doom.v1.Query/State` means the screen you are
// watching is a pure function of what is in the blocks.
//
//	gaiad export --home ~/.gaia-doom > export.json
//	go run ./x/doom/scripts/replay -genesis export.json -wad ~/.cache/gaia-doom-iwad.wad
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"

	"github.com/cosmos/gaia/v29/x/doom/engine"
)

// exported is the slice of an exported genesis this tool needs.
type exported struct {
	AppState struct {
		Doom struct {
			Params struct {
				WadHash string `json:"wad_hash"`
			} `json:"params"`
			Inputs []uint32 `json:"inputs"`
		} `json:"doom"`
	} `json:"app_state"`
}

func main() {
	genesis := flag.String("genesis", "", "exported genesis json, from `gaiad export`")
	wadPath := flag.String("wad", "", "the IWAD the chain was started with")
	shot := flag.String("png", "", "write the final frame here")
	stop := flag.Uint64("tic", 0, "stop after this many tics, 0 for the whole log")
	flag.Parse()

	if err := run(*genesis, *wadPath, *shot, *stop); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

func run(genesis, wadPath, shot string, stop uint64) error {
	if genesis == "" || wadPath == "" {
		return fmt.Errorf("-genesis and -wad are both required")
	}

	f, err := os.Open(genesis)
	if err != nil {
		return err
	}
	defer f.Close()

	var exp exported
	if err := json.NewDecoder(f).Decode(&exp); err != nil {
		return fmt.Errorf("decode %s: %w", genesis, err)
	}

	wad, err := os.ReadFile(wadPath)
	if err != nil {
		return err
	}

	// The chain only commits to the WAD's hash, so this is the one thing that
	// has to be checked by hand rather than derived.
	sum := sha256.Sum256(wad)
	got := hex.EncodeToString(sum[:])
	if want := exp.AppState.Doom.Params.WadHash; got != want {
		return fmt.Errorf("wad mismatch: have %s, chain played %s", got, want)
	}
	fmt.Printf("wad      %s (matches params.wad_hash)\n", got)

	inputs := exp.AppState.Doom.Inputs
	if stop > 0 {
		if stop > uint64(len(inputs)) {
			return fmt.Errorf("log only reaches tic %d", len(inputs))
		}
		inputs = inputs[:stop]
	}
	fmt.Printf("inputs   %d tics from the chain's log\n", len(inputs))

	ctx := context.Background()

	eng, err := engine.New(ctx, wad, io.Discard)
	if err != nil {
		return fmt.Errorf("boot doom: %w", err)
	}
	defer eng.Close(ctx)

	if err := eng.Replay(ctx, inputs); err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	hash, err := eng.StateHash(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("tic      %d\n", eng.Tics())
	fmt.Printf("hash     %s\n", hex.EncodeToString(hash))

	if shot == "" {
		return nil
	}

	pixels, palette, err := eng.Frame(ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(shot+".raw", pixels, 0o644); err != nil {
		return err
	}
	return writePNG(shot, pixels, palette)
}

// writePNG renders a paletted frame, so the replay can be compared with the
// browser by eye as well as by hash.
func writePNG(path string, pixels, palette []byte) error {
	pal := make(color.Palette, 256)
	for i := range pal {
		// BGRA, matching DOOM's in-memory colour struct.
		pal[i] = color.RGBA{R: palette[i*4+2], G: palette[i*4+1], B: palette[i*4], A: 255}
	}

	img := image.NewPaletted(image.Rect(0, 0, engine.ScreenWidth, engine.ScreenHeight), pal)
	copy(img.Pix, pixels)

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	return png.Encode(out, img)
}
