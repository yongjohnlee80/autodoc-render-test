package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// TestDoneMeasuresLatencyAndInterval: a drawn frame's latency is from its Present; the interval is
// between consecutive drawn frames; superseded frames count but measure nothing.
func TestDoneMeasuresLatencyAndInterval(t *testing.T) {
	s := newStats(os.Getpid())
	t0 := time.Unix(100, 0)
	s.presented(1, t0)
	s.presented(2, t0.Add(10*time.Millisecond))
	s.presented(3, t0.Add(16*time.Millisecond))
	s.done(plugin.SurfaceFrameDone{Seq: 1, Outcome: "drawn", WhenNS: t0.Add(5 * time.Millisecond).UnixNano()})
	s.done(plugin.SurfaceFrameDone{Seq: 2, Outcome: "superseded", WhenNS: t0.Add(12 * time.Millisecond).UnixNano()})
	s.done(plugin.SurfaceFrameDone{Seq: 3, Outcome: "drawn", WhenNS: t0.Add(25 * time.Millisecond).UnixNano()})
	if got := s.latency; len(got) != 2 || got[0] != 5 || got[1] != 9 {
		t.Fatalf("latency = %v, want [5 9]", got)
	}
	if got := s.interval; len(got) != 1 || got[0] != 20 {
		t.Fatalf("interval = %v, want [20]", got)
	}
	if s.outcomes["drawn"] != 2 || s.outcomes["superseded"] != 1 {
		t.Fatalf("outcomes = %v", s.outcomes)
	}
	if len(s.presentAt) != 0 {
		t.Fatalf("presentAt keeps %v after every outcome", s.presentAt)
	}
	lines, history := s.second(plugin.SurfaceInfo{W: 100, H: 50, Scale: 2}, "bgra8", true)
	if len(history) != 1 || !strings.Contains(strings.Join(lines, "\n"), "shown 2 fps   superseded 1") {
		t.Fatalf("second = %q, %v", lines, history)
	}
	if s.outcomes["drawn"] != 0 || s.latency != nil {
		t.Fatal("second did not reset the counts")
	}
}

// TestAPauseIsNotAFrame: a gap of a second or more between drawn frames is a pause, not a frame time.
func TestAPauseIsNotAFrame(t *testing.T) {
	s := newStats(os.Getpid())
	s.done(plugin.SurfaceFrameDone{Seq: 1, Outcome: "drawn", WhenNS: 1e9})
	s.done(plugin.SurfaceFrameDone{Seq: 2, Outcome: "drawn", WhenNS: 5e9})
	if len(s.interval) != 0 || len(s.history) != 0 {
		t.Fatalf("a 4 s gap measured as a frame: %v %v", s.interval, s.history)
	}
}

func TestPct(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3, 10, 9, 8, 7, 6}
	for _, c := range []struct {
		p    int
		want float64
	}{{50, 6}, {95, 10}, {100, 10}, {0, 1}} {
		if got := pct(xs, c.p); got != c.want {
			t.Errorf("pct(%d) = %v, want %v", c.p, got, c.want)
		}
	}
	if pct(nil, 50) != -1 || ms(-1) != "-" {
		t.Error("an empty sample must read as none")
	}
}

// TestProcStatusReadsThisProcess: the memory line reads /proc; this test's own RSS is positive.
func TestProcStatusReadsThisProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc here")
	}
	if procStatus(os.Getpid())["VmRSS"] == 0 {
		t.Fatal("VmRSS is 0")
	}
	if _, ok := procCPU(os.Getpid()); !ok {
		t.Fatal("procCPU could not read /proc/self/stat")
	}
}
