package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostFnKeyEnumIRX86_64(t *testing.T) {
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
		{"fnkey-enum-var-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let f: (i32) => i32 = (x: i32): i32 => { return x + 40; }; let o: Opt[(i32) => i32] = Has(f); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		{"fnkey-enum-capturefree",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let o: Opt[(i32) => i32] = Has((x: i32): i32 => { return x + 40; }); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		{"fnkey-enum-capturing",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let base: i32 = 40; let f: (i32) => i32 = (x: i32): i32 => { return x + base; }; let o: Opt[(i32) => i32] = Has(f); match (o) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		{"fnkey-enum-param",
			`enum Opt[T] { Non, Has(T) } function run(o: Opt[(i32) => i32]): i32 { match (o) { Has(g) => { return g(2); }, Non => { return 0; } } } function main(): i32 { let f: (i32) => i32 = (x: i32): i32 => { return x + 40; }; return run(Has(f)); }`,
			42},
		{"fnkey-enum-non-branch",
			`enum Opt[T] { Non, Has(T) } function pick(b: boolean): Opt[(i32) => i32] { if (b) { return Has((x: i32): i32 => { return x + 40; }); } return Non; } function main(): i32 { let o: Opt[(i32) => i32] = pick(false); match (o) { Has(g) => { return g(2); }, Non => { return 42; } } }`,
			42},
		{"fnkey-enum-inline-match",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let f: (i32) => i32 = (x: i32): i32 => { return x + 40; }; match (Has(f)) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		{"fnkey-enum-inline-capturing",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { let base: i32 = 40; let f: (i32) => i32 = (x: i32): i32 => { return x + base; }; match (Has(f)) { Has(g) => { return g(2); }, Non => { return 0; } } }`,
			42},
		{"fnkey-enum-inline-i32-regress",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { match (Has(42)) { Has(n) => { return n; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the fn-keyed enum did not lower through the IR", tc.name)
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
