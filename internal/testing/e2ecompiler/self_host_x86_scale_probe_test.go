package e2ecompiler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelfHostX86ScaleProbe(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/wasm_run.fern")
	asmRun := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "asm_run")
	wasmRun := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	nat := mustRead(t, "../../../compiler/x86_native.fern")
	elf := mustRead(t, "../../../compiler/elf.fern")
	prelude := string(nat) + "\n" + string(elf) + toU8Src

	driverWat := runCapture(t, gcc, runner, wasmRun, []byte(prelude+x86CapstoneDriver))
	driverPath := filepath.Join(dir, "scale_driver.wat")
	if err := os.WriteFile(driverPath, driverWat, 0o644); err != nil {
		t.Fatalf("write driver wat: %v", err)
	}

	genProg := func(nfn, want int) string {
		var b strings.Builder
		for i := 0; i < nfn; i++ {
			fmt.Fprintf(&b, "function g%d(): i32 { return 1; }\n", i)
		}
		b.WriteString("function main(): i32 { let s: i32 = 0;")
		for i := 0; i < want; i++ {
			fmt.Fprintf(&b, " s = s + g%d();", i)
		}
		b.WriteString(" return s; }\n")
		return b.String()
	}

	cases := []struct{ nfn, want int }{
		{40, 40},
		{150, 150},
		{400, 200},
		{500, 250},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("nfn%d_want%d", tc.nfn, tc.want), func(t *testing.T) {
			prog := genProg(tc.nfn, tc.want)
			asmText := runCapture(t, gcc, runner, asmRun, []byte(prog))
			if len(asmText) == 0 {
				t.Fatal("asm.fern produced no assembly")
			}
			if err := os.WriteFile(filepath.Join(dir, "in.s"), asmText, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			t.Logf("nfn=%d want=%d asm=%dKB", tc.nfn, tc.want, len(asmText)/1024)

			bin, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", driverPath).Output()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					t.Fatalf("driver run: %v\n%s", err, ee.Stderr)
				}
				t.Fatalf("driver run: %v", err)
			}
			binPath := filepath.Join(dir, "scale.bin")
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write bin: %v", err)
			}
			got := runExit(t, binPath)

			refElf := filepath.Join(dir, "ref.bin")
			out, gerr := exec.Command(gcc, "-static", "-nostdlib", "-o", refElf, filepath.Join(dir, "in.s")).CombinedOutput()
			if gerr != nil {
				t.Logf("gcc assemble note: %v\n%s", gerr, out)
			}
			refGot := -1
			if gerr == nil {
				refGot = runExit(t, refElf)
			}
			t.Logf("self-host exit=%d  gcc-ref exit=%d  want=%d", got, refGot, tc.want)
			if got != tc.want {
				t.Errorf("self-host exit=%d, want %d (gcc-ref=%d)", got, tc.want, refGot)
			}
		})
	}
}

func runExit(t *testing.T, bin string) int {
	t.Helper()
	err := exec.Command(bin).Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	t.Fatalf("run %s: %v", bin, err)
	return -1
}
