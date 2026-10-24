package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

var compoundBitwiseIRCases = []struct {
	name string
	main string
}{
	{"and-eq", `function main(): i32 { let x = 12; x &= 6; return x; }`},
	{"or-eq", `function main(): i32 { let x = 8; x |= 5; return x; }`},
	{"xor-eq", `function main(): i32 { let x = 12; x ^= 10; return x; }`},
	{"shl-eq", `function main(): i32 { let x = 3; x <<= 4; return x; }`},
	{"shr-eq", `function main(): i32 { let x = 100; x >>= 2; return x; }`},
	{"shl-eq-in-loop", `function main(): i32 { let x = 1; let i = 0; while (i < 5) { x <<= 1; i += 1; } return x; }`},
	{"and-eq-mask-loop", `function main(): i32 { let acc = 0; let i = 0; while (i < 8) { acc |= i; i += 1; } acc &= 7; return acc; }`},
	{"add-eq-regress", `function main(): i32 { let x = 10; x += 5; return x; }`},
}

func TestSelfHostCompoundBitwiseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range compoundBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestNativeCompoundBitwiseX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	for _, tc := range compoundBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main+"\n")
			_, code := compileAndRunX86_64(t, tc.main+"\n")
			if code != want {
				t.Errorf("%s native exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
