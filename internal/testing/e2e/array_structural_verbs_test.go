package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

const arrayStructuralVerbsProgram = `
import "std/array";
function main(): i32 {
    let a: i32[] = [1, 2, 3, 4, 5];
    if (array.reverse(a)[0] != 5) { return 1; }
    if (array.reverse(a).len() != 5) { return 2; }
    if (array.take(a, 2).len() != 2 || array.take(a, 2)[1] != 2) { return 3; }
    if (array.take(a, 0).len() != 0) { return 4; }
    if (array.take(a, 99).len() != 5) { return 5; }
    if (array.drop(a, 3).len() != 2 || array.drop(a, 3)[0] != 4) { return 6; }
    if (array.drop(a, 99).len() != 0) { return 7; }
    if (array.drop(a, 0).len() != 5) { return 8; }
    // take(n) ++ drop(n) == a
    let split: i32[] = array.concat(array.take(a, 2), array.drop(a, 2));
    if (split.len() != 5 || split[0] != 1 || split[4] != 5) { return 9; }
    // concat with an empty operand copies the other side.
    let e: i32[] = [];
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

func TestWASMArrayStructuralVerbs(t *testing.T) {
	if code := runWasm(t, arrayStructuralVerbsProgram); code != 0 {
		t.Errorf("wasm generic structural verbs: exit = %d, want 0", code)
	}
}
