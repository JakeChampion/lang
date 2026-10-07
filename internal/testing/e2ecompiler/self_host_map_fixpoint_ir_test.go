package e2ecompiler

import (
	"testing"
)

// Heap-bump FIXPOINTS for the self-host's map reclaim (#4357): a cow-threaded
// map rebuilt in a loop, and `m.get_or(k, d)` on its hit and miss paths, keep
// the heap flat.
//
// Fixpoint contract: growth at N=50 == growth at N=5000, non-zero, under a
// hard leak guard. The fixed-exit cases pin value-correctness churn and the
// alias negative (`let x = m.insert(..)` must leave m intact while staying
// value-correct). self_host_map_reclaim_ir_test.go keeps the value-only
// reclaim cases; these are the bump-scaling twins.
var mapFixpointIRCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	// A cow-threaded map DECLARED INSIDE a loop body: each iteration's box
	// is released before the next is stored, so the loop's high-water is
	// one box wide rather than growing a box per iteration.
	{name: "cow-loop-getor", src: func(n string) string {
		return `import "core/map";
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "straightline-cow-getor", src: func(n string) string {
		return `import "core/map";
function step2(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step2(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "per-call-getor", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m.get_or(k, 7);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "getor-miss-path", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    return m.get_or(k, 7);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 7) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// Bound-from-call: `let m: Map[..] = mk(i)`, where mk returns a fresh
	// map, is released like a local map_new — at the loop reinit and at
	// scope end — rather than leaking every mk() box.
	{name: "mkcall-loop-getor", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc != ` + n + ` * (` + n + ` - 1)) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "mkcall-straightline", src: func(n string) string {
		return `import "core/map";
function mk2(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function step(k: i32): i32 {
    let m: Map[i32, i32] = mk2(k);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// A DISCARDED builder call (`mk(i);` as a bare statement) has no binding,
	// so no slot reclaim fires and the whole fresh map leaks per call. The
	// "MAPRET:" seeding (from the MAPF registry, the map
	// sibling of TUPRET) now frees the result on the spot via
	// __fern_map_free.
	{name: "mkcall-discard", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "value-churn", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 300) {
        let m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        m = m.insert(i + 1, i * 3);
        acc = acc + m.get_or(i, 0) + m.get_or(i + 1, 0);
        i = i + 1;
    }
    if (acc != 5 * (300 * 299 / 2)) { return 121; }
    return 0;
}`
	}},
	// `x = m.insert(..)` leaves m as it was: a map is a value, so m still
	// misses key 1 (11 a round from x, 0 from m), as the interpreter answers.
	{name: "alias-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    let x: Map[i32, i32] = m.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { acc = acc + x.get_or(1, 0) + m.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 11) { return 121; }
    return 0;
}`
	}},
	// Param-shaped "builders" must not count as returning a fresh map: feed
	// returns its param, and grow returns a map built by a method call on its
	// param. If either result were reclaimed as the caller's own fresh map, it
	// would free base's live buffers → value corruption.
	{name: "mkcall-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    let t: Map[i32, i32] = m.insert(k, k * 2);
    return t;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let x: Map[i32, i32] = feed(base);
        let y: Map[i32, i32] = grow(base, i + 2);
        acc = acc + x.get_or(1, 0) + y.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
	// A discarded call to a NON-builder map-returning function must NOT be
	// freed: feed returns its param, so freeing the discarded result would
	// free base's live box + buffers out from under the loop.
	{name: "discard-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { feed(base); acc = acc + base.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 11) { return 121; }
    return 0;
}`
	}},
	// A NESTED `return p;`: pick's only top-level return is the bare fresh local,
	// but the k<0 arm hands out the param, so pick's result must not count as
	// fresh — otherwise the caller's loop-reinit drop would free base every
	// iteration.
	{name: "mkcall-nested-return-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function pick(p: Map[i32, i32], k: i32): Map[i32, i32] {
    if (k < 0) { return p; }
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let m: Map[i32, i32] = pick(base, 0 - 1);
        acc = acc + m.get_or(1, 0) + base.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
}

// TestSelfHostMapFixpointIRX86_64 runs the shapes through the self-hosted
// CLI, which loads core/map. Fixpoint cases assert growth(N=50) ==
// growth(N=5000), non-zero, under the leak guard; fixed cases assert their
// exact exit (121 = value mismatch, 119 = leak guard).
func TestSelfHostMapFixpointIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	sh := func(t *testing.T, prog string) int {
		t.Helper()
		_, exit := cli.exitOf(t, prog+"\n", "x86-64-linux")
		return exit
	}

	for _, tc := range mapFixpointIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				code := sh(t, tc.src(""))
				if code != tc.want {
					t.Errorf("%s: exited %d, want %d (121=value mismatch, 119=leak guard)", tc.name, code, tc.want)
				}
				if native := runInterpExit(t, tc.src("")); native != code {
					t.Errorf("%s: differential mismatch — native -interp exited %d, self-host exited %d", tc.name, native, code)
				}
				return
			}
			small := sh(t, tc.src("50"))
			large := sh(t, tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: growth is 0 — probe does not allocate", tc.name)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
