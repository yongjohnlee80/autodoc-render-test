package main

import (
	"fmt"
	"os"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// budget is one refresh of a 60 Hz display: a frame slower than it shows late.
const budget = 1000.0 / 60

// historyLen is how many displayed-frame intervals the graph keeps.
const historyLen = 180

// stats gathers a second of measurements at a time. The render loop and the outcome reader write
// it; measure turns it into the overlay's text once a second.
type stats struct {
	hostPID int

	mu         sync.Mutex
	renderer   string
	device     string
	note       string
	renders    []time.Duration  // Draw's time, this second
	presentAt  map[uint64]int64 // a presented frame's seq, to when (Unix ns), until its outcome
	outcomes   map[string]int   // this second's
	latency    []float64        // present to drawn, ms, this second
	interval   []float64        // between drawn frames, ms, this second
	lastDrawn  int64
	history    []float64 // the latest historyLen intervals
	lastCPU    uint64    // the host's utime+stime, in clock ticks, at the last second
	lastSelf   uint64    // the plugin's own, likewise
	lastSecond time.Time
}

func newStats(hostPID int) *stats {
	return &stats{hostPID: hostPID, presentAt: map[uint64]int64{}, outcomes: map[string]int{}}
}

func (s *stats) setRenderer(name, device, note string) {
	s.mu.Lock()
	s.renderer, s.device, s.note = name, device, note
	s.mu.Unlock()
}

func (s *stats) rendered(d time.Duration) {
	s.mu.Lock()
	s.renders = append(s.renders, d)
	s.mu.Unlock()
}

func (s *stats) presented(seq uint64, at time.Time) {
	s.mu.Lock()
	s.presentAt[seq] = at.UnixNano()
	if len(s.presentAt) > 1024 { // outcomes the bounded Done channel dropped
		for k := range s.presentAt {
			if k+512 < seq {
				delete(s.presentAt, k)
			}
		}
	}
	s.mu.Unlock()
}

// done takes a frame's outcome: drawn frames give the latency from Present and the interval
// since the frame drawn before.
func (s *stats) done(d plugin.SurfaceFrameDone) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[d.Outcome]++
	at, ok := s.presentAt[d.Seq]
	delete(s.presentAt, d.Seq)
	if d.Outcome != "drawn" || d.WhenNS == 0 {
		return
	}
	if ok && d.WhenNS > at {
		s.latency = append(s.latency, float64(d.WhenNS-at)/1e6)
	}
	if s.lastDrawn != 0 && d.WhenNS > s.lastDrawn {
		ms := float64(d.WhenNS-s.lastDrawn) / 1e6
		if ms < 1000 { // a pause, not a frame
			s.interval = append(s.interval, ms)
			s.history = append(s.history, ms)
			if len(s.history) > historyLen {
				s.history = s.history[len(s.history)-historyLen:]
			}
		}
	}
	s.lastDrawn = d.WhenNS
}

// second closes the second: the overlay's lines and the interval history, and the counts reset.
func (s *stats) second(info plugin.SurfaceInfo, format string, paced bool) ([]string, []float64) {
	s.mu.Lock()
	renders, latency, interval := s.renders, s.latency, s.interval
	outcomes := s.outcomes
	renderer, device, note := s.renderer, s.device, s.note
	history := slices.Clone(s.history)
	s.renders, s.latency, s.interval, s.outcomes = nil, nil, nil, map[string]int{}
	now := time.Now()
	elapsed := now.Sub(s.lastSecond).Seconds()
	s.lastSecond = now
	s.mu.Unlock()

	w, h := info.Pixels()
	name := renderer
	if device != "" {
		name += " (" + device + ")"
	}
	pacing := "unpaced"
	if paced {
		pacing = "paced"
	}
	lines := []string{
		fmt.Sprintf("renderer %s   %s   surface %dx%d px @%.2f %s", name, pacing, w, h, info.Scale, format),
		fmt.Sprintf("shown %d fps   superseded %d   not shown %d   rendered %d/s",
			outcomes["drawn"], outcomes["superseded"], outcomes["not_shown"], len(renders)),
		fmt.Sprintf("frame time (shown) p50 %s  p95 %s  max %s   budget %.1f ms",
			ms(pct(interval, 50)), ms(pct(interval, 95)), ms(pct(interval, 100)), budget),
		fmt.Sprintf("render p50 %s  p95 %s   present->shown p50 %s  p95 %s",
			ms(pct(durMS(renders), 50)), ms(pct(durMS(renders), 95)), ms(pct(latency, 50)), ms(pct(latency, 95))),
		s.hostLine(elapsed),
		s.selfLine(elapsed),
	}
	if note != "" {
		lines = append(lines, "note: "+note)
	}
	lines = append(lines, "keys: r renderer   u paced/unpaced   space pause   o overlay")
	return lines, history
}

