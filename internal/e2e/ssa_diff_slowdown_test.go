package e2e

import (
	"fmt"
	"testing"
	"time"
)

// The backend differentials' ratio gate (#8069).
//
// A differential that only compares the ANSWER cannot see a performance
// divergence at all. What caught one — `-backend ssa` 66x slower than the flat
// arm64 backend on examples/bench/pmap_insert.fern — was the fixed run timeout,
// and it reported the program as HUNG and DISAGREEING when it neither hung nor
// disagreed: it finished, in 49.8s, with the same exit code. On a native runner
// the same work stayed under the wall, so CI never saw it at all.
//
// A wall sized for native execution and applied unchanged to emulated execution
// cannot do this job. A ratio can: it means the same thing on both, because a
// faster host shrinks both sides of it.
const (
	// ssaDiffMaxSlowdown is how much slower the SSA build may be than the flat
	// one before it is a finding. The gap this is sized against was 66x and
	// growing with input size; the same program is 1.9x since fa48944.
	//
	// 8x is chosen against the measured spread, not by taste: dropping the
	// threshold to 1.5x over the whole corpus (qemu-aarch64, 4-core container)
	// names four programs and the worst of them is 3.7x —
	//
	//	map_int          3.0x, 3.7x on a second run   (191 ms vs 64 ms)
	//	map_probe_chain  2.4x                         (1.67 s vs 711 ms)
	//	wordladder       2.2x                         (2.66 s vs 1.19 s)
	//	pvec_with        1.9x                         (718 ms vs 381 ms)
	//
	// so 8x sits above everything the backend costs today and far below the
	// class of gap it is here to catch. LOWER it as those close; raising it to
	// quiet a program is the move this gate exists to prevent.
	ssaDiffMaxSlowdown = 8.0

	// ssaDiffMinAbsGap stops the ratio firing on process startup. A 5 ms
	// program that takes 40 ms is 8x and means nothing. Requiring an absolute
	// gap as well is also what keeps the check host-independent: a faster host
	// shrinks both elapsed times, so a fixed millisecond floor only ever makes
	// the gate quieter, never louder.
	ssaDiffMinAbsGap = 250 * time.Millisecond
)

// ssaSlowdown returns the finding for one flat/ssa elapsed pair, or "" when
// there is none. Both conditions have to hold: the SSA build must be
// disproportionately slower AND materially slower.
func ssaSlowdown(flat, ssa time.Duration) string {
	if flat <= 0 || ssa-flat < ssaDiffMinAbsGap {
		return ""
	}
	ratio := float64(ssa) / float64(flat)
	if ratio < ssaDiffMaxSlowdown {
		return ""
	}
	return fmt.Sprintf("`-backend ssa` is %.1fx slower than the flat backend here — %s against %s.\n"+
		"Both produce the same answer, so this is a performance divergence rather than a\n"+
		"miscompile, and it is checked as a RATIO so it reads the same on an emulated cross\n"+
		"host as on a native runner. The gate that this replaces reported a 66x gap as a hang\n"+
		"and a disagreement (#8069); it was neither.\n"+
		"Either fix the divergence or, if the program is legitimately harder for the SSA\n"+
		"backend, say so with a measurement rather than widening %.0fx to cover it.",
		ratio, ssa.Round(time.Millisecond), flat.Round(time.Millisecond), ssaDiffMaxSlowdown)
}

// TestSSASlowdownGate pins the two conditions. A ratio alone fires on process
// startup; an absolute gap alone fires on any program that simply takes a
// while. It takes both, and neither on its own is enough.
func TestSSASlowdownGate(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name        string
		flat, ssa   time.Duration
		wantFinding bool
	}{
		{"proportionate and material", 100 * ms, 900 * ms, true},
		{"the #8069 gap", 750 * ms, 49800 * ms, true},
		{"disproportionate but immaterial", 5 * ms, 45 * ms, false},
		{"material but proportionate", 1000 * ms, 2000 * ms, false},
		{"exactly at the ratio, material", 100 * ms, 800 * ms, true},
		{"just under the ratio", 100 * ms, 799 * ms, false},
		{"just under the absolute gap", 40 * ms, 289 * ms, false},
		{"ssa faster", 900 * ms, 100 * ms, false},
		{"flat unmeasured", 0, 5000 * ms, false},
	}
	for _, c := range cases {
		got := ssaSlowdown(c.flat, c.ssa) != ""
		if got != c.wantFinding {
			t.Errorf("%s: ssaSlowdown(%s, %s) reported=%v, want %v",
				c.name, c.flat, c.ssa, got, c.wantFinding)
		}
	}
}
