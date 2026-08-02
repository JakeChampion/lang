package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostTypeResolve pins the self-hosted checker's structured type-name
// resolver (examples/self_host/checker.fern's type_from_name_with_structs_unions
// / type_from_ref_su — SH-021 slice 4, docs/SELF-HOST-AUDIT.md T2). The resolver
// maps a type STRING to the checker's Type against a struct/union context; it now
// decodes via the structured TypeRef (parser.parse_type_ref + pattern-match)
// instead of the former array-suffix / tuple / `Map[` first-comma byte scans.
//
// The type_resolve_run driver resolves a corpus spanning every branch (scalars,
// struct / union / unknown names, multi-depth arrays of struct/union/unknown
// elements, tuples incl. nested + single-element/empty fallthrough, Map incl.
// nested value + single-arg fallthrough, array-of-map / array-of-tuple, non-Map
// generics, qualified name) against fixed fixtures, printing type_debug of each.
// The golden below is the EXACT output the old byte scan produced (captured
// before the migration), so this is the byte-identical guard: a shifted resolved
// Type — or a differing `unrecognised type name` reason — fails here rather than
// silently mis-typing a param / field / return in the checker.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostTypeResolve(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("type_resolve_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "type_resolve_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "type_resolve_run.fern", "type_resolve_run")

	// Golden — the exact type_from_name_with_structs_unions resolution the former
	// byte scan produced, captured before the TypeRef migration. Byte-identical.
	const want = "i32 => i32\n" +
		"i64 => i64\n" +
		"u32 => u32\n" +
		"u64 => u64\n" +
		"bool => bool\n" +
		"boolean => bool\n" +
		"string => string\n" +
		"f64 => f64\n" +
		"f32 => f64\n" +
		"float => f64\n" +
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
		"(i32, string, bool) => tuple<i32, string, bool>\n" +
		"(string, (i32, bool)) => tuple<string, tuple<i32, bool>>\n" +
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
		// Option[i32] resolves since the self-host checker learned to
		// instantiate a generic enum's type args (#4346 piece 2) —
		// updated from the pre-#4913 `unknown(...)` capture.
		"Option[i32] => union:Option\n" +
		// Vec[T] (an UNDECLARED generic base) resolves to a name-only
		// union since the E023 port: the Go checker's resolveType keeps
		// the shape as an unknown EnumType, and matching on it draws
		// E023 — the self-host mirrors that with a union whose
		// lookup_union misses. A DECLARED generic-struct base (Foo[T])
		// now resolves NAME-ONLY to that struct (#4346 piece 2, generic-
		// struct instantiations) — dropping the args, exactly as a bare
		// `Foo` does and as `Option[i32] => union:Option` does above.
		"Vec[T] => union:Vec\n" +
		"Foo[T] => struct:Foo\n" +
		"Bogus[] => array<unknown(unrecognised type name: Bogus)>\n" +
		"mod.Thing => unknown(unrecognised type name: mod.Thing)\n" +
		" => unknown(unrecognised type name: )\n"

	cmd := exec.Command(bin)
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
