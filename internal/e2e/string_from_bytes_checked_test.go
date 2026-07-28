package e2e

import "testing"

// utf8FromBytesProgram pins `std/utf8.from_bytes` — the validating
// bytes-to-string constructor from #5634, decision D9, slice 2.
//
// The split it belongs to: `string_from_bytes_unchecked` is the builtin
// the caller vouches for, `utf8.from_bytes` is the one that checks. The
// contract here is that it accepts exactly what `is_valid_utf8` accepts
// and returns the original bytes untouched when it does — no lossy
// U+FFFD substitution, which is what separates it from `codepoints`.
//
// Exits 0 on success, a distinct code per failed step.
const utf8FromBytesProgram = `
import "std/utf8" as utf8;

function must(b: u8[], code: i32): string {
    match (utf8.from_bytes(b)) {
        Some(s) => { return s; },
        None => { exit(code); }
    }
    return "";
}

function rejects(b: u8[]): boolean {
    match (utf8.from_bytes(b)) {
        Some(s) => { return false; },
        None => { return true; }
    }
}

function main(): i32 {
    // A + e-acute + euro + grinning face: all four widths, 10 bytes.
    var mixed: u8[] = [65 as u8, 195 as u8, 169 as u8, 226 as u8, 130 as u8,
        172 as u8, 240 as u8, 159 as u8, 152 as u8, 128 as u8];
    var s: string = must(mixed, 1);
    if (s.len() != 10) { return 2; }
    // The bytes come back exactly, not lossily re-encoded.
    var i: i32 = 0;
    while (i < 10) {
        if (s[i] != mixed[i] as i32) { return 3; }
        i = i + 1;
    }
    // Empty and pure ASCII are valid.
    if (must([], 4).len() != 0) { return 5; }
    if (must([104 as u8, 105 as u8], 6) != "hi") { return 7; }

    // Every malformed shape is rejected.
    if (!rejects([128 as u8])) { return 8; }                    // stray continuation
    if (!rejects([226 as u8, 130 as u8])) { return 9; }         // truncated 3-byte
    if (!rejects([240 as u8, 159 as u8, 152 as u8])) { return 10; } // truncated 4-byte
    if (!rejects([237 as u8, 160 as u8, 128 as u8])) { return 11; } // surrogate U+D800
    if (!rejects([192 as u8, 175 as u8])) { return 12; }        // overlong '/'
    if (!rejects([244 as u8, 144 as u8, 128 as u8, 128 as u8])) { return 13; } // > U+10FFFF
    if (!rejects([248 as u8, 128 as u8, 128 as u8, 128 as u8])) { return 14; } // 0xF8 lead
    if (!rejects([65 as u8, 255 as u8, 66 as u8])) { return 15; } // valid text, one bad byte

    // from_bytes agrees with is_valid_utf8 over the whole 2-byte space:
    // the checked constructor admits a byte sequence iff the scanner
    // says it is well-formed.
    var p: i32 = 0;
    while (p < 256) {
        var q: i32 = 0;
        while (q < 256) {
            var pair: u8[] = [p as u8, q as u8];
            var accepted: boolean = !rejects(pair);
            var valid: boolean = utf8.is_valid_utf8(string_from_bytes_unchecked(pair));
            if (accepted != valid) { return 16; }
            q = q + 1;
        }
        p = p + 1;
    }
    return 0;
}
`

func TestUtf8FromBytesInterp(t *testing.T) {
	if got := runInterpExit(t, utf8FromBytesProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestUtf8FromBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, utf8FromBytesProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestUtf8FromBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, utf8FromBytesProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestUtf8FromBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, utf8FromBytesProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
