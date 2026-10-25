package e2e

import "testing"

// The byte predicate and public string operations must agree for every byte.
// This checks the shared stdlib predicate without calling its private helper.
const asciiWsProg = `import "std/utf8";
import "std/i32";
import "std/string";

function main(): i32 {
    // SP TAB LF CR VT FF are whitespace under both spellings.
    let ws: i32[] = [32, 9, 10, 13, 11, 12];
    let i: i32 = 0;
    while (i < ws.len()) {
        if (!(ws[i] as u8).is_ascii_whitespace()) { return 1; }
        if (!utf8.encode(ws[i] as char).is_blank()) { return 2; }
        i = i + 1;
    }
    // Boundaries either side of the 9..13 run and of SP must be false.
    let no: i32[] = [0, 8, 14, 31, 33, 48, 65, 97, 127, 255];
    let j: i32 = 0;
    while (j < no.len()) {
        if ((no[j] as u8).is_ascii_whitespace()) { return 3; }
        if (utf8.encode(no[j] as char).is_blank()) { return 4; }
        j = j + 1;
    }
    // Exhaustive: the two spellings agree on every byte value.
    let b: i32 = 0;
    while (b < 256) {
        if ((b as u8).is_ascii_whitespace() != utf8.encode(b as char).is_blank()) { return 5; }
        b = b + 1;
    }
    // The trim() / is_blank() family shares the same body.
    if ("  hi \t\n".trim() != "hi") { return 6; }
    if (!"  \t ".is_blank()) { return 7; }
    return 42;
}
`

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
