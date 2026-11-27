package scene

import (
	"syscall"
	"testing"
)

// TestVulkanDrawsWhatTheCPUDraws: the Vulkan renderer's read-back picture is the CPU renderer's,
// within rasterization noise. Skipped where no Vulkan device exports dma-bufs (hosted CI, Mesa's
// software Vulkan); run where there is a GPU.
func TestVulkanDrawsWhatTheCPUDraws(t *testing.T) {
	v, err := NewVulkan()
	if err != nil {
		t.Skip("no Vulkan device here:", err)
	}
	defer v.Close()
	const w, h, pitch = 160, 100, 160*4 + 32
	vb := make([]byte, pitch*h)
	cb := make([]byte, pitch*h)
	for i := 0; i < 3; i++ { // the target is made on the first frame and reused after
		if err := v.Draw(1.25, vb, w, h, pitch, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewCPU().Draw(1.25, cb, w, h, pitch, true); err != nil {
		t.Fatal(err)
	}
	off := 0
	for y := range h {
		for x := range w {
			for k := range 3 {
				d := int(vb[y*pitch+x*4+k]) - int(cb[y*pitch+x*4+k])
				if d > 48 || d < -48 {
					off++
					break
				}
			}
		}
	}
	if off > w*h/100 {
		t.Fatalf("%d of %d pixels differ by more than 48: the GPU picture is not the CPU's", off, w*h)
	}
}

// TestAnExportedTargetIsALinearPlaneAtItsStart: an exported target is what AutoDoc admits, one
// LINEAR plane from the DMA-BUF's first byte with a pitch that holds a row, the device renders
// into it again and again, and Free closes its DMA-BUF. Skipped where no Vulkan device exports a
// LINEAR DMA-BUF (hosted CI, Mesa's software Vulkan); run where there is a GPU.
func TestAnExportedTargetIsALinearPlaneAtItsStart(t *testing.T) {
	v, err := NewVulkan()
	if err != nil {
		t.Skip("no Vulkan device here:", err)
	}
	defer v.Close()
	const w, h = 160, 100
	tg, err := v.NewTarget(w, h, false)
	if err != nil {
		t.Skip("this device exports no LINEAR DMA-BUF:", err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(tg.FD(), &st); err != nil {
		t.Fatalf("the target's fd %d: %v", tg.FD(), err)
	}
	if tg.Pitch() < w*4 {
		t.Fatalf("pitch %d holds no row of %d pixels", tg.Pitch(), w)
	}
	for i := range 3 {
		if err := tg.Render(float64(i)); err != nil {
			t.Fatal(err)
		}
	}
	fd := tg.FD()
	tg.Free()
	tg.Free() // twice is once
	if err := syscall.Fstat(fd, &st); err == nil {
		t.Fatalf("Free left the target's DMA-BUF %d open", fd)
	}
}
