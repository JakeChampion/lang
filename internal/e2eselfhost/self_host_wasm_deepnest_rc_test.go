package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcDeepNestWasm proves Stage A of transitive reclamation (the
// self-host port of native's __drop_struct_<N>): a swept struct/enum local now
// routes through a generated per-type drop function ($__fern_release_<T>), and
// a nested concrete-struct field recurses through ITS release fn instead of a
// flat one-level dec — so an arbitrarily-deep struct tree's inner arrays /
// strings / nested boxes reclaim on the owning value's last reference. Each
// generated fn rc==1-gates internally, so a shared child at rc>1 just
// decrements (no over-release). (Arrays-of-structs deep-release + enum variant
// payload dispatch are later stages; here struct-array fields still free one
// level and enum boxes free flat.)
func TestSelfHostRcDeepNestWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm deep-nest e2e")
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
		// 2-level: releasing Outer transitively frees Inner's array (the inner
		// box was already freed one-level before; now its array is too).
		{"deep-nested-2", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function main(): i32 { var i = Inner { xs: [1, 2, 3], n: 5 }; var o = Outer { inner: i, m: 9 }; return o.inner.xs[2] + o.inner.n + o.m + __fern_rc_underflow_count(); }", 17},
		// 3-level transitive: C -> B -> A -> A.v array.
		{"deep-nested-3", "struct A { v: i32[] } struct B { a: A, x: i32 } struct C { b: B, y: i32 } function main(): i32 { var a = A { v: [7, 8] }; var b = B { a: a, x: 1 }; var c = C { b: b, y: 2 }; return c.b.a.v[1] + c.b.x + c.y + __fern_rc_underflow_count(); }", 11},
		// A nested STRING field deep in the tree is released too.
		{"deep-nested-string", "struct In { s: string } struct Out { in: In, n: i32 } function main(): i32 { var i = In { s: \"ab\" + \"cd\" }; var o = Out { in: i, n: 38 }; return o.in.s.len() + o.n + __fern_rc_underflow_count(); }", 42},
		// A deep-nested-struct churn: built + freed 50k times. The inner arrays
		// reclaim transitively each cycle (no OOM), detector clean — proves real
		// transitive free (50k leaked inner arrays would exhaust memory).
		{"deep-nested-churn", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function mk(): i32 { var i = Inner { xs: [1, 2, 3, 4, 5, 6, 7, 8], n: 5 }; var o = Outer { inner: i, m: 9 }; return o.inner.xs[7] + o.m; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 3},
		// Builder-escape across the deep tree: mk builds the nested value and
		// returns the outer (move); the caller's transitive release frees the
		// whole tree exactly once.
		{"deep-builder-escape", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function mk(): Outer { var i = Inner { xs: [3, 4, 5], n: 6 }; return Outer { inner: i, m: 7 }; } function main(): i32 { var o = mk(); var p = mk(); return o.inner.xs[1] + o.inner.n + o.m + __fern_rc_underflow_count(); }", 17},
		// Stage B: an array-of-structs LOCAL deep-releases each element's fields
		// (here each Inner's xs array) via $__fern_arr_release_<Inner>, not just
		// the element boxes — value-correct + detector clean.
		{"arr-of-struct-released", "struct Inner { xs: i32[], n: i32 } function main(): i32 { var ps: Inner[] = [Inner { xs: [1, 2], n: 3 }, Inner { xs: [4, 5], n: 6 }]; return ps[0].xs[1] + ps[1].xs[0] + ps[1].n + __fern_rc_underflow_count(); }", 12},
		// Stage B churn: 50k arrays-of-structs-holding-arrays built + freed. The
		// inner arrays reclaim transitively each cycle (no OOM) — proves the
		// deep array-element free, not a flat one-level dec.
		{"arr-of-struct-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var ps: Inner[] = [Inner { xs: [1, 2, 3, 4], n: 5 }, Inner { xs: [6, 7, 8, 9], n: 1 }]; return ps[0].xs[3] + ps[1].n; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 5},
		// The recursive Node tree (a Node[] field, deep): $__fern_release_Node ↔
		// $__fern_arr_release_Node mutual recursion reclaims the WHOLE tree to
		// arbitrary depth (the watbin-parser shape). Value-correct + detector 0.
		{"node-tree-deep-released", "struct Node { kind: i32, items: Node[] } struct POne { node: Node, pos: i32 } function parse(xs: i32[], i: i32): POne { if (xs[i] == 0) { return POne { node: Node { kind: xs[i], items: [] }, pos: i + 1 }; } var children: Node[] = []; var j: i32 = i + 1; while (j < xs.len() && xs[j] != 9) { var r: POne = parse(xs, j); children = children.append(r.node); j = r.pos; } return POne { node: Node { kind: 7, items: children }, pos: j + 1 }; } function sum(n: Node): i32 { var s: i32 = n.kind; var i: i32 = 0; while (i < n.items.len()) { s = s + sum(n.items[i]); i = i + 1; } return s; } function main(): i32 { var xs: i32[] = [1, 0, 0, 1, 0, 9, 9]; var r: POne = parse(xs, 0); return sum(r.node) + __fern_rc_underflow_count(); }", 14},
		// Stage C: a user enum's variant PAYLOAD is released on free, via the
		// generated $__fern_release_<Enum> struct_id dispatch to the matching
		// variant struct's release fn (native genEnumDrops). Here Circle's heap
		// string payload is freed.
		{"enum-string-payload-released", "enum Shape { Circle(string), Square(i32) } function main(): i32 { var s: Shape = Circle(\"ab\" + \"cd\"); match (s) { Circle(name) => { return name.len() + 38 + __fern_rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		// An enum array-payload variant releases its array.
		{"enum-array-payload-released", "enum Box { Has(i32[]), Empty } function main(): i32 { var b: Box = Has([10, 20, 30]); match (b) { Has(xs) => { return xs[1] + 22 + __fern_rc_underflow_count(); }, Empty => { return 0; } } }", 42},
		// Churn: 50k enum-with-string-payload built + freed; the payload reclaims
		// each cycle (no OOM), detector clean — proves the variant-dispatch free.
		{"enum-payload-churn", "enum Shape { Circle(string), Square(i32) } function mk(): i32 { var s: Shape = Circle(\"x\" + \"y\"); match (s) { Circle(name) => { return name.len(); }, Square(w) => { return w; } } } function main(): i32 { var k = 0; var n = 0; while (k < 50000) { n = mk(); k = k + 1; } return (n % 7) + __fern_rc_underflow_count(); }", 2},
		// Stage D: an array-of-enum deep-releases each element's variant payload
		// (via the generated $__fern_arr_release_<Enum>, which calls the
		// dispatching $__fern_release_<Enum> per element). Drives the AST's
		// Stmt[] / Expr[]. Here each Circle's string payload is freed.
		{"arr-of-enum-released", "enum Shape { Circle(string), Square(i32) } function main(): i32 { var shapes: Shape[] = [Circle(\"ab\" + \"cd\"), Square(7)]; match (shapes[0]) { Circle(name) => { return name.len() + 38 + __fern_rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		// Churn: 50k arrays-of-enums-with-string-payloads built + freed; every
		// element payload reclaims each cycle (no OOM), detector clean.
		{"arr-of-enum-churn", "enum Shape { Circle(string), Square(i32) } function mk(): i32 { var shapes: Shape[] = [Circle(\"x\" + \"y\"), Circle(\"z\" + \"w\"), Square(3)]; match (shapes[1]) { Circle(name) => { return name.len(); }, Square(w) => { return w; } } } function main(): i32 { var k = 0; var n = 0; while (k < 50000) { n = mk(); k = k + 1; } return (n % 7) + __fern_rc_underflow_count(); }", 2},
		// Stage E: an ENUM tuple element (kind 'i' but carrying an svtype)
		// deep-releases its variant payload through $__fern_release_<Enum>. Here
		// the Circle string payload held in the tuple is freed; value via match.
		{"tuple-enum-payload-released", "enum Shape { Circle(string), Square(i32) } function main(): i32 { var s: Shape = Circle(\"ab\" + \"cd\"); var t = (s, 38); match (t.0) { Circle(name) => { return name.len() + t.1 + __fern_rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		// Stage E soundness: 50k struct-in-tuple churn (the tuple deep-releases
		// the Inner struct + its array via $__fern_release_Inner). The array
		// reclaims each cycle (no OOM), detector clean. (Value uses t.1 only —
		// tuple-of-struct FIELD access `t.0.field` is a pre-existing,
		// RC-orthogonal value bug.)
		{"tuple-struct-element-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var i = Inner { xs: [1, 2, 3, 4], n: 5 }; var t = (i, 9); return t.1; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 2},
		// Stage F: an ARRAY-OF-TUPLE local deep-releases each element tuple's
		// pointer-bearing fields (native __drop_arr_tuple_) before freeing the
		// element box + the buffer — not the flat one-level dec that leaked the
		// inner strings/arrays. Each element tuple is rc==1-gated, so a shared
		// element only dec's. Here each (i32, string) element's string is freed.
		{"arr-tuple-string-released", "function main(): i32 { var ps: (i32, string)[] = [(1, \"ab\" + \"cd\"), (2, \"ef\" + \"gh\")]; return ps[0].0 + ps[1].0 + ps[0].1.len() + 35 + __fern_rc_underflow_count(); }", 42},
		// Stage F churn: 200k arrays-of-(i32,string)-tuples built + freed; every
		// element tuple's string reclaims each cycle (no OOM), detector clean —
		// 400k leaked strings + tuple boxes would exhaust memory.
		{"arr-tuple-string-churn", "function mk(): i32 { var ps: (i32, string)[] = [(1, \"ab\" + \"cd\"), (2, \"ef\" + \"gh\")]; return ps[0].0 + ps[1].0 + ps[0].1.len() + ps[1].1.len(); } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 4},
		// Stage F: a tuple element that is itself an ARRAY (i32[]) is freed per
		// element on the array's death — 200k cycles reclaim, no OOM, detector 0.
		{"arr-tuple-arrelem-churn", "function mk(): i32 { var ps: (i32, i32[])[] = [(1, [10, 20]), (2, [30, 40])]; return ps[0].0 + ps[1].1[0]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 3},
		// Stage F: a STRUCT tuple element (svtype, kind 'i') deep-releases through
		// $__fern_release_Inner — the struct's own array field reclaims too. 50k
		// cycles, no OOM, detector clean. (Value via the i32 element; tuple-of-
		// struct field access is a pre-existing RC-orthogonal bug, avoided here.)
		{"arr-tuple-struct-elem-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var ps: (Inner, i32)[] = [(Inner { xs: [1, 2, 3], n: 4 }, 7), (Inner { xs: [5, 6], n: 8 }, 9)]; return ps[0].1 + ps[1].1; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 2},
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
