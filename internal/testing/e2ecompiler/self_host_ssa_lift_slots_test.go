package e2ecompiler

import (
	"testing"
)

// A read of a slot whose offset does not fit in an i32 is a refusal, not a
// load at a wrapped offset or another kind of instruction.
func TestSelfHostSSALiftRefusesUnplacedSlot(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_lift_slots_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_lift_slots_run.fern", "ssa_lift_slots_run")

	want := ""
	for _, kind := range []string{"struct_get", "tuple_get"} {
		want += kind + " 0 load at 0\n" +
			kind + " 1 load at 8\n" +
			kind + " 268435455 load at 2147483640\n" +
			kind + " 268435456 refused\n" +
			kind + " 2147483647 refused\n" +
			kind + " -1 refused\n"
	}

	out, err := runX86_64Bin(runner, bin).Output()
	if err != nil {
		t.Fatalf("ssa_lift_slots_run: %v\n%s", err, out)
	}
	if got := string(out); got != want {
		t.Errorf("slot lift mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
