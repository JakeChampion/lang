package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The WIT extern/export bridge on the IR path (#3457, follows #5974).
//
// The bridge lifts record / variant / tuple leaves into Fern-side heap boxes,
// and the two wasm emitters read those boxes at DIFFERENT slot widths — the IR
// consumer at 8 + i*8, the legacy AST emitter at 4 + i*4. Emitting one layout
// for both is a silent wrong-answer bug that has landed twice (#5795, #5974), so
// the geometry is parameterised by consumer (wasm_ir.xbox_field_off) and these
// tests pin the parts the end-to-end component tests cannot see on their own:
//
//   - that an extern/export module ROUTES the IR path at all (a regression to
//     the AST emitter would keep the component tests green while quietly
//     reinstating the layout that is about to be deleted);
//   - the emitted wrapper's box offsets, so a mismatch is a readable diff rather
//     than a `mr-bad` from wasmtime;
//   - the two calling-convention shims the IR path needs and the AST path does
//     not: a void extern and a void export.
//
// TestSelfHostWasmVariantF32ArmMatchIR covers the irlower fix the bridge
// surfaced, on a program with no WIT in it at all.

// The routing assertion. The IR framing emits tid_globals_section
// unconditionally, and its literal-name list (tid_named) always includes
// NotFound — so the global is present in every IR core and, on the AST path,
// only in a module that does file I/O. astFuncMarker is the converse: every
// AST-emitted function body opens with the same scratch-local block, so its
// ABSENCE says no function came from the AST emitter.
const irRouteMarker = "(global $__tid$NotFound i32"
const astFuncMarker = "(local $__lit0 i32)"

func writeWasmSelfHostSources(t *testing.T, dir, driver string) string {
	t.Helper()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", driver} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return driver
}

// TestSelfHostWasmExternBridgeIRLayout checks the emitted core for each bridge
// shape: it routes IR, and the wrapper writes the IR consumer's 8-byte slots.
func TestSelfHostWasmExternBridgeIRLayout(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	driver := writeWasmSelfHostSources(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, driver, "wasm_runio_run")

	cases := []struct {
		name string
		src  string
		// substrings the emitted core must contain
		want []string
	}{
		{
			// A 3-field record result: the wrapper allocates the IR box
			// (8 + 3*8 = 32 bytes, rc-headered via $__fern_str_box, which is what
			// the IR consumer's $__fern_arr_dec expects at [p-8]) and stores the
			// canonical fields into slots 8 / 16 / 24.
			name: "record-result-slots",
			src: `struct Mix { a: i32, b: u32, c: i32 }
@import("local:test/src@0.1.0", "make-mix")
function make_mix(): Mix;
function main(): i32 { var p: Mix = make_mix(); return p.a + (p.b as i32) + p.c; }`,
			want: []string{
				"(local.set $s (call $__fern_str_box (i32.const 32)))",
				"(i32.const 8)) (i32.load (i32.add (local.get $rb) (i32.const 0))))",
				"(i32.const 16)) (i32.load (i32.add (local.get $rb) (i32.const 4))))",
				"(i32.const 24)) (i32.load (i32.add (local.get $rb) (i32.const 8))))",
			},
		},
		{
			// A variant result with a wide (f64) arm: payload slot at 8, box
			// 8 + 8 = 16 bytes.
			name: "variant-wide-payload-slot",
			src: `enum Ev { I(i32), D(f64) }
@import("local:test/src@0.1.0", "classify")
function classify(n: i32): Ev;
function main(): i32 { match (classify(1)) { I(a) => { return a; }, D(x) => { return 2; } } }`,
			want: []string{
				"(local.set $s (call $__fern_str_box (i32.const 16)))",
				"(i64.store offset=8 (local.get $s) (i64.load offset=8 (local.get $rb)))",
			},
		},
		{
			// A tuple param: the Fern tuple has no id word, so element i sits at
			// i*slot — 0 / 8 / 16 for the IR consumer, NOT the canonical i*4.
			name: "tuple-param-stride",
			src: `@import("local:test/src@0.1.0", "sum3")
function sum3(t: (i32, i32, i32)): i32;
function main(): i32 { return sum3((1, 2, 3)); }`,
			want: []string{
				"(i32.load (i32.add (local.get $ep0) (i32.const 0))) (i32.load (i32.add (local.get $ep0) (i32.const 8))) (i32.load (i32.add (local.get $ep0) (i32.const 16)))",
			},
		},
		{
			// A VOID extern: the IR path emits every Fern-callable with
			// `(result i32)` and drops a discarded call, so the bridge shims the
			// import to push the promised 0. Without it the core fails to
			// validate ("expected a type but nothing on stack").
			name: "void-extern-shim",
			src: `@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: i32);
function main(): i32 { block(3); return 0; }`,
			want: []string{
				"(func $block (param $ep0 i32) (result i32)",
				"(call $block__import (local.get $ep0))\n    (i32.const 0))",
			},
		},
		{
			// A VOID export: mirror shim on the export side — the wrapper drops
			// the i32 so the canonical lift sees the WIT signature's empty
			// result ("lowered result types [] do not match result types [I32]").
			name: "void-export-shim",
			src: `@export("local:test/handler@0.1.0", "handle")
function on_request(x: i32): void { return; }`,
			want: []string{
				"(drop (call $on_request (local.get 0)))",
				`(export "local:test/handler@0.1.0#handle" (func $__xwrap_on_request))`,
				// A reactor module has no `main`, but the framing's run entry
				// still calls one, so a stub is synthesised.
				"(func $main (result i32)\n    (i32.const 0))",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := string(runCapture(t, gcc, runner, driverBin, []byte(tc.src)))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			if !strings.Contains(wat, irRouteMarker) || strings.Contains(wat, astFuncMarker) {
				t.Fatalf("module did not route the IR path (want %q, want no %q) — the extern/export bridge is back on the AST emitter\n--- WAT ---\n%s", irRouteMarker, astFuncMarker, wat)
			}
			for _, w := range tc.want {
				if !strings.Contains(wat, w) {
					t.Errorf("emitted core missing:\n%s\n--- WAT ---\n%s", w, wat)
				}
			}
		})
	}
}

// TestSelfHostWasmVariantF32ArmMatchIR pins the irlower fix the f32-arm WIT
// variant surfaced, with no WIT involved: an f32 enum payload is CONSTRUCTED
// widened to an 8-byte f64 (op_struct_new's f64.store), so the match arm must
// read it back with f64.load; reading the low half with i32.load makes
// `x == 2.5` simply false. Runs under wasmtime, so the assertion is the
// answer rather than the instruction selection.
func TestSelfHostWasmVariantF32ArmMatchIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	driver := writeWasmSelfHostSources(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, driver, "wasm_run")

	// F(2.5) round-trips through the box: exit 0 only if the bound f32 compares
	// equal to 2.5 AND the i32 arm still reads its own 4-byte payload.
	const src = `enum Nv { I(i32), F(f32) }
function pick(n: i32): Nv { if (n < 10) { return I(7); } return F(2.5); }
function rank(n: i32): i32 {
    match (pick(n)) {
        I(a) => { if (a == 7) { return 100; } return 1; },
        F(x) => { if (x == 2.5) { return 200; } return 2; },
    }
}
function main(): i32 { return (rank(5) - 100) + (rank(50) - 200); }`

	wat := runCapture(t, gcc, runner, driverBin, []byte(src))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	if !strings.Contains(string(wat), irRouteMarker) || strings.Contains(string(wat), astFuncMarker) {
		t.Fatalf("program did not route the IR path (want %q, want no %q)\n--- WAT ---\n%s", irRouteMarker, astFuncMarker, wat)
	}
	watPath := filepath.Join(dir, "f32arm.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command(wasmtime, "run", watPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("wasm exited %d, want 0 — an f32 enum payload did not survive the box round-trip\n%s\n--- WAT ---\n%s", code, out, wat)
	}
}
