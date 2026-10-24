package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTupleEnumScrutineeIRX86_64(t *testing.T) {
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
		{"tuple-elem0-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let t: (Opt[i32], i32) = (Has(40), 2); match (t.0) { Has(v) => { return v + t.1; }, Non => { return 0; } } }`,
			42},
		{"tuple-elem1-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let t: (i32, Opt[i32]) = (2, Has(40)); match (t.1) { Has(v) => { return v + t.0; }, Non => { return 0; } } }`,
			42},
		{"tuple-elem-mid-of-three",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let t: (i32, Opt[i32], i32) = (1, Has(39), 2); match (t.1) { Has(v) => { return v + t.0 + t.2; }, Non => { return 0; } } }`,
			42},
		{"tuple-elem-string-payload",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let t: (Opt[string], i32) = (Has("abcd"), 3); match (t.0) { Has(s) => { return s.len() + t.1 + 35; }, Non => { return 0; } } }`,
			42},
		{"tuple-elem-var-extracted",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let t: (Opt[i32], i32) = (Has(40), 2); let e: Opt[i32] = t.0; match (e) { Has(v) => { return v + t.1; }, Non => { return 0; } } }`,
			42},
		{"struct-field-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct Box { o: Opt[i32] } function main(): i32 { let b: Box = Box { o: Has(42) }; match (b.o) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the tuple-enum scrutinee did not lower through the IR", tc.name)
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
