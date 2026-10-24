package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcDeepNestWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm deep-nest e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"deep-nested-2", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let i = Inner { xs: id([1, 2, 3]), n: 5 }; let o = Outer { inner: i, m: 9 }; return o.inner.xs[2] + o.inner.n + o.m + __rc_underflow_count(); }", 17},
		{"deep-nested-3", "struct A { v: i32[] } struct B { a: A, x: i32 } struct C { b: B, y: i32 } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let a = A { v: id([7, 8]) }; let b = B { a: a, x: 1 }; let c = C { b: b, y: 2 }; return c.b.a.v[1] + c.b.x + c.y + __rc_underflow_count(); }", 11},
		{"deep-nested-string", "struct In { s: string } struct Out { in: In, n: i32 } function ids(s: string): string { return s; } function main(): i32 { let i = In { s: ids(\"ab\") + \"cd\" }; let o = Out { in: i, n: 38 }; return o.in.s.len() + o.n + __rc_underflow_count(); }", 42},
		{"deep-nested-churn", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function id(xs: i32[]): i32[] { return xs; } function mk(): i32 { let i = Inner { xs: id([1, 2, 3, 4, 5, 6, 7, 8]), n: 5 }; let o = Outer { inner: i, m: 9 }; return o.inner.xs[7] + o.m; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"deep-builder-escape", "struct Inner { xs: i32[], n: i32 } struct Outer { inner: Inner, m: i32 } function id(xs: i32[]): i32[] { return xs; } function mk(): Outer { let i = Inner { xs: id([3, 4, 5]), n: 6 }; return Outer { inner: i, m: 7 }; } function main(): i32 { let o = mk(); let p = mk(); return o.inner.xs[1] + o.inner.n + o.m + __rc_underflow_count(); }", 17},
		{"arr-of-struct-released", "struct Inner { xs: i32[], n: i32 } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let ps: Inner[] = [Inner { xs: id([1, 2]), n: 3 }, Inner { xs: id([4, 5]), n: 6 }]; return ps[0].xs[1] + ps[1].xs[0] + ps[1].n + __rc_underflow_count(); }", 12},
		{"arr-of-struct-churn", "struct Inner { xs: i32[], n: i32 } function id(xs: i32[]): i32[] { return xs; } function mk(): i32 { let ps: Inner[] = [Inner { xs: id([1, 2, 3, 4]), n: 5 }, Inner { xs: id([6, 7, 8, 9]), n: 1 }]; return ps[0].xs[3] + ps[1].n; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		{"node-tree-deep-released", "struct Node { kind: i32, items: Node[] } struct POne { node: Node, pos: i32 } function parse(xs: i32[], i: i32): POne { if (xs[i] == 0) { return POne { node: Node { kind: xs[i], items: [] }, pos: i + 1 }; } let children: Node[] = []; let j: i32 = i + 1; while (j < xs.len() && xs[j] != 9) { let r: POne = parse(xs, j); children = children.append(r.node); j = r.pos; } return POne { node: Node { kind: 7, items: children }, pos: j + 1 }; } function sum(n: Node): i32 { let s: i32 = n.kind; let i: i32 = 0; while (i < n.items.len()) { s = s + sum(n.items[i]); i = i + 1; } return s; } function main(): i32 { let xs: i32[] = [1, 0, 0, 1, 0, 9, 9]; let r: POne = parse(xs, 0); return sum(r.node) + __rc_underflow_count(); }", 14},
		{"enum-string-payload-released", "enum Shape { Circle(string), Square(i32) } function ids(s: string): string { return s; } function main(): i32 { let s: Shape = Circle(ids(\"ab\") + \"cd\"); match (s) { Circle(name) => { return name.len() + 38 + __rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		{"enum-array-payload-released", "enum Box { Has(i32[]), Empty } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let b: Box = Has(id([10, 20, 30])); match (b) { Has(xs) => { return xs[1] + 22 + __rc_underflow_count(); }, Empty => { return 0; } } }", 42},
		{"enum-payload-churn", "enum Shape { Circle(string), Square(i32) } function ids(s: string): string { return s; } function mk(): i32 { let s: Shape = Circle(ids(\"x\") + \"y\"); match (s) { Circle(name) => { return name.len(); }, Square(w) => { return w; } } } function main(): i32 { let k = 0; let n = 0; while (k < 50000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 2},
		{"arr-of-enum-released", "enum Shape { Circle(string), Square(i32) } function ids(s: string): string { return s; } function main(): i32 { let shapes: Shape[] = [Circle(ids(\"ab\") + \"cd\"), Square(7)]; match (shapes[0]) { Circle(name) => { return name.len() + 38 + __rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		{"arr-of-enum-churn", "enum Shape { Circle(string), Square(i32) } function ids(s: string): string { return s; } function mk(): i32 { let shapes: Shape[] = [Circle(ids(\"x\") + \"y\"), Circle(ids(\"z\") + \"w\"), Square(3)]; match (shapes[1]) { Circle(name) => { return name.len(); }, Square(w) => { return w; } } } function main(): i32 { let k = 0; let n = 0; while (k < 50000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 2},
		{"tuple-enum-payload-released", "enum Shape { Circle(string), Square(i32) } function ids(s: string): string { return s; } function main(): i32 { let s: Shape = Circle(ids(\"ab\") + \"cd\"); let t = (s, 38); match (t.0) { Circle(name) => { return name.len() + t.1 + __rc_underflow_count(); }, Square(w) => { return w; } } }", 42},
		{"tuple-struct-element-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { let i = Inner { xs: [1, 2, 3, 4], n: 5 }; let t = (i, 9); return t.1; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
		{"arr-tuple-string-released", "function main(): i32 { let ps: (i32, string)[] = [(1, \"ab\" + \"cd\"), (2, \"ef\" + \"gh\")]; return ps[0].0 + ps[1].0 + ps[0].1.len() + 35 + __rc_underflow_count(); }", 42},
		{"arr-tuple-string-churn", "function mk(): i32 { let ps: (i32, string)[] = [(1, \"ab\" + \"cd\"), (2, \"ef\" + \"gh\")]; return ps[0].0 + ps[1].0 + ps[0].1.len() + ps[1].1.len(); } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4},
		{"arr-tuple-arrelem-churn", "function mk(): i32 { let ps: (i32, i32[])[] = [(1, [10, 20]), (2, [30, 40])]; return ps[0].0 + ps[1].1[0]; } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"arr-tuple-struct-elem-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { let ps: (Inner, i32)[] = [(Inner { xs: [1, 2, 3], n: 4 }, 7), (Inner { xs: [5, 6], n: 8 }, 9)]; return ps[0].1 + ps[1].1; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
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
