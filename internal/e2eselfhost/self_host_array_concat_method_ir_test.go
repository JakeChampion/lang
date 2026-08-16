package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic array-receiver method `xs.concat(ys)` on the self-host IR path
// (array-method monomorphisation, slice 1). A generic array method
// (`(xs: T[]) concat(other: T[]): T[]` in std/array) has no struct to
// instantiate, so it was skipped by both monomorphize_module (receiver methods
// are skipped) and monomorphize_structs (an array is not a generic struct) —
// `T` stayed free and the legacy AST array-method dispatch typed the receiver as
// i32 and mis-dispatched `i32.concat`, dragging the module to the AST emitter.
// register_array_method_generics now folds such a method into a bounded free
// generic `__arrm_concat[T](xs: T[], other: T[])` (receiver as arg0) and
// mono_expr rewrites `xs.concat(ys)` to `__arrm_concat(xs, ys)`, which the
// existing free-generic worklist clones per element type — so the method rides
// the IR path and matches the interpreter. (This flipped
// array_structural_verbs_test.fern from AST to IR.)
//
// The cases here are single-element-type per program; `concat` at two element
// types is covered by TestSelfHostArrayMethodMultiElemIR. Each case is checked
// against the interpreter oracle.
var arrayConcatMethodIRCases = []struct {
	name string
	src  string
}{
	// i32[] concat, indexed back to a scalar: [1,2,3]+[4,5] -> len 5, c[4]=5.
	{"i32", `import "std/array";
function main(): i32 { var a: i32[] = [1, 2, 3]; var b: i32[] = [4, 5]; var c: i32[] = a.concat(b); return c.len() * 10 + c[4]; }`},
	// string[] concat: the joined array's length (the element kind exercises
	// the string-width array path, distinct from i32).
	{"string", `import "std/array";
function main(): i32 { var a: string[] = ["ab", "c"]; var b: string[] = ["de", "f"]; var c: string[] = a.concat(b); return c.len(); }`},
	// concat of a fresh array literal argument (receiver is still a bare local).
	{"literal-arg", `import "std/array";
function main(): i32 { var a: i32[] = [7, 8]; var c: i32[] = a.concat([9]); return c.len() * 10 + c[2]; }`},
	// chained: the outer receiver is itself an array-method call, so mono_infer
	// must recover `a.concat(b)`'s return type for the outer `.concat(a)` to
	// rewrite onto the IR path too. Single element type → one instantiation.
	{"chained", `import "std/array";
function main(): i32 { var a: i32[] = [1]; var b: i32[] = [2, 3]; var c: i32[] = a.concat(b).concat(a); return c.len(); }`},
}

func TestSelfHostArrayConcatMethodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
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

	for _, tc := range arrayConcatMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "arrcat_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			if !strings.Contains(asm, "__arrm_concat__") {
				t.Errorf("%s: no monomorphised __arrm_concat__ clone in asm (method did not ride the IR array-method path)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "arrcat_"+tc.name+"_bin", asm)
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
