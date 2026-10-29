package scene

import "testing"

// TestCPUDrawsWithinThePitch: a row's padding past w*4 bytes is the host's, and the CPU renderer
// leaves it alone; every pixel it draws is opaque, and the scene is more than the sky.
func TestCPUDrawsWithinThePitch(t *testing.T) {
	const w, h, pitch = 64, 48, 64*4 + 16
	buf := make([]byte, pitch*h)
	for i := range buf {
		buf[i] = 0xab
	}
	if err := NewCPU().Draw(1.5, buf, w, h, pitch, false); err != nil {
		t.Fatal(err)
	}
	notSky := 0
	for y := range h {
		for x := w * 4; x < pitch; x++ {
			if buf[y*pitch+x] != 0xab {
				t.Fatalf("row %d: padding byte %d written", y, x)
			}
		}
		for x := range w {
			p := buf[y*pitch+x*4:]
			if p[3] != 0xff {
				t.Fatalf("pixel %d,%d: alpha %d, want 255", x, y, p[3])
			}
			if p[0] != clearColour.R || p[1] != clearColour.G || p[2] != clearColour.B {
				notSky++
			}
		}
	}
	if notSky < w*h/10 {
		t.Fatalf("only %d of %d pixels differ from the sky: the scene was not drawn", notSky, w*h)
	}
}

// TestCPUBGRAIsRGBASwapped: the BGRA picture is the RGBA one with red and blue exchanged.
func TestCPUBGRAIsRGBASwapped(t *testing.T) {
	const w, h = 40, 30
	rgba := make([]byte, w*h*4)
	bgra := make([]byte, w*h*4)
	if err := NewCPU().Draw(0.7, rgba, w, h, w*4, false); err != nil {
		t.Fatal(err)
	}
	if err := NewCPU().Draw(0.7, bgra, w, h, w*4, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(rgba); i += 4 {
		if rgba[i] != bgra[i+2] || rgba[i+1] != bgra[i+1] || rgba[i+2] != bgra[i] || rgba[i+3] != bgra[i+3] {
			t.Fatalf("pixel %d: rgba %v, bgra %v", i/4, rgba[i:i+4], bgra[i:i+4])
		}
	}
}

// TestCopyRows: tightly packed rows land at the pitch, swapped for BGRA, padding untouched.
func TestCopyRows(t *testing.T) {
	src := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16} // 2x2 RGBA
	dst := make([]byte, 2*12)
	for i := range dst {
		dst[i] = 0xee
	}
	copyRows(dst, 12, src, 2, 2, true)
	want := []byte{3, 2, 1, 4, 7, 6, 5, 8, 0xee, 0xee, 0xee, 0xee, 11, 10, 9, 12, 15, 14, 13, 16, 0xee, 0xee, 0xee, 0xee}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d (dst %v)", i, dst[i], want[i], dst)
		}
	}
}
