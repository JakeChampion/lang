package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSSALiftAdmitsEveryOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_lift_admits_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_lift_admits_run.fern", "ssa_lift_admits_run")

	// Each IR kind is registered, so the table of kind ids gives the count.
	ids, err := os.ReadFile(filepath.Join("testdata", "ir-kind-ids.txt"))
	if err != nil {
		t.Fatal(err)
	}
	registered := len(strings.Split(strings.TrimSpace(string(ids)), "\n"))

	want := "load pops=-1 pushes=1\n" +
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
		fmt.Sprintf("registered=%d declined=3\n", registered)

	cmd := runX86_64Bin(runner, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ssa_lift_admits_run: %v\n%s", err, out)
	}
	if got := string(out); got != want {
		t.Errorf("lift admission census mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
