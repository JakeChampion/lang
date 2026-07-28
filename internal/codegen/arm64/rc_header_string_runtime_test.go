package arm64

import (
	"strings"
	"testing"
)

// helperBody returns the slice of `asm` from the `.global <sym>` line up to
// that symbol's `.size <sym>` directive — i.e. exactly one runtime helper's
// body. Empty if the symbol isn't emitted.
func helperBody(asm, sym string) string {
	start := strings.Index(asm, ".global "+sym+"\n")
	if start < 0 {
		return ""
	}
	rest := asm[start:]
	if end := strings.Index(rest, ".size "+sym); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestTwoWordStringRuntimesUseRcHeaderedAlloc guards #2817 and its two siblings.
//
// On the two-word string ABI (arm64) every heap string buffer that an owned
// string local later reclaims via __fern_str_dec MUST be allocated through the
// rc-headered allocator (__fern_alloc_rc1) — which writes the live rc at data-8
// and the payload size at data-4 that __fern_str_dec reads. A plain __fern_alloc
// buffer has no such header, so the drop reads garbage: it either rc_dec's a
// neighbouring cell's bytes or box_free's a wrong-sized block overlapping a
// still-live cell, recycling it through the freelist (the arm64-only
// heap-corruption #2817 surfaced via std/url).
//
// string_from_bytes_unchecked (fixed in #2907), env, and read_line all return owned
// strings and all used plain __fern_alloc on this path before the fix. This
// pins the rc-headered allocation in all three two-word bodies and fails loudly
// if any regresses to a bare __fern_alloc for the string payload.
func TestTwoWordStringRuntimesUseRcHeaderedAlloc(t *testing.T) {
	src := `function main(): i32 {
    var a: string = string_from_bytes_unchecked([65 as u8, 66 as u8]);
    var n: i32 = a.len();
    match (env("FERN_X")) { Some(v) => { n = n + v.len(); }, None => {} }
    match (read_line()) { Some(l) => { n = n + l.len(); }, None => {} }
    return n;
}`
	asm := compile(t, src, Options{})

	for _, sym := range []string{"string_from_bytes_unchecked", "__fern_env", "__fern_read_line"} {
		body := helperBody(asm, sym)
		if body == "" {
			t.Fatalf("%s runtime was not emitted; cannot verify its allocation path", sym)
		}
		if !strings.Contains(body, "bl __fern_alloc_rc1") {
			t.Errorf("%s must allocate its string buffer via __fern_alloc_rc1 "+
				"(rc=1 at data-8, size at data-4) so __fern_str_dec can reclaim it; "+
				"a plain __fern_alloc buffer has no rc header and corrupts the heap (#2817)\n--- body ---\n%s",
				sym, body)
		}
	}
}
