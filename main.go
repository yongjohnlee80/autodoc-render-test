// autodoc-render-test is an AutoDoc plugin for measuring rendering: a docked, resizable card whose
// pixel surface (protocol 10) shows golib's scene3d, drawn by the CPU or on the GPU with Vulkan,
// with the frame rate, frame times and memory drawn over it, updated once a second.
//
// Keys, while the card has them: r switches the renderer, u switches between paced (the next frame
// once AutoDoc showed the last) and unpaced (as fast as AutoDoc credits buffers), space pauses and
// resumes rendering (to watch AutoDoc idle), o hides or shows the overlay.
package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc-render-test/scene"
	"github.com/yongjohnlee80/autodoc/plugin"
)

type card struct {
	plugin.Base
	peer   *plugin.Peer
	ctx    context.Context
	cancel context.CancelFunc

	cellsW, cellsH int

	hidden atomic.Bool // the card's panel is closed: render nothing until it shows again

	paused   atomic.Bool
	overlay  atomic.Bool
	switchTo atomic.Bool // the render loop switches renderer before its next frame
	unpaced  atomic.Bool
	answered chan uint64 // the seq of each frame AutoDoc answered, for the paced loop
	wake     chan struct{}

	mu       sync.Mutex
	running  bool          // a render loop is running
	done     chan struct{} // closed when that loop has ended and freed its renderer
	measured bool          // the measure loop started (it runs until Close)
	info     plugin.SurfaceInfo
	format   string
	lines    []string // the overlay's text, the latest second's
	history  []float64
	stats    *stats
}

func newCard() *card {
	c := &card{wake: make(chan struct{}, 1), answered: make(chan uint64, 64), stats: newStats(os.Getppid())}
	c.overlay.Store(true)
	return c
}

// logf writes to the plugin's log (its stderr, kept by AutoDoc in
// ~/.local/state/autodoc/plugins/render-test.log): the lifecycle, to tell a plugin that never got
// a surface from one that stopped drawing.
func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s render-test: "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, a...)...)
}

func (c *card) Open(p *plugin.Peer, o plugin.Open) {
	logf("open: %dx%d cells, pid %d, host pid %d", o.Width, o.Height, os.Getpid(), os.Getppid())
	c.peer, c.cellsW, c.cellsH = p, o.Width, o.Height
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.drawCells()
}

// drawCells is the card where the host draws no pixels: the terminal, or the GUI before the
// first surface.
func (c *card) drawCells() {
	f := plugin.NewFrame(c.cellsW, c.cellsH)
	f.Text(1, 1, "Render test: a 3D scene on a pixel surface.", plugin.Style{})
	f.Text(1, 2, "Open it in AutoDoc's GUI (autodoc --gui) to see it.", plugin.Style{})
	c.peer.Frame(f)
}

func (c *card) Resize(w, h int) { c.cellsW, c.cellsH = w, h; c.drawCells() }

func (c *card) Key(k plugin.Key) {
	switch k.Text {
	case "r":
		c.switchTo.Store(true)
	case " ":
		if c.paused.Load() {
			c.paused.Store(false)
			c.poke()
		} else {
			c.paused.Store(true)
		}
	case "o":
		c.overlay.Store(!c.overlay.Load())
	case "u":
		c.unpaced.Store(!c.unpaced.Load())
	}
}

func (c *card) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Surface is each generation of the surface: the first after Open, then each resize, scale or caps
// change, and the one AutoDoc sends when the card shows again. It (re)starts the render loop when
// none runs: a loop that ended (the side channel closed under it) would otherwise leave the card
// blank for good.
func (c *card) Surface(sf *plugin.Surface, info plugin.SurfaceInfo) {
	w, h := info.Pixels()
	kinds := make([]string, len(info.Kinds))
	for i, k := range info.Kinds {
		kinds[i] = k.Kind + "/" + k.Format
	}
	logf("surface: gen %d, %.0fx%.0f dp at %.2f (%dx%d px), kinds %v", info.Gen, info.W, info.H, info.Scale, w, h, kinds)
	c.mu.Lock()
	c.info = info
	start := !c.running && c.ctx.Err() == nil
	if start {
		c.running, c.done = true, make(chan struct{})
	}
	measure := !c.measured
	c.measured = true
	done := c.done
	c.mu.Unlock()
	if start {
		go func() {
			defer close(done)
			c.render(sf)
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}()
	}
	if measure {
		go c.measure(sf)
	}
	c.poke()
}

// Hide is the card's panel closed (Esc, or its close): the plugin keeps running, so it stops
// drawing until Show, and AutoDoc has nothing to upload meanwhile.
func (c *card) Hide() {
	logf("hide")
	c.hidden.Store(true)
}

// Show is the card's panel open again.
func (c *card) Show() {
	logf("show")
	c.hidden.Store(false)
	c.poke()
}

// SurfacePointer is the pointer on the surface. The card takes no pointer input of its own (its
// keys are r, space and o); the method is what makes the card a plugin.SurfaceHandler, without
// which the SDK never calls Surface and the card stays on its line of text.
func (c *card) SurfacePointer(plugin.SurfacePointer) {}

