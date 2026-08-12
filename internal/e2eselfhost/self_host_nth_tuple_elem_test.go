package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostNthTupleElem pins the wasm backend's tuple-element decoder
// (examples/self_host/wasm_ir.fern's nth_tuple_type_elem — SH-021,
// docs/SELF-HOST-AUDIT.md T2). It returns the idx-th element type of a tuple
// spelling "(A, B, …)", or "" when the spelling isn't a tuple / idx is out of
// range, and now decodes via the structured TypeRef (parser.parse_type_ref)
// instead of a hand-rolled bracket/paren depth scan.
//
// The corpus × indices span plain tuples, tuples with nested-generic / nested-
// tuple elements (whose inner commas the scan must not split on), non-tuples, and
// a tuple-array "(i32, i32)[]". On every non-array spelling the result matches the
// former scan exactly; the tuple-array row resolves to "" (array_depth > 0 is a
// value of array type, not a tuple) — the former scan keyed only off a leading
// "(" and mis-read the trailing "[]", wrongly reporting (i32, i32)[] as a flat
// extern tuple param. The three x86 self-compile fixpoints confirm no such
// tuple-array spelling reaches this on the self-host sources, so the extern-param
// codegen is unchanged.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostNthTupleElem(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("nth_tuple_elem_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "astwalk.fern", "ir.fern", "parser.fern",
		"asmcore.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "nth_tuple_elem_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "nth_tuple_elem_run.fern", "nth_tuple_elem_run")

	const want = "(i32, i32)[0]=i32\n" +
		"(i32, i32)[1]=i32\n" +
		"(i32, i32)[2]=<empty>\n" +
		"(i32, i32)[3]=<empty>\n" +
		"(i32, u32, string)[0]=i32\n" +
		"(i32, u32, string)[1]=u32\n" +
		"(i32, u32, string)[2]=string\n" +
		"(i32, u32, string)[3]=<empty>\n" +
		"(Map[K, V], c)[0]=Map[K, V]\n" +
		"(Map[K, V], c)[1]=c\n" +
		"(Map[K, V], c)[2]=<empty>\n" +
		"(Map[K, V], c)[3]=<empty>\n" +
		"((a, b), c)[0]=(a, b)\n" +
		"((a, b), c)[1]=c\n" +
		"((a, b), c)[2]=<empty>\n" +
		"((a, b), c)[3]=<empty>\n" +
		"(Option[i32], Result[i32, u32])[0]=Option[i32]\n" +
		"(Option[i32], Result[i32, u32])[1]=Result[i32, u32]\n" +
		"(Option[i32], Result[i32, u32])[2]=<empty>\n" +
		"(Option[i32], Result[i32, u32])[3]=<empty>\n" +
		"(x)[0]=x\n" +
		"(x)[1]=<empty>\n" +
		"(x)[2]=<empty>\n" +
		"(x)[3]=<empty>\n" +
		"(i32, i32)[][0]=<empty>\n" +
		"(i32, i32)[][1]=<empty>\n" +
		"(i32, i32)[][2]=<empty>\n" +
		"(i32, i32)[][3]=<empty>\n" +
		"i32[0]=<empty>\n" +
		"i32[1]=<empty>\n" +
		"i32[2]=<empty>\n" +
		"i32[3]=<empty>\n" +
		"Map[a, b][0]=<empty>\n" +
		"Map[a, b][1]=<empty>\n" +
		"Map[a, b][2]=<empty>\n" +
		"Map[a, b][3]=<empty>\n" +
		"<empty>[0]=<empty>\n" +
		"<empty>[1]=<empty>\n" +
		"<empty>[2]=<empty>\n" +
		"<empty>[3]=<empty>\n" +
		"(a, (b, c), d)[0]=a\n" +
		"(a, (b, c), d)[1]=(b, c)\n" +
		"(a, (b, c), d)[2]=d\n" +
		"(a, (b, c), d)[3]=<empty>\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("nth_tuple_elem_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("nth_tuple_type_elem decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
