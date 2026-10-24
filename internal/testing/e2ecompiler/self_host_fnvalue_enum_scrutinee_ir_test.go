package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostFnValueEnumScrutineeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name   string
		src    string
		want   int
		wantIR bool
	}{
		{"fn-param-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[i32]): i32 { match (f(7)) { Has(v) => { return v; }, Non => { return 0; } } } function main(): i32 { return run((k: i32): Opt[i32] => { return Has(k * 6); }); }`,
			42, true},
		{"fn-param-string-payload-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[string]): i32 { match (f(1)) { Has(s) => { return s.len() + 40; }, Non => { return 0; } } } function main(): i32 { return run((k: i32): Opt[string] => { return Has("xx"); }); }`,
			42, true},
		{"fn-param-non-branch-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[i32], k: i32): i32 { match (f(k)) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return run((k: i32): Opt[i32] => { if (k > 0) { return Has(k); } return Non; }, 0); }`,
			42, true},
		{"fn-local-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let f: (i32) => Opt[i32] = (k: i32): Opt[i32] => { if (k > 0) { return Has(k * 6); } return Non; }; match (f(7)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42, false},
		{"fn-value-result-var-bound",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let f: (i32) => Opt[i32] = (k: i32): Opt[i32] => { return Has(k * 6); }; let r: Opt[i32] = f(7); match (r) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if tc.wantIR && !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the fn-value enum scrutinee did not lower through the IR", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
