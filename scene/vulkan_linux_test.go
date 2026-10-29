package scene

import "testing"

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
