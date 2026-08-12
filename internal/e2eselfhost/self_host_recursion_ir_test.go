package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recursionIRCases pin top-level direct self-recursion and mutual recursion to
// the self-host IR path on x86-64 + wasm. Recursion is just an OpCall to a
// function already in the module's symbol table — no new IR construct — and all
// the building blocks (if, arithmetic, calls, string +) are individually pinned,
// so eligibility never bails. Recursive *local* (nested, hoisted) functions are
// covered by self_host_recursive_local_test.go (a Go-backend cross-check, no path
// pin, no wasm leg); top-level direct + mutual recursion had no path-probe "ir"
// pin at all.
//
// Each case is routing-pinned via asm_pathprobe_run (assert path == "ir") and
// oracle-checked against the interpreter; every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908). Mirrors self_host_nested_tuple_ir_test.go.
var recursionIRCases = []struct {
	name string
	main string
}{
	// Classic self-recursion: factorial. fact(5) = 120.
	{"fact", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }\nfunction main(): i32 { return fact(5); }"},
	// Tree recursion (two self-calls per frame): fib(10) = 55.
	{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\nfunction main(): i32 { return fib(10); }"},
	// Tail-accumulator self-recursion. sum_to(10, 0) = 55.
	{"tail-acc", "function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); }\nfunction main(): i32 { return sum_to(10, 0); }"},
	// Mutual recursion across two top-level functions. ping(7) = 7.
	{"mutual", "function ping(n: i32): i32 { if (n <= 0) { return 0; } return 1 + pong(n - 1); }\nfunction pong(n: i32): i32 { if (n <= 0) { return 0; } return 1 + ping(n - 1); }\nfunction main(): i32 { return ping(7); }"},
	// Euclid's gcd via self-recursion. gcd(48, 36) = 12.
	{"gcd", "function gcd(a: i32, b: i32): i32 { if (b == 0) { return a; } return gcd(b, a - (a / b) * b); }\nfunction main(): i32 { return gcd(48, 36); }"},
	// Integer power via self-recursion. ipow(2, 6) = 64.
	{"ipow", "function ipow(base: i32, e: i32): i32 { if (e == 0) { return 1; } return base * ipow(base, e - 1); }\nfunction main(): i32 { return ipow(2, 6); }"},
	// Ackermann (nested self-call in an argument position). ack(2, 3) = 9.
	{"ackermann", "function ack(m: i32, n: i32): i32 { if (m == 0) { return n + 1; } if (n == 0) { return ack(m - 1, 1); } return ack(m - 1, ack(m, n - 1)); }\nfunction main(): i32 { return ack(2, 3); }"},
}

// TestSelfHostRecursionIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostRecursionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range recursionIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostRecursionIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostRecursionIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host recursion wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range recursionIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "recursion_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("recursion wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
