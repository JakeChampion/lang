package e2e

// A Map COW CHAIN reclaims the columns the copy claimed (#6828).
//
// The sibling file pins the copy's CLAIM on the columns it copied (#6242).
// This one pins the release of that claim at the other end: `a = <a COW copy
// of a>`, where the reassignment ends the old handle's ownership. That release
// goes through the shared map-drop chain (emitMapSlotDrop): the value column by
// kind, then a string key column, then the buf and handle, every helper
// self-guarded on the handle's own rc==1.
//
// It did not always. The site kept its own narrower body, which freed only the
// buf, the handle and the string keys, because the column walks could not run
// while `__map_cow_inplace` copied the columns shallowly — freeing a column
// pulled storage out from under the fresh handle (#6227). Every column is
// claimed now (#7114, #8390, #8420), so that reason expired and the second
// body went with it (#8431).
//
// Once the copy owned its columns, that narrow free became a per-copy leak of
// everything the copy had just claimed, which in a chain is quadratic: round N
// reboxes N key cells and the round-N map is then dropped without freeing any
// of them. Measured on a 100/200/400-round chain, `__heap_bump_bytes()`:
//
//	              d(200→400) / d(100→200)
//	arm64-darwin  3.8x   (357328 → 1354704)
//	wasm          3.8x   (347088 → 1334224)
//	x86-64        2.0x   (unchanged — see below)
//
// It is a two-word-ABI reading, and for once the single-word side is honestly
// unaffected rather than accidentally surviving: there the key claim is one inc
// on the string data, so a missing release inflates a count on a block the
// chain keeps reachable anyway, where the two-word claim ALLOCATES a cell per
// entry per copy. The bounded leg below is therefore non-vacuous on wasm and
// arm64 and vacuous on x86-64, which is stated rather than papered over.
//
// A release may only cover the columns the copy CLAIMS, or it frees what the
// new handle reads — so the value cases below are what keeps the widened walk
// honest, and they are why widening was safe to do.
//
// What the two un-walked columns had been costing, 100 rounds, live_bytes,
// x86-64 / arm64 / wasm, before #8431 and after:
//
//	arr_chain     (kind 2, always walked)  0 / 3200 / 3200   unchanged
//	str_chain     (kind 5)   3168 / 83648 / 83968  ->  0 / 0 / 0
//	struct_chain  (kind 4)   6368 /  6400 /  9568  ->  0 / 3200 / 3200
//
// TestMapCowChainReclaimCensus below holds those numbers. The residual the two
// two-word columns still read is #8432's, not this site's — it is present in
// arr_chain, which this release has always walked.

import "testing"

// Every value kind and every aliasing shape the widened release has to keep
// correct. A wrong read here is the walk having freed something still live.
const mapCowChainReclaimProg = `
import "core/map";
import "std/i32";

struct Rec { name: string, n: i32 }

// Array values (valKind 2) — the column the copy claims with an inc and the
// chain's release now dec's, so an over-release shows up as a wrong element.
function arr_chain(rounds: i32): i32 {
    var a: Map[string, i32[]] = map_new(16);
    a = a.insert("arr-seed-key-that-heap-allocates", [11, 22, 33]);
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("arr-chain-key-that-heap-allocates" + i.to_string(), [i, i + 1]);
        a = b;
        if (a.get_or("arr-seed-key-that-heap-allocates", [0])[2] != 33) { return 0 - 1; }
        i = i + 1;
    }
    return a.len();
}

// String values (valKind 5) — claimed by the copy since #8390, and walked by
// the release since #8431, through __drop_map_str_values. The claim is what
// makes that walk a balanced release rather than a use-after-free. This chain
// is where the un-walked column cost the most: 83648 / 83968 bytes on arm64 /
// wasm over 100 rounds, now 0.
function str_chain(rounds: i32): i32 {
    var a: Map[string, string] = map_new(16);
    a = a.insert("sv-seed-key-that-heap-allocates", "sv-seed-value-that-heap-allocates");
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("sv-chain-key" + i.to_string(), "sv-chain-value" + i.to_string());
        a = b;
        if (a.get_or("sv-seed-key-that-heap-allocates", "?") != "sv-seed-value-that-heap-allocates") { return 0 - 1; }
        i = i + 1;
    }
    return a.len();
}

// Struct values (valKind 4) — claimed on the same terms, and walked on the same
// terms, through the generated __drop_map_via_<perValueDrop>. This chain and
// arr_chain both carried a further 32 B/round on the two-word ABIs until
// #8432: their get_or fallback, not their column, which is why arr_chain
// read the same despite having been walked all along.
function struct_chain(rounds: i32): i32 {
    var a: Map[string, Rec] = map_new(16);
    a = a.insert("st-seed-key-that-heap-allocates", Rec { name: "st-seed-name-that-heap-allocates", n: 7 });
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("st-chain-key" + i.to_string(), Rec { name: "st-chain-name" + i.to_string(), n: i });
        a = b;
        var got = a.get_or("st-seed-key-that-heap-allocates", Rec { name: "?", n: 0 - 1 });
        if (got.n != 7) { return 0 - 1; }
        if (got.name != "st-seed-name-that-heap-allocates") { return 0 - 2; }
        i = i + 1;
    }
    return a.len();
}

// The old handle is STILL held by another binding at the rebind, so the walk
// must not run at all — every helper self-guards on the handle's rc==1.
function aliased_chain(rounds: i32): i32 {
    var keep: Map[string, i32] = map_new(16);
    keep = keep.insert("keep-key-that-heap-allocates", 5);
    var a = keep;
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("al-chain-key-that-heap-allocates" + i.to_string(), i);
        a = b;
        if (keep.get_or("keep-key-that-heap-allocates", 0 - 1) != 5) { return 0 - 1; }
        i = i + 1;
    }
    if (keep.len() != 1) { return 0 - 2; }
    return a.len();
}

// keys() snapshots the key column and co-owns it, so the snapshot is read
// after the map it came from was released.
function keys_chain(rounds: i32): i32 {
    var a: Map[string, i32] = map_new(16);
    a = a.insert("ky-seed-key-that-heap-allocates", 8);
    var i: i32 = 0;
    while (i < rounds) {
        var ks = a.keys();
        var b = a;
        b = b.insert("ky-chain-key-that-heap-allocates" + i.to_string(), i);
        a = b;
        if (ks[0].len() == 0) { return 0 - 1; }
        i = i + 1;
    }
    return a.len();
}

// i32 keys: no key column, so only the buf and handle are ever owed.
function scalar_chain(rounds: i32): i32 {
    var a: Map[i32, i32] = map_new(16);
    a = a.insert(999, 5);
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert(i, i);
        a = b;
        i = i + 1;
    }
    if (a.get_or(999, 0 - 1) != 5) { return 0 - 1; }
    return a.len();
}

function main(): i32 {
    if (arr_chain(20) != 21) { return 1; }
    if (str_chain(20) != 21) { return 2; }
    if (struct_chain(20) != 21) { return 3; }
    if (aliased_chain(20) != 21) { return 4; }
    if (keys_chain(20) != 21) { return 5; }
    if (scalar_chain(20) != 21) { return 6; }
    return 42;
}
`

