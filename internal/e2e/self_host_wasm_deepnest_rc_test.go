package e2e

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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
