package e2e

// A Map field replaced by a COW mutator while the struct's own box is reused
// in place.
//
// `p = T{ ...p, m: p.m.insert(k, v) }` takes the struct-update reuse path
// (emitStructUpdateReuse), which writes the new field values into p's own box
// and deep-drops each REPLACED field's old value first. That drop is sound for
// a field whose new value carries a count of its own — an array push leaves
// the grown buffer at rc 2 — and unsound for a Map, because
// `__map_cow_inplace` mutates in place at rc<=1 and bumps nothing: the "old"
// value being dropped IS the handle just stored, so the box is left pointing
// at a freed buffer.
//
// The fresh-alloc StructLit lowering has cloned such a field since #2763; the
// reuse path owed the same copy and did not make it. What it looked like: the
// map read back EMPTY one statement later, or a segfault once the freed block
// was recycled, and only when the struct was rebuilt through a local alias of
// a parameter (`var h = h0`) — the shape that makes the frame the owner and so
// lets the reuse fire at all. A `-sanitize` build, which never recycles a
// freed block, printed the right answer throughout.

import "testing"

// Threading a counter map through a function, which is the shape that reaches
// the reuse: the frame owns `h`, so its box is a reuse donor, and the caller's
// `k = step(k, …)` rebind is what the freed box is handed back to.
const mapStructUpdateReuseProg = `
import "core/map";
import "std/i32";

struct Seen { m: Map[string, i32], hits: i32 }

function step(s0: Seen, key: string): Seen {
    var s: Seen = s0;
    match (s.m.get(key)) {
        Some(_) => {
            return Seen { ...s, hits: s.hits + 1 };
        },
        None => {
            s = Seen { ...s, m: s.m.insert(key, 1) };
        }
    }
    return s;
}

function main(): i32 {
    var m: Map[string, i32] = map_new(16);
    var k: Seen = Seen { m: m, hits: 0 };
    var i: i32 = 0;
    while (i < 24) {
        k = step(k, "key-that-heap-allocates-" + i.to_string());
        if (k.m.len() != i + 1) { return 1; }
        i = i + 1;
    }
    // Every key is still there after the last rebind, and every one of them
    // is a hit the second time round.
    if (k.m.len() != 24) { return 2; }
    var j: i32 = 0;
    while (j < 24) {
        k = step(k, "key-that-heap-allocates-" + j.to_string());
        j = j + 1;
    }
    if (k.hits != 24) { return 3; }
    if (k.m.len() != 24) { return 4; }
    return 42;
}
`

func TestMapStructUpdateReuseInterp(t *testing.T) {
	if got := runInterpExit(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// The rc probe, for the over-release direction: the value assertions above
// only bite once the freed block has been recycled into something else, and a
// `return T{ ...p, m: p.m.insert(…) }` — the return-position spread, the other
// caller of emitStructUpdateReuse — is here too.
const mapStructUpdateReuseRcProg = `
import "core/map";
import "std/i32";

struct Seen { m: Map[string, i32], hits: i32 }

function put(s0: Seen, key: string): Seen {
    var s: Seen = s0;
    return Seen { ...s, m: s.m.insert(key, 1) };
}

function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    var k: Seen = Seen { m: m, hits: 0 };
    var i: i32 = 0;
    while (i < 32) {
        k = put(k, "key-that-heap-allocates-" + i.to_string());
        i = i + 1;
    }
    if (k.m.len() != 32) { return 1; }
    return 42 + __rc_underflow_count();
}
`

func TestMapStructUpdateReuseNoUnderflowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapStructUpdateReuseNoUnderflowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapStructUpdateReuseNoUnderflowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}
