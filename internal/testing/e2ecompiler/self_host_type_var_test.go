package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostTypeVar(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("type_var_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/type_var_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/type_var_run.fern", "type_var_run")

	const want = "declared: unknown=yes name=0_T reason=type parameter 0_T label=unknown\n" +
		"unresolved: unknown=yes name=0_Elem reason=unrecognised type name: 0_Elem label=unknown\n" +
		"not a variable spelling: unknown=yes name= reason=unrecognised type name: widget label=unknown\n" +
		"unchecked: unknown=yes name= reason=not yet checked label=unknown\n" +
		"erased: unknown=no name= reason= label=T\n" +
		"i32: unknown=no name= reason= label=i32\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("type_var_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("type variable mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
