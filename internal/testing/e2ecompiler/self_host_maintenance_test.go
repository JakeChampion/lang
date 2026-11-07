package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const maintenanceSrc = `function main(): i32 {
  unsafe {
    let p = __alloc(64);
    __cache_clean(p);
    __cache_flush(p);
    __tlb_flush(p);
    __tlb_flush_all();
  }
  return 0;
}
`

func TestSelfHostMaintenanceOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wantInOrder(t, "arm64-kernel", emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, maintenanceSrc), []string{
		"    dc cvac, x9\n",
		"    dc civac, x9\n",
		"    dsb ishst\n    lsr x9, x9, #12\n    tlbi vaae1is, x9\n    dsb ish\n    isb\n",
		"    dsb ishst\n    tlbi vmalle1is\n    dsb ish\n    isb\n",
	})
	wantInOrder(t, "x86-64-kernel", emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, maintenanceSrc), []string{
		"    clflush (%rax)\n",
		"    clflush (%rax)\n",
		"    invlpg (%rax)\n",
		"    movq %cr3, %rax\n    movq %rax, %cr3\n",
	})
}

func TestSelfHostMaintenanceNeedsAKernel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(maintenanceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		msg, err := cli.compileCmd(target, filepath.Join(dir, "out-"+target), src).CombinedOutput()
		for _, op := range []string{"__cache_clean", "__cache_flush", "__tlb_flush", "__tlb_flush_all"} {
			if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), op) {
				t.Errorf("%s: err %v; want E066 naming %s in:\n%s", target, err, op, msg)
			}
		}
	}
}

func TestSelfHostMaintenanceArm64MatchesGas(t *testing.T) {
	compareArm64Cases(t, []string{
		"dc cvac, x9", "dc cvau, x3", "dc civac, x0", "ic ivau, x1", "ic iallu",
		"tlbi vmalle1", "tlbi vmalle1is", "tlbi vaae1, x2", "tlbi vaae1is, x9",
	})
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	for _, c := range []string{"dc cvac", "dc cvac, w9", "tlbi vmalle1is, x0", "tlbi vaae1is", "ic ivau, x1, x2", "dc zva2, x0"} {
		if len(refusalsFor(t, bin, runner, ".text\n_start:\n    "+c+"\n")) == 0 {
			t.Errorf("%q: the self-host assembler accepts it", c)
		}
	}
}

func TestSelfHostMaintenanceX86MatchesGas(t *testing.T) {
	compareFormCases(t, []string{
		"clflush (%rax)", "clflush 8(%r12)", "clflush (%rsp)", "invlpg (%rdi)", "invlpg -16(%r9,%rcx,4)", "invlpg (%r13)",
	})
}
