package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string-reclaim wasm IR e2e")
	}
	l := newWasmStdlibLoader(t)
	dir := t.TempDir()

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"loop-concat", `function churn(n: i32): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < n) { let s: string = "ab" + "cd"; sum = (sum + s.len()) % 100; i = i + 1; } return sum; } function main(): i32 { let r: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		{"loop-to-upper", `import "std/string";
function churn(n: i32): i32 { let base: string = "xyz"; let sum: i32 = 0; let i: i32 = 0; while (i < n) { let s: string = base.to_ascii_upper(); sum = (sum + s.len()) % 100; i = i + 1; } return sum; } function main(): i32 { let r: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		{"scope-exit", `function churn(): i32 { let s: string = "hi" + "!"; return s.len(); } function main(): i32 { let r: i32 = churn(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 3},
		{"aliased-safe", `function churn(): i32 { let s: string = "ab" + "c"; let t: string = s; return s.len() + t.len(); } function main(): i32 { let r: i32 = churn(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 6},
		{"loop-i32-to-string", `import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 50) { let s: string = i.to_string(); sum = sum + s.len(); i = i + 1; } return sum; }`, 90},
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
				t.Errorf("string reclaim wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
