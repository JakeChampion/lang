package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// arrayCallbackVerbsProgram exercises the generic callback / comparator array
// verbs added to std/array (#2689): zip / flat_map / reduce / sort_by over an
// arbitrary T[], in both the free-function and receiver-method forms.
//
// Unlike the structural verbs (reverse/take/drop/concat), these take a callback
// or comparator, so — like the existing map/filter/fold combinators — they ride
// the indirect-call path and are gated through the interpreter (oracle) + the
// native wasm backend rather than the self-host stdtest gate (the self-host
// can't lower a closure passed to a generic function yet). Callbacks are kept
// scalar-shaped per the #2753 indirect-call constraint. main returns 0 iff
// every check holds.
const arrayCallbackVerbsProgram = `
import "std/array";

function dup(x: i32): i32[] { return [x, x]; }
function maxi(a: i32, b: i32): i32 { if (a > b) { return a; } return b; }
function asc(a: i32, b: i32): i32 { return a - b; }
function desc(a: i32, b: i32): i32 { return b - a; }

function main(): i32 {
    // zip: positional pairing, truncated to the shorter input.
    var z: (i32, string)[] = array.zip([1, 2, 3], ["a", "b"]);
    if (z.len() != 2) { return 1; }
    if (z[0].0 != 1 || z[1].0 != 2) { return 2; }
    // zip receiver-method form.
    var z2: (i32, i32)[] = [10, 20].zip([1, 2, 3]);
    if (z2.len() != 2 || z2[1].0 != 20 || z2[1].1 != 2) { return 3; }

    // flat_map: map each element to a U[], then flatten.
    var fm: i32[] = array.flat_map([1, 2, 3], dup);
    if (fm.len() != 6 || fm[0] != 1 || fm[5] != 3) { return 4; }
    var fm2: i32[] = [4, 5].flat_map(dup);
    if (fm2.len() != 4 || fm2[2] != 5) { return 5; }

    // reduce: seedless fold; Some on non-empty, None on empty.
    match (array.reduce([3, 1, 4, 1, 5], maxi)) {
        Some(v) => { if (v != 5) { return 6; } },
        None => { return 7; }
    }
    match ([7, 2, 9].reduce(maxi)) {
        Some(v) => { if (v != 9) { return 8; } },
        None => { return 9; }
    }
    var empty: i32[] = [];
    match (array.reduce(empty, maxi)) {
        Some(_) => { return 10; },
        None => {}
    }

    // sort_by: comparator-driven stable sort, ascending and descending.
    // (The std/array xs.sort_by(cmp) method delegates to std/sort's generic
    // sort_by; the free function lives in std/sort, not std/array -- #5348.)
    var sa: i32[] = [3, 1, 2].sort_by(asc);
    if (sa[0] != 1 || sa[1] != 2 || sa[2] != 3) { return 11; }
    var sd: i32[] = [3, 1, 2].sort_by(desc);
    if (sd[0] != 3 || sd[1] != 2 || sd[2] != 1) { return 12; }
    // sort_by leaves an already-sorted run alone and handles duplicates.
    var sdup: i32[] = [2, 1, 2, 1].sort_by(asc);
    if (sdup[0] != 1 || sdup[1] != 1 || sdup[2] != 2 || sdup[3] != 2) { return 13; }

    return 0;
}
`

func TestInterpArrayCallbackVerbs(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(arrayCallbackVerbsProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (failing check index)\nstderr: %s", code, errb.String())
	}
}

func TestWASMArrayCallbackVerbs(t *testing.T) {
	if code := runWasm(t, arrayCallbackVerbsProgram); code != 0 {
		t.Errorf("wasm generic callback verbs: exit = %d, want 0", code)
	}
}
