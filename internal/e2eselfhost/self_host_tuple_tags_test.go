package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostTupleTags pins the self-hosted asmcore tuple type-tag decoders
// (examples/self_host/asmcore.fern's split_tuple_ret / tuple_ret_tag_at — SH-021
// slice 3, docs/SELF-HOST-AUDIT.md T2). Both map a tuple type STRING to the
// coarse asmcore Ty of its elements, now via the structured TypeRef
// (parser.parse_type_ref + ty_from_ref — element idx is args[idx]) instead of the
// former top-level-comma byte scan.
//
// The tuple_tags_run driver exercises both decoders' paths (2-element OK/first+
// second, nested-generic and nested-tuple elements, non-tuple / single-element
// fallbacks, the 3+-element first+rejoined-rest, every index + out-of-range) and
// prints ty_tag of each result. The golden below is the EXACT output the old byte
// scan produced (captured before the migration), so this is the byte-identical
// guard: a shifted decoded Ty fails here rather than mis-tagging a tuple
// destructure or a Result's OK type downstream.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostTupleTags(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("tuple_tags_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "tuple_tags_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "tuple_tags_run.fern", "tuple_tags_run")

	// Golden — the exact split_tuple_ret / tuple_ret_tag_at tag mapping the former
	// byte scan produced, captured before the TypeRef migration. Byte-identical.
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
