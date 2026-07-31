package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelfHostX86ScaleProbe guards the self-hosted assembler at scale:
// large (many-function) programs must assemble + run correctly, matching
// gcc's assembly of the same .s. It also pins the lesson behind a retracted
// "assembler-at-scale bug": the earlier nfn=400 "failure" (exit 144, want
// 400) was just the Unix 8-bit exit-code truncation (400 & 0xFF == 144),
// not a miscompile, and the "150-fn → 85" figure came from a malformed test
// program (f32/f64 reserved-keyword function names), not the assembler.
//
// To keep the signal honest this probe holds each program's result < 256 so
// the exit code is unmasked, and cross-checks every size against gcc. Up to
// 600 functions / ~124 KB asm the self-host output is exit-for-exit
// identical to gcc — there is no O(n²)-label or fixup defect at scale.
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
	for _, name := range []string{"astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asmRun := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "asm_run")
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	enc := mustRead(t, "../../examples/self_host/x86_encode.fern")
	gas := mustRead(t, "../../examples/self_host/x86_gas.fern")
	elf := mustRead(t, "../../examples/self_host/elf.fern")
	prelude := string(enc) + "\n" + string(gas) + "\n" + string(elf) + "\n"

	driverWat := runCapture(t, gcc, runner, wasmRun, []byte(prelude+x86CapstoneDriver))
	driverPath := filepath.Join(dir, "scale_driver.wat")
	if err := os.WriteFile(driverPath, driverWat, 0o644); err != nil {
		t.Fatalf("write driver wat: %v", err)
	}

	// genProg: nfn functions g0..g(nfn-1), g_i returns 1; main returns the
	// sum of the first `want` of them (so result == want, kept < 256).
	genProg := func(nfn, want int) string {
		var b strings.Builder
		for i := 0; i < nfn; i++ {
			fmt.Fprintf(&b, "function g%d(): i32 { return 1; }\n", i)
		}
		b.WriteString("function main(): i32 { var s: i32 = 0;")
		for i := 0; i < want; i++ {
			fmt.Fprintf(&b, " s = s + g%d();", i)
		}
		b.WriteString(" return s; }\n")
		return b.String()
	}

	// nfn stays UNDER the 512-function merged-bundle budget (#3425). asm_run.fern
	// routes IR-or-error now (#3457 slice 5), so an over-budget program is a hard
	// error there rather than a silent drop to the AST emitter — and the budget is
	// a property of the single-module merged path, not of the x86 encoder this test
	// probes. 500 exercises the same scale (a ~130 KB asm text) without changing
	// what is under test; a genuinely over-budget program is compiled by the
	// per-module path, and the budget refusal itself is pinned by
	// TestSelfHostStrictIRRefusesBail.
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

			// Self-host assembler path.
			bin, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", driverPath).Output()
			if err != nil {
				t.Fatalf("driver run: %v", err)
			}
			binPath := filepath.Join(dir, "scale.bin")
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write bin: %v", err)
			}
			got := runExit(t, binPath)

			// gcc reference of the same .s.
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
