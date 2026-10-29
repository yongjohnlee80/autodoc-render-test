package main

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// TestOverlayStaysInsideTheBuffer: a card smaller than the panel gets the panel clipped, and the
// bytes past each row's pixels (the pitch's padding) are left alone.
func TestOverlayStaysInsideTheBuffer(t *testing.T) {
	const w, h, pitch = 50, 20, 50*4 + 8
	pix := make([]byte, pitch*h)
	for i := range pix {
		pix[i] = 0x11
	}
	b := &plugin.SurfaceBuffer{W: w, H: h, Pitch: pitch, Format: "rgba8", Pix: pix}
	drawOverlay(b, 2, []string{"a line far longer than fifty pixels of card"}, []float64{10, 20, 40}, true, false)
	changed := 0
	for y := range h {
		for x := w * 4; x < pitch; x++ {
			if pix[y*pitch+x] != 0x11 {
				t.Fatalf("row %d: padding byte %d written", y, x)
			}
		}
		for x := 0; x < w*4; x++ {
			if pix[y*pitch+x] != 0x11 {
				changed++
			}
		}
	}
	if changed == 0 {
		t.Fatal("the overlay drew nothing")
	}
}
