package e2ecompiler

import (
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
	l := newWasmStdlibLoader(t)
	dir := t.TempDir()

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Concat chain `pre + "x" + suf` reclaimed each iteration (intermediate +
		// final; the "x" literal is a data-section no-op). A double-free of any freed
		// box would tick the underflow detector → 99. r = "aaxbb" len 5; t stays 0.
		{"chain-churn", `function churn(n: i32): i32 { let pre: string = "aa"; let suf: string = "bb"; let t: i32 = 0; let i: i32 = 0; while (i < n) { let r: string = pre + "x" + suf; if (r.len() < 5) { t = 1; } i = i + 1; } return t; } function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// GUARD: a string-returning call used as a concat operand may hand
		// back a BORROWED box — `pick` returns a box a live local still
		// holds. Freeing it once the concat has read it drops the box under
		// `a`, the next iteration's allocation reuses it, and the length read
		// goes wrong (measured 96 when that was tried). `mk` is present so the
		// fixture keeps a fresh-returning callee alongside the borrowed one.
		{"call-operand-borrowed-return", `function mk(s: string): string { return s + "!"; } function pick(a: string, b: string): string { if (a.len() > 3) { return a; } return b; } function main(): i32 { let a: string = mk("abcdefg"); let b: string = "xy"; let i: i32 = 0; while (i < 2000) { let r: string = "[" + pick(a, b); if (r.len() != 9) { return 96; } i = i + 1; } if (a != "abcdefg!") { return 97; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
		// An ARITHMETIC `.to_string()` receiver (`(i % 8).to_string()`) is the
		// builtin scalar producer just as a bare slot is, so its operand box is
		// freed each iteration. wasm's $__fern_arr_dec ticks the over-release
		// detector if that free were ever applied to an alias (#6544).
		{"arith-tostring-operand-churn", `import "std/i32";
function churn(n: i32): i32 { let t: i32 = 0; let i: i32 = 0; while (i < n) { let r: string = "n" + (i % 8).to_string(); if (r.len() != 2) { t = 1; } i = i + 1; } return t; } function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := l.emit(t, tc.src)
			if len(wat) == 0 {
				t.Fatalf("no WAT for %q", tc.src)
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
