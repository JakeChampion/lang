package e2e

import "testing"

// `std/i32`'s `(b: u8) is_ascii_white_space()` and `std/string`'s
// `__is_ascii_ws(b: i32)` are ONE predicate: the method widens its byte and
// defers, rather than open-coding the same six comparisons (#4387 item 1).
//
// The delegation runs that way round on purpose. `u8 -> i32` is lossless, so
// no input changes meaning; the reverse (`__is_ascii_ws` calling the method)
// would truncate, and a caller passing e.g. 288 would flip from false to true.
//
// The exhaustive 0..255 sweep is the property that matters — the two spellings
// agreeing on the six whitespace bytes alone would also pass if one of them
// wrongly admitted a seventh.
const asciiWsProg = `import "std/i32";
import "std/string";

function main(): i32 {
    // SP TAB LF CR VT FF are whitespace under both spellings.
    var ws: i32[] = [32, 9, 10, 13, 11, 12];
    var i: i32 = 0;
    while (i < ws.len()) {
        if (!(ws[i] as u8).is_ascii_white_space()) { return 1; }
        if (!string.__is_ascii_ws(ws[i])) { return 2; }
        i = i + 1;
    }
    // Boundaries either side of the 9..13 run and of SP must be false.
    var no: i32[] = [0, 8, 14, 31, 33, 48, 65, 97, 127, 255];
    var j: i32 = 0;
    while (j < no.len()) {
        if ((no[j] as u8).is_ascii_white_space()) { return 3; }
        if (string.__is_ascii_ws(no[j])) { return 4; }
        j = j + 1;
    }
    // Exhaustive: the two spellings agree on every byte value.
    var b: i32 = 0;
    while (b < 256) {
        if ((b as u8).is_ascii_white_space() != string.__is_ascii_ws(b)) { return 5; }
        b = b + 1;
    }
    // The trim() / is_blank() family shares the same body.
    if ("  hi \t\n".trim() != "hi") { return 6; }
    if (!"  \t ".is_blank()) { return 7; }
    return 42;
}
`

// TestNativeAsciiWsSingleImpl runs the shipped modules on interp / x86-64 / wasm.
func TestNativeAsciiWsSingleImpl(t *testing.T) {
	p := writeIterProg(t, asciiWsProg)
	if _, code := runFixtureInterp(t, p, ""); code != 42 {
		t.Errorf("ascii ws interp = %d, want 42", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 42 {
		t.Errorf("ascii ws x86-64 = %d, want 42", code)
	}
	if code := runWasm(t, asciiWsProg); code != 42 {
		t.Errorf("ascii ws wasm = %d, want 42", code)
	}
}
