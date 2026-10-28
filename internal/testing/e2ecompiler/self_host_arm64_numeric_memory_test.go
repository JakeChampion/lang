package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostArm64NumericLabelMemory(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the memory regression requires a native executable")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_fuzz_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_fuzz_run.fern", "asm_fuzz_run")
	input := ".text\n134217728:\n b 134217728b\n"
	path := mustWrite(t, dir, "large-label.s.txt", input)
	r := runUnderBudget(exec.Command(driver, "arm64", path))
	if failOutsideBudget(t, "large numeric label", r, []byte(input)) {
		return
	}
	if r.code != 0 || r.stdout != "arm64 code_end -1 data 0\n" {
		t.Fatalf("large numeric label: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestSelfHostArm64NumericLabelsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := arm64NativeSrc(t) + "\n" + arm64NumericLabelSelfTestMain
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, source, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("numeric labels: exit %d\n%s", code, stderr)
			}
		})
	}
}
