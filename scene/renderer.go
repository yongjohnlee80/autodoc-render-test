// Package scene is golib's gui/examples/scene3d made into a renderer a plugin draws with: a crate
// over a tiled floor, textured and lit, drawn by the CPU or on the GPU with Vulkan, into a
// buffer the caller owns (AutoDoc's shared memory). The scene, the CPU rasterizer and the Vulkan
// C code are copied from that example (Apache-2.0, the same author); keep them in step with it.
package scene

import "image"

// Renderer draws the scene at time t (seconds) into dst: w by h pixels, pitch bytes a row, BGRA
// byte order when bgra is set and RGBA otherwise.
type Renderer interface {
	Name() string
	Draw(t float64, dst []byte, w, h, pitch int, bgra bool) error
	Close()
}

func imagePt(w, h int) image.Point { return image.Pt(w, h) }

// copyRows copies tightly packed RGBA rows into dst at pitch, swapping red and blue for BGRA.
func copyRows(dst []byte, pitch int, src []byte, w, h int, bgra bool) {
	for y := range h {
		s := src[y*w*4 : (y+1)*w*4]
		d := dst[y*pitch : y*pitch+w*4]
		if !bgra {
			copy(d, s)
			continue
		}
		for x := 0; x < len(s); x += 4 {
			d[x], d[x+1], d[x+2], d[x+3] = s[x+2], s[x+1], s[x], s[x+3]
		}
	}
}
