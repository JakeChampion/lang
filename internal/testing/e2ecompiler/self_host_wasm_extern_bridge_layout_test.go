package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeWasmSelfHostSources(t *testing.T, dir, driver string) string {
	t.Helper()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", driver)
	return driver
}

func TestSelfHostWasmExternBridgeIRLayout(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	driver := writeWasmSelfHostSources(t, dir, "drivers/wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, "", dir, driver, "wasm_runio_run")

	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "record-result-slots",
			src: `struct Mix { a: i32, b: u32, c: i32 }
@import("local:test/src@0.1.0", "make-mix")
function make_mix(): Mix;
function main(): i32 { let p: Mix = make_mix(); return p.a + (p.b as i32) + p.c; }`,
			want: []string{
				"(local.set $s (call $__fern_str_box (i32.const 32)))",
				"(i32.const 8)) (i32.load (i32.add (local.get $rb) (i32.const 0))))",
				"(i32.const 16)) (i32.load (i32.add (local.get $rb) (i32.const 4))))",
				"(i32.const 24)) (i32.load (i32.add (local.get $rb) (i32.const 8))))",
			},
		},
		{
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
			name: "tuple-param-stride",
			src: `@import("local:test/src@0.1.0", "sum3")
function sum3(t: (i32, i32, i32)): i32;
function main(): i32 { return sum3((1, 2, 3)); }`,
			want: []string{
				"(i32.load (i32.add (local.get $ep0) (i32.const 0))) (i32.load (i32.add (local.get $ep0) (i32.const 8))) (i32.load (i32.add (local.get $ep0) (i32.const 16)))",
			},
		},
		{
			name: "tuple-result-retptr",
			src: `@import("local:test/src@0.1.0", "make-pair")
function make_pair(a: i32, b: i32): (i32, i32);
function main(): i32 { let p: (i32, i32) = make_pair(1, 2); return p.0 + p.1; }`,
			want: []string{
				`(import "local:test/src@0.1.0" "make-pair" (func $make_pair__import (param i32) (param i32) (param i32)))`,
				"(call $make_pair__import (local.get $ep0) (local.get $ep1) (local.get $rb))",
				"(local.set $s (call $__fern_str_box (i32.const 16)))",
				"(i32.const 0)) (i32.load (i32.add (local.get $rb) (i32.const 0))))",
				"(i32.const 8)) (i32.load (i32.add (local.get $rb) (i32.const 4))))",
			},
		},
		{
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
			name: "void-export-shim",
			src: `@export("local:test/handler@0.1.0", "handle")
function on_request(x: i32): void { return; }`,
			want: []string{
				"(drop (call $on_request (local.get 0)))",
				`(export "local:test/handler@0.1.0#handle" (func $__xwrap_on_request))`,
				"(func $main (result i32)\n    (i32.const 0))",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := string(runCapture(t, "", runner, driverBin, []byte(tc.src)))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			for _, w := range tc.want {
				if !strings.Contains(wat, w) {
					t.Errorf("emitted core missing:\n%s\n--- WAT ---\n%s", w, wat)
				}
			}
		})
	}
}

func TestSelfHostWasmVariantF32ArmMatchIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	runner := x86_64Runner(t)
	dir := t.TempDir()
	driver := writeWasmSelfHostSources(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, "", dir, driver, "wasm_run")

	const src = `enum Nv { I(i32), F(f32) }
function pick(n: i32): Nv { if (n < 10) { return I(7); } return F(2.5); }
function rank(n: i32): i32 {
    match (pick(n)) {
        I(a) => { if (a == 7) { return 100; } return 1; },
        F(x) => { if (x == 2.5) { return 200; } return 2; },
    }
}
function main(): i32 { return (rank(5) - 100) + (rank(50) - 200); }`

	wat := runCapture(t, "", runner, driverBin, []byte(src))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
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
