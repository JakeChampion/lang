package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostTupleTags(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("tuple_tags_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/tuple_tags_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/tuple_tags_run.fern", "tuple_tags_run")

	const want = "split((i32, string)) = {i32, string}\n" +
		"split((string, i32)) = {string, i32}\n" +
		"split((Map[string, i32], Err)) = {map:i32, unknown}\n" +
		"split((i32, Option[string])) = {i32, option:string}\n" +
		"split((string, (i32, bool))) = {string, unknown}\n" +
		"split(i32) = {i32, i32}\n" +
		"split((i32)) = {i32, i32}\n" +
		"split() = {i32, i32}\n" +
		"split((i32, string, bool)) = {i32, unknown}\n" +
		"at((i32, string, bool),0) = i32\n" +
		"at((i32, string, bool),1) = string\n" +
		"at((i32, string, bool),2) = bool\n" +
		"at((i32, string, bool),3) = i32\n" +
		"at((Map[a, b], Option[i32]),0) = map:unknown\n" +
		"at((Map[a, b], Option[i32]),1) = option:i32\n" +
		"at((string),0) = string\n" +
		"at((string),1) = i32\n" +
		"at(i32,0) = i32\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("tuple_tags_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("tuple tag decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("tuple_tags_run exit code = %d, want 0", code)
	}
}
