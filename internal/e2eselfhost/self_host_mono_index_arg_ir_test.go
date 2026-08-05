package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generic call whose type parameter is inferable only from an ARRAY ELEMENT
// read — `f(xs[i])`, where `xs: T[]` — must monomorphise and route through the
// IR path.
//
// `mono_infer` had no `ExprIndex` arm, so `xs[i]` inferred as "unknown". With
// no type argument the call was left generic, and the un-monomorphised symbol
// (`cmp____cmp_probe_after`, with no `__i32` suffix) survived into the IR
// eligibility gate, which rejected the CALLING function and bailed the module
// to an AST emitter that no longer exists (#6189). A hard compile error.
//
// THESE CASES NEED THE STDLIB. The bug reproduces through a bounded generic
// (`T: Ord` calling `.cmp`), which is what core/cmp's adaptive sort does — so
// the driver here is asm_load_run with the stdlib resolved off disk, not the
// pathprobe driver. A first version of this test used pathprobe with plain
// `[T]` generics and passed with the fix REVERTED: unbounded single-module
// generics never reproduced it, so that test guarded nothing. Verified by
// reverting the fix and watching these fail.
//
// Each case is oracle-checked against the interpreter and routing-pinned
// to "ir".
var monoIndexArgIRCases = []struct {
	name string
	src  string
}{
	// The shape core/cmp's sort hit: a bounded generic helper receiving two
	// element reads, called from inside a loop in a generic caller.
	{"bounded-elem-args", `import "core/cmp";
function after[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function count_desc[T: cmp.Ord](a: T[]): i32 {
    var c: i32 = 0;
    var i: i32 = 1;
    while (i < a.len()) {
        if (after(a[i - 1], a[i])) { c = c + 1; }
        i = i + 1;
    }
    return c;
}
function main(): i32 { var xs: i32[] = [3, 1, 2]; return count_desc(xs); }`},

	// Same, with the call in a condition rather than a statement.
	{"bounded-elem-args-condition", `import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    var xs: i32[] = [9, 4];
    if (bigger(xs[0], xs[1])) { return 7; }
    return 3;
}`},

	// A string element binds `T = string` through the same path -- guards
	// against the element type being taken as the array type verbatim.
	{"bounded-elem-args-string", `import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    var xs: string[] = ["b", "a"];
    if (bigger(xs[0], xs[1])) { return 7; }
    return 3;
}`},

	// Indexing a value whose type is still an UNSUBSTITUTED `T[]` -- here the
	// return of an unbounded generic (`array.reverse`), whose type params are
	// erased, so its return type comes back as the literal "T[]".
	//
	// The element type is then the bare type variable "T", which is not a
	// usable concrete type. Reporting it keyed a clone named after the
	// variable (`bigger__T`), whose body calls `T.cmp` -- an unknown symbol, so
	// the gate rejected the caller and the module failed to compile. The first
	// version of the ExprIndex arm had no such guard and broke
	// examples/tests/array_structural_verbs_test.fern
	// (`test.assert_eq(array.reverse(xs)[0], 42)` -> `test__assert_eq__T` ->
	// `T.eq`) on every backend.
	{"typevar-elem-stays-generic", `import "std/array";
import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    if (bigger(array.reverse(xs)[0], 1)) { return 7; }
    return 3;
}`},
}

func TestSelfHostMonoIndexArgIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "monoidx")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range monoIndexArgIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "monoidx_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\" (the un-monomorphised call bailed the module)",
					tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "monoidx_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
