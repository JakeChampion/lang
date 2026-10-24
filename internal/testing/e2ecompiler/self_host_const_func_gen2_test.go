package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostConstFuncGen2(t *testing.T) {
	if os.Getenv("RUN_CONST_FUNC_GEN2") == "" {
		t.Skip("set RUN_CONST_FUNC_GEN2=1 to run the two-generation const_func eligibility check")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("two-generation test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")

	selfSrc := filepath.Join(dir, "drivers/asm_load_run.fern")
	mmc1 := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "cfg_mmc1")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	stage2Asm, err := exec.Command(mmc1, selfSrc, stdlibRoot).Output()
	if err != nil {
		t.Fatalf("mmc1 compile self failed: %v", err)
	}
	if !strings.Contains(string(stage2Asm), ".Lssa_") {
		t.Fatal("stage-2 asm has no .Lssa_ labels — gen 2 is not IR-built, so the merged bundle is being refused again")
	}
	mmc2 := buildBin(t, gcc, dir, "cfg_mmc2", string(stage2Asm))

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"fn-value", "function work(): void { let i: i32 = 0; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work); }\n"},
		{"two-fn-values", "function work(): void { let i: i32 = 0; }\n" +
			"function work2(): void { let j: i32 = 1; }\n" +
			"function run(f: () => void): i32 { f(); return 0; }\n" +
			"function main(): i32 { return run(work) + run(work2); }\n"},
		{"control-direct-call", "function work(): void { let i: i32 = 0; }\n" +
			"function main(): i32 { work(); return 0; }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prog := filepath.Join(dir, "cfg_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			n1 := lirLabelCount(t, mmc1, prog)
			n2 := lirLabelCount(t, mmc2, prog)
			if n1 == 0 {
				t.Fatalf("gen 1 emitted no .Lssa_ labels — the program did not take the IR path at all")
			}
			if n1 != n2 {
				t.Errorf("gen 1 emitted %d .Lssa_ labels, gen 2 emitted %d: the IR-built compiler disagrees about IR eligibility (#5649)", n1, n2)
			}
		})
	}
}

func lirLabelCount(t *testing.T, compiler, prog string) int {
	t.Helper()
	asm, err := exec.Command(compiler, prog).Output()
	if err != nil {
		t.Fatalf("%s %s: %v", filepath.Base(compiler), filepath.Base(prog), err)
	}
	if len(asm) == 0 {
		t.Fatalf("%s emitted 0 bytes for %s", filepath.Base(compiler), filepath.Base(prog))
	}
	return strings.Count(string(asm), ".Lssa_")
}
