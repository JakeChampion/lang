package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostCountTypeArgs pins the checker's generic-arity counter
// (examples/self_host/checker.fern's count_type_args — SH-021,
// docs/SELF-HOST-AUDIT.md T2). It returns how many top-level type args a
// `Name[A, B, …]` annotation supplies to its head generic, or -1 when the
// annotation is not a top-level generic instantiation. It now decodes via the
// structured TypeRef (parser.parse_type_ref, through the checker) instead of a
// first-`[` + trailing-`]` window with a depth-tracking top-level-comma count.
//
// The corpus covers plain generics of 1/2/3 args, nested generics and tuples
// whose inner commas must not be counted at top level, bare names, tuples, and
// arrays. On every non-array annotation the count matches the former scan
// exactly; arrays/tuples resolve to -1 (not a generic head — the former scan
// returned a garbage count on a trailing `[]`, but that value only ever fed the
// E019 struct-arity check on a struct's own generic head, never an array, so the
// arity diagnostics are unchanged, which the three x86 self-compile fixpoints
// confirm).
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostCountTypeArgs(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("count_type_args_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "count_type_args_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "count_type_args_run.fern", "count_type_args_run")

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
