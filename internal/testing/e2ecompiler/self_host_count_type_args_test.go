package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostCountTypeArgs(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("count_type_args_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/count_type_args_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/count_type_args_run.fern", "count_type_args_run")

	const want = "Box[i32] -> 1\n" +
		"Map[K, V] -> 2\n" +
		"Triple[a, b, c] -> 3\n" +
		"Pair[Map[a, b], c] -> 2\n" +
		"Wrap[(a, b)] -> 1\n" +
		"Nest[Box[Box[i32]]] -> 1\n" +
		"Foo -> -1\n" +
		"i32 -> -1\n" +
		"(a, b) -> -1\n" +
		"(Map[a, b], c) -> -1\n" +
		"Box[i32][] -> -1\n" +
		"i32[] -> -1\n" +
		"Foo[] -> -1\n" +
		"Map[K, V][] -> -1\n" +
		"<empty> -> -1\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("count_type_args_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("count_type_args decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
