package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLinkerAgnosticIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("linker-agnostic gate runs only natively (multi-linker link+run)")
	}
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
		exit int
	}{
		{"issue-4081-repro",
			`function main(): i32 { let g: string[][] = [["ab", "c"], ["de"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len(); }`, 5},
		{"string-grid-len",
			`function main(): i32 { let g: string[][] = [["abcd", "efg"], ["hi", "j"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len() + g[1][1].len(); }`, 10},
		{"nested-rebind-len",
			`function main(): i32 { let g: string[][] = [["xyz", "w"], ["uv"]]; let r: string = g[0][0]; return r.len(); }`, 3},
	}

	type linker struct {
		name string
		flag string
	}
	linkers := []linker{{"bfd", ""}}
	if _, err := exec.LookPath("ld.lld"); err == nil {
		linkers = append(linkers, linker{"lld", "-fuse-ld=lld"})
	}
	if _, err := exec.LookPath("mold"); err == nil {
		linkers = append(linkers, linker{"mold", "-fuse-ld=mold"})
	}
	if len(linkers) < 2 {
		t.Skip("no alternative linker (ld.lld / mold) on PATH; nothing to cross-check")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			asmPath := filepath.Join(dir, tc.name+".s")
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write %s asm: %v", tc.name, err)
			}
			for _, ln := range linkers {
				binPath := filepath.Join(dir, tc.name+"."+ln.name)
				args := []string{"-static", "-nostdlib", "-no-pie"}
				if ln.flag != "" {
					args = append(args, ln.flag)
				}
				args = append(args, asmPath, "-o", binPath)
				if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
					t.Fatalf("%s: link with %s failed: %v\n%s", tc.name, ln.name, err, out)
				}
				cmd := exec.Command(binPath)
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.exit {
					t.Errorf("%s linked with %s: exit %d, want %d (linker-dependent layout bug — #4081)",
						tc.name, ln.name, code, tc.exit)
				}
			}
		})
	}
}
