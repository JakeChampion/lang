package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		{"chain-churn", `function churn(n: i32): i32 { let pre: string = "aa"; let suf: string = "bb"; let t: i32 = 0; let i: i32 = 0; while (i < n) { let r: string = pre + "x" + suf; if (r.len() < 5) { t = 1; } i = i + 1; } return t; } function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"call-operand-borrowed-return", `function mk(s: string): string { return s + "!"; } function pick(a: string, b: string): string { if (a.len() > 3) { return a; } return b; } function main(): i32 { let a: string = mk("abcdefg"); let b: string = "xy"; let i: i32 = 0; while (i < 2000) { let r: string = "[" + pick(a, b); if (r.len() != 9) { return 96; } i = i + 1; } if (a != "abcdefg!") { return 97; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
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
