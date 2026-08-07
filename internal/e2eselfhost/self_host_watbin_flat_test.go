package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWatbinFlat validates that watbin (the self-host WAT->binary
// encoder) assembles the stack-IR emitter's FLAT WAT — the prerequisite for
// flipping wasm's default to the IR path, since wasm.emit_module's output feeds
// not only wasmtime (which accepts flat WAT) but also watbin, which otherwise
// handles only FOLDED S-expressions. The enc_flat_body path encodes a linear
// instruction-atom sequence directly (the natural shape
// of the wasm binary format), dispatched per-function by body shape — so a
// program mixing flat user functions with the emitter's folded heap/RC helpers
// (array programs) assembles correctly too.
//
// Pipeline per case: wasm_ir_run -ir emits flat WAT -> the watbin driver
// (wat_to_binary) assembles it to a .wasm -> wasmtime runs it -> assert the
// exit code matches the program's expected result.
func TestSelfHostWatbinFlat(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping watbin flat e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
		"watbin.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// The flat WAT emitter.
	irDriver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "ir_driver")

	// The watbin assembler: read WAT from stdin, print the module bytes as
	// newline-separated decimals.
	asmDriver := `
import "std/io";
import "./util";
import "./watbin";
function main(): i32 {
    var wat: string = io.read_all_stdin();
    var bs: i32[] = watbin.wat_to_binary(wat);
    var i: i32 = 0;
    while (i < bs.len()) { write(util.i32_to_string(bs[i])); write("\n"); i = i + 1; }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "watbin_run.fern"), []byte(asmDriver), 0o644); err != nil {
		t.Fatalf("write watbin_run.fern: %v", err)
	}
	asmBin := buildSelfHostBin(t, gcc, dir, "watbin_run.fern", "watbin_run")

	// pipe runs a host binary with stdin, returns stdout.
	pipe := func(bin string, stdin []byte, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
		}
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run %s: %v", filepath.Base(bin), err)
		}
		return out
	}

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"if-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }", 9},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		// Array program: flat user functions + the emitter's folded heap/RC
		// helpers in the SAME module — exercises the per-function dispatch.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-set", "function main(): i32 { var a = [0, 0, 0]; a[1] = 99; return a[0] + a[1] + a[2]; }", 99},
		// Scientific-notation f64 literal (#4342): the literal's SOURCE TEXT
		// rides the IR (op_const_f64_text) into `f64.const 1e3` in the WAT, so
		// watbin's parse_f64 must honour the exponent — the pre-fix parser
		// stopped at the 'e' and assembled 1.0.
		{"sci-float-exp", "function main(): i32 { var x = 1e3; var y = 1.5e-2; if (x == 1000.0 && y > 0.0149 && y < 0.0151) { return 42; } return 1; }", 42},
		// f64 -> i32 SATURATING truncation (`as i32`): the stack-IR backend emits
		// `i32.trunc_sat_f64_s` (the two-byte 0xFC 0x02 form), which watbin's
		// opcode table lacked — it fell through both enc paths emitting NOTHING,
		// so an f64 stayed on the stack where an i32 was expected and the module
		// failed validation (`type mismatch: expected i32, found f64`). These pin
		// the 0xFC saturating-conversion family (#4801).
		{"f64-trunc-mul", "function main(): i32 { var x: f64 = 3.5; return (x * 2.0) as i32; }", 7},
		{"f64-trunc-sub", "function main(): i32 { var a: f64 = 10.5; var b: f64 = 3.5; return (a - b) as i32; }", 7},
		{"f64-trunc-sqrt", "function main(): i32 { var a: f64 = 9.0; return (__sqrt_f64(a)) as i32; }", 3},
		{"f64-int-roundtrip", "function main(): i32 { var n: i32 = 7; var x: f64 = n as f64; return (x + 0.5) as i32; }", 7},
		// A struct with a POINTER field generates a `__struct_drop_<T>` helper whose
		// folded body reads the field via `(i32.load offset=8 …)`. watbin ignored
		// the `offset=N` memarg — it hardcoded offset 0 AND recursed into the
		// `offset=8` ATOM as if it were the address operand (its `items` is null →
		// SIGSEGV). Any struct with a nested-struct / array / string field crashed
		// the assembler (#4801). Pins the memarg parse on both the load and the
		// field read.
		{"struct-nested-field", "struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { var o = Outer { inner: Inner { v: 8 }, k: 34 }; return o.inner.v + o.k; }", 42},
		// An ESCAPING closure (returned from a function, capturing a param) is
		// called via `call_indirect (type $c)` through the funcref table. The FLAT
		// emitter's `call_indirect` was unhandled in enc_flat_body (only the folded
		// enc_instr path had it), so the indirect call was DROPPED — the module
		// validated but computed garbage. Pins the flat call_indirect encoding
		// (#4801).
		{"closure-capture-return", "function adder(n: i32): fn { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var a = adder(10); return a(5); }", 15},
		{"lambda-as-arg", "function apply(f: fn, v: i32): i32 { return f(v); } function main(): i32 { return apply(function(x: i32): i32 { return x * 7; }, 6); }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flatWat := pipe(irDriver, []byte(tc.src), "-ir")
			if len(flatWat) == 0 || !bytes.Contains(flatWat, []byte("i32.const")) {
				t.Fatalf("ir driver did not emit flat WAT for %q:\n%s", tc.src, flatWat)
			}
			byteLines := pipe(asmBin, flatWat)
			// Parse the newline-separated decimals into a .wasm.
			var wasmBytes []byte
			for _, ln := range strings.Fields(string(byteLines)) {
				v, err := strconv.Atoi(ln)
				if err != nil {
					t.Fatalf("bad byte %q from watbin: %v", ln, err)
				}
				wasmBytes = append(wasmBytes, byte(v))
			}
			if len(wasmBytes) == 0 {
				t.Fatal("watbin produced 0 bytes")
			}
			wasmPath := filepath.Join(dir, tc.name+".wasm")
			if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
				t.Fatalf("write wasm: %v", err)
			}
			run := exec.Command(wasmtime, "run", wasmPath)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: binary exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
