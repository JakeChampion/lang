package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckerDeclarationScopeSequenceX86_64(t *testing.T) {
	bin, runner, dir := buildCheckerCodesBin(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"variable-then-function", `let x = (); function x(): i32 { let inner = (): i32 => x(); return 0; }`, ""},
		{"function-then-function", `function x(): i32 { return 1; } function x(): i32 { return 2; }`, ""},
		{"function-then-variable", `function x(): i32 { return 1; } let x = 2;`, "E013"},
		{"variable-then-variable", `let x = 1; let x = 2;`, "E013"},
		{"variable-then-arrow", `let x = 1; let x = (): i32 => 2;`, "E013"},
		{"inner-variable-shadow", `let x = 1; if (true) { let x = 2; }`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { " + tc.body + " return 0; }"
			var goCodes []string
			for _, d := range goCheckerDiags(t, dir, src) {
				goCodes = append(goCodes, d.code)
			}
			if got := strings.Join(goCodes, ","); got != tc.want {
				t.Fatalf("Go checker diagnostic sequence = %q, want %q", got, tc.want)
			}
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			var codes []string
			for _, d := range driverDiags(runCheckerDriver(t, cmd, tc.name)) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(codes, ","); got != tc.want {
				t.Errorf("self-host diagnostic sequence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelfHostCheckerDeclarationScopeWasm(t *testing.T) {
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
		want string
	}{
		{"variable-then-function", `let x = (); function x(): i32 { let inner = (): i32 => x(); return 0; }`, ""},
		{"function-then-function", `function x(): i32 { return 1; } function x(): i32 { return 2; }`, ""},
		{"function-then-variable", `function x(): i32 { return 1; } let x = 2;`, "E013"},
		{"variable-then-variable", `let x = 1; let x = 2;`, "E013"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { " + tc.body + " return 0; }"
			cmd := exec.Command(wasmtime, "run", bin)
			cmd.Stdin = strings.NewReader(src)
			out := runCheckerDriver(t, cmd, tc.name)
			var codes []string
			for _, d := range driverDiags(out) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(codes, ","); got != tc.want {
				t.Errorf("Wasm diagnostic sequence = %q, want %q", got, tc.want)
			}
		})
	}
}
