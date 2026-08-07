package e2e

import "testing"

// utf8ValidateProgram pins std/utf8's `is_valid_utf8` against the
// definition it used to be implemented as (#5634, decision D9).
//
// The scan was a loop over `utf8_decode_at`, which returns
// `Option[(i32, i32)]` — so it allocated and refcounted a tuple box per
// codepoint, ~48 ms for a 346 KB body. It now checks continuation bytes
// inline. That is only a safe swap if it accepts *exactly* the same
// language, so this test keeps the old decoder-driven walk as a
// reference implementation and asserts the two agree.
//
// The 1- and 2-byte spaces are checked exhaustively (all 65,792
// sequences, which covers every stray-continuation, truncated-tail,
// invalid-lead and overlong-2 case). The 3- and 4-byte spaces are far
// too large to enumerate, so they get the boundary cases that actually
// separate the two implementations: overlong forms, the surrogate
// range, the U+10FFFF ceiling, and truncation at each length.
//
// Exits 0 on success, a distinct code per failed step.
const utf8ValidateProgram = `
import "std/utf8" as utf8;

// The reference: the pre-rewrite implementation, a walk driven by
// utf8_decode_at. Kept here rather than in the stdlib so the stdlib
// carries one scanner and this test still pins the contract.
function valid_ref(s: string): boolean {
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => { i = i + pair.1; },
            None => { return false; }
        }
    }
    return true;
}

function bytes1(a: i32): string {
    return string_from_bytes_unchecked([a as u8]);
}
function bytes2(a: i32, b: i32): string {
    return string_from_bytes_unchecked([a as u8, b as u8]);
}
function bytes3(a: i32, b: i32, c: i32): string {
    return string_from_bytes_unchecked([a as u8, b as u8, c as u8]);
}
function bytes4(a: i32, b: i32, c: i32, d: i32): string {
    return string_from_bytes_unchecked([a as u8, b as u8, c as u8, d as u8]);
}

function check(s: string, code: i32): i32 {
    if (utf8.is_valid_utf8(s) != valid_ref(s)) { return code; }
    return 0;
}

function main(): i32 {
    // Every 1-byte sequence: ASCII accepted, everything else a lead
    // byte with no tail.
    var a: i32 = 0;
    while (a < 256) {
        if (check(bytes1(a), 1) != 0) { return 1; }
        a = a + 1;
    }

    // Every 2-byte sequence: 65,536 pairs. Catches overlong C0/C1,
    // stray continuations, and truncated 3-/4-byte leads.
    var p: i32 = 0;
    while (p < 256) {
        var q: i32 = 0;
        while (q < 256) {
            if (check(bytes2(p, q), 2) != 0) { return 2; }
            q = q + 1;
        }
        p = p + 1;
    }

    // 3-byte: the surrogate range and the overlong boundary are where
    // a hand-rolled scan drifts from the decoder.
    if (check(bytes3(224, 160, 128), 3) != 0) { return 3; }   // U+0800, shortest 3-byte
    if (check(bytes3(224, 159, 191), 4) != 0) { return 4; }   // overlong U+07FF
    if (check(bytes3(237, 159, 191), 5) != 0) { return 5; }   // U+D7FF, just below surrogates
    if (check(bytes3(237, 160, 128), 6) != 0) { return 6; }   // U+D800, first surrogate
    if (check(bytes3(237, 191, 191), 7) != 0) { return 7; }   // U+DFFF, last surrogate
    if (check(bytes3(238, 128, 128), 8) != 0) { return 8; }   // U+E000, just above
    if (check(bytes3(239, 191, 191), 9) != 0) { return 9; }   // U+FFFF
    if (check(bytes3(226, 130, 172), 10) != 0) { return 10; } // euro sign
    if (check(bytes3(226, 130, 40), 11) != 0) { return 11; }  // bad third byte
    if (check(bytes3(226, 40, 172), 12) != 0) { return 12; }  // bad second byte

    // 4-byte: the overlong boundary and the U+10FFFF ceiling.
    if (check(bytes4(240, 144, 128, 128), 13) != 0) { return 13; } // U+10000, shortest 4-byte
    if (check(bytes4(240, 143, 191, 191), 14) != 0) { return 14; } // overlong U+FFFF
    if (check(bytes4(244, 143, 191, 191), 15) != 0) { return 15; } // U+10FFFF, the ceiling
    if (check(bytes4(244, 144, 128, 128), 16) != 0) { return 16; } // U+110000, past it
    if (check(bytes4(245, 128, 128, 128), 17) != 0) { return 17; } // lead past the range
    if (check(bytes4(248, 128, 128, 128), 18) != 0) { return 18; } // 0xF8 is never a lead
    if (check(bytes4(240, 159, 152, 128), 19) != 0) { return 19; } // grinning face
    if (check(bytes4(240, 159, 152, 40), 20) != 0) { return 20; }  // bad fourth byte

    // Truncation at every length: a valid prefix that runs out of bytes.
    if (check(bytes2(226, 130), 21) != 0) { return 21; }
    if (check(bytes3(240, 159, 152), 22) != 0) { return 22; }
    if (check(bytes1(240), 23) != 0) { return 23; }

    // Mixed-width text, and the empty string.
    if (check("", 24) != 0) { return 24; }
    if (check("hello, world", 25) != 0) { return 25; }
    var mixed: string = string_from_bytes_unchecked([65 as u8, 195 as u8, 169 as u8,
        226 as u8, 130 as u8, 172 as u8, 240 as u8, 159 as u8, 152 as u8, 128 as u8]);
    if (check(mixed, 26) != 0) { return 26; }
    if (!utf8.is_valid_utf8(mixed)) { return 27; }

    // A valid string with one byte corrupted at each position in turn
    // is rejected by both, at every width.
    var n: i32 = mixed.len();
    var k: i32 = 0;
    while (k < n) {
        var b: u8[] = [];
        var j: i32 = 0;
        while (j < n) {
            if (j == k) { b = b.append(255 as u8); } else { b = b.append(mixed[j] as u8); }
            j = j + 1;
        }
        var broken: string = string_from_bytes_unchecked(b);
        if (check(broken, 28) != 0) { return 28; }
        if (utf8.is_valid_utf8(broken)) { return 29; }
        k = k + 1;
    }

    // The ASCII skip is now __ascii_run, a 16-byte-per-iteration vector
    // kernel (ATLAS-PLATFORM-PLAN §3.4 step 2), so the scan has a block
    // boundary that the byte loop it replaced did not. None of the cases
    // above is long enough to reach it: the longest is 12 bytes.
    //
    // Sweep an ASCII run of every length 0..40 — two full blocks plus a
    // partial tail either side — with a 2-byte sequence placed at every
    // offset in it, valid and then corrupted. A skip that overshot its
    // block, stopped one byte early, or mislocated the lane would land the
    // scan on the wrong byte and disagree with the reference here.
    var run: i32 = 0;
    while (run <= 40) {
        var at: i32 = 0;
        while (at <= run) {
            var pre: u8[] = [];
            var p: i32 = 0;
            while (p < run) {
                if (p < at) { pre = pre.append(97 as u8); } else { pre = pre.append(98 as u8); }
                p = p + 1;
            }
            // Splice a valid 2-byte codepoint in at the offset, then the rest.
            var withcp: u8[] = [];
            var q: i32 = 0;
            while (q < at) { withcp = withcp.append(pre[q]); q = q + 1; }
            withcp = withcp.append(195 as u8);
            withcp = withcp.append(169 as u8);
            while (q < run) { withcp = withcp.append(pre[q]); q = q + 1; }
            var good: string = string_from_bytes_unchecked(withcp);
            if (check(good, 30) != 0) { return 30; }
            if (!utf8.is_valid_utf8(good)) { return 31; }

            // Same shape with the continuation byte replaced by an ASCII
            // one: the lead byte is still found, but the sequence is now
            // truncated. This is the case a skip that ran PAST the high
            // byte would wrongly accept.
            var bad: u8[] = [];
            var r2: i32 = 0;
            while (r2 < withcp.len()) {
                if (r2 == at + 1) { bad = bad.append(97 as u8); } else { bad = bad.append(withcp[r2]); }
                r2 = r2 + 1;
            }
            var broke: string = string_from_bytes_unchecked(bad);
            if (check(broke, 32) != 0) { return 32; }
            if (utf8.is_valid_utf8(broke)) { return 33; }
            at = at + 1;
        }
        run = run + 1;
    }
    return 0;
}
`

func TestUtf8ValidateInterp(t *testing.T) {
	if got := runInterpExit(t, utf8ValidateProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestUtf8ValidateX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, utf8ValidateProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestUtf8ValidateWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, utf8ValidateProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestUtf8ValidateArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, utf8ValidateProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
