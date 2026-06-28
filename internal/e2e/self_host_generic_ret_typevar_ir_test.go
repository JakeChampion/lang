package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A call whose generic callee returns an UNBOUNDED type parameter — `fold[T, A,
// I]: A` (core/iter.fold), where `A` is erased from `type_params`, so the
// monomorphiser never substitutes it — used to make the self-host `mono_infer`
// report the bare type variable "A" as the call's type. Binding `var s =
// iter.fold(..)` to "A" then keyed a spurious clone of any trait-bounded generic
// `s` flowed into (`assert_eq[T: Eq + Display](s, 6)` -> `assert_eq__A`), whose
// `A.eq` / `A.to_string` can't resolve — dragging the whole module to the AST
// emitter (e.g. examples/tests/iter_test.fern). mono_infer now reports a bare
// type variable as "unknown" instead, so the other concrete-literal argument
// binds the call at `i32` and the module routes IR.
//
// `gfold` reproduces the exact shape (return type = an unbounded type param,
// with the other type var only in a closure param so the key can't be inferred
// from the call), and `showeq` reproduces assert_eq's `Eq + Display` trait-method
// body — the combination that bailed pre-fix.
var genericRetTypeVarIRCases = []struct {
	name string
	src  string
}{
	{"fold-into-trait-bound", `import "core/cmp";
pub function gfold[T, A](init: A, f: (A, T) => A): A { return init; }
pub function showeq[T: cmp.Eq + cmp.Display](a: T, b: T): i32 {
    if (a == b) { return a.to_string().len(); }
    return b.to_string().len();
}
function main(): i32 {
    var s = gfold(0, function(a: i32, x: i32): i32 { return a + x; });
    return showeq(s, 7);
}`},
}

// TestSelfHostGenericRetTypeVarIR — a generic call returning an (erased)
// unbounded type param no longer keys a spurious bare-type-variable clone, so a
// program that funnels its result into a trait-bounded generic routes the IR
// path and runs correctly. Cross-checked against the interpreter oracle.
func TestSelfHostGenericRetTypeVarIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "grtvlr")
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

	for _, tc := range genericRetTypeVarIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "grtv_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "grtv_"+tc.name+"_bin", asm)
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
