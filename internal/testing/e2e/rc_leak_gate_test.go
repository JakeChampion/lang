package e2e

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The rc corpus's LEAK leg (#7790).
//
// rc_correctness_test.go runs every corpus case for its exit code, which
// catches an over-release: `__rc_underflow_count()` is folded into the
// result, so a stray dec shows up as a non-zero exit. Its own header
// records what that cannot see:
//
//	Drop handlers that only LEAK (closures, maps, generic enums, deep
//	nesting) are still exercised here — leaks don't bump the underflow
//	counter, so they read 0 too.
//
// So the corpus already runs the shapes that leak and is structurally
// blind to the leaking. `FERN_LEAKCHECK=1` has reported the live-byte
// balance at exit since #5362 and nothing consumed it; this leg does.
//
// # Why a pinned baseline rather than a flat zero
//
// Each leaking case is pinned at its exact byte count and everything else
// must be zero. What that buys:
//
//   - every clean case is GATED. A change that starts leaking in any of
//     them fails here.
//   - a new corpus case that leaks fails, because absent from the table
//     means zero. Joining the leaking set is a deliberate act.
//   - a pinned case that leaks MORE fails.
//   - a pinned case that leaks LESS fails too, asking for the number to
//     be banked, so a fix cannot silently leave the table stale. Same
//     discipline as the complexity ratchet in internal/tools/lint.
//
// Deliberately NOT a total byte budget: one number over the whole corpus
// hides a new leak behind a fixed one, and the point of the leg is that
// leaks stop being invisible.
//
// The x86-64 and arm64 legs compile with the self-host compiler, and no case
// leaks on either, so their tables are empty: a case that starts leaking
// fails until it is fixed or pinned here on purpose.
var rcCorpusLeakBaselineX86_64 = map[string]int64{}

var rcCorpusLeakBaselineArm64 = map[string]int64{}

// The wasm table (#7912). Empty too: the closure drop paths and the json
// stdlib, which leaked on the native wasm backend, reclaim completely on the
// self-host's. The `m.without(k)` shapes were split out of one case so a fix
// to one could bank its own zero (#8276), and the call-argument projection
// joined them as a case rather than a pin.
var rcCorpusLeakBaselineWasm = map[string]int64{}

// checkCorpusLeaks runs every corpus case under the leak detector and
// holds each one at its baseline.
func checkCorpusLeaks(t *testing.T, backend string, baseline map[string]int64,
	run func(*testing.T, string) (string, string, int)) {
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if backend == "wasm" && c.skipWasm != "" {
				t.Skip(c.skipWasm)
			}
			_, stderr, code := run(t, c.src)
			// The natives own their stderr, so the report is the whole
			// of it and the anchored pattern says so. Under wasmtime it
			// is not: the host is free to write there too.
			re := leakCheckLineRe
			if backend == "wasm" {
				re = wasmLeakCheckLineRe
			}
			m := re.FindStringSubmatch(stderr)
			if m == nil {
				t.Fatalf("%s: no leakcheck report on stderr (exit %d): %q — "+
					"the detector must print exactly one line at the exit seam", c.name, code, stderr)
			}
			live, err := strconv.ParseInt(m[3], 10, 64)
			if err != nil {
				t.Fatalf("%s: unparseable live_bytes %q: %v", c.name, m[3], err)
			}
			want := baseline[c.name]
			switch {
			case live == want:
			case want == 0:
				t.Errorf("%s (%s): leaks %d bytes at exit, want 0 — this case reclaimed everything "+
					"before this change. Fix the drop path; do not add it to the baseline table",
					c.name, backend, live)
			case live > want:
				t.Errorf("%s (%s): leaks %d bytes at exit, up from the pinned %d — "+
					"this case already leaked and now leaks more", c.name, backend, live, want)
			default:
				t.Errorf("%s (%s): leaks %d bytes at exit, down from the pinned %d — "+
					"a leak got fixed. Bank it: set the entry to %d, or delete it if it is now 0",
					c.name, backend, live, want, live)
			}
		})
	}
}

func TestX86_64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "x86-64", rcCorpusLeakBaselineX86_64, runLeakCheckX86_64)
}

func TestArm64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "arm64", rcCorpusLeakBaselineArm64, runLeakCheckArm64)
}

// The wasm leg (#7912). Until the census reached this backend the corpus
// ran here for its exit code only, so every leaking case on the list
// above was invisible on the one target with no detector at all.
func TestWASMRcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "wasm", rcCorpusLeakBaselineWasm,
		func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		})
}

// TestRcCorpusLeakBaselinesNameRealCases keeps the two tables from
// rotting: an entry naming a case the corpus no longer has would sit
// there forever, unreachable and unfalsifiable.
func TestRcCorpusLeakBaselinesNameRealCases(t *testing.T) {
	have := map[string]bool{}
	for _, c := range rcCorpus {
		have[c.name] = true
	}
	for _, tbl := range []struct {
		name string
		m    map[string]int64
	}{
		{"rcCorpusLeakBaselineX86_64", rcCorpusLeakBaselineX86_64},
		{"rcCorpusLeakBaselineArm64", rcCorpusLeakBaselineArm64},
		{"rcCorpusLeakBaselineWasm", rcCorpusLeakBaselineWasm},
	} {
		var stale []string
		for name := range tbl.m {
			if !have[name] {
				stale = append(stale, name)
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			t.Errorf("%s names %d case(s) no longer in rcCorpus: %s — delete the entries",
				tbl.name, len(stale), strings.Join(stale, ", "))
		}
	}
}
