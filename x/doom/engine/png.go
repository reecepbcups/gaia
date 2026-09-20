package engine

import (
	"image"
	"image/color"
	"image/png"
	"io"
)

// EncodePNG writes a paletted frame as a png, so a screen pulled out of a
// block or rebuilt by a replay can be looked at rather than just hashed.
func EncodePNG(w io.Writer, pixels, palette []byte) error {
	pal := make(color.Palette, 256)
	for i := range pal {
		// Palette entries are BGRA, matching DOOM's in-memory colour struct.
		pal[i] = color.RGBA{R: palette[i*4+2], G: palette[i*4+1], B: palette[i*4], A: 255}
	}

	img := image.NewPaletted(image.Rect(0, 0, ScreenWidth, ScreenHeight), pal)
	copy(img.Pix, pixels)

	return png.Encode(w, img)
}
