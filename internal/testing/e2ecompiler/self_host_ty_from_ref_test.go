package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostTyFromRef(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ty_from_ref_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ty_from_ref_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ty_from_ref_run.fern", "ty_from_ref_run")

	const want = "i32 => i32\n" +
		"u32 => u32\n" +
		"u64 => u64\n" +
		"i64 => i64\n" +
		"usize => usize\n" +
		"f64 => f64\n" +
		"f32 => f64\n" +
		"bool => bool\n" +
		"boolean => bool\n" +
		"string => string\n" +
		"i32[] => array_i32\n" +
		"u32[] => array_u32\n" +
		"u64[] => array_u64\n" +
		"i64[] => array_i64\n" +
		"string[] => array_string\n" +
		"bool[] => array_i32\n" +
		"boolean[] => array_i32\n" +
		"f64[] => array\n" +
		"i32[][] => array\n" +
		"string[][] => array\n" +
		"Foo[] => array\n" +
		"Option[i32][] => array\n" +
		"Map[string, i32][] => array\n" +
		"Map[string, i32] => map:i32\n" +
		"Map[i32, string] => mapI:string\n" +
		"Map[i32, Option[i32]] => mapI:option:i32\n" +
		"Map[string, Map[i32, string]] => map:mapI:string\n" +
		"Map => map:unknown\n" +
		"MapConfig => unknown\n" +
		"Map[string] => map:unknown\n" +
		"MapIter[i32, string] => mapiter:string\n" +
		"MapIter[string] => mapiter:unknown\n" +
		"Cell[i32] => cell:i32\n" +
		"Cell[string] => cell:string\n" +
		"Option[i32] => option:i32\n" +
		"Option[string] => option:string\n" +
		"Option[Map[string, i32]] => option:map:i32\n" +
		"Option[(string, string)] => option:unknown\n" +
		"Result[string, Err] => result:string\n" +
		"Result[i32, string] => result:i32\n" +
		"Result[Foo] => result:i32\n" +
		"Vec[T] => array\n" +
		"Foo[A, B] => array\n" +
		"Bar[i32] => array\n" +
		"(i32, string) => unknown\n" +
		"(string, (i32, bool)) => unknown\n" +
		"Foo => unknown\n" +
		"mod.Bar => unknown\n" +
		"TestRunner => unknown\n" +
		" => unknown\n" +
		"dyn Shape => unknown\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ty_from_ref_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("ty_from_name decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ty_from_ref_run exit code = %d, want 0", code)
	}
}