// hostLine is AutoDoc's memory and CPU: the plugin's parent process.
func (s *stats) hostLine(elapsed float64) string {
	comm := strings.TrimSpace(readFile(fmt.Sprintf("/proc/%d/comm", s.hostPID)))
	st := procStatus(s.hostPID)
	cpu := ""
	if ticks, ok := procCPU(s.hostPID); ok {
		s.mu.Lock()
		if s.lastCPU != 0 && elapsed > 0 {
			cpu = fmt.Sprintf("   CPU %.0f%%", float64(ticks-s.lastCPU)/clockTicks/elapsed*100)
		}
		s.lastCPU = ticks
		s.mu.Unlock()
	}
	return fmt.Sprintf("host %s (pid %d): RSS %s  swap %s%s", comm, s.hostPID, kb(st["VmRSS"]), kb(st["VmSwap"]), cpu)
}

var heapSamples = []metrics.Sample{
	{Name: "/memory/classes/heap/objects:bytes"},
	{Name: "/gc/cycles/total:gc-cycles"},
}

// selfLine is the plugin's own CPU and memory: where the two renderers differ most, since the CPU
// one draws on every core and the Vulkan one mostly waits on the GPU.
func (s *stats) selfLine(elapsed float64) string {
	metrics.Read(heapSamples)
	heap := heapSamples[0].Value.Uint64()
	gcs := heapSamples[1].Value.Uint64()
	st := procStatus(os.Getpid())
	cpu := ""
	if ticks, ok := procCPU(os.Getpid()); ok {
		s.mu.Lock()
		if s.lastSelf != 0 && elapsed > 0 {
			cpu = fmt.Sprintf("CPU %.0f%%   ", float64(ticks-s.lastSelf)/clockTicks/elapsed*100)
		}
		s.lastSelf = ticks
		s.mu.Unlock()
	}
	return fmt.Sprintf("plugin: %sRSS %s   Go heap %.1f MB   GC %d", cpu, kb(st["VmRSS"]), float64(heap)/(1<<20), gcs)
}

// clockTicks is USER_HZ, 100 on Linux.
const clockTicks = 100

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// procStatus is /proc/pid/status's kB fields.
func procStatus(pid int) map[string]uint64 {
	out := map[string]uint64{}
	for _, l := range strings.Split(readFile(fmt.Sprintf("/proc/%d/status", pid)), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 2 && f[1] == "kB" {
			n, _ := strconv.ParseUint(f[0], 10, 64)
			out[k] = n
		}
	}
	return out
}

// procCPU is the process's utime+stime, in clock ticks.
func procCPU(pid int) (uint64, bool) {
	s := readFile(fmt.Sprintf("/proc/%d/stat", pid))
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0, false
	}
	u, err1 := strconv.ParseUint(f[11], 10, 64)
	st, err2 := strconv.ParseUint(f[12], 10, 64)
	return u + st, err1 == nil && err2 == nil
}

// pct is the p-th percentile of xs (100 is the maximum); NaN-free, -1 for none.
func pct(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return -1
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	i := min(len(s)-1, len(s)*p/100)
	return s[i]
}

func durMS(ds []time.Duration) []float64 {
	out := make([]float64, len(ds))
	for i, d := range ds {
		out[i] = float64(d) / 1e6
	}
	return out
}

func ms(v float64) string {
	if v < 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f ms", v)
}

func kb(n uint64) string {
	switch {
	case n == 0:
		return "0"
	case n >= 1<<20:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<10))
}
