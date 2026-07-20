package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructDropWasm — the wasm Perceus slice 1c: the per-type
// $__struct_drop_<T> body now REALLY deep-drops a reclaimable struct's
// rc-array fields at scope exit (it was a leak-safe pass-through before).
// The wasm sibling of the register backends' emit_ir_struct_drop_one and of
// arm64's emit_arm64_struct_drop_one (slice 1a/1b): for each ARRAY field it
// releases the buffer via $__fern_arr_dec (scalar-element array — flat free)
// or $__fern_arr_dec_ptr (pointer-element array — also releases ITS element
// boxes), at the IR struct layout offset (8 + i*8 — an 8-byte header then
// 8-byte slots, matching wasm_ir's struct_make / struct_get / struct_set).
//
// These programs route the wasm IR path (they emit $__struct_drop_<T>, asserted
// below). The RC-introspection builtins (__fern_rc_underflow_count /
// __fern_arr_dec / __fern_rc_is_unique) all force a module onto the wasm AST
// path, so they CANNOT appear in an IR-routed reclaim test — reclaim is proven
// instead by a memory-pressure differential: a long alloc→drop churn is run
// under a tight `max-memory-size` cap with `trap-on-grow-failure`. With the real
// drop the field buffers (and, for the struct-array case, their element boxes)
// are reclaimed onto the freelist and reused, so memory stays bounded and the
// program completes (exit 0); a regression to the pass-through leaks one block
// per iteration, blows past the cap, and traps. The WAT-shape assertions pin the
// emitted body so a silent reroute to the AST path (where a different drop
// handles it) can't make the gate pass vacuously.
func TestSelfHostStructDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A long churn (500k alloc→drop cycles) under a 16 MiB cap. The bounded
	// reclaim footprint is ~1 MiB; the pass-through leak is ~90 B/iter ≈ 45 MiB,
	// well over the cap, so a regression traps on memory.grow.
	const cap = "16777216" // 16 MiB
	cases := []struct {
		name string
		src  string
		// wantBody is a substring the emitted $__struct_drop_<T> body must contain
		// (the real deep-drop call), proving IR routing + the real (non-pass-through)
		// shape. The pass-through body is just `(local.get $box))` with no call.
		wantBody string
	}{
		// SCALAR-array field (i32[]) — the k_scalar path: the buffer is freed flat
		// via $__fern_arr_dec at field offset 8 (IR layout). 500k cycles stay
		// bounded under the cap ⇒ the buffer is reclaimed each iteration.
		{
			"scalar-array-field-reclaim",
			"struct Bag { items: i32[] } " +
				"function mk(): i32 { var b: Bag = Bag { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }; return b.items[0] + b.items[15]; } " +
				"function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 17; }",
			"(func $__struct_drop_Bag (param $box i32) (result i32)\n    (drop (call $__fern_arr_dec (i32.load offset=8 (local.get $box))))",
		},
		// STRUCT-array field (Inner[]) — the k_box path: $__fern_arr_dec_ptr also
		// releases each element box. 400k cycles stay bounded ⇒ both the buffer and
		// its element boxes are reclaimed (a buffer-only free would still leak the
		// elements and blow the cap).
		{
			"struct-array-field-reclaim",
			"struct Inner { v: i32 } struct Nest { inners: Inner[] } " +
				"function mk(): i32 { var nz: Nest = Nest { inners: [Inner{v:1},Inner{v:2},Inner{v:3},Inner{v:4},Inner{v:5},Inner{v:6},Inner{v:7},Inner{v:8}] }; return nz.inners[0].v + nz.inners[7].v; } " +
				"function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 9; }",
			"(func $__struct_drop_Nest (param $box i32) (result i32)\n    (drop (call $__fern_arr_dec_ptr (i32.load offset=8 (local.get $box))))",
		},
		// DIRECT nested-struct field (Inner, not an array) — the k_struct path
		// (Perceus slice 3c): the inner box is freed SHALLOW via $__fern_arr_dec at
		// the field offset (8 + i*8). The inner is a fresh literal (sole-owned, no
		// construction-inc), so 500k cycles stay bounded under the cap ⇒ the inner
		// box is reclaimed each iteration; the slice-1c pass-through leaked it.
		{
			"nested-struct-field-reclaim",
			"struct Inner { v: i32, w: i32 } struct Outer { inner: Inner, tag: i32 } " +
				"function mk(): i32 { var o: Outer = Outer { inner: Inner { v: 5, w: 6 }, tag: 3 }; return o.inner.v + o.inner.w + o.tag; } " +
				"function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 14; }",
			"(func $__struct_drop_Outer (param $box i32) (result i32)\n    (drop (call $__fern_arr_dec (i32.load offset=8 (local.get $box))))",
		},
		// DEEP nested-struct field (Perceus slice 3 deep-drop): the inner is a LEAF
		// carrying its OWN rc-array field (`Inner { items: i32[] }`). When the inner
		// box is uniquely owned, $__struct_drop_Outer first calls $__struct_drop_Inner
		// (releasing inner.items) before freeing the inner box — instead of the shallow
		// box-only free that leaked inner.items. The recursive call + the wasm_ir
		// transitive closure (which emits $__struct_drop_Inner even though no lowered
		// op references it) are both asserted. 400k churn cycles stay bounded under the
		// cap ⇒ inner.items is reclaimed; the slice-3c shallow drop leaked it → trap.
		{
			"nested-struct-field-deep-drop",
			"struct Inner { items: i32[] } struct Outer { inner: Inner, tag: i32 } " +
				"function mk(): i32 { var o: Outer = Outer { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, tag: 7 }; return o.inner.items[0] + o.inner.items[15] + o.tag; } " +
				"function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 24; }",
			"(func $__struct_drop_Outer (param $box i32) (result i32)\n    (if (call $__fern_rc_is_unique (i32.load offset=8 (local.get $box))) (then\n      (drop (call $__struct_drop_Inner (i32.load offset=8 (local.get $box))))))\n    (drop (call $__fern_arr_dec (i32.load offset=8 (local.get $box))))",
		},
		// DEPTH-2 DEEP-DROP (#5336): `Outer { mid: Mid }`, `Mid { inner: Inner }`,
		// `Inner { items: i32[] }`. nested_field_deep_drop_ok admits arbitrary acyclic
		// depth, so $__struct_drop_Outer calls $__struct_drop_Mid, which must ITSELF
		// recursively call $__struct_drop_Inner (releasing the depth-2 inner.items
		// buffer). The wantBody asserts the transitive $__struct_drop_Mid body (proving
		// struct_drop_types walked the whole DAG, not just depth-1). 400k churn cycles
		// stay bounded under the cap ⇒ the depth-2 buffer is reclaimed; a depth-1-only
		// deep-drop leaks inner.items → over the cap → trap.
		{
			"nested-struct-field-deep-drop-depth2",
			"struct Inner { items: i32[] } struct Mid { inner: Inner, m: i32 } struct Outer { mid: Mid, tag: i32 } " +
				"function mk(): i32 { var o: Outer = Outer { mid: Mid { inner: Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, m: 2 }, tag: 7 }; return o.mid.inner.items[0] + o.mid.inner.items[15] + o.mid.m + o.tag; } " +
				"function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 26; }",
			"(func $__struct_drop_Mid (param $box i32) (result i32)\n    (if (call $__fern_rc_is_unique (i32.load offset=8 (local.get $box))) (then\n      (drop (call $__struct_drop_Inner (i32.load offset=8 (local.get $box))))))\n    (drop (call $__fern_arr_dec (i32.load offset=8 (local.get $box))))",
		},
		// STRING field (#4297 A2 — the k_str path): a reclaimable struct (it has the
		// `items` rc-array field, so it gets a $__struct_drop) whose `name: string`
		// field is now freed too. `name` is a FRESH concat (sole-owned rc=1, no
		// construction-inc), stored at field offset 8; the drop frees it via the
		// rc-aware $__fern_arr_dec. 400k churn cycles under the cap stay bounded ⇒
		// the fresh name box is reclaimed each iteration (a pass-through leaked a
		// fresh string/iter → over the cap → trap).
		{
			"string-field-reclaim",
			"struct R { name: string, items: i32[] } " +
				"function mk(pre: string): i32 { var r: R = R { name: pre + \"x\", items: [1,2,3,4] }; return r.name.len() + r.items[0]; } " +
				"function main(): i32 { var p: string = \"aa\"; var s: i32 = 0; var k: i32 = 0; while (k < 400000) { s = mk(p); k = k + 1; } return s - 4; }",
			"(func $__struct_drop_R (param $box i32) (result i32)\n    (drop (call $__fern_arr_dec (i32.load offset=8 (local.get $box))))",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			if !strings.Contains(string(wat), tc.wantBody) {
				t.Fatalf("%s: emitted $__struct_drop body missing real deep-drop\nwant substring:\n%s\n--- WAT ---\n%s", tc.name, tc.wantBody, wat)
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
				t.Errorf("%s: wasm exited %d, want 0 (a non-zero/trap means the field buffer leaked past the %s-byte cap — struct_drop did not reclaim)\n--- WAT ---\n%s", tc.name, code, cap, wat)
			}
		})
	}
}
