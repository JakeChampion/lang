package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrConcatTempWasmIR proves the anonymous concat-operand reclaim
// (#2649) never DOUBLE-FREES on the wasm IR backend. A wasm heap string is
// rc-headered, so __fern_str_free maps to $__fern_arr_dec, whose over-release
// detector ticks on a dec below rc 0 (a data-section literal operand is a guarded
// no-op). The churn evaluates a concat chain many times — freeing the fresh
// intermediate + the final each iteration — and main checks
// __rc_underflow_count() == 0 (a double-free surfaces as exit 99).
func TestSelfHostStrConcatTempWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host concat-temp wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Concat chain `pre + "x" + suf` reclaimed each iteration (intermediate +
		// final; the "x" literal is a data-section no-op). A double-free of any freed
		// box would tick the underflow detector → 99. r = "aaxbb" len 5; t stays 0.
		{"chain-churn", `function churn(n: i32): i32 { var pre: string = "aa"; var suf: string = "bb"; var t: i32 = 0; var i: i32 = 0; while (i < n) { var r: string = pre + "x" + suf; if (r.len() < 5) { t = 1; } i = i + 1; } return t; } function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// GUARD against widening is_fresh_str_temp to admit a general
		// string-returning call as a concat operand. That widening is tempting
		// — `"x" + f(a)` leaks f's box once per evaluation today, because the
		// concat only READS its operand and nothing else reclaims a call result
		// in that position — and the array side has a documented precedent for
		// sweeping call results ("StmtReturn always applies return-retain", see
		// docs/RC-PERCEUS-SELF-HOST-PORT.md). That precedent does NOT extend to
		// strings: a callee returning a BORROWED value hands back a box a live
		// local still holds, WITHOUT an inc. Freeing it at the concat drops the
		// box under `a`, the next iteration's allocation reuses it, and the
		// length read goes wrong — measured 96 when the naive widening was
		// tried. `mk` is present so the fixture keeps a fresh-returning callee
		// alongside the borrowed one.
		//
		// The obvious repair — gate on a per-callee analysis proving EVERY
		// return of that callee is a fresh producer — is ALSO not sufficient,
		// and this case does not catch that second failure. str_fresh_ret_fns
		// is exactly that analysis (str_fresh_ret_fns_of, whose
		// body_has_nonfresh_str_return does recurse into nested if / while /
		// for / match returns), yet gating the call arm on it still breaks
		// TestSelfHostWasmWholeCompilerShardedLink: the wasm-HOSTED compiler
		// emits malformed WAT (`i32.const"0)` — text corrupted by memory reuse
		// of a box that was still live). Verified both directions locally,
		// PASS before the gate and FAIL with it. The likely reason is
		// LIFETIME, not freshness: that registry was validated for a BINDING,
		// where the box becomes a local reclaimed at scope end behind
		// slot_is_reclaimable_str's extra aliasing guards, whereas the concat
		// frees immediately after reading. A future attempt needs to explain
		// that gap before re-widening — the whole-compiler test is the gate
		// that catches it, not this one.
		{"call-operand-borrowed-return", `function mk(s: string): string { return s + "!"; } function pick(a: string, b: string): string { if (a.len() > 3) { return a; } return b; } function main(): i32 { var a: string = mk("abcdefg"); var b: string = "xy"; var i: i32 = 0; while (i < 2000) { var r: string = "[" + pick(a, b); if (r.len() != 9) { return 96; } i = i + 1; } if (a != "abcdefg!") { return 97; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
		// An ARITHMETIC `.to_string()` receiver (`(i % 8).to_string()`) is the
		// builtin scalar producer just as a bare slot is, so its operand box is
		// freed each iteration. wasm's $__fern_arr_dec ticks the over-release
		// detector if that free were ever applied to an alias (#6544).
		{"arith-tostring-operand-churn", `function churn(n: i32): i32 { var t: i32 = 0; var i: i32 = 0; while (i < n) { var r: string = "n" + (i % 8).to_string(); if (r.len() != 2) { t = 1; } i = i + 1; } return t; } function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR box path (no box in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("concat-temp wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
