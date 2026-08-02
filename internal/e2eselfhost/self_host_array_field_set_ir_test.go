package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Assignment to an ARRAY-typed struct field (`s.code = [1, 2]`) on the IR path.
//
// irlower's __set_field arm used to REFUSE an array field and bail to the AST
// emitter, on the grounds that the store orphans the old buffer and needs a
// retain on the new one. Since #5972 deleted the register-backend AST emitters
// that bail is a hard error on x86-64 / arm64 ("module is not IR-eligible; the
// AST emitter is no longer reachable from this driver"), and on wasm it was the
// first mode-0 decliner — `a.code = x86_mov_r32_imm32(…)` in x86_encode.fern.
//
// What lowers now, and the ownership rules (see the arm's comment):
//
//   - the new value is RETAINED when it aliases an rc-tracked array local, so
//     the field's reference is counted and the exit dec-sweep cannot free a
//     buffer the struct still points at. The AST emitter stored bare, no inc —
//     a use-after-free for that shape, so this is strictly better;
//   - the REPLACED buffer is orphaned (the safe-leak floor). Reclaiming it needs
//     #4355's whole-program read-safety analysis generalised from string fields
//     to arrays.
//
// These programs have NO interpreter oracle: `s.f = v` is E048 in the native
// checker (immutable data), and the self-host compiler's own dialect permits it
// because its immutability gate is filtered to the cycle rules. So the expected
// exit codes are stated here rather than differentially derived.
var arrayFieldSetCases = []struct {
	name string
	src  string
	exit int
}{
	{
		// The reduced repro: a fresh array literal replaces an empty one.
		"set-fresh-literal",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [], n: 0 }; s.code = [1, 2]; return s.code.len(); }`,
		2,
	},
	{
		// Control: the SCALAR field of the same struct always lowered.
		"set-scalar-field",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [1], n: 0 }; s.n = 5; return s.n + s.code.len(); }`,
		6,
	},
	{
		// The retain case. `o` is an rc-tracked array local stored into the
		// field, so both own the buffer; reading through BOTH after the store
		// must see live data. Without the alias-inc the function-exit dec-sweep
		// on `o` frees the buffer the struct still points at.
		"set-aliased-local",
		`struct S { code: i32[], n: i32 }
function main(): i32 { var s: S = S { code: [], n: 0 }; var o: i32[] = [7, 8, 9]; s.code = o; return s.code[1] + o[2]; }`,
		17,
	},
	{
		// Repeated replacement, the x86_encode.fern shape: the field is
		// reassigned in a loop and read after. Each replaced buffer leaks (by
		// design, above), but the value read must be the LAST one stored.
		"set-in-loop",
		`struct S { code: i32[], n: i32 }
function main(): i32 {
    var s: S = S { code: [], n: 0 };
    var i: i32 = 0;
    while (i < 50) { s.code = [i, i + 1]; i = i + 1; }
    return s.code[0] + s.code[1];
}`,
		99,
	},
	{
		// A STRUCT-element array field (is_struct_array_field_type), the other
		// half of the refused set.
		"set-struct-array-field",
		`struct P { x: i32 }
struct T { items: P[], n: i32 }
function main(): i32 { var t: T = T { items: [], n: 0 }; t.items = [P { x: 4 }, P { x: 5 }]; return t.items[0].x + t.items[1].x; }`,
		9,
	},
}

// TestSelfHostArrayFieldSetIRWasm routes each case through the wasm IR path and
// runs it under wasmtime. The `-decide` probe assertion is the important half:
// it is what would catch a silent return of these programs to the AST emitter.
func TestSelfHostArrayFieldSetIRWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, rerr := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), src, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range arrayFieldSetCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — array-field assignment is back on the AST emitter", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s\n--- WAT ---\n%s", tc.name, code, tc.exit, out, wat)
			}
		})
	}
}

// TestSelfHostArrayFieldSetIRX86_64 is the register-backend half: the same
// programs through the x86-64 IR emitter, which REJECTED them outright before
// (the AST emitter it used to fall back to is gone), then linked and run.
func TestSelfHostArrayFieldSetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_ir_run.fern"} {
		src, rerr := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), src, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrayFieldSetCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host x86-64 IR emitter produced 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "arrfieldset_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
