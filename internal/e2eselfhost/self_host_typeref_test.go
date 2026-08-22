package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostTypeRef exercises the self-hosted parser's structured type
// reference (examples/self_host/parser.fern's TypeRef / parse_type_ref /
// render_type_ref — the SH-021 foundation slice that unblocks #4394 lever 1,
// replacing the dozen ad-hoc byte-scan type-string decoders with one structured
// tree the consumers can pattern-match on).
//
// The typeref_run driver round-trips a corpus covering every shape the type-
// string grammar produces (scalars, named + qualified names, multi-depth arrays,
// nested generics, nested tuples, tuples inside generics, arrays of tuples, and
// the opaque `dyn` spelling), asserting render_type_ref(parse_type_ref(s)) == s
// for each, then spot-checks the decoded STRUCTURE (base / arg-count /
// array_depth / is_tuple) so a tree that round-trips but decodes wrongly is
// still caught. Function types need that structure half more than any other
// shape: an unrecognised spelling survives whole as `base` and renders back
// byte-identical, so `(A, B) => C` round-trips just as happily when it is not
// understood at all — only the arg count and `is_fn` separate the two.
//
// It prints a deterministic report and exits with the round-trip failure count
// (0 on a clean sweep). This pins the parse/render contract end-
// to-end through the self-host -> native pipeline (proving it compiles + round-
// trips, not just type-checks); a byte-scan regression fails the golden here.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the
// report and its exit code is the round-trip failure count.
func TestSelfHostTypeRef(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("typeref_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "typeref_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "typeref_run.fern", "typeref_run")

	// Golden report — locks the round-trip of every corpus spelling and the
	// decoded structure of the representative nested/tuple/array cases.
	const want = "ok   i32\n" +
		"ok   u32\n" +
		"ok   string\n" +
		"ok   bool\n" +
		"ok   f64\n" +
		"ok   Foo\n" +
		"ok   mod.Bar\n" +
		"ok   i32[]\n" +
		"ok   string[][]\n" +
		"ok   Foo[][][]\n" +
		"ok   Option[i32]\n" +
		"ok   Map[string, i32]\n" +
		"ok   Result[string, Err]\n" +
		"ok   Map[string, Option[i32]]\n" +
		"ok   Map[i32, Vec[string]][]\n" +
		"ok   Vec[T]\n" +
		"ok   (i32, string)\n" +
		"ok   (string, (i32, bool))\n" +
		"ok   Option[(string, string)]\n" +
		"ok   (i32, string)[]\n" +
		"ok   dyn Shape\n" +
		"ok   dyn Shape[]\n" +
		"ok   () => P\n" +
		"ok   (i32) => i32\n" +
		"ok   (T, T) => i32\n" +
		"ok   (T) => U[]\n" +
		"ok   (parser.Expr, T) => T\n" +
		"ok   (() => T) => U\n" +
		"ok   (i32) => (string, i32)\n" +
		"ok   Vec[(T) => boolean]\n" +
		"struct Map base=Map args=2 depth=0 tuple=0\n" +
		"struct Map.arg1 base=Option args=1\n" +
		"struct tuparr base= args=2 depth=1 tuple=1\n" +
		"struct arr3 base=Foo args=0 depth=3\n" +
		"struct fn2 base= args=3 depth=0 tuple=0 fn=1 ret=i32\n" +
		"struct fn0 args=1 fn=1 ret=P\n" +
		"struct fnho args=2 fn=1 p0fn=1 ret=U\n" +
		"round_trip_failures=0\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("typeref_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("typeref report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Exit code is the round-trip failure count — 0 proves every corpus spelling
	// round-tripped, an independent check of round_trip_failures in the report.
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("typeref_run exit code = %d, want 0 (round-trip failures)", code)
	}
}
