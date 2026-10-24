package e2e

import (
	"strings"
	"testing"
)

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

// Read through a call, so the probe's State is a box rather than its fields.
@noinline function count_of(s: State): i32 { return s.count; }

// A State of constant fields is a static box, which nothing frees or reuses;
// one field from a call makes it a heap box.
@noinline function one(): i32 { return 1; }

function main(): i32 {
	// The observable is not stuck at zero: a fresh box moves it. This runs
	// first because a dead State later on would donate its box to the probe.
	let before: i64 = __heap_alloc_count();
	let fresh: State = State { count: one(), total: 1 as i64, tag: 1 };
	if (count_of(fresh) != 1) { return 94; }
	if (__heap_alloc_count() - before <= (0 as i64)) { return 95; }

	let s: State = State { count: one() - 1, total: 0 as i64, tag: 0 };
	let at: i64 = __heap_alloc_count();
	let i: i32 = 0;
	while (i < 10000) {
		s = step(s, i);
		i = i + 1;
	}
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	// The values prove the loop did the work rather than being elided.
	if (s.count != 10000) { return 91; }
	if (s.total != (20000 as i64)) { return 92; }
	if (s.tag != 9999) { return 93; }

	return 42;
}
`

func TestFipCarriesStructStateWithoutAllocating(t *testing.T) {
	out, code := compileAndRunX86_64(t, fipStructStateSrc)
	if code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
	}
}
