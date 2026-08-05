package e2e

// Coverage for core/map's string-key bucket hash.
//
// It was textbook byte-at-a-time FNV-1a. It now mixes 4-byte blocks and
// finishes with a murmur3 fmix32 avalanche, because callers mask the result
// with `cap - 1` and therefore read only the LOW bits — which is where FNV-1a
// diffuses worst.
//
// Two programs, mirroring how the RNG change is covered:
//
//  1. `map_string_hash` exercises the REAL map through the public API across
//     key lengths chosen around the 4-byte block boundary (1, 2, 3, 4, 5, 7,
//     8, 15, 16, 17 bytes), so both the block loop and the tail loop are hit,
//     plus a 2000-key round trip with deletes. Changing a hash cannot change
//     map semantics — iteration is insertion order and lookups re-check keys
//     by equality — so this is the guard that the new mixing did not break
//     bucketing, growth, or delete's swap-with-last.
//
//  2. `map_string_hash_spread` replicates both the old and the new hash and
//     compares bucket occupancy, since the real map exposes no bucket view.
//     For 1000 short keys in 1024 buckets a uniform hash fills ~638 buckets;
//     the old hash manages 562 and the new one 618. The test asserts the new
//     hash clears 590, which the old hash does not, so it fails if the
//     avalanche is removed.

import "testing"

const mapStringHashProg = `
import "core/map";
import "std/i32";
import "std/string";

function main(): i32 {
    // Key lengths around the 4-byte block boundary: both the block loop and
    // the 1-3 byte tail loop must produce stable, distinct buckets.
    var lens: i32[] = [1, 2, 3, 4, 5, 7, 8, 15, 16, 17];
    var li: i32 = 0;
    while (li < lens.len()) {
        var want_len: i32 = lens[li];
        var m: Map[string, i32] = map_new(8);
        var made: string[] = [];
        var i: i32 = 0;
        while (i < 200) {
            // Pad a counter out to exactly want_len bytes.
            var k: string = i.to_string();
            while (k.len() < want_len) { k = k + "x"; }
            k = k[0:want_len].to_owned();
            if (!m.has(k)) {
                m = m.insert(k, i);
                made = made.append(k);
            }
            i = i + 1;
        }
        // Every key inserted must be found, with its own value.
        var j: i32 = 0;
        while (j < made.len()) {
            if (!m.has(made[j])) { return 1; }
            if (m.get_or(made[j], 0 - 1) < 0) { return 2; }
            j = j + 1;
        }
        if (m.len() != made.len()) { return 3; }
        li = li + 1;
    }

    // Larger round trip: insert, look up, delete half, re-check both halves.
    var big: Map[string, i32] = map_new(16);
    var n: i32 = 2000;
    var a: i32 = 0;
    while (a < n) {
        big = big.insert("key_" + a.to_string(), a * 3);
        a = a + 1;
    }
    if (big.len() != n) { return 4; }
    var b: i32 = 0;
    while (b < n) {
        if (big.get_or("key_" + b.to_string(), 0 - 1) != b * 3) { return 5; }
        b = b + 1;
    }
    if (big.has("absent")) { return 6; }
    if (big.get_or("absent", 0 - 7) != (0 - 7)) { return 7; }

    var d: i32 = 0;
    while (d < n) {
        big = big.without("key_" + d.to_string()).0;
        d = d + 2;
    }
    if (big.len() != n / 2) { return 8; }
    var e: i32 = 1;
    while (e < n) {
        if (big.get_or("key_" + e.to_string(), 0 - 1) != e * 3) { return 9; }
        e = e + 2;
    }
    var f: i32 = 0;
    while (f < n) {
        if (big.has("key_" + f.to_string())) { return 10; }
        f = f + 2;
    }

    // Empty key and a repeated insert (overwrite in place).
    var q: Map[string, i32] = map_new(4);
    q = q.insert("", 5);
    if (q.get_or("", 0) != 5) { return 11; }
    q = q.insert("", 9);
    if (q.get_or("", 0) != 9) { return 12; }
    if (q.len() != 1) { return 13; }

    // Sizes straddling the small-map linear-scan threshold (8). Lookups must
    // agree on both sides of it, and a delete that drops a map back UNDER the
    // threshold must keep working -- the scan walks entries [0, len), which is
    // only equivalent to a probe because delete is swap-with-last and leaves
    // no tombstones in that range.
    var sz: i32 = 0;
    while (sz <= 12) {
        var t: Map[string, i32] = map_new(4);
        var g: i32 = 0;
        while (g < sz) {
            t = t.insert("k" + g.to_string(), g * 7);
            g = g + 1;
        }
        if (t.len() != sz) { return 14; }
        var h: i32 = 0;
        while (h < sz) {
            if (t.get_or("k" + h.to_string(), 0 - 1) != h * 7) { return 15; }
            if (!t.has("k" + h.to_string())) { return 16; }
            h = h + 1;
        }
        if (t.has("k" + sz.to_string())) { return 17; }
        // Delete down across the threshold, re-checking survivors each step.
        var d2: i32 = sz - 1;
        while (d2 >= 0) {
            t = t.without("k" + d2.to_string()).0;
            if (t.len() != d2) { return 18; }
            var s2: i32 = 0;
            while (s2 < d2) {
                if (t.get_or("k" + s2.to_string(), 0 - 1) != s2 * 7) { return 19; }
                s2 = s2 + 1;
            }
            if (t.has("k" + d2.to_string())) { return 20; }
            d2 = d2 - 1;
        }
        sz = sz + 1;
    }

    return 42;
}
`

