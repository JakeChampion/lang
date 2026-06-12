package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// arrayStructuralVerbsProgram exercises the generic structural array verbs
// added to std/array (#2689): reverse / take / drop / concat over an arbitrary
// T[]. The verbs take no callback, so they lower on every AOT backend (no
// indirect-call limitation). main checks reverse/take/drop/concat over an i32[]
// and the `take(n) ++ drop(n) == xs` complement law; it returns 0 iff every
// check holds, so exit 0 means the verbs are correct on the backend under test.
//
// (Element types are kept scalar here. reverse/take/drop/concat are correct
// over struct[] too — see the interp + self-host coverage in
// examples/tests/array_structural_verbs_test.fern — but field access on a
// generic function's struct[] *return* hits a separate, pre-existing native
// codegen limit ("field access on unresolved struct"), out of scope for #2689.)
const arrayStructuralVerbsProgram = `
import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4, 5];
    if (array.reverse(a)[0] != 5) { return 1; }
    if (array.reverse(a).len() != 5) { return 2; }
    if (array.take(a, 2).len() != 2 || array.take(a, 2)[1] != 2) { return 3; }
    if (array.take(a, 0).len() != 0) { return 4; }
    if (array.take(a, 99).len() != 5) { return 5; }
    if (array.drop(a, 3).len() != 2 || array.drop(a, 3)[0] != 4) { return 6; }
    if (array.drop(a, 99).len() != 0) { return 7; }
    if (array.drop(a, 0).len() != 5) { return 8; }
    // take(n) ++ drop(n) == a
    var split: i32[] = array.concat(array.take(a, 2), array.drop(a, 2));
    if (split.len() != 5 || split[0] != 1 || split[4] != 5) { return 9; }
    // concat with an empty operand copies the other side.
    var e: i32[] = [];
    if (array.concat(e, a).len() != 5) { return 10; }
    // receiver-method form of concat.
    if (a.concat(a).len() != 10) { return 11; }
    return 0;
}
`

func TestInterpArrayStructuralVerbs(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(arrayStructuralVerbsProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (failing check index)\nstderr: %s", code, errb.String())
	}
}

// Note: the x86-64 / arm64 AOT backends are covered by the self-host
// stdtest gate (TestSelfHostStdTestE2E / …Arm64, case "array_structural_verbs"),
// which compiles examples/tests/array_structural_verbs_test.fern through the
// real self-hosted compiler — full pipeline incl. monomorphisation — and
// diffs it against the interpreter. The compileAndRunX86_64 / compileAndRunArm64
// helpers skip the monomorph pass, so they can't compile these generic verbs;
// runWasm (below) uses the full CLI pipeline and can.
func TestWASMArrayStructuralVerbs(t *testing.T) {
	if code := runWasm(t, arrayStructuralVerbsProgram); code != 0 {
		t.Errorf("wasm generic structural verbs: exit = %d, want 0", code)
	}
}