func TestMapCowChainReclaimInterp(t *testing.T) {
	if got := runInterpExit(t, mapCowChainReclaimProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapCowChainReclaimX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowChainReclaimProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapCowChainReclaimWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowChainReclaimProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapCowChainReclaimArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowChainReclaimProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// The byte census over the same six shapes. The answers above say nothing was
// freed too early; this says nothing was left behind.
//
// ABSOLUTE on every backend — every byte back. Two changes bought that, and
// the numbers before each are what the shapes below are sized to show:
// #8431 routed this site through the shared map-drop chain (the struct chain
// read 6368 on x86-64, the string one 3168, and 83648 / 83968 on the two-word
// ABIs), and #8432 stopped a `get_or` with a fresh fallback stranding it under
// a boxed key, which was the last 1280 on arm64 and wasm.
//
// A rise here means a column the copy claims is going unreleased again.
func TestMapCowChainReclaimCensus(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", runLeakCheckX86_64},
		{"arm64", runLeakCheckArm64},
		{"wasm", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := tc.run(t, mapCowChainReclaimProg)
			if code != 42 && code != 0 {
				t.Fatalf("exit=%d, want the program's own 42 (or wasm's 0)", code)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs == 0 {
				t.Fatalf("no allocations — the chains are not running")
			}
			if allocs != frees || live != 0 {
				t.Errorf("the COW chain leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
}

// The boundedness probe. A chain doubles its own work when the rounds double
// (each round copies a one-longer buffer), so the assertion is a RATIO and not
// the sibling file's "must not grow" — linear reclamation lands at 2.0x and the
// leak at 3.8x, which 2.5x separates with room on both sides.
//
// __rc_underflow_count() rides along as the other direction: releasing a column
// the copy does NOT own would show up here rather than as growth.
const mapCowChainBoundedProg = `
import "core/map";
import "std/i32";

function chain(rounds: i32): i32 {
    var a: Map[string, i32] = map_new(16);
    a = a.insert("seed-key-that-heap-allocates", 1);
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("chain-key-that-heap-allocates" + i.to_string(), i);
        a = b;
        i = i + 1;
    }
    return a.len();
}

function main(): i32 {
    if (chain(100) < 0) { return 1; }
    var b1: i64 = __heap_bump_bytes();
    if (chain(200) < 0) { return 2; }
    var b2: i64 = __heap_bump_bytes();
    if (chain(400) < 0) { return 3; }
    var b3: i64 = __heap_bump_bytes();
    if ((b3 - b2) > (b2 - b1) * 5 / 2) { return 4; }
    if (__rc_underflow_count() != 0) { return 5; }
    return 42;
}
`

func TestMapCowChainBoundedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowChainBoundedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (4 = the chain's release is not keeping up with the copy's claim)", got)
	}
}

func TestMapCowChainBoundedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowChainBoundedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (4 = the chain's release is not keeping up with the copy's claim)", got)
	}
}

func TestMapCowChainBoundedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowChainBoundedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (4 = the chain's release is not keeping up with the copy's claim)", got)
	}
}
