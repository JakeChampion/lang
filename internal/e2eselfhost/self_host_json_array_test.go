package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jsonArrayIRCases exercise the element-polymorphic array serialiser
// `(xs: T[]) to_json[T: Json]()` (the std/json array `to_json`) through the
// self-hosted compiler's IR path. The self-host emits generic bodies by
// ERASURE — so the one emitted `(xs: T[]) to_json()` body bakes in the i32
// element dispatch and can't serialise a string/struct array. irlower
// special-cases the CALL SITE (`arr.to_json()`, where the element type IS
// known) into an inline loop whose per-element `arr[i].to_json()` dispatches to
// the right impl: i32 -> __fn_i32__to_json, string -> __fn_string__to_json, a
// derived struct -> its synthesised `<Struct>.to_json`. Issue #2766.
//
// Each case is a complete, IR-eligible program (inline `trait Json` + the
// primitive impls + the array method) — the same shape as the
// derive-default IR cases. Bundling the full std/json is deliberately avoided:
// std/json as a whole isn't IR-eligible, so it routes through the AST emitter
// where this fix does not apply (a legacy gap that, per project scope, does not
// need fixing). The program returns the rendered JSON's length as its exit
// code.
const jsonArrayPrelude = `trait Json { function to_json(self: Self): string; }
impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } }
impl Json for string { function to_json(self: Self): string { return "\"" + self + "\""; } }
pub function (xs: T[]) to_json[T: Json](): string {
    var out: string = "[";
    var i: i32 = 0;
    while (i < xs.len()) { if (i > 0) { out = out + ","; } out = out + xs[i].to_json(); i = i + 1; }
    return out + "]";
}
`

var jsonArrayIRCases = []struct {
	name string
	prog string // full program tail (top-level decls + main) appended to the prelude
	exit int
}{
	// [1,2,3] -> "[1,2,3]" (7 chars). Scalar elements.
	{"i32-array", `function main(): i32 { var a: i32[] = [1, 2, 3]; return a.to_json().len(); }`, 7},
	// ["x","y"] -> `["x","y"]` (9 chars). String elements (each quoted).
	{"string-array", `function main(): i32 { var a: string[] = ["x", "y"]; return a.to_json().len(); }`, 9},
	// [] -> "[]" (2 chars). The empty array short-circuits the loop.
	{"empty-array", `function main(): i32 { var a: i32[] = []; return a.to_json().len(); }`, 2},
	// `@derive(Json)` struct elements: each renders as a JSON object via its
	// synthesised to_json. [{"id":1,"tag":"x"},{"id":2,"tag":"y"}] (39 chars).
	{"struct-array",
		`@derive(Json) struct Item { id: i32, tag: string }
function main(): i32 { var items: Item[] = [Item { id: 1, tag: "x" }, Item { id: 2, tag: "y" }]; return items.to_json().len(); }`, 39},
}

func jsonArraySrc(prog string) string {
	return jsonArrayPrelude + "\n" + prog + "\n"
}

// TestSelfHostJsonArrayIRX86_64 compiles each case with the self-hosted x86-64
// driver (IR on), asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostJsonArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range jsonArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(jsonArraySrc(tc.prog))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostJsonArrayIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir), so the array serialiser is verified on the stack-machine
// backend too, not just the register ABI.
func TestSelfHostJsonArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host json-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range jsonArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(jsonArraySrc(tc.prog)))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "jsonarray_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("json-array wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
