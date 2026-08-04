package e2e

import "testing"

// TestX86_64StringLocalRecycles pins the native-x86-64 string-reclamation fix:
// fresh heap string locals AND nested-concat intermediates must be freed and
// their boxes recycled from the freelist, so build-and-discard loops stay flat.
//
// Three things had to line up (all guarded here): (1) computeFreeEligible admits
// native single-word string locals so the reinit-drop fires; (2) every
// heap-string producer requests length+1 from __fern_alloc_rc1 AND __fern_str_dec
// frees length+1, so the box is size-classed identically on alloc and free
// (before, strcat allocated length+1 but str_dec freed length — for length ≡ 8
// (mod 16) the freed box landed in a smaller class than its re-allocation looked
// up and was never reused); and (3) the nested-concat operand drop uses the
// freeing __fern_str_dec on native, not the dec-only __fern_rc_dec. See
// docs/IR-SELFCOMPILE-OOM-FINDINGS.md.
//
// Both probes read __heap_bump_bytes() (the high-water bump pointer, which counts
// bytes handed out but NOT recycled) after a 100k-iteration loop and assert it is
// bounded (< 1 MiB). Leaked, each would be ~100k × the box size (multiple MiB).
func TestX86_64StringLocalRecycles(t *testing.T) {
	// (a) a fresh 24-byte (length ≡ 8) strcat string local, reinit-dropped each
	// iteration — exercises the local-eligibility + size-class fix.
	const localSrc = `function churn(base: string, n: i32): i32 {
    var i: i32 = 0;
    while (i < n) {
        var s: string = base + "_payload_data_here";
        if (s.len() == 0) { return 99; }
        i = i + 1;
    }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn("prefix", 100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, localSrc); code != 0 {
		t.Errorf("string-local churn: got exit %d, want 0 (heap bump < 1 MiB — freed strcat boxes must recycle)", code)
	}

	// (b) a nested concat `a + b + a + b` — the intermediate temps must recycle
	// via the freeing operand drop.
	const nestedSrc = `function churn(a: string, b: string, n: i32): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < n) { acc = acc + (a + b + a + b).len(); i = i + 1; }
    if (acc == 0) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn("longer_string_one_here", "longer_string_two_here", 100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, nestedSrc); code != 0 {
		t.Errorf("nested-concat churn: got exit %d, want 0 (intermediate temps must recycle)", code)
	}
}
