package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostMethodEnumScrutineeIRX86_64(t *testing.T) {
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
		{"method-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Non; } function main(): i32 { let s: S = S { n: 21 }; match (s.find(21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		{"method-scrutinee-both-payload",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Has(0); } function main(): i32 { let s: S = S { n: 21 }; match (s.find(21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		{"method-string-payload-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[string] { if (k == s.n) { return Has("hi"); } return Non; } function main(): i32 { match (S { n: 5 }.find(5)) { Has(v) => { return v.len() + 40; }, Non => { return 0; } } }`,
			42},
		{"free-fn-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function find(k: i32, n: i32): Opt[i32] { if (k == n) { return Has(n * 2); } return Non; } function main(): i32 { match (find(21, 21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		{"method-result-var-bound",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Non; } function main(): i32 { let s: S = S { n: 21 }; let r: Opt[i32] = s.find(21); match (r) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the method-enum scrutinee did not lower through the IR", tc.name)
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