const mapStringHashSpreadProg = `
import "std/i32";
import "std/string";

// The OLD byte-at-a-time FNV-1a.
function old_hash(s: string): i32 {
    var h: i32 = 0 - 2128831035;
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        h = h ^ (s[i] as i32);
        h = h * 16777619;
        i = i + 1;
    }
    return h;
}

// The NEW 4-byte-block FNV-1a with an fmix32 avalanche.
function new_hash(s: string): i32 {
    var h: u32 = 2166136261 as u32;
    var prime: u32 = 16777619 as u32;
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i + 4 <= n) {
        var w: u32 = (s[i] as u32)
                   | ((s[i + 1] as u32) << (8 as u32))
                   | ((s[i + 2] as u32) << (16 as u32))
                   | ((s[i + 3] as u32) << (24 as u32));
        h = (h ^ w) * prime;
        i = i + 4;
    }
    while (i < n) {
        h = (h ^ (s[i] as u32)) * prime;
        i = i + 1;
    }
    h = h ^ (h >> (16 as u32));
    h = h * (2246822507 as u32);
    h = h ^ (h >> (13 as u32));
    h = h * (3266489909 as u32);
    h = h ^ (h >> (16 as u32));
    return h as i32;
}

function occupied(keys: string[], nbuckets: i32, which: i32): i32 {
    var counts: i32[] = [];
    var b: i32 = 0;
    while (b < nbuckets) { counts = counts.append(0); b = b + 1; }
    var i: i32 = 0;
    while (i < keys.len()) {
        var h: i32 = 0;
        if (which == 0) { h = old_hash(keys[i]); } else { h = new_hash(keys[i]); }
        var slot: i32 = h & (nbuckets - 1);
        counts = counts.with(slot, counts[slot] + 1);
        i = i + 1;
    }
    var occ: i32 = 0;
    var c: i32 = 0;
    while (c < nbuckets) {
        if (counts[c] > 0) { occ = occ + 1; }
        c = c + 1;
    }
    return occ;
}

function main(): i32 {
    // 1000 short keys into 1024 buckets. A uniform hash fills ~638 of them
    // (1024 * (1 - e^-(1000/1024))). Measured: old 562, new 618.
    var shorts: string[] = [];
    var a: i32 = 0;
    while (a < 1000) {
        shorts = shorts.append(a.to_string());
        a = a + 1;
    }
    var old_occ: i32 = occupied(shorts, 1024, 0);
    var new_occ: i32 = occupied(shorts, 1024, 1);
    // The new hash must clear a bar the old one does not: this is what fails
    // if the fmix32 avalanche is dropped.
    if (new_occ < 590) { return 1; }
    if (new_occ <= old_occ) { return 2; }

    // Common-prefix keys differing only in their tail -- FNV-1a's weak shape.
    // 512 keys in 1024 buckets: ideal ~403, old 392, new 402.
    var tails: string[] = [];
    var t: i32 = 0;
    while (t < 512) {
        tails = tails.append("prefix_common_" + t.to_string());
        t = t + 1;
    }
    if (occupied(tails, 1024, 1) < 395) { return 3; }

    return 42;
}
`

func TestMapStringHashInterp(t *testing.T) {
	if got := runInterpExit(t, mapStringHashProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapStringHashX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStringHashProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapStringHashWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStringHashProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapStringHashArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStringHashProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestMapStringHashSpreadInterp(t *testing.T) {
	if got := runInterpExit(t, mapStringHashSpreadProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapStringHashSpreadX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStringHashSpreadProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapStringHashSpreadWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStringHashSpreadProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapStringHashSpreadArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStringHashSpreadProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
