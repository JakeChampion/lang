package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureStrRetIRCases pin #5306 gap 2: a call THROUGH a fn-typed struct FIELD
// whose declared closure return type is `string` (`f: (i32) => string`) must
// str-track its result, so a chained `obj.f(args).len()` dispatches op_str_len
// (reads the string box's length field) instead of op_arr_len (which read the
// box's data-ptr slot as a length — a silent miscompile, `.len()` == 0).
//
// The captured string box itself round-trips correctly through the env box; the
// bug was purely the CALL RESULT's lost type. `fn_ret` is now recorded on the
// StructFieldDecl (parse_type_name preserves a `string` return; parse_struct_field
// keeps it) and consulted in expr_is_str.
//
// Each case is a full program routing-pinned to "ir" (asm_pathprobe_run) and
// oracle-checked against the interpreter; results stay <= 120 (wasm exit clamp).
var closureStrRetIRCases = []struct {
	name string
	src  string
	want int
}{
	// The exact #5306 gap-2 repro: a string captured through a tuple destructure,
	// returned by a closure stored in a struct field, chained `.len()`.
	// len("hi") + h.id(4) = 6.
	{"captured-str-destructure-field", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var t: (string, i32) = ("hi", 4);
    var (s, b) = t;
    var h: H = H { f: function (x: i32): string { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }
`, 6},
	// A struct with ONLY the closure field: the string comes through the field
	// call and `.len()` must read the string length. len("hi") = 2.
	{"captured-str-field-only", `struct H { f: (i32) => string }
function g(): i32 {
    var s: string = "hi";
    var h: H = H { f: function (x: i32): string { return s; } };
    return h.f(1).len();
}
function main(): i32 { return g(); }
`, 2},
	// A plain-var string capture (not a destructure) through the same field shape
	// — the same result-type gap bit regardless of how the string was bound.
	// len("hi") + h.id(4) = 6.
	{"captured-str-plainvar-field", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var b: i32 = 4;
    var h: H = H { f: function (x: i32): string { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }
`, 6},
	// i32-return sibling control: a fn field returning i32 needs no per-result
	// str-tracking and already lowered correctly. f(1)=b+x=5, +h.id(4) = 9.
	{"i32-ret-field-control", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
    var b: i32 = 4;
    var h: H = H { f: function (x: i32): i32 { return b + x; }, id: b };
    return h.f(1) + h.id;
}
function main(): i32 { return g(); }
`, 9},
	// Control: binding the field-call result to an explicitly-typed `string`
	// local types it directly (StmtVar), so this shape already worked. It stays
	// green — the fix must not regress the annotated path. len("hi") + 4 = 6.
	{"typed-var-result-control", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var b: i32 = 4;
    var h: H = H { f: function (x: i32): string { return s; }, id: b };
    var r: string = h.f(1);
    return r.len() + h.id;
}
function main(): i32 { return g(); }
`, 6},
}

// TestSelfHostClosureStrRetIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostClosureStrRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range closureStrRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostClosureStrRetIRWasm runs the same cases through the wasm IR
// backend (where the string box pointer rides wasm32's 4-byte slot).
func TestSelfHostClosureStrRetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-str-ret wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range closureStrRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "closure_str_ret_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("closure-str-ret wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
