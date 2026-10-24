package e2e

import (
	"strings"
	"testing"
)

const fieldRmwSrc = `import "std/i64";

struct Box { xs: i64[], n: i64 }

// The read sits inside the update that rewrites the array.
fbip function inline_read(own b: Box): Box {
	return Box { ...b, xs: b.xs.with(0, b.xs[0] + (1 as i64)), n: b.n + (1 as i64) };
}

// The same computation with the read hoisted — free before #9605 too, and
// still here so a fix that only moved the cost is not mistaken for one.
fbip function hoisted_read(own b: Box): Box {
	let current: i64 = b.xs[0];
	return Box { ...b, xs: b.xs.with(0, current + (1 as i64)), n: b.n + (1 as i64) };
}

// A length read in value position is the other shape the excusal names.
fbip function len_read(own b: Box): Box {
	return Box { ...b, xs: b.xs.with(1, b.xs.len() as i64), n: b.n };
}

// Reading one slot while writing another must not confuse the two.
fbip function cross_slot(own b: Box): Box {
	return Box { ...b, xs: b.xs.with(2, b.xs[0] + (100 as i64)), n: b.n };
}

function fresh(): Box {
	let xs: i64[] = [];
	let i: i32 = 0;
	while (i < 256) { xs = xs.append(0 as i64); i = i + 1; }
	return Box { xs: xs, n: 0 as i64 };
}

function main(): i32 {
	// 1. The inline read allocates nothing over 1000 updates. Before the fix
	//    this was exactly 1000 — one whole-buffer copy per call.
	let b: Box = fresh();
	let at: i64 = __heap_alloc_count();
	let i: i32 = 0;
	while (i < 1000) { b = inline_read(b); i = i + 1; }
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	// The accumulated value proves every read saw the pre-store element.
	if (b.xs[0] != (1000 as i64)) { return 91; }

	// 2. The hoisted spelling stays free.
	let c: Box = fresh();
	let at2: i64 = __heap_alloc_count();
	let j: i32 = 0;
	while (j < 1000) { c = hoisted_read(c); j = j + 1; }
	if (__heap_alloc_count() - at2 != (0 as i64)) { return 92; }
	if (c.xs[0] != (1000 as i64)) { return 93; }

	// 3. A length read in value position.
	let d: Box = fresh();
	let at3: i64 = __heap_alloc_count();
	let k: i32 = 0;
	while (k < 1000) { d = len_read(d); k = k + 1; }
	if (__heap_alloc_count() - at3 != (0 as i64)) { return 94; }
	if (d.xs[1] != (256 as i64)) { return 95; }

	// 4. Read one slot, write another.
	let e: Box = fresh();
	let at4: i64 = __heap_alloc_count();
	let m: i32 = 0;
	while (m < 1000) { e = cross_slot(e); m = m + 1; }
	if (__heap_alloc_count() - at4 != (0 as i64)) { return 96; }
	if (e.xs[2] != (100 as i64)) { return 97; }
	if (e.xs[0] != (0 as i64)) { return 98; }

	// The observable is not simply stuck at zero: a fresh array moves it.
	let before: i64 = __heap_alloc_count();
	let extra: Box = fresh();
	if (extra.xs.len() != 256) { return 99; }
	if (__heap_alloc_count() - before <= (0 as i64)) { return 100; }
	return 42;
}
`

func TestFieldReadModifyWriteWritesInPlace(t *testing.T) {
	out, code := compileAndRunX86_64(t, fieldRmwSrc)
	if code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
	}
}
