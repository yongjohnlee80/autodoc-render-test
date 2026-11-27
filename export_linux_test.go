package main

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc-render-test/scene"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// fakeExporter makes targets backed by temporary files: what export does with them is the cell's.
type fakeExporter struct {
	scene.Renderer
	mu          sync.Mutex
	made, freed int
	fail        error
}

type fakeTarget struct {
	e     *fakeExporter
	f     *os.File
	pitch int
}

func (e *fakeExporter) NewTarget(w, h int, bgra bool) (scene.Target, error) {
	if e.fail != nil {
		return nil, e.fail
	}
	f, err := os.CreateTemp("", "target")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	e.mu.Lock()
	e.made++
	e.mu.Unlock()
	return &fakeTarget{e: e, f: f, pitch: w * 4}, nil
}

func (t *fakeTarget) FD() int              { return int(t.f.Fd()) }
func (t *fakeTarget) Pitch() int           { return t.pitch }
func (t *fakeTarget) Render(float64) error { return nil }
func (t *fakeTarget) Free() {
	t.f.Close()
	t.e.mu.Lock()
	t.e.freed++
	t.e.mu.Unlock()
}

// hosted is a card's surface on a scripted host, which answers each registration as accept says
// and records it.
func hosted(t *testing.T, kinds []plugin.SurfaceKind, accept bool) (*card, *plugin.Surface, func() []map[string]any) {
	t.Helper()
	host, child, err := plugin.NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(child.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	t.Setenv(plugin.SurfaceFDEnv, strconv.Itoa(fd))
	plug, ok, err := plugin.OpenPluginSurfaceConn()
	if err != nil || !ok {
		t.Fatal(err)
	}
	sf, setInfo := plugin.NewSurface(plug)
	var mu sync.Mutex
	var adds []map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			m, g, err := host.Recv()
			if err != nil {
				return
			}
			g.CloseAll()
			if plugin.WireOp(m) != plugin.OpBufferAdd {
				continue
			}
			mu.Lock()
			adds = append(adds, m)
			mu.Unlock()
			id := n(m["id"])
			if accept {
				host.Send(plugin.BufferFreeMsg(id))
			} else {
				host.Send(map[string]any{"op": plugin.OpBufferRefused, "id": id, "reason": "budget"})
			}
		}
	}()
	t.Cleanup(func() { sf.Close(); host.Shutdown(); <-done; host.Close() })

	pool, err := os.CreateTemp("", "pool")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	os.Remove(pool.Name())
	pool.Truncate(4096)
	spec := plugin.SurfaceBufferSpec{ID: 2, Kind: "memory", Format: "rgba8", W: 1, H: 1, Pitch: 4, Size: 4096}
	if err := host.Send(plugin.BuffersMsg(1, []plugin.SurfaceBufferSpec{spec}), int(pool.Fd())); err != nil {
		t.Fatal(err)
	}
	setInfo(plugin.SurfaceInfo{Gen: 1, W: 16, H: 8, Scale: 1, Kinds: kinds})
	c := newCard()
	var cancel context.CancelFunc
	// NOTE: a bound on a failing run alone: every registration is answered at once.
	c.ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c, sf, func() []map[string]any { mu.Lock(); defer mu.Unlock(); return append([]map[string]any(nil), adds...) }
}

// n is a wire integer.
func n(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case uint64:
		return int(x)
	case int:
		return x
	}
	return -1
}

var bothKinds = []plugin.SurfaceKind{{Kind: "memory", Format: "rgba8"}, {Kind: "dmabuf", Format: "bgra8"}}

// export registers three images of the surface's pixel size, in the format the window's DMA-BUF
// kind names, and keeps each by the id AutoDoc accepted it under.
func TestExportRegistersTheGenerationsThreeImages(t *testing.T) {
	c, sf, adds := hosted(t, bothKinds, true)
	ex := &fakeExporter{}
	e, why := c.export(sf, ex)
	if e == nil {
		t.Fatalf("export registered nothing: %s", why)
	}
	if len(e.targets) != plugin.MaxAddedBuffers || e.gen != 1 || ex.made != 3 || ex.freed != 0 {
		t.Fatalf("%d targets of generation %d, %d made, %d freed; want three of generation 1, none freed", len(e.targets), e.gen, ex.made, ex.freed)
	}
	sent := adds()
	if len(sent) != 3 {
		t.Fatalf("%d registrations reached the host, want 3", len(sent))
	}
	for _, m := range sent {
		id := n(m["id"])
		planes, _ := m["planes"].([]any)
		plane, _ := append(planes, nil)[0].(map[string]any)
		if m["format"] != "bgra8" || n(m["w"]) != 16 || n(m["h"]) != 8 || n(m["gen"]) != 1 || n(plane["pitch"]) != 64 || e.targets[id] == nil {
			t.Errorf("registration %v; want a bgra8 16×8 image of generation 1, pitch 64, kept under its id", m)
		}
	}
	if c.format != "bgra8" {
		t.Errorf("the card's format is %q", c.format)
	}
	e.free()
	if ex.freed != 3 {
		t.Errorf("free freed %d of the three", ex.freed)
	}
}

