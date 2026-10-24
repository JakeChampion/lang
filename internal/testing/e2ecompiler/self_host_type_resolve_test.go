package e2ecompiler

import (
	"testing"
)

func TestSelfHostTypeResolve(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/type_resolve_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/type_resolve_run.fern", "type_resolve_run")

	const want = "i32 => i32\n" +
		"i64 => i64\n" +
		"u32 => u32\n" +
		"u64 => u64\n" +
		"bool => unknown(unrecognised type name: bool)\n" +
		"boolean => bool\n" +
		"string => string\n" +
		"f64 => f64\n" +
		"f32 => f32\n" +
		"Foo => struct:Foo\n" +
		"Bar => struct:Bar\n" +
		"Shape => union:Shape\n" +
		"Baz => unknown(unrecognised type name: Baz)\n" +
		"i32[] => array<i32>\n" +
		"string[] => array<string>\n" +
		"Foo[] => array<struct:Foo>\n" +
		"Shape[] => array<union:Shape>\n" +
		"i32[][] => array<array<i32>>\n" +
		"Foo[][] => array<array<struct:Foo>>\n" +
		"(i32, string) => tuple<i32, string>\n" +
		"(Foo, Shape) => tuple<struct:Foo, union:Shape>\n" +
		"(i32, string, boolean) => tuple<i32, string, bool>\n" +
		"(string, (i32, boolean)) => tuple<string, tuple<i32, bool>>\n" +
		"(i32) => unknown(unrecognised type name: (i32))\n" +
		"() => unknown(unrecognised type name: ())\n" +
		"Map[string, i32] => map<string, i32>\n" +
		"Map[i32, Foo] => map<i32, struct:Foo>\n" +
		"Map[string, Map[i32, string]] => map<string, map<i32, string>>\n" +
		"Map[string, Foo[]] => map<string, array<struct:Foo>>\n" +
		"Map[Foo, Shape] => map<struct:Foo, union:Shape>\n" +
		"Map[string] => unknown(unrecognised type name: Map[string])\n" +
		"Map[string, i32][] => array<map<string, i32>>\n" +
		"(i32, string)[] => array<tuple<i32, string>>\n" +
		"Option[i32] => union:Option\n" +
		"Vec[T] => union:Vec\n" +
		"Foo[T] => struct:Foo\n" +
		"Bogus[] => array<unknown(unrecognised type name: Bogus)>\n" +
		"mod.Thing => unknown(unrecognised type name: mod.Thing)\n" +
		" => unknown(unrecognised type name: )\n" +
		"callable () => i32[] => fn<array<i32>>\n" +
		"callable ((i32) => string[][]) => fn<array<array<string>>>\n" +
		"callable ((i32) => string)[] => array<fn<string>>\n" +
		"callable ((((i32) => string[][]))) => fn<array<array<string>>>\n" +
		"callable () => () => i32[] => fn<fn<array<i32>>>\n" +
		"callable () => (() => i32[])[] => fn<array<fn<array<i32>>>>\n" +
		"callable fn => fn<unknown(fn result (the coarse tag records none))>\n"

	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("type_resolve_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("checker type resolve mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("type_resolve_run exit code = %d, want 0", code)
	}
}
