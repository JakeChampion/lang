package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// mapCompositeKeysProgram exercises Map[K, V] with COMPOSITE key types — a
// struct, an enum, and a tuple — keyed by VALUE (#2671). Two distinct instances
// that are value-equal must hit the same entry; distinct values must not
// collide. Covers insert / get / get_or / has / delete / overwrite.
//
// This is the interpreter oracle: `findKey` deep-compares composite values
// (interp.valuesEqual), so the reference compiler is value-correct for every
// composite key, INCLUDING tuples. The struct/enum legs now also lower on the
// compiled backends (via the keyed hash/eq runtime — see the
// map_struct_enum_keys fixture), but TUPLE keys still have no nominal type to
// hang Eq/Hash on, so the compiled path can't dispatch them yet — this stays
// interp-only because of the tuple section. main returns 0 iff every check
// holds.
const mapCompositeKeysProgram = `
import "core/map";
import "core/cmp" as cmp;

@derive(cmp.Eq, cmp.Hash)
struct Point { x: i32, y: i32 }
@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(i32) }

function main(): i32 {
    // --- struct keys: distinct value-equal instances hit the same entry ---
    var sm: Map[Point, i32] = map_new(8);
    sm = sm.insert(Point { x: 1, y: 2 }, 10);
    sm = sm.insert(Point { x: 3, y: 4 }, 20);
    if (sm.get_or(Point { x: 1, y: 2 }, -1) != 10) { return 1; }   // fresh instance, same value
    if (sm.get_or(Point { x: 3, y: 4 }, -1) != 20) { return 2; }
    if (sm.get_or(Point { x: 9, y: 9 }, -1) != -1) { return 3; }   // absent
    if (!sm.has(Point { x: 1, y: 2 })) { return 4; }
    if (sm.has(Point { x: 2, y: 1 })) { return 5; }                // field order matters
    // overwrite an existing key
    sm = sm.insert(Point { x: 1, y: 2 }, 99);
    if (sm.len() != 2 || sm.get_or(Point { x: 1, y: 2 }, -1) != 99) { return 6; }

    // --- enum keys (payload-carrying + unit variants) ---
    var em: Map[Tag, i32] = map_new(8);
    em = em.insert(A(1), 100);
    em = em.insert(B, 200);
    em = em.insert(C(1), 300);
    if (em.get_or(A(1), 0) != 100) { return 8; }
    if (em.get_or(B, 0) != 200) { return 9; }
    if (em.get_or(C(1), 0) != 300) { return 10; }
    if (em.get_or(A(2), -1) != -1) { return 11; }                  // same variant, diff payload
    if (em.len() != 3) { return 12; }                              // A(1) / C(1) distinct despite same payload

    // --- tuple keys ---
    var tm: Map[(i32, i32), i32] = map_new(8);
    tm = tm.insert((1, 2), 5);
    tm = tm.insert((2, 1), 6);
    if (tm.get_or((1, 2), 0) != 5) { return 13; }
    if (tm.get_or((2, 1), 0) != 6) { return 14; }
    if (tm.get_or((7, 7), -1) != -1) { return 15; }
    return 0;
}
`

// TestInterpMapCompositeKeys gates composite (struct / enum / tuple) map keys
// through the reference interpreter (#2671 slice 1). See the program comment for
// why this is interp-only for now.
func TestInterpMapCompositeKeys(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(mapCompositeKeysProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (failing check index)\nstderr: %s", code, errb.String())
	}
}