// Refused, export keeps nothing and says why, and the image it made is freed: the card copies.
// Nor does it make an image when the window offers no DMA-BUF frames, or when the device exports none.
func TestExportRefusedOrUnofferedFallsBackToCopying(t *testing.T) {
	c, sf, adds := hosted(t, bothKinds, false)
	ex := &fakeExporter{}
	e, why := c.export(sf, ex)
	if e != nil || !strings.Contains(why, "refused") || !strings.Contains(why, "copying") {
		t.Fatalf("refused: %+v, %q; want nothing kept and the refusal said", e, why)
	}
	if ex.made != 1 || ex.freed != 1 || len(adds()) != 1 {
		t.Errorf("refused: %d made, %d freed, %d registrations; want the one image made, registered and freed", ex.made, ex.freed, len(adds()))
	}

	ex = &fakeExporter{fail: errors.New("no LINEAR export")}
	if e, why := c.export(sf, ex); e != nil || !strings.Contains(why, "no LINEAR export") {
		t.Errorf("a device that exports none: %+v, %q", e, why)
	}

	c, sf, adds = hosted(t, bothKinds[:1], true)
	ex = &fakeExporter{}
	if e, why := c.export(sf, ex); e != nil || !strings.Contains(why, "offers no DMA-BUF") || ex.made != 0 || len(adds()) != 0 {
		t.Errorf("no DMA-BUF kind offered: %+v, %q, %d made, %d registrations", e, why, ex.made, len(adds()))
	}
}

// The title carries the second's figures where nothing can be drawn over the frame.
func TestTheTitleCarriesTheSecondsFigures(t *testing.T) {
	s := newStats(os.Getpid())
	s.setRenderer("vulkan, zero-copy", "a GPU", "")
	s.shown, s.hostCPU, s.selfCPU = 60, 7.4, 3
	if got, want := s.title(), "Render test: vulkan, zero-copy, 60 fps, AutoDoc CPU 7%, plugin CPU 3%"; got != want {
		t.Errorf("title %q, want %q", got, want)
	}
}

// r keeps the Vulkan device through every mode, so the generation's images are registered once:
// coming round to zero-copy finds them, and sends AutoDoc nothing more. (AutoDoc
// counts three registrations a generation and none can be taken back, so a second round would be
// refused.)
func TestZeroCopyAndBackRegistersOnce(t *testing.T) {
	c, sf, adds := hosted(t, bothKinds, true)
	ex := &fakeExporter{}
	var z zeroCopy
	if use, why := c.zeroFor(sf, ex, &z); !use {
		t.Fatalf("zero-copy not taken: %s", why)
	}
	// copied and CPU ask zeroFor nothing; then r comes round to zero-copy again
	for range 3 {
		if use, why := c.zeroFor(sf, ex, &z); !use {
			t.Fatalf("back in zero-copy, it was not taken: %s", why)
		}
	}
	if n := len(adds()); n != 3 || ex.made != 3 || ex.freed != 0 {
		t.Fatalf("%d registrations, %d images made, %d freed; want the three, once, all kept", n, ex.made, ex.freed)
	}
}

// After the Vulkan device failed it is closed and its images with it, while AutoDoc still counts
// their registrations: zero-copy can't register again at this size, on a device opened again. The card says that in
// its own words, naming the resize, and sends AutoDoc nothing; a resize registers again.
func TestAfterTheDeviceWentZeroCopySaysToResize(t *testing.T) {
	c, sf, adds := hosted(t, bothKinds, true)
	ex := &fakeExporter{}
	var z zeroCopy
	if use, why := c.zeroFor(sf, ex, &z); !use {
		t.Fatalf("zero-copy not taken: %s", why)
	}
	z.leaveDevice() // the device failed and was closed
	if ex.freed != 3 {
		t.Fatalf("leaving the device freed %d of its three images", ex.freed)
	}
	use, why := c.zeroFor(sf, ex, &z)
	if use || !strings.Contains(why, "used up") || !strings.Contains(why, "resize the card") {
		t.Fatalf("on a new device at the same size: use %v, %q; want it to copy and name the resize", use, why)
	}
	if n := len(adds()); n != 3 || ex.made != 3 {
		t.Fatalf("%d registrations, %d images made; want no more than the first three", n, ex.made)
	}
	z.leaveDevice() // with no images: nothing to free, and the note stands
	if _, why := c.zeroFor(sf, ex, &z); !strings.Contains(why, "resize the card") {
		t.Errorf("asked again: %q", why)
	}
}

// The Vulkan device is opened once while r cycles through every mode, the CPU's included; and a
// device that failed is opened again only at the next switch.
func TestRKeepsTheVulkanDeviceThroughEveryMode(t *testing.T) {
	opens := 0
	newVulkan = func() (*scene.Vulkan, error) { opens++; return &scene.Vulkan{}, nil }
	t.Cleanup(func() { newVulkan = scene.NewVulkan })
	var rs renderers
	names := ""
	for i := range 2 * modes {
		r, _, _ := rs.pick(i % modes)
		names += r.Name() + " "
	}
	if opens != 1 || names != "vulkan vulkan cpu vulkan vulkan cpu " {
		t.Fatalf("%d device opens over two rounds of r, renderers %q; want one open, and vulkan vulkan cpu each round", opens, names)
	}
	newVulkan = func() (*scene.Vulkan, error) { opens++; return nil, errors.New("no device") }
	rs.vk = nil // as dropVulkan leaves it
	if r, _, note := rs.pick(modeCopied); r.Name() != "cpu" || note != "no device" || opens != 2 {
		t.Fatalf("with no device: %s, %q, %d opens; want the CPU, the reason, one more try", r.Name(), note, opens)
	}
}
