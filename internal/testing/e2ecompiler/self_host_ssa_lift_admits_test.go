package e2ecompiler

import "testing"

func TestSelfHostSSALiftAdmitsEveryOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_lift_admits_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_lift_admits_run.fern", "ssa_lift_admits_run")

	const want = "load pops=-1 pushes=1\n" +
		"store pops=-1 pushes=1\n" +
		"call_closure_direct pops=-1 pushes=1\n" +
		"admitted mmio_load pops=1 pushes=1\n" +
		"admitted mmio_store pops=2 pushes=1\n" +
		"admitted fence pops=0 pushes=1\n" +
		"admitted heap_adopt pops=2 pushes=1\n" +
		"admitted worker_region pops=1 pushes=1\n" +
		"admitted worker_exit pops=2 pushes=1\n" +
		"admitted worker_reap pops=1 pushes=1\n" +
		"admitted send_adopt pops=1 pushes=1\n" +
		"admitted worker_unmap pops=3 pushes=1\n" +
		"admitted send_root pops=1 pushes=1\n" +
		"registered=448 declined=3\n"

	cmd := runX86_64Bin(runner, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ssa_lift_admits_run: %v\n%s", err, out)
	}
	if got := string(out); got != want {
		t.Errorf("lift admission census mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
