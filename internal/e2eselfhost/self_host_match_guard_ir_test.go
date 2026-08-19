package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// matchGuardIRCases pin match-arm GUARDS (`Pattern when <cond> => …`) to the
// self-host IR path on x86-64 + wasm. A guarded arm lowers through IR — irlower
// emits `lower_expr(guard)` + a not/br_if skip and propagates `.ok`, so the
// module stays IR-eligible — for both the enum-payload-variant arm and the
// literal-match arm. No existing self-host test exercises a `when` guard at all,
// so a regression that kicked guarded matches off the IR path would pass
// silently. These cases close that gap with the path-probe pin (assert path ==
// "ir") + interp oracle, mirroring self_host_block_expr_ir_test.go.
//
// Scope notes (kept to the shapes that lower AND type-check natively, so the
// interp oracle agrees): exactly ONE guarded arm per match plus an unguarded
// fallback of the same pattern. A SECOND guarded arm on the same variant bails
// the module to AST; a bool-scrutinee guard needs an unguarded `_` (E030); and a
// guarded wildcard `_ when …` must be last so it can't precede a catch-all
// (E026) — all excluded. Every guard is a scalar i32/bool comparison and every
// result is <= 126 (wasmtime exit-code truncation, cf. #2908).
var matchGuardIRCases = []struct {
	name string
	main string
}{
	// Enum-payload variant-arm guard with same-name re-bind across the guarded
	// and unguarded arm (#2644 — the slot-allocation-prone shape). Has(7): n>5
	// -> 7; Has(2): else -> 102; Nil -> 0. 7 + 102 + 0 = 109.
	{"variant-rebind-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n > 5 => { return n; }, Has(n) => { return n + 100; }, Nil => { return 0; } } }
function main(): i32 { var a = pick(Has(7)); var b = pick(Has(2)); var c = pick(Nil); return a + b + c; }`},
	// Enum-payload variant-arm guard with an equality predicate. Has(0): n==0
	// -> 7; Has(5): else -> 5; Nil -> 9. 7 + 5 + 9 = 21.
	{"variant-eq-guard", `enum Opt { Has(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) when n == 0 => { return 7; }, Has(n) => { return n; }, Nil => { return 9; } } }
function main(): i32 { var a = pick(Has(0)); var b = pick(Has(5)); var c = pick(Nil); return a + b + c; }`},
	// Literal-match arm guard on a bool flag: `0 when big`. f(0,true)=100,
	// f(0,false)=1, f(5,_)=9. 100 + 1 + 9 = 110.
	{"litmatch-guard", `function f(tag: i32, big: boolean): i32 { match (tag) { 0 when big => { return 100; }, 0 => { return 1; }, _ => { return 9; } } }
function main(): i32 { var a = f(0, true); var b = f(0, false); var c = f(5, false); return a + b + c; }`},
	// A second literal-match arm guard (different literal + flag): `1 when flag`.
	// f(1,true)=50, f(1,false)=5, f(2,_)=9. 50 + 5 + 9 = 64.
	{"litmatch-guard-flag", `function f(tag: i32, flag: boolean): i32 { match (tag) { 1 when flag => { return 50; }, 1 => { return 5; }, _ => { return 9; } } }
function main(): i32 { var a = f(1, true); var b = f(1, false); var c = f(2, true); return a + b + c; }`},
}

// TestSelfHostMatchGuardIRX86_64 routes each guarded-match case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostMatchGuardIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range matchGuardIRCases {
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

// TestSelfHostMatchGuardIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostMatchGuardIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host match-guard wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range matchGuardIRCases {
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
			watFile := filepath.Join(dir, "match_guard_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("match-guard wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
