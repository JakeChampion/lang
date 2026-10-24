package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostWatbinFlat(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping watbin flat e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern", "watbin.fern")
	irDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "ir_driver")

	asmDriver := `
import "std/io";
import "./util";
import "./watbin";
function main(): i32 {
    let wat: string = "";
    match (io.read_all_stdin()) { Ok(text) => { wat = text; }, Err(_) => { return 253; } }
    let bs: i32[] = watbin.wat_to_binary(wat);
    let i: i32 = 0;
    while (i < bs.len()) { write(util.i32_to_string(bs[i])); write("\n"); i = i + 1; }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "watbin_run.fern"), []byte(asmDriver), 0o644); err != nil {
		t.Fatalf("write watbin_run.fern: %v", err)
	}
	asmBin := buildSelfHostBin(t, gcc, dir, "watbin_run.fern", "watbin_run")

	pipe := func(bin string, stdin []byte) []byte {
		t.Helper()
		cmd := runX86_64Bin(runner, bin)
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
		{"hex-immediates", "function main(): i32 { let wide = 0xFFFFFFFF; let upper = 0X80000000; let word: u32 = 0xffffffff; if (wide / 65536 == 65535 && upper > 0 && word + 1 == 0u32) { return 42; } return 1; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"while-sum", "function main(): i32 { let i = 1; let s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"if-else", "function main(): i32 { let x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }", 9},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"arr-index", "function main(): i32 { let a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-loop-sum", "function main(): i32 { let a = [5, 10, 15, 20, 25]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-set", "function main(): i32 { let a = [0, 0, 0]; a = a.with(1, 99); return a[0] + a[1] + a[2]; }", 99},
		{"sci-float-exp", "function main(): i32 { let x = 1e3; let y = 1.5e-2; if (x == 1000.0 && y > 0.0149 && y < 0.0151) { return 42; } return 1; }", 42},
		{"f64-trunc-mul", "function main(): i32 { let x: f64 = 3.5; return (x * 2.0) as i32; }", 7},
		{"f64-trunc-sub", "function main(): i32 { let a: f64 = 10.5; let b: f64 = 3.5; return (a - b) as i32; }", 7},
		{"f64-trunc-sqrt", "function main(): i32 { let a: f64 = 9.0; return (__sqrt_f64(a)) as i32; }", 3},
		{"f64-int-roundtrip", "function main(): i32 { let n: i32 = 7; let x: f64 = n as f64; return (x + 0.5) as i32; }", 7},
		{"struct-nested-field", "struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { let o = Outer { inner: Inner { v: 8 }, k: 34 }; return o.inner.v + o.k; }", 42},
		{"closure-capture-return", "function adder(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; } function main(): i32 { let a = adder(10); return a(5); }", 15},
		{"lambda-as-arg", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply((x: i32): i32 => { return x * 7; }, 6); }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flatWat := pipe(irDriver, []byte(tc.src))
			if len(flatWat) == 0 || !bytes.Contains(flatWat, []byte("i32.const")) {
				t.Fatalf("ir driver did not emit flat WAT for %q:\n%s", tc.src, flatWat)
			}
			byteLines := pipe(asmBin, flatWat)
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
