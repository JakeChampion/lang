package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A struct literal that fixes no type parameter of its struct is E040 unless
// something at its place fixes it: a written argument, an annotation, a
// field, or another argument of the call (#15547).
func structLitTypeParamCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = "struct W[T] { k: i32 }\n" +
		"struct V[T] { v: T }\n" +
		"function early[T](w: W[T]): i32 { return w.k; }\n" +
		"function two[T](a: T, w: W[T]): i32 { return w.k; }\n" +
		"function vv[T](w: V[T]): i32 { return 1; }\n"
	cases := []struct {
		name, body string
		reject     bool
	}{
		{"argument-of-a-callee-parameter", `let n = early(W { k: 1 });`, true},
		{"argument-in-a-generic-caller", `let n = early(W { k: 1 });`, true},
		{"plain-let", `let w = W { k: 1 };`, true},
		{"fixed-by-another-argument", `let n = two(1, W { k: 1 });`, false},
		{"written-argument", `let n = early(W[i32] { k: 1 });`, false},
		{"annotated-local", `let w: W[i32] = W { k: 2 }; let n = early(w);`, false},
		{"fixed-by-a-field", `let n = vv(V { v: 3 });`, false},
		{"annotated-let", `let w: W[string] = W { k: 1 };`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := "function f(): i32 { " + tc.body + " return 0; }\n"
			if tc.name == "argument-in-a-generic-caller" {
				fn = "function f[U](x: U): i32 { " + tc.body + " return 0; }\n"
			}
			source := decls + fn + "function main(): i32 { return 0; }\n"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				found := false
				for _, d := range ds {
					if d.code == "E040" {
						found = true
					}
				}
				if found != tc.reject || !tc.reject && len(ds) != 0 {
					t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
				}
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostStructLitTypeParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "struct_lit_type_param_checker")
	structLitTypeParamCases(t, bin, runner, dir)
}

func TestSelfHostStructLitTypeParamWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "struct_lit_type_param_checker.wasm", e2eharness.TargetWasm32Wasi)
	structLitTypeParamCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
