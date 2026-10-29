package main

import (
	"image"
	"image/color"
	"math"

	"github.com/yongjohnlee80/autodoc/plugin"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	lineH    = 15 // basicfont's 13 px glyphs, with spacing
	pad      = 8
	graphH   = 60   // the frame-time graph's height, in overlay px
	graphMax = 50.0 // ms at the graph's top; slower frames clip there
)

var (
	panelBG  = color.RGBA{0, 0, 0, 170}
	textFG   = color.RGBA{235, 235, 235, 255}
	good     = color.RGBA{80, 200, 120, 255}  // within budget
	late     = color.RGBA{230, 180, 60, 255}  // within two refreshes
	bad      = color.RGBA{230, 80, 70, 255}   // later
	budgetFG = color.RGBA{255, 255, 255, 140} // the 16.7 ms line
)

// drawOverlay draws the metrics panel at the surface's top left: the lines, then the graph of the
// latest shown-frame intervals. It is drawn at 1x and scaled by the display's scale, rounded, so
// it reads the same on any screen.
func drawOverlay(b *plugin.SurfaceBuffer, scale float32, lines []string, history []float64, paused, bgra bool) {
	if len(lines) == 0 {
		lines = []string{"measuring..."}
	}
	if paused {
		lines = append(lines[:len(lines):len(lines)], "paused: space resumes")
	}
	width := 0
	for _, l := range lines {
		width = max(width, len(l)*7)
	}
	width += 2 * pad
	height := pad + len(lines)*lineH + pad + graphH + pad
	k := max(1, int(math.Round(float64(scale))))
	if width*k > b.W || height*k > b.H { // a small card: fall back to 1x, clipped
		k = 1
	}

	panel := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(panel, panel.Bounds(), image.NewUniform(panelBG), image.Point{}, draw.Src)
	d := &font.Drawer{Dst: panel, Src: image.NewUniform(textFG), Face: basicfont.Face7x13}
	for i, l := range lines {
		d.Dot = fixed.P(pad, pad+11+i*lineH)
		d.DrawString(l)
	}
	drawGraph(panel, image.Rect(pad, height-pad-graphH, width-pad, height-pad), history)
	if bgra {
		swapRB(panel.Pix)
	}

	dst := &image.RGBA{Pix: b.Pix, Stride: b.Pitch, Rect: image.Rect(0, 0, b.W, b.H)} // b.RGBA() is Linux's only
	// the whole panel's rectangle: Scale clips to dst, so a small card crops the panel rather than
	// squashing it
	draw.NearestNeighbor.Scale(dst, image.Rect(0, 0, width*k, height*k), panel, panel.Bounds(), draw.Over, nil)
}

// drawGraph draws one bar per shown frame, newest on the right, and the budget's line.
func drawGraph(dst *image.RGBA, r image.Rectangle, history []float64) {
	n := len(history)
	if n > r.Dx() {
		history = history[n-r.Dx():]
		n = r.Dx()
	}
	y := func(ms float64) int {
		return r.Max.Y - int(math.Min(ms, graphMax)/graphMax*float64(r.Dy()))
	}
	for i, v := range history {
		c := good
		switch {
		case v > 2*budget:
			c = bad
		case v > budget*1.05:
			c = late
		}
		x := r.Max.X - n + i
		draw.Draw(dst, image.Rect(x, y(v), x+1, r.Max.Y), image.NewUniform(c), image.Point{}, draw.Over)
	}
	by := y(budget)
	draw.Draw(dst, image.Rect(r.Min.X, by, r.Max.X, by+1), image.NewUniform(budgetFG), image.Point{}, draw.Over)
}

// swapRB turns RGBA pixels into BGRA ones, in place.
func swapRB(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
	}
}
