package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostTypeResolveSimple pins the self-hosted checker's three simpler
// type-name resolvers (examples/self_host/checker.fern's
// type_from_name_with_structs / _with_struct_names / _with_names_and_unions —
// docs/SELF-HOST-AUDIT.md T2). All three share the one resolution ladder
// (type_from_ref_names) with the richest resolver, so they differ from it only
// in the name context each carries: the two struct-only ones cannot name a
// union, and none of them substitutes a type parameter. A tuple, a Map, a fn
// value and a generic instantiation resolve in every one of them — they used to
// collapse to unknown by their full spelling here, which is how a struct field
// declared `(i32, f64)[]` reached irlower as `unknown[]` and had its f64
// element read at 4 bytes (#8459).
//
// The type_resolve_simple_run driver resolves a corpus through all three and
// prints type_debug of each result.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostTypeResolveSimple(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("type_resolve_simple_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "type_resolve_simple_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "type_resolve_simple_run.fern", "type_resolve_simple_run")

	const want = "i32 => structs=i32 names=i32 names+unions=i32\n" +
		"i64 => structs=i64 names=i64 names+unions=i64\n" +
		"u32 => structs=u32 names=u32 names+unions=u32\n" +
		"u64 => structs=u64 names=u64 names+unions=u64\n" +
		"bool => structs=unknown(unrecognised type name: bool) names=unknown(unrecognised type name: bool) names+unions=unknown(unrecognised type name: bool)\n" +
		"boolean => structs=bool names=bool names+unions=bool\n" +
		"string => structs=string names=string names+unions=string\n" +
		"f64 => structs=f64 names=f64 names+unions=f64\n" +
		"f32 => structs=f64 names=f64 names+unions=f64\n" +
		"float => structs=f64 names=f64 names+unions=f64\n" +
		"Foo => structs=struct:Foo names=struct:Foo names+unions=struct:Foo\n" +
		"Bar => structs=struct:Bar names=struct:Bar names+unions=struct:Bar\n" +
		"Shape => structs=unknown(unrecognised type name: Shape) names=unknown(unrecognised type name: Shape) names+unions=union:Shape\n" +
		"Baz => structs=unknown(unrecognised type name: Baz) names=unknown(unrecognised type name: Baz) names+unions=unknown(unrecognised type name: Baz)\n" +
		"i32[] => structs=array<i32> names=array<i32> names+unions=array<i32>\n" +
		"string[] => structs=array<string> names=array<string> names+unions=array<string>\n" +
		"Foo[] => structs=array<struct:Foo> names=array<struct:Foo> names+unions=array<struct:Foo>\n" +
		"Shape[] => structs=array<unknown(unrecognised type name: Shape)> names=array<unknown(unrecognised type name: Shape)> names+unions=array<union:Shape>\n" +
		"i32[][] => structs=array<array<i32>> names=array<array<i32>> names+unions=array<array<i32>>\n" +
		"Foo[][] => structs=array<array<struct:Foo>> names=array<array<struct:Foo>> names+unions=array<array<struct:Foo>>\n" +
		"Bar[][][] => structs=array<array<array<struct:Bar>>> names=array<array<array<struct:Bar>>> names+unions=array<array<array<struct:Bar>>>\n" +
		"(i32, string) => structs=tuple<i32, string> names=tuple<i32, string> names+unions=tuple<i32, string>\n" +
		"Map[string, i32] => structs=map<string, i32> names=map<string, i32> names+unions=map<string, i32>\n" +
		"Option[i32] => structs=union:Option names=union:Option names+unions=union:Option\n" +
		"Vec[T] => structs=union:Vec names=union:Vec names+unions=union:Vec\n" +
		"(i32, string)[] => structs=array<tuple<i32, string>> names=array<tuple<i32, string>> names+unions=array<tuple<i32, string>>\n" +
		"Map[string, i32][] => structs=array<map<string, i32>> names=array<map<string, i32>> names+unions=array<map<string, i32>>\n" +
		"Bogus[] => structs=array<unknown(unrecognised type name: Bogus)> names=array<unknown(unrecognised type name: Bogus)> names+unions=array<unknown(unrecognised type name: Bogus)>\n" +
		"mod.Thing => structs=unknown(unrecognised type name: mod.Thing) names=unknown(unrecognised type name: mod.Thing) names+unions=unknown(unrecognised type name: mod.Thing)\n" +
		" => structs=unknown(unrecognised type name: ) names=unknown(unrecognised type name: ) names+unions=unknown(unrecognised type name: )\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("type_resolve_simple_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("simple type resolve mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("type_resolve_simple_run exit code = %d, want 0", code)
	}
}
