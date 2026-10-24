package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostGenericEnumUnitArgIRX86_64(t *testing.T) {
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
		name string
		src  string
		want int
	}{
		{"qualified-unit-variant-arg",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Opt.Non); }`,
			42},
		{"bare-unit-variant-arg",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Non); }`,
			42},
		{"string-payload-enum-bare-arg",
			`enum Opt[T] { Non, Has(T) } function first(o: Opt[string]): i32 { match (o) { Has(s) => { return s.len(); }, Non => { return 9; } } } function main(): i32 { return first(Non) + 33; }`,
			42},
		{"two-unit-variant-args",
			`enum Opt[T] { Non, Has(T) } function pick(a: Opt[i32], b: Opt[i32]): i32 { match (a) { Has(v) => { return v; }, Non => { match (b) { Has(w) => { return w; }, Non => { return 42; } } } } } function main(): i32 { return pick(Non, Non); }`,
			42},
		{"mixed-payload-and-unit-args",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32], d: i32): i32 { match (o) { Has(v) => { return v; }, Non => { return d; } } } function main(): i32 { return get(Has(30), 0) + get(Non, 12); }`,
			42},
		{"non-generic-unit-variant-arg",
			`enum Opt { Has(i32), Non } function get(o: Opt): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Non); }`,
			42},
		{"var-annotated-unit-variant",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { let e: Opt[i32] = Non; return get(e); }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the generic-enum unit-variant arg did not lower through the IR", tc.name)
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
