package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckerCaptureScopeSequenceX86_64(t *testing.T) {
	bin, runner, dir := buildCheckerCodesBin(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"nested", `let x = v(); let outer = (): i32 => { let inner = () => x; return 0; };`, 2},
		{"three-levels", `let x = v(); let outer = (): i32 => { let middle = (): i32 => { let inner = () => x; return 0; }; return 0; };`, 3},
		{"parameter-shadow", `let x = v(); let outer = (x: i32): i32 => { let inner = () => x; return inner(); };`, 0},
		{"local-shadow", `let x = v(); let outer = (): i32 => { let x = 42; let inner = () => x; return inner(); };`, 0},
		{"block-shadow", `let x = v(); if (true) { let x = 42; let inner = () => x; }`, 0},
		{"for-shadow", `let x = v(); for x in [1, 2] { let inner = () => x; }`, 0},
		{"match-shadow", `let x = v(); match (Some(42)) { Some(x) => { let inner = () => x; }, None => {} }`, 0},
		{"match-guard-capture", `let x = v(); match (Some(42)) { Some(n) when (((): boolean => { let ignored = x; return true; })()) => {}, Some(_) => {}, None => {} }`, 1},
		{"match-guard-shadow", `let x = v(); match (Some(42)) { Some(x) when (((): boolean => x > 0)()) => {}, Some(_) => {}, None => {} }`, 0},
		{"tuple-shadow", `let x = v(); let outer = (): i32 => { let (x, y) = (42, 1); let inner = () => x; return inner(); };`, 0},
		{"named-function-shadow", `let x = v(); if (true) { function x(): i32 { let inner = (): i32 => x(); return 0; } }`, 0},
		{"lambda-local-void", `let outer = (): i32 => { let x = v(); let inner = () => x; return 0; };`, 1},
		{"shadow-does-not-escape", `let x = v(); if (true) { let x = 42; let inner = () => x; } let bad = () => x;`, 1},
		{"value-block", `let x = v(); let n: i32 = if (true) { let inner = () => x; 0 } else { 0 };`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function v(): void { return; }\nfunction main(): i32 { " + tc.body + " return 0; }"
			want := strings.TrimSuffix(strings.Repeat("E044,", tc.want), ",")
			if got := strings.Join(goCheckerDiagCodes(t, dir, src), ","); got != want {
				t.Fatalf("Go checker diagnostics = %q, want %q", got, want)
			}
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			out := runCheckerDriver(t, cmd, tc.name)
			var codes []string
			for _, d := range driverDiags(out) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(codes, ","); got != want {
				t.Errorf("self-host diagnostic sequence = %q, want %q", got, want)
			}
		})
	}
}

func TestSelfHostCheckerCaptureScopeWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "capture_checker.wasm", e2eharness.TargetWasm32Wasi)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"nested", `let x = v(); let outer = (): i32 => { let inner = () => x; return 0; };`, 2},
		{"parameter-shadow", `let x = v(); let outer = (x: i32): i32 => { let inner = () => x; return inner(); };`, 0},
		{"for-shadow", `let x = v(); for x in [1, 2] { let inner = () => x; }`, 0},
		{"match-shadow", `let x = v(); match (Some(42)) { Some(x) => { let inner = () => x; }, None => {} }`, 0},
		{"match-guard-capture", `let x = v(); match (Some(42)) { Some(n) when (((): boolean => { let ignored = x; return true; })()) => {}, Some(_) => {}, None => {} }`, 1},
		{"value-block", `let x = v(); let n: i32 = if (true) { let inner = () => x; 0 } else { 0 };`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function v(): void { return; }\nfunction main(): i32 { " + tc.body + " return 0; }"
			cmd := exec.Command(wasmtime, "run", bin)
			cmd.Stdin = strings.NewReader(src)
			out := runCheckerDriver(t, cmd, tc.name)
			var codes []string
			for _, d := range driverDiags(out) {
				codes = append(codes, d.code)
			}
			want := strings.TrimSuffix(strings.Repeat("E044,", tc.want), ",")
			if got := strings.Join(codes, ","); got != want {
				t.Errorf("Wasm diagnostic sequence = %q, want %q", got, want)
			}
		})
	}
}
