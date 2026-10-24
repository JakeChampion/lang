package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcStructBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-box e2e")
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
		{"struct-values-intact", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 30, y: 12 }; return p.x + p.y; }", 42},
		{"struct-holds-array", "struct B { xs: i32[], n: i32 } function main(): i32 { let ys: i32[] = [1, 2, 3]; let b = B { xs: ys, n: 9 }; return b.xs[2] + b.n + __rc_underflow_count(); }", 12},
		{"struct-holds-string", "struct N { name: string, n: i32 } function main(): i32 { let s: string = \"ab\" + \"cd\"; let v = N { name: s, n: 5 }; return v.name.len() + v.n + __rc_underflow_count(); }", 9},
		{"struct-update-intact", "struct P { x: i32, y: i32 } function main(): i32 { let a = P { x: 10, y: 20 }; let b = P { ...a, y: 32 }; return b.x + b.y; }", 42},
		{"unit-variant-match", "enum E { A, B } function main(): i32 { let e: E = B; match (e) { A => { return 1; }, B => { return 41; } } }", 41},
		{"variant-payload-intact", "enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Square(w) => { return w + w; } } } function main(): i32 { let c: Shape = Circle(6); return area(c); }", 36},
		{"struct-return-intact", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 8, y: 34 }; } function main(): i32 { let p = mk(); return p.x + p.y + __rc_underflow_count(); }", 42},
		{"struct-loop-clean", "struct P { x: i32, y: i32 } function main(): i32 { let s = 0; let k = 0; while (k < 1000) { let p = P { x: k, y: 2 }; s = s + p.y; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		{"struct-swept-clean", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 5, y: 7 }; return p.x + p.y + __rc_underflow_count(); }", 12},
		{"struct-alias-clean", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 3, y: 4 }; let u = p; return u.x + p.y + __rc_underflow_count(); }", 7},
		{"struct-move-return-clean", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 8, y: 34 }; } function main(): i32 { let p = mk(); let q = mk(); return p.x + q.y + __rc_underflow_count(); }", 42},
		{"struct-borrowed-field-return", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function get_inner(o: Outer): Inner { return o.inner; } function main(): i32 { let i = Inner { v: 9 }; let o = Outer { inner: i, n: 5 }; let r = get_inner(o); return r.v + o.inner.v + __rc_underflow_count(); }", 18},
		{"struct-base-copy-churn-clean", "struct Acc { xs: i32[], n: i32 } function step(a: Acc): Acc { return Acc { ...a, n: a.n + 1 }; } function main(): i32 { let a = Acc { xs: [1, 2, 3], n: 0 }; let k = 0; while (k < 1000) { a = step(a); k = k + 1; } return (a.n % 7) + a.xs[2] + __rc_underflow_count(); }", 9},
		{"struct-field-array-released", "struct B { xs: i32[], n: i32 } function main(): i32 { let ys: i32[] = [1, 2, 3]; let b = B { xs: ys, n: 9 }; return b.xs[2] + b.n + __rc_underflow_count(); }", 12},
		{"struct-field-string-released", "struct N { name: string, n: i32 } function main(): i32 { let s: string = \"ab\" + \"cd\"; let v = N { name: s, n: 5 }; return v.name.len() + v.n + __rc_underflow_count(); }", 9},
		{"struct-nested-released", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let i = Inner { v: 7 }; let o = Outer { inner: i, n: 5 }; return o.inner.v + o.n + __rc_underflow_count(); }", 12},
		{"struct-builder-escape-clean", "struct Inner { v: i32 } struct Outer { inner: Inner, n: i32 } function mk(): Outer { let i = Inner { v: 9 }; return Outer { inner: i, n: 5 }; } function main(): i32 { let o = mk(); let p = mk(); return o.inner.v + p.n + __rc_underflow_count(); }", 14},
		{"struct-array-churn-clean", "struct B { xs: i32[], n: i32 } function mk(): i32 { let a: i32[] = [1, 2, 3, 4, 5, 6, 7, 8]; let b = B { xs: a, n: 5 }; return b.xs[7] + b.n; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		{"struct-append-borrowed-field-clean", "struct Node { kind: i32, items: Node[] } struct POne { node: Node, pos: i32 } function parse(xs: i32[], i: i32): POne { if (xs[i] == 0) { return POne { node: Node { kind: xs[i], items: [] }, pos: i + 1 }; } let children: Node[] = []; let j: i32 = i + 1; while (j < xs.len() && xs[j] != 9) { let r: POne = parse(xs, j); children = children.append(r.node); j = r.pos; } return POne { node: Node { kind: 7, items: children }, pos: j + 1 }; } function sum(n: Node): i32 { let s: i32 = n.kind; let i: i32 = 0; while (i < n.items.len()) { s = s + sum(n.items[i]); i = i + 1; } return s; } function main(): i32 { let xs: i32[] = [1, 0, 0, 1, 0, 9, 9]; let r: POne = parse(xs, 0); return sum(r.node) + __rc_underflow_count(); }", 14},
		{"struct-field-strarray-released", "struct Bag { items: string[], n: i32 } function main(): i32 { let xs: string[] = [\"a\" + \"b\", \"c\" + \"d\"]; let bag = Bag { items: xs, n: 5 }; return bag.items[0].len() + bag.items[1].len() + bag.n + __rc_underflow_count(); }", 9},
		{"struct-field-i32array-flat-safe", "struct C { ns: i32[], k: i32 } function main(): i32 { let ns: i32[] = [262184, 262192, 262200]; let c = C { ns: ns, k: 3 }; return c.ns.len() + c.k + __rc_underflow_count(); }", 6},
		{"struct-field-strarray-churn-clean", "struct Bag { items: string[], n: i32 } function mk(): i32 { let bag = Bag { items: [\"x\" + \"y\", \"z\" + \"w\"], n: 4 }; return bag.items[0].len() + bag.n; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		{"struct-rebind-loop-reclaim", "struct P { x: i32, y: i32 } function main(): i32 { let n = 0; let k = 0; while (k < 100000) { let p = P { x: k, y: 2 }; n = n + p.y; k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 3},
		{"struct-reassign-base-copy-reclaim", "struct St { names: string[], n: i32 } function step(s: St): St { return St { ...s, n: s.n + 1 }; } function main(): i32 { let s = St { names: [\"a\" + \"b\"], n: 0 }; let k = 0; while (k < 100000) { s = step(s); k = k + 1; } return (s.n % 7) + s.names[0].len() + __rc_underflow_count(); }", 7},
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
