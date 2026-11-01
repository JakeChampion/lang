package e2ecompiler

import "testing"

func TestSelfHostSSATrivialPhiVoidReturn(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_void_phi_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_void_phi_run.fern", "ssa_void_phi")
	if output, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("trivial phi with a void return: %v\n%s", err, output)
	}
}
