package e2e

// A Map COW mutator's result stored straight into a tuple or array literal
// (#11121).
//
// On an owned receiver at its last use `__map_cow_inplace` hands back the
// receiver's own handle un-retained. A `let` owes that handle the COW-seam
// retain (#6227); a tuple or array element did not get it, so the container
// and the receiver's binding shared one count and both released it. Returning
// `(m.insert(k, v), 1)` from an `own m` function SEGV'd on the caller's next
// probe, and the three-element form silently emptied the map.

import (
	"strings"
	"testing"
)

const mapCowTupleElemDefs = `
import "core/map";
import "std/i64";
import "std/i32";

struct C { n: i32 }

function ins(own m: Map[i64, i64], k: i64, v: i64): (Map[i64, i64], i32) {
    return (m.insert(k, v), 1);
}

function ins3(own m: Map[i64, i64], own c: C, k: i64, v: i64): (Map[i64, i64], C, i32) {
    return (m.insert(k, v), C { ...c, n: c.n + 1 }, 1);
}

function clr(own m: Map[i64, i64]): (Map[i64, i64], i32) { return (m.cleared(), 5); }

function first(t: (Map[i64, i64], i32)): i32 { return t.0.len() as i32 + t.1; }
function first_of(a: Map[i64, i64][]): i32 { return a[0].len() as i32; }
function tmp_tuple(own m: Map[i64, i64]): i32 { return first((m.insert(7, 7), 1)); }
function tmp_arr(own m: Map[i64, i64]): i32 { return first_of([m.insert(8, 8)]); }

function fill(n: i32): Map[i64, i64] {
    let m: Map[i64, i64] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i as i64, i as i64); i = i + 1; }
    return m;
}

function check(): i32 {
    let m: Map[i64, i64] = fill(150);
    let i: i32 = 0;
    while (i < 100) {
        let (m2, _) = ins(m, (i % 150) as i64, (i + 1000) as i64);
        m = m2;
        i = i + 1;
    }
    if (m.len() != 150) { return 1; }
    if (m.get_or(5, 0) != 1005) { return 2; }

    let n: Map[i64, i64] = fill(150);
    let c: C = C { n: 0 };
    let j: i32 = 0;
    while (j < 100) {
        let (n2, c2, _) = ins3(n, c, (j % 150) as i64, j as i64);
        n = n2;
        c = c2;
        j = j + 1;
    }
    if (n.len() != 150) { return 3; }
    if (c.n != 100) { return 4; }

    if (tmp_tuple(fill(2)) != 4) { return 5; }
    if (tmp_arr(fill(2)) != 3) { return 6; }
    let (e, five) = clr(fill(2));
    if (e.len() != 0 || five != 5) { return 7; }
    return 0;
}
`

const mapCowTupleElemProg = mapCowTupleElemDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42;
}
`

// 42 when clean; every rc underflow the run recorded adds to it.
const mapCowTupleElemRcProg = mapCowTupleElemDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42 + __rc_underflow_count();
}
`

func TestMapCowTupleElemInterp(t *testing.T) {
	if got := runInterpExit(t, mapCowTupleElemProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

// Under the sanitizer the shared count is a use-after-free report the moment
// the receiver's release frees what the container still holds, and an
// over-retain is a leak verdict; a plain run often reads the stale block back
// intact.
func TestMapCowTupleElemSanitizedX86_64(t *testing.T) {
	checkMapCowTupleElemSanitized(t, runSanitizeX86_64)
}

func TestMapCowTupleElemSanitizedArm64(t *testing.T) {
	checkMapCowTupleElemSanitized(t, runSanitizeArm64)
}

func checkMapCowTupleElemSanitized(t *testing.T, run func(*testing.T, string) (string, string, int)) {
	t.Helper()
	_, stderr, code := run(t, mapCowTupleElemProg)
	if code != 42 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("got exit %d, want 42 with no sanitizer finding\nstderr: %s", code, stderr)
	}
}

func TestMapCowTupleElemWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowTupleElemRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