// the SDK hands a surface only to a plugin.SurfaceHandler: this fails to compile without one
var _ plugin.SurfaceHandler = (*card)(nil)

// and Hide/Show only to a plugin.Hider
var _ plugin.Hider = (*card)(nil)

// Close is the plugin turned off, removed or updated, or AutoDoc quitting: the render loop stops,
// and the renderer (the Vulkan device and its buffers) is freed before Close returns, so the next
// start finds the GPU as this one found it.
func (c *card) Close() {
	reason := ""
	if c.peer != nil {
		reason = c.peer.CloseReason()
	}
	logf("close (%s)", reason)
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
		logf("closed: the render loop ended and freed its renderer")
	case <-time.After(2 * time.Second):
		logf("closed: the render loop did not end within 2 s")
	}
}

// open is the renderer to use: Vulkan when want is "vulkan" and a device takes it, else the CPU.
// The note says why a Vulkan request fell back.
func open(want string) (r scene.Renderer, device, note string) {
	if want == "vulkan" {
		v, err := scene.NewVulkan()
		if err == nil {
			return v, v.Device(), ""
		}
		note = err.Error()
	}
	return scene.NewCPU(), "", note
}

// render draws a frame into each buffer the host credits, as fast as it credits them: one frame
// in flight per buffer, so the rate it reaches is what AutoDoc draws. Paused, it draws one last
// frame saying so, then waits.
func (c *card) render(sf *plugin.Surface) {
	r, device, note := open("vulkan")
	defer func() {
		r.Close()
		logf("render loop ended; %s renderer freed", r.Name())
	}()
	c.stats.setRenderer(r.Name(), device, note)
	logf("render loop started: %s %s %s", r.Name(), device, note)
	var sceneT time.Duration // the scene's clock, which stands still while paused
	last := time.Now()
	shownPaused := false
	for {
		if c.hidden.Load() {
			select {
			case <-c.ctx.Done():
				return
			case <-c.wake:
			}
			last = time.Now()
			continue
		}
		paused := c.paused.Load()
		if paused && shownPaused {
			select {
			case <-c.ctx.Done():
				return
			case <-c.wake:
			}
			last, shownPaused = time.Now(), false
			continue
		}
		if c.switchTo.Swap(false) {
			want := "vulkan"
			if r.Name() == "vulkan" {
				want = "cpu"
			}
			r.Close()
			r, device, note = open(want)
			c.stats.setRenderer(r.Name(), device, note)
		}
		b, err := sf.Buffer(c.ctx)
		if err != nil {
			if c.ctx.Err() == nil {
				logf("render loop: no buffer: %v", err)
			}
			return
		}
		now := time.Now()
		if !paused {
			sceneT += now.Sub(last)
		}
		last = now
		bgra := b.Format == "bgra8"
		start := time.Now()
		if err := r.Draw(sceneT.Seconds(), b.Pix, b.W, b.H, b.Pitch, bgra); err != nil {
			r.Close()
			r, device, _ = open("cpu")
			c.stats.setRenderer(r.Name(), device, "fell back to the CPU: "+err.Error())
			logf("vulkan failed, on the CPU now: %v", err)
			if err := r.Draw(sceneT.Seconds(), b.Pix, b.W, b.H, b.Pitch, bgra); err != nil {
				return
			}
		}
		c.stats.rendered(time.Since(start))
		c.mu.Lock()
		c.format = b.Format
		lines, history, info := c.lines, c.history, c.info
		c.mu.Unlock()
		if c.overlay.Load() {
			drawOverlay(b, info.Scale, lines, history, paused, bgra)
		}
		seq, err := sf.Present(b, image.Rectangle{}, 0)
		if err != nil {
			logf("render loop: present: %v", err)
			return
		}
		c.stats.presented(seq, time.Now())
		shownPaused = paused
		if !c.unpaced.Load() {
			c.awaitAnswer(seq)
		}
	}
}

// awaitAnswer waits, paced, until AutoDoc answered frame seq (drawn, superseded or not shown), so
// the next frame is drawn for the next refresh rather than thrown away. Done is lossy, so it gives
// up after a few refreshes rather than stall.
func (c *card) awaitAnswer(seq uint64) {
	timeout := time.NewTimer(100 * time.Millisecond)
	defer timeout.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timeout.C:
			return
		case got := <-c.answered:
			if got >= seq {
				return
			}
		}
	}
}

// measure reads each frame's outcome and, once a second, turns the counts into the overlay's text.
func (c *card) measure(sf *plugin.Surface) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case d := <-sf.Done():
			c.stats.done(d)
			select {
			case c.answered <- d.Seq:
			default: // the render loop isn't waiting (unpaced): drop it
			}
		case <-tick.C:
			c.mu.Lock()
			info, format := c.info, c.format
			c.mu.Unlock()
			lines, history := c.stats.second(info, format, !c.unpaced.Load())
			c.mu.Lock()
			c.lines, c.history = lines, history
			c.mu.Unlock()
		}
	}
}

func main() {
	if err := plugin.Serve(context.Background(), newCard()); err != nil {
		fmt.Fprintln(os.Stderr, "autodoc-render-test:", err)
		os.Exit(1)
	}
}
