package e2e

import (
	"strings"
	"testing"
)

// Read-modify-write through a struct field must write in place (#9605).
//
// `o.xs.with(i, o.xs[i] + 1)` is what an update IS — "add delta to the value
// at this key" — and it used to cost a copy of the WHOLE buffer, once per
// call, because the element read was treated as a later reader of the field
// and the CoW was forced down its shared path. Hoisting the read one line
// into a local made it free, which is folklore, not a language rule.
//
// Nothing else could see it. The copies are recycled, so the fresh-byte mark
// stays flat and the leak census balances allocs against frees; only
// `__heap_alloc_count()` (#9596) shows the allocator running at all. That is
// why this gate counts allocations rather than measuring time or bytes.
//
// The verdict rides the EXIT CODE so the same program serves every backend
// with no output parsing, and the value assertions are what keep the fix
// honest: the element read has to yield the value from BEFORE the store. If
// the write were ever ordered first, `xs[0] + 1` would read back what it just
// wrote and the counts would still be zero.
const fieldRmwSrc = `import "std/i64";

struct Box { xs: i64[], n: i64 }

// The read sits inside the update that rewrites the array.
fbip function inline_read(own b: Box): Box {
	return Box { ...b, xs: b.xs.with(0, b.xs[0] + (1 as i64)), n: b.n + (1 as i64) };
}

// The same computation with the read hoisted — free before #9605 too, and
// still here so a fix that only moved the cost is not mistaken for one.
fbip function hoisted_read(own b: Box): Box {
	var current: i64 = b.xs[0];
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
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < 256) { xs = xs.append(0 as i64); i = i + 1; }
	return Box { xs: xs, n: 0 as i64 };
}

function main(): i32 {
	// 1. The inline read allocates nothing over 1000 updates. Before the fix
	//    this was exactly 1000 — one whole-buffer copy per call.
	var b: Box = fresh();
	var at: i64 = __heap_alloc_count();
	var i: i32 = 0;
	while (i < 1000) { b = inline_read(b); i = i + 1; }
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	// The accumulated value proves every read saw the pre-store element.
	if (b.xs[0] != (1000 as i64)) { return 91; }

	// 2. The hoisted spelling stays free.
	var c: Box = fresh();
	var at2: i64 = __heap_alloc_count();
	var j: i32 = 0;
	while (j < 1000) { c = hoisted_read(c); j = j + 1; }
	if (__heap_alloc_count() - at2 != (0 as i64)) { return 92; }
	if (c.xs[0] != (1000 as i64)) { return 93; }

	// 3. A length read in value position.
	var d: Box = fresh();
	var at3: i64 = __heap_alloc_count();
	var k: i32 = 0;
	while (k < 1000) { d = len_read(d); k = k + 1; }
	if (__heap_alloc_count() - at3 != (0 as i64)) { return 94; }
	if (d.xs[1] != (256 as i64)) { return 95; }

	// 4. Read one slot, write another.
	var e: Box = fresh();
	var at4: i64 = __heap_alloc_count();
	var m: i32 = 0;
	while (m < 1000) { e = cross_slot(e); m = m + 1; }
	if (__heap_alloc_count() - at4 != (0 as i64)) { return 96; }
	if (e.xs[2] != (100 as i64)) { return 97; }
	if (e.xs[0] != (0 as i64)) { return 98; }

	// The observable is not simply stuck at zero: a fresh array moves it.
	var before: i64 = __heap_alloc_count();
	var extra: Box = fresh();
	if (extra.xs.len() != 256) { return 99; }
	if (__heap_alloc_count() - before <= (0 as i64)) { return 100; }
	return 42;
}
`

func TestFieldReadModifyWriteWritesInPlace(t *testing.T) {
	// 90/92/94/96 name which shape still copies; 91/93/95/97/98 mean the
	// element read stopped seeing the pre-store value, which would be a
	// miscompile rather than a missed optimisation; 100 means the counter
	// never moved and the zeros above meant nothing.
	out, code := compileAndRunX86Native(t, fieldRmwSrc)
	if code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
	}
}
