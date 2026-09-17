package e2e

import (
	"strings"
	"testing"
)

// `fip iteration(own state) -> state` over a STRUCT (#9602).
//
// This shape was not expressible at all. A field write is E048, whose message
// names the rebuild `T { ...old, f: v }` as the remedy, and that rebuild was
// E053 inside a `fip` body — each diagnostic forbidding what the other
// advised. What was left for a strict `fip` data plane was one array threaded
// through `.with`, so real state had to be packed into a single array behind
// hand-written index constants.
//
// The checker now admits the constructor SHAPE in every tier because it cannot
// tell a rebuild that reuses a dead donor's box from one that allocates a
// fresh box; the IR decides, and a bare `fip` is the allowance-0 case. The
// checker half is internal/checker/fip_test.go and the E068 half is
// internal/ir/fip_verify_test.go — this is the part neither can state: that
// the accepted program really does run without touching the allocator.
//
// Zero is the whole claim, so the count is what this measures rather than time
// or bytes: the rebuild's box is recycled, which leaves `__heap_bump_bytes()`
// flat whether the reuse fired or not.
const fipStructStateSrc = `import "std/i32";
import "std/i64";

struct State { count: i32, total: i64, tag: i32 }

// The architecture: owned state in, transformed, handed back.
fip function bump(own s: State): State {
	return State { ...s, count: s.count + 1, total: s.total + (2 as i64) };
}

// Threading through two fip steps keeps the box across a call boundary.
fip function retag(own s: State, t: i32): State {
	return State { ...s, tag: t };
}

fip function step(own s: State, t: i32): State {
	s = bump(s);
	return retag(s, t);
}

function main(): i32 {
	var s: State = State { count: 0, total: 0 as i64, tag: 0 };
	var at: i64 = __heap_alloc_count();
	var i: i32 = 0;
	while (i < 10000) {
		s = step(s, i);
		i = i + 1;
	}
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	// The values prove the loop did the work rather than being elided.
	if (s.count != 10000) { return 91; }
	if (s.total != (20000 as i64)) { return 92; }
	if (s.tag != 9999) { return 93; }

	// The observable is not stuck at zero: a fresh box moves it.
	var before: i64 = __heap_alloc_count();
	var fresh: State = State { count: 1, total: 1 as i64, tag: 1 };
	if (fresh.count != 1) { return 94; }
	if (__heap_alloc_count() - before <= (0 as i64)) { return 95; }
	return 42;
}
`

func TestFipCarriesStructStateWithoutAllocating(t *testing.T) {
	// 90 means the rebuild stopped reusing its donor and the annotation is
	// no longer worth making; 91-93 mean the loop computed the wrong thing;
	// 95 means the counter never moves and 90 passed for the wrong reason.
	out, code := compileAndRunX86Native(t, fipStructStateSrc)
	if code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
	}
}
