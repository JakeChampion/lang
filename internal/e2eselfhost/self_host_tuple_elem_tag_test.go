package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTupleElemTag pins irlower's tuple element-tag decoder
// (examples/self_host/irlower.fern's tuple_type_elem_tag — SH-021,
// docs/SELF-HOST-AUDIT.md T2). It now extracts element n of a tuple type spelling
// "(t0, t1, …)" via the structured TypeRef (parser.parse_type_ref) instead of a
// hand-rolled depth-tracking top-level-comma scan.
//
// The tuple_elem_tag_run driver runs the REAL (imported) function against a frozen
// copy of the FORMER scan over a corpus spanning every branch — plain tuples,
// tuples with nested-generic / nested-tuple / array elements (whose inner commas
// the depth scan must not split on), single-element tuples, out-of-range and
// negative indices, non-tuples, and a tuple-array "(a, b)[]" — for each element
// index, and exits with the mismatch count. The golden below (all "ok",
// mismatches=0) is the byte-identical guard: a regression in the TypeRef decode
// (or in parse_type_ref feeding it) fails here rather than silently mis-reading a
// tuple field type.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostTupleElemTag(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("tuple_elem_tag_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{
		"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "ir.fern",
		"irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "tuple_elem_tag_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "tuple_elem_tag_run.fern", "tuple_elem_tag_run")

	const want = "ok  (i32, i32)[-1]=<empty>\n" +
		"ok  (i32, i32)[0]=i32\n" +
		"ok  (i32, i32)[1]=i32\n" +
		"ok  (i32, i32)[2]=<empty>\n" +
		"ok  (i32, i32)[3]=<empty>\n" +
		"ok  (i32, i32)[4]=<empty>\n" +
		"ok  (i32, string, boolean)[-1]=<empty>\n" +
		"ok  (i32, string, boolean)[0]=i32\n" +
		"ok  (i32, string, boolean)[1]=string\n" +
		"ok  (i32, string, boolean)[2]=boolean\n" +
		"ok  (i32, string, boolean)[3]=<empty>\n" +
		"ok  (i32, string, boolean)[4]=<empty>\n" +
		"ok  (Map[a, b], i32)[-1]=<empty>\n" +
		"ok  (Map[a, b], i32)[0]=Map[a, b]\n" +
		"ok  (Map[a, b], i32)[1]=i32\n" +
		"ok  (Map[a, b], i32)[2]=<empty>\n" +
		"ok  (Map[a, b], i32)[3]=<empty>\n" +
		"ok  (Map[a, b], i32)[4]=<empty>\n" +
		"ok  ((a, b), c)[-1]=<empty>\n" +
		"ok  ((a, b), c)[0]=(a, b)\n" +
		"ok  ((a, b), c)[1]=c\n" +
		"ok  ((a, b), c)[2]=<empty>\n" +
		"ok  ((a, b), c)[3]=<empty>\n" +
		"ok  ((a, b), c)[4]=<empty>\n" +
		"ok  (i32[], string)[-1]=<empty>\n" +
		"ok  (i32[], string)[0]=i32[]\n" +
		"ok  (i32[], string)[1]=string\n" +
		"ok  (i32[], string)[2]=<empty>\n" +
		"ok  (i32[], string)[3]=<empty>\n" +
		"ok  (i32[], string)[4]=<empty>\n" +
		"ok  (Option[i32], Result[i32, u32])[-1]=<empty>\n" +
		"ok  (Option[i32], Result[i32, u32])[0]=Option[i32]\n" +
		"ok  (Option[i32], Result[i32, u32])[1]=Result[i32, u32]\n" +
		"ok  (Option[i32], Result[i32, u32])[2]=<empty>\n" +
		"ok  (Option[i32], Result[i32, u32])[3]=<empty>\n" +
		"ok  (Option[i32], Result[i32, u32])[4]=<empty>\n" +
		"ok  (x)[-1]=<empty>\n" +
		"ok  (x)[0]=x\n" +
		"ok  (x)[1]=<empty>\n" +
		"ok  (x)[2]=<empty>\n" +
		"ok  (x)[3]=<empty>\n" +
		"ok  (x)[4]=<empty>\n" +
		"ok  (i32, i32)[][-1]=<empty>\n" +
		"ok  (i32, i32)[][0]=<empty>\n" +
		"ok  (i32, i32)[][1]=<empty>\n" +
		"ok  (i32, i32)[][2]=<empty>\n" +
		"ok  (i32, i32)[][3]=<empty>\n" +
		"ok  (i32, i32)[][4]=<empty>\n" +
		"ok  ()[-1]=<empty>\n" +
		"ok  ()[0]=<empty>\n" +
		"ok  ()[1]=<empty>\n" +
		"ok  ()[2]=<empty>\n" +
		"ok  ()[3]=<empty>\n" +
		"ok  ()[4]=<empty>\n" +
		"ok  i32[-1]=<empty>\n" +
		"ok  i32[0]=<empty>\n" +
		"ok  i32[1]=<empty>\n" +
		"ok  i32[2]=<empty>\n" +
		"ok  i32[3]=<empty>\n" +
		"ok  i32[4]=<empty>\n" +
		"ok  Map[a, b][-1]=<empty>\n" +
		"ok  Map[a, b][0]=<empty>\n" +
		"ok  Map[a, b][1]=<empty>\n" +
		"ok  Map[a, b][2]=<empty>\n" +
		"ok  Map[a, b][3]=<empty>\n" +
		"ok  Map[a, b][4]=<empty>\n" +
		"ok  Option[i32][-1]=<empty>\n" +
		"ok  Option[i32][0]=<empty>\n" +
		"ok  Option[i32][1]=<empty>\n" +
		"ok  Option[i32][2]=<empty>\n" +
		"ok  Option[i32][3]=<empty>\n" +
		"ok  Option[i32][4]=<empty>\n" +
		"ok  <empty>[-1]=<empty>\n" +
		"ok  <empty>[0]=<empty>\n" +
		"ok  <empty>[1]=<empty>\n" +
		"ok  <empty>[2]=<empty>\n" +
		"ok  <empty>[3]=<empty>\n" +
		"ok  <empty>[4]=<empty>\n" +
		"ok  (a, (b, c), d)[-1]=<empty>\n" +
		"ok  (a, (b, c), d)[0]=a\n" +
		"ok  (a, (b, c), d)[1]=(b, c)\n" +
		"ok  (a, (b, c), d)[2]=d\n" +
		"ok  (a, (b, c), d)[3]=<empty>\n" +
		"ok  (a, (b, c), d)[4]=<empty>\n" +
		"mismatches=0\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("tuple_elem_tag_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("tuple elem-tag decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("tuple_elem_tag_run exit code = %d, want 0 (byte-identity mismatches)", code)
	}
}
