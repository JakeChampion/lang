package e2e

import (
	"testing"
)

// Reading a value OUT of a container and letting the read die is a reclaim
// shape, and two of them leaked once per read rather than once per program.
// Both are pinned the way docs/ALLOCATION-OBSERVABLE.md states the observable:
// run the loop at n and at 2n and compare the FRESH bytes each round consumed,
// never an absolute byte count.
//
//   - `m.get(k)` (#6561) stranded two blocks per lookup — the Map runtime's
//     uniform `Option[usize]` and the consumer-shaped box emitMapGetRebox
//     rebuilds from it. Measured 48 B/get on x86-64, arm64-linux and
//     arm64-darwin and 32 B/get on wasm; a MISS stranded 16 B on every
//     backend through the rebuilt box alone.
//   - `var line = words[0]` followed by `line = line + …` (#6567) stranded
//     every intermediate concat, 64 B/round on all four compiled backends.
//     Neither ingredient leaks alone: a `""` seed is flat, and so is a single
//     append onto a container-read seed, which is what let the shape survive
//     casual testing.
//
// The comparison happens INSIDE the program and only a verdict crosses the
// harness boundary. A byte count cannot: it comes back as an exit code clamped
// to 0..255, so two leaking measurements 256 apart compare equal and the gate
// passes on the commit the fix has not landed on.
//
// Each program also checks its own ANSWER and `__rc_underflow_count()`. The
// borrow-soundness half — that a value read out is one the CONTAINER still
// owns — is in `rcCorpus` (map_get_string_value_borrowed_repeatedly,
// accumulator_seeded_from_array_element), which runs the free-off legs too.
//
// arm64 has no seeded-accumulator bound: the seed taint is fixed there as
// well, but that backend still defers the accumulator's intermediates under
// #6554 — the same residual an accumulator seeded from `""` shows — so
// asserting flatness here would pin someone else's bug to this test.

// Verdict codes shared by both probes.
//
//	0    flat, and no over-release
//	1    the second run consumed more fresh bytes than the first
//	99x  the loop computed the wrong answer, so the shape it measured is not
//	     the shape under test
//	>1   __rc_underflow_count(), i.e. an over-release
const bumpVerdictFlat = 0

func mapGetVerdictSrc() string {
	return `import "core/map";
function lookups(index: Map[string, i32], n: i32): i32 {
    var i: i32 = 0;
    var hits: i32 = 0;
    while (i < n) {
        match (index.get("alpha")) {
            Some(g) => { hits = hits + g; },
            None => { hits = hits - 1; }
        }
        match (index.get("absent")) {
            Some(g) => { hits = hits + g; },
            None => { hits = hits + 2; }
        }
        i = i + 1;
    }
    return hits;
}
function main(): i32 {
    var index: Map[string, i32] = map_new(64);
    index = index.insert("alpha", 1);
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = lookups(index, 400);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = lookups(index, 800);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1200) { return 991; }
    if (y != 2400) { return 992; }
    // The map still owns its value after 3600 borrowed reads.
    match (index.get("alpha")) { Some(g) => { if (g != 1) { return 993; } }, None => { return 994; } }
    if ((b2 - b1) > (b1 - b0)) { return 1; }
    return __rc_underflow_count();
}`
}

func seededAccumulatorVerdictSrc() string {
	return `function build(words: string[]): string {
    var line: string = words[0];
    var g: i32 = 1;
    while (g < words.len()) { line = line + "  " + words[g]; g = g + 1; }
    return line;
}
function rounds(words: string[], n: i32): i32 {
    var i: i32 = 0;
    var t: i32 = 0;
    while (i < n) { t = t + build(words).len(); i = i + 1; }
    return t;
}
function main(): i32 {
    var words: string[] = ["alpha", "beta", "gamma", "delta"];
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(words, 400);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(words, 800);
    var b2: i64 = __heap_bump_bytes();
    if (x != 10000) { return 991; }
    if (y != 20000) { return 992; }
    if ((b2 - b1) > (b1 - b0)) { return 1; }
    return __rc_underflow_count();
}`
}

func TestX86_64MapGetBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestArm64MapGetBounded(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestWASMMapGetBounded(t *testing.T) {
	if code := runWasm(t, mapGetVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("map-get bump should be bounded (#6561): verdict=%d", code)
	}
}

func TestX86_64SeededAccumulatorBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, seededAccumulatorVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("seeded-accumulator bump should be bounded (#6567): verdict=%d", code)
	}
}

func TestWASMSeededAccumulatorBounded(t *testing.T) {
	if code := runWasm(t, seededAccumulatorVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("seeded-accumulator bump should be bounded (#6567): verdict=%d", code)
	}
}
