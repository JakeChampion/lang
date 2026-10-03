package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructDropWasm checks that a reclaimable struct's rc fields are
// released at scope exit: scalar and struct arrays, nested structs at any
// acyclic depth, and strings.
//
// Reclaim is proven by a memory-pressure differential: a long alloc→drop churn
// is run under a tight `max-memory-size` cap with `trap-on-grow-failure`. With
// the real drop the field buffers (and, for the struct-array case, their element
// boxes) are reclaimed onto the freelist and reused, so memory stays bounded and
// the program completes (exit 0); a leak goes past the cap and traps. The WAT
// assertions pin the typed lowering's drop helper and the release it makes, so
// the cap is not passed vacuously.
func TestSelfHostStructDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A long churn (500k alloc→drop cycles) under a 16 MiB cap. The bounded
	// reclaim footprint is ~1 MiB; the pass-through leak is ~90 B/iter ≈ 45 MiB,
	// well over the cap, so a regression traps on memory.grow.
	const cap = "16777216" // 16 MiB
	cases := []struct {
		name string
		src  string
		// wantFn is the $__sem_drop_<T> whose body carries the reclaim, and
		// wantBody the release call that body must contain. Naming the function
		// rather than searching the whole module keeps the assertion on ONE body,
		// so another type's identically-shaped body cannot satisfy it.
		wantFn   string
		wantBody string
	}{
		// SCALAR-array field (i32[]): the buffer is freed flat. 500k cycles stay
		// bounded under the cap ⇒ the buffer is reclaimed each iteration.
		{
			"scalar-array-field-reclaim",
			"struct Bag { items: i32[] } " +
				"function mk(): i32 { let b: Bag = Bag { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }; return b.items[0] + b.items[15]; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 17; }",
			"$__sem_drop_Bag",
			"call $__fern_arr_dec",
		},
		// STRUCT-array field (Inner[]): the buffer and each element box are
		// released. 400k cycles stay bounded (a buffer-only free would still leak
		// the elements and exceed the cap).
		{
			"struct-array-field-reclaim",
			"struct Inner { v: i32 } struct Nest { inners: Inner[] } " +
				"function mk(): i32 { let nz: Nest = Nest { inners: [Inner{v:1},Inner{v:2},Inner{v:3},Inner{v:4},Inner{v:5},Inner{v:6},Inner{v:7},Inner{v:8}] }; return nz.inners[0].v + nz.inners[7].v; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 9; }",
			"$__sem_drop_Nest",
			"call $__fern_arr_dec",
		},
		// DIRECT nested-struct field (Inner, not an array): the inner box, a fresh
		// literal, is freed. 500k cycles stay bounded under the cap.
		{
			"nested-struct-field-reclaim",
			"struct Inner { v: i32, w: i32 } struct Outer { inner: Inner, tag: i32 } " +
				"function mk(): i32 { let o: Outer = Outer { inner: Inner { v: 5, w: 6 }, tag: 3 }; return o.inner.v + o.inner.w + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 14; }",
			"$__sem_drop_Outer",
			"call $__fern_arr_dec",
		},
		// DEEP nested-struct field: the inner carries its own rc-array field
		// (`Inner { items: i32[] }`), and $__sem_drop_Outer releases the inner through
		// $__sem_release_Inner, which frees inner.items too. 400k churn cycles stay
		// bounded under the cap; a shallow box-only free would leak inner.items.
		{
			"nested-struct-field-deep-drop",
			"struct Inner { items: i32[] } struct Outer { inner: Inner, tag: i32 } " +
				"function mk(): i32 { let o: Outer = Outer { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 }; return o.inner.items[0] + o.inner.items[15] + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 24; }",
			"$__sem_drop_Outer",
			"call $__sem_release_Inner",
		},
		// DEPTH-2 DEEP-DROP (#5336): `Outer { mid: Mid }`, `Mid { inner: Inner }`,
		// `Inner { items: i32[] }`. $__sem_drop_Mid must itself release the inner,
		// which frees the depth-2 inner.items buffer. 400k churn cycles stay bounded;
		// a depth-1-only drop leaks inner.items → over the cap → trap.
		{
			"nested-struct-field-deep-drop-depth2",
			"struct Inner { items: i32[] } struct Mid { inner: Inner, m: i32 } struct Outer { mid: Mid, tag: i32 } " +
				"function mk(): i32 { let o: Outer = Outer { mid: Mid { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, m: 2 }, tag: 7 }; return o.mid.inner.items[0] + o.mid.inner.items[15] + o.mid.m + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 26; }",
			"$__sem_drop_Mid",
			"call $__sem_release_Inner",
		},
		// STRING field (#4297 A2): `name` is a fresh concat, and the drop frees it
		// with the items buffer. 400k churn cycles under the cap stay bounded.
		{
			"string-field-reclaim",
			"struct R { name: string, items: i32[] } " +
				"function mk(pre: string): i32 { let r: R = R { name: pre + \"x\", items: [1,2,3,4] }; return r.name.len() + r.items[0]; } " +
				"function main(): i32 { let p: string = \"aa\"; let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(p); k = k + 1; } return s - 4; }",
			"$__sem_drop_R",
			"call $__fern_arr_dec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			bodies := wasmFuncBodies(string(wat), tc.wantFn+" ")
			if len(bodies) != 1 {
				t.Fatalf("%s: want exactly one %s body, found %d\n--- WAT ---\n%s", tc.name, tc.wantFn, len(bodies), wat)
			}
			if !strings.Contains(bodies[0], tc.wantBody) {
				t.Fatalf("%s: %s body missing real deep-drop\nwant substring:\n%s\n--- BODY ---\n%s", tc.name, tc.wantFn, tc.wantBody, bodies[0])
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run",
				"-W", "max-memory-size="+cap,
				"-W", "trap-on-grow-failure=y",
				"--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s: wasm exited %d, want 0 (a non-zero/trap means the field buffer leaked past the %s-byte cap — the drop did not reclaim)\n--- WAT ---\n%s", tc.name, code, cap, wat)
			}
		})
	}
}
