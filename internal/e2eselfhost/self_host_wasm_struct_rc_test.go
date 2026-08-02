package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcStructBoxWasm proves the Phase-1e container-layout
// foundation for structs (and enum variants, which share the struct
// layout): a struct block is now rc-boxed via the generic
// $__fern_str_box (8-byte rc+bsz header, returns base+8), so it carries
// an rc word at [s-8] while every s-relative access is unchanged — the
// type id stays at slot 0 (so `match` reads the right tag) and each field
// stays at struct_field_off. Observed through __fern_rc_is_unique: a fresh
// struct / variant value is unique (rc==1). Field values + array/string
// members (already construction-inc'd) survive. Counting + recursive
// field-release ride on this foundation in later slices.
//
// Extern-ABI structs (canonical-ABI result records) are intentionally left
// raw in this slice — layout-only never sweeps structs, so the mix is
// value-safe; they migrate when struct counting lands.
func TestSelfHostRcStructBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-box e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A fresh struct literal is rc-boxed at rc 1 => unique.
		{"struct-fresh-unique", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 7 }; return __fern_rc_is_unique(p); }", 1},
		// Field values survive the rc header (s-relative access unchanged).
		{"struct-values-intact", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 30, y: 12 }; return p.x + p.y; }", 42},
		// A struct holding an array field: value intact, detector clean.
		{"struct-holds-array", "struct B { xs: i32[], n: i32 } function main(): i32 { var ys: i32[] = [1, 2, 3]; var b = B { xs: ys, n: 9 }; return b.xs[2] + b.n + __fern_rc_underflow_count(); }", 12},
		// A struct holding a string field: value intact, detector clean.
		{"struct-holds-string", "struct N { name: string, n: i32 } function main(): i32 { var s: string = \"ab\" + \"cd\"; var v = N { name: s, n: 5 }; return v.name.len() + v.n + __fern_rc_underflow_count(); }", 9},
		// Struct update syntax (`S { ...base, f: v }`) keeps both copied and
		// overridden fields correct under the rc header.
		{"struct-update-intact", "struct P { x: i32, y: i32 } function main(): i32 { var a = P { x: 10, y: 20 }; var b = P { ...a, y: 32 }; return b.x + b.y; }", 42},
		// A unit enum variant (0-field struct) is boxed too and matches.
		{"unit-variant-match", "enum E { A, B } function main(): i32 { var e: E = B; match (e) { A => { return 1; }, B => { return 41; } } }", 41},
		// A positional variant constructor carries its payload at field 0 and
		// is unique when fresh.
		{"variant-payload-intact", "enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Square(w) => { return w + w; } } } function main(): i32 { var c: Shape = Circle(6); return area(c); }", 36},
		// A built struct returned survives in the caller (no struct sweep yet,
		// so this is a value-correctness + detector-clean check across the
		// rc-boxed return path).
		{"struct-return-intact", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 8, y: 34 }; } function main(): i32 { var p = mk(); return p.x + p.y + __fern_rc_underflow_count(); }", 42},
		// A struct built each loop iteration: detector stays clean (layout +
		// construction-incs only; free off, so this leaks soundly).
		{"struct-loop-clean", "struct P { x: i32, y: i32 } function main(): i32 { var s = 0; var k = 0; while (k < 1000) { var p = P { x: k, y: 2 }; s = s + p.y; k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 5},
		// COUNTING milestone (free off): an owned struct local is released
		// (rc dec) at exit, value-correct + detector clean.
		{"struct-swept-clean", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 7 }; return p.x + p.y + __fern_rc_underflow_count(); }", 12},
		// Aliasing a struct: the alias is inc'd, both swept, balanced.
		{"struct-alias-clean", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 3, y: 4 }; var u = p; return u.x + p.y + __fern_rc_underflow_count(); }", 7},
		// Move-on-return: a builder hands its struct to the caller (excluded
		// from the builder's sweep), the caller sweeps it — balanced, clean.
		{"struct-move-return-clean", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 8, y: 34 }; } function main(): i32 { var p = mk(); var q = mk(); return p.x + q.y + __fern_rc_underflow_count(); }", 42},
		// Regression: a function returning a BORROWED struct field must
		// return-retain it (struct counting on), or the caller's sweep of the
		// result over-releases the field the outer struct still owns (the
		// struct analogue of the node_head/watbin UAF). i is stored into o
		// WITHOUT a struct construction-inc (struct fields aren't inc'd yet),
		// so the retain is what keeps i's rc balanced across both decs.
		{"struct-borrowed-field-return", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function get_inner(o: Outer): Inner { return o.inner; } function main(): i32 { var i = Inner { v: 9 }; var o = Outer { inner: i, n: 5 }; var r = get_inner(o); return r.v + o.inner.v + __fern_rc_underflow_count(); }", 18},
		// A struct re-bound (`a = step(a)`) each iteration with a base-copy
		// (`{ ...a, n: … }`) churns: intermediates leak (free off), the final
		// owned struct is swept once, detector stays clean across many cycles.
		{"struct-base-copy-churn-clean", "struct Acc { xs: i32[], n: i32 } function step(a: Acc): Acc { return Acc { ...a, n: a.n + 1 }; } function main(): i32 { var a = Acc { xs: [1, 2, 3], n: 0 }; var k = 0; while (k < 1000) { a = step(a); k = k + 1; } return (a.n % 7) + a.xs[2] + __fern_rc_underflow_count(); }", 9},
		// FREE + recursive field-release: freeing a struct at exit releases its
		// rc-tracked array element (the source ys is dec'd to 0 by the struct's
		// recursive release) — value-correct + detector clean.
		{"struct-field-array-released", "struct B { xs: i32[], n: i32 } function main(): i32 { var ys: i32[] = [1, 2, 3]; var b = B { xs: ys, n: 9 }; return b.xs[2] + b.n + __fern_rc_underflow_count(); }", 12},
		// Same for a string field.
		{"struct-field-string-released", "struct N { name: string, n: i32 } function main(): i32 { var s: string = \"ab\" + \"cd\"; var v = N { name: s, n: 5 }; return v.name.len() + v.n + __fern_rc_underflow_count(); }", 9},
		// A nested struct field: freeing the outer struct releases the inner
		// struct box (construction-inc'd on store, recursively dec'd on free).
		{"struct-nested-released", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var i = Inner { v: 7 }; var o = Outer { inner: i, n: 5 }; return o.inner.v + o.n + __fern_rc_underflow_count(); }", 12},
		// Builder-escape (the UAF guard): mk() builds an inner, stores it in an
		// outer, and returns the outer (move-on-return). The inner survives
		// mk's exit sweep ONLY because of the struct-value construction-inc;
		// the caller's recursive release then frees it exactly once.
		{"struct-builder-escape-clean", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function mk(): Outer { var i = Inner { v: 9 }; return Outer { inner: i, n: 5 }; } function main(): i32 { var o = mk(); var p = mk(); return o.inner.v + p.n + __fern_rc_underflow_count(); }", 14},
		// A build-struct-with-array-field churn (bare-ident array field →
		// recursively released each time the struct is freed): detector clean
		// with free on across many cycles.
		{"struct-array-churn-clean", "struct B { xs: i32[], n: i32 } function mk(): i32 { var a: i32[] = [1, 2, 3, 4, 5, 6, 7, 8]; var b = B { xs: a, n: 5 }; return b.xs[7] + b.n; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 6},
		// Recursive-parser shape (the watbin UAF regression): a borrowed struct
		// FIELD read appended into an array (`children.append(r.node)`) must be
		// retained, or releasing the source struct `r` frees the node out from
		// under `children` (use-after-free when `sum` later walks the tree). AND
		// the loop-body local `r` is null on the leaf early-return path, so the
		// recursive release's `[s-8]` read must be null-guarded. Exercises both
		// fixes; value-correct (nested kind-sum = 14) + detector clean.
		{"struct-append-borrowed-field-clean", "struct Node { kind: i32, items: Node[] } struct POne { node: Node, pos: i32 } function parse(xs: i32[], i: i32): POne { if (xs[i] == 0) { return POne { node: Node { kind: xs[i], items: [] }, pos: i + 1 }; } var children: Node[] = []; var j: i32 = i + 1; while (j < xs.len() && xs[j] != 9) { var r: POne = parse(xs, j); children = children.append(r.node); j = r.pos; } return POne { node: Node { kind: 7, items: children }, pos: j + 1 }; } function sum(n: Node): i32 { var s: i32 = n.kind; var i: i32 = 0; while (i < n.items.len()) { s = s + sum(n.items[i]); i = i + 1; } return s; } function main(): i32 { var xs: i32[] = [1, 0, 0, 1, 0, 9, 9]; var r: POne = parse(xs, 0); return sum(r.node) + __fern_rc_underflow_count(); }", 14},
		// DEPTH: a struct holding a string[] field now DEEP-releases the array's
		// string elements (emit_struct_release uses arr_dec_ptr for a
		// pointer-element array field), not just the buffer — value-correct +
		// detector clean.
		{"struct-field-strarray-released", "struct Bag { items: string[], n: i32 } function main(): i32 { var xs: string[] = [\"a\" + \"b\", \"c\" + \"d\"]; var bag = Bag { items: xs, n: 5 }; return bag.items[0].len() + bag.items[1].len() + bag.n + __fern_rc_underflow_count(); }", 9},
		// SAFETY: a struct holding an i32[] field must stay FLAT — the scalar
		// elements (here values >= heap_base and even, which look like heap
		// pointers) must NOT be arr_dec'd, or arr_dec_ptr would corrupt / trip
		// the detector. array_field_elem_is_ptr returns false for i32[].
		{"struct-field-i32array-flat-safe", "struct C { ns: i32[], k: i32 } function main(): i32 { var ns: i32[] = [262184, 262192, 262200]; var c = C { ns: ns, k: 3 }; return c.ns.len() + c.k + __fern_rc_underflow_count(); }", 6},
		// A churn of structs each holding a fresh string[]: deep element release
		// reclaims the strings (no growth), detector clean across many cycles.
		{"struct-field-strarray-churn-clean", "struct Bag { items: string[], n: i32 } function mk(): i32 { var bag = Bag { items: [\"x\" + \"y\", \"z\" + \"w\"], n: 4 }; return bag.items[0].len() + bag.n; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 6},
		// RECLAIM: a struct local re-bound (`var p = …`) each loop iteration now
		// releases the prior value (recursive, cow-guarded) instead of leaking
		// it — 100k iterations stay reclaimed + detector clean.
		{"struct-rebind-loop-reclaim", "struct P { x: i32, y: i32 } function main(): i32 { var n = 0; var k = 0; while (k < 100000) { var p = P { x: k, y: 2 }; n = n + p.y; k = k + 1; } return (n % 7) + __fern_rc_underflow_count(); }", 3},
		// RECLAIM: the `cx = Ctx{...cx}`-style reassignment idiom — a struct
		// holding a string[] field, reassigned via base-copy each iteration —
		// reclaims each intermediate (old struct + its retained array fields)
		// across 100k cycles, detector clean.
		{"struct-reassign-base-copy-reclaim", "struct St { names: string[], n: i32 } function step(s: St): St { return St { ...s, n: s.n + 1 }; } function main(): i32 { var s = St { names: [\"a\" + \"b\"], n: 0 }; var k = 0; while (k < 100000) { s = step(s); k = k + 1; } return (s.n % 7) + s.names[0].len() + __fern_rc_underflow_count(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
