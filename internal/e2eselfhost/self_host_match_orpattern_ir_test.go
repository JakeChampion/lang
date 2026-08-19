package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// matchOrPatternIRCases pin match-arm OR-PATTERNS (`A | B => …`, issue #2698)
// to the self-host IR path on x86-64 + wasm. The parser desugars an or-pattern
// into one arm per alternative sharing the (per-alternative) guard + body, so
// the checker (exhaustiveness, payload binding) and irlower see an ordinary
// flat arm list — no new IR. These cases prove the desugar survives the
// self-host parser end to end: payloadless variants, a same-name payload
// binding reused across alternatives, a guard applied to every alternative,
// and the expression-form match. Scope: variant patterns only (a bare `|`
// between integer literals is the bitwise-or operator on the literal-match
// path, so literal or-patterns are intentionally rejected — see the parser).
// Each result is <= 126 (wasmtime exit-code truncation, cf. #2908), routing is
// path-probe-pinned to "ir", and each is oracle-checked against the reference
// interpreter. Mirrors self_host_match_guard_ir_test.go.
var matchOrPatternIRCases = []struct {
	name string
	main string
}{
	// Payloadless variant or-pattern (stmt form). pick(Red)=1, pick(Green)=2,
	// pick(Blue)=1 (via the `Red | Blue` arm). 1 + 2*10 + 1*100 = 121.
	{"variant-payloadless", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { match (c) { Red | Blue => { return 1; }, Green => { return 2; } } }
function main(): i32 { return pick(Red) + pick(Green) * 10 + pick(Blue) * 100; }`},
	// Same-name payload binding reused across both alternatives of the
	// or-pattern (the slot-allocation-prone shape). f(Sq(5))=10, f(Circ(7))=14,
	// f(Tri(3))=3. 10 + 14 + 3 = 27.
	{"variant-binding", `enum Shape { Sq(i32), Circ(i32), Tri(i32) }
function f(s: Shape): i32 { match (s) { Sq(x) | Circ(x) => { return x * 2; }, Tri(x) => { return x; } } }
function main(): i32 { return f(Sq(5)) + f(Circ(7)) + f(Tri(3)); }`},
	// A guard applied to every alternative of an or-pattern, with an unguarded
	// or-pattern fallback over the same variants (one guarded + one unguarded
	// arm per variant — the IR-eligible guard shape). pick(Has(7))=1,
	// pick(Big(2))=2, pick(Nil)=3. 1 + 2*5 + 3*25 = 86.
	{"variant-guard", `enum Opt { Has(i32), Big(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) | Big(n) when n > 5 => { return 1; }, Has(n) | Big(n) => { return 2; }, Nil => { return 3; } } }
function main(): i32 { return pick(Has(7)) + pick(Big(2)) * 5 + pick(Nil) * 25; }`},
	// Expression-form match with an or-pattern arm. pick(Red)=7, pick(Green)=9,
	// pick(Blue)=7. 7 + 9 + 7*2 = 30.
	{"variant-expr", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { return match (c) { Red | Blue => 7, Green => 9 }; }
function main(): i32 { return pick(Red) + pick(Green) + pick(Blue) * 2; }`},
}

// TestSelfHostMatchOrPatternIRX86_64 routes each or-pattern case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostMatchOrPatternIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range matchOrPatternIRCases {
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

// TestSelfHostMatchOrPatternIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostMatchOrPatternIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host match-or-pattern wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range matchOrPatternIRCases {
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
			watFile := filepath.Join(dir, "match_orpattern_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("match-or-pattern wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
