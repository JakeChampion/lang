package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// mapVerbsProgram exercises the higher-level Map verbs added to core/map
// (#2685): entries / merge / extend / from / get_or_insert, over both i32 and
// string keys, including the get_or_insert word-count use case. main returns 0
// iff every check holds.
//
// These verbs use Option (get) + tuples + generic map ops, which the self-host
// compiler can't lower yet, so they are covered on the native compiler
// (interpreter + wasm here; x86-64/arm64 via the interp oracle) rather than the
// self-host stdtest gate — mirroring the interp-only closure combinators.
const mapVerbsProgram = `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    // entries: sum of every k+v = (1+10)+(2+20)+(3+30) = 66
    var s: i32 = 0;
    for e in m.entries() { s = s + e.0 + e.1; }
    if (s != 66) { return 1; }
    if (m.entries().len() != 3) { return 2; }

    // from + duplicate-last-wins
    var f: Map[i32, i32] = map.from([(5, 50), (6, 60), (5, 55)]);
    if (f.len() != 2) { return 3; }
    if (f.get_or(5, 0) != 55) { return 4; }

    // merge: other wins on a shared key; both unique keys survive.
    var a: Map[i32, i32] = map.from([(1, 100), (2, 200)]);
    var b: Map[i32, i32] = map.from([(2, 999), (5, 500)]);
    var mg: Map[i32, i32] = a.merge(b);
    if (mg.len() != 3 || mg.get_or(2, 0) != 999 || mg.get_or(1, 0) != 100) { return 5; }
    if (a.extend(b).len() != 3) { return 6; }

    // get_or_insert: present -> unchanged; absent -> inserted.
    var r1: (Map[i32, i32], i32) = m.get_or_insert(1, 99);
    if (r1.1 != 10 || r1.0.len() != 3) { return 7; }
    var r2: (Map[i32, i32], i32) = m.get_or_insert(9, 90);
    if (r2.1 != 90 || r2.0.len() != 4) { return 8; }

    // string keys: word-count via get_or_insert.
    var counts: Map[string, i32] = map_new(8);
    var words: string[] = ["a", "b", "a", "c", "a", "b"];
    for w in words {
        var rc: (Map[string, i32], i32) = counts.get_or_insert(w, 0);
        counts = rc.0.insert(w, rc.1 + 1);
    }
    if (counts.get_or("a", 0) != 3 || counts.get_or("b", 0) != 2 || counts.get_or("c", 0) != 1) { return 9; }
    return 0;
}
`

func TestInterpMapVerbs(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(mapVerbsProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (failing check index)\nstderr: %s", code, errb.String())
	}
}

func TestWASMMapVerbs(t *testing.T) {
	if code := runWasm(t, mapVerbsProgram); code != 0 {
		t.Errorf("wasm Map verbs: exit = %d, want 0", code)
	}
}
