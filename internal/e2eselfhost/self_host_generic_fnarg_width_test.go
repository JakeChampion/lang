package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genericFnArgWidthProgram reaches its type parameter only through a lambda's
// parameter: `filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[]` binds
// `I` from `it` and has `T` nowhere else. The monomorphiser substitutes the
// BOUNDED parameters only, so the instance still declares `T[]` as its return;
// with the element type left unresolved, type_to_irtag yields "" for it and the
// backends fall back to an untyped 4-byte read of an 8-byte element — every
// value comes back 0 while the length is right (#9485).
//
// f64 is the whole point: a `string` element is pointer-width either way, which
// is why the `strings` position of conformance/cases/generic_fnarg_typevar did
// not catch this.
const genericFnArgWidthProgram = `import "core/iter" as iter;

function main(): i32 {
    var xs: f64[] = [5.5, 2.25, 8.5, 1.75, 4.5];
    var big = iter.filter(iter.of(xs), (x: f64): boolean => { return x > 3.0; });
    if (big.len() != 3) { return 99; }
    return (big[0] as i32) + (big[1] as i32) + (big[2] as i32);
}
`

// TestSelfHostGenericFnArgWidthX86_64 pins the answer against the native
// compiler rather than a literal, so the fixture cannot drift away from the
// oracle. The self-host answered 0 for every element before the checker learned
// to unify a callable parameter's spelling against the argument's type.
//
// x86-64 only. The self-host WASM emitter is separately wrong on this shape —
// the residual `T` also reaches the funcref type a call_indirect dispatches
// through and the array-push helper the append selects, so it emits a module
// wasmtime rejects (#9488). That is pre-existing and reproduces on main; it is
// not what this test is for, and a wasm leg here would assert someone else's
// bug rather than this one.
func TestSelfHostGenericFnArgWidthX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(genericFnArgWidthProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	native := buildAndRunCLI(t, buildLangBinForInterp(t), stdlibRoot, src, "native")
	if native == 99 {
		t.Fatalf("the oracle itself filtered wrong (exit 99) — the fixture is broken, not the self-host")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHost := buildAndRunCLI(t, buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), stdlibRoot, src, "selfhost")
	if selfHost != native {
		t.Fatalf("self-host exited %d, native %d — an f64 element read at 4 bytes answers 0 "+
			"for every value while the length stays right (#9485)", selfHost, native)
	}
}

// buildAndRunCLI compiles `src` for x86-64 with `cli` and returns the exit code
// the program answers with. `-o` precedes the positionals: the native CLI stops
// reading flags at the first one and writes the assembly to stdout instead.
func buildAndRunCLI(t *testing.T, cli, stdlibRoot, src, tag string) int {
	t.Helper()
	out := filepath.Join(t.TempDir(), tag)
	build := exec.Command(cli, "-target", "x86-64-linux", "-o", out, src, stdlibRoot)
	if compiled, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s compile: %v\n%s", tag, err, compiled)
	}
	run := exec.Command(out)
	if _, err := run.CombinedOutput(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		t.Fatalf("%s run: %v", tag, err)
	}
	return 0
}
