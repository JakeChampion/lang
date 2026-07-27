package e2e

import "testing"

// asBytesViewProgram pins `[u8]` as a non-owning byte view over a string
// (#5632, decision D8) on every backend.
//
// `as_bytes()` returns a slice header aliasing the string's payload,
// where `bytes()` allocates and copies. The distinction is the whole
// point: every byte-level consumer in the tree paid for a full copy just
// to read bytes it never mutated.
//
// Exits 0 on success, a distinct code per failed step.
const asBytesViewProgram = `
import "std/utf8" as utf8;

function sum_view(b: [u8]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    return t;
}

function main(): i32 {
    var s: string = "hello";
    var b: [u8] = s.as_bytes();
    if (b.len() != 5) { return 1; }
    if (b[0] as i32 != 104) { return 2; }  // h
    if (b[4] as i32 != 111) { return 3; }  // o

    // The view sees the same bytes the copying constructor produces.
    var owned: u8[] = s.bytes();
    if (owned.len() != b.len()) { return 4; }
    var i: i32 = 0;
    while (i < owned.len()) {
        if ((owned[i] as i32) != (b[i] as i32)) { return 5; }
        i = i + 1;
    }

    // A view is a value: it passes to a function without copying.
    if (sum_view(b) != sum_view(s.as_bytes())) { return 6; }
    if (sum_view("abc".as_bytes()) != 294) { return 7; }  // 97+98+99

    // Empty string yields an empty, still-indexable-length view.
    if ("".as_bytes().len() != 0) { return 8; }

    // Works on a str receiver too -- a str is already a view, so this
    // is a reinterpretation rather than a copy.
    var v: str = utf8.substring("hello world", 0, 5);
    var vb: [u8] = v.as_bytes();
    if (vb.len() != 5) { return 9; }
    if (vb[0] as i32 != 104) { return 10; }
    if (sum_view(vb) != sum_view(s.as_bytes())) { return 11; }

    // Multi-byte UTF-8 is bytes, not scalars: the view length is the
    // BYTE length, which is exactly why this type exists.
    var e: string = "é";
    if (e.as_bytes().len() != 2) { return 12; }
    if (e.len() != 2) { return 13; }
    return 0;
}
`

func TestAsBytesViewInterp(t *testing.T) {
	if got := runInterpExit(t, asBytesViewProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestAsBytesViewX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, asBytesViewProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestAsBytesViewWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, asBytesViewProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestAsBytesViewArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, asBytesViewProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}

// bytesWriterViewProgram covers the consumer migrated off the copying
// `bytes()` in this change: BytesWriter.write_string only ever READS the
// bytes it appends, so the copy was pure waste.
//
// No wasm leg, and that is a PRE-EXISTING gap rather than a consequence
// of the migration: this program traps at runtime under wasm on the
// unmodified stdlib too (verified by re-running it with `bytes()`
// restored). The trap is in std/io_buffered on wasm generally, not in
// `as_bytes` — the view itself passes on all four backends above. Add
// the leg here once that gap is fixed.
const bytesWriterViewProgram = `
import "std/io_buffered" as iob;

function main(): i32 {
    var w = iob.bytes_writer_new();
    w = w.write_string("hello ");
    w = w.write_string("world");
    var d: u8[] = w.data;
    if (d.len() != 11) { return 1; }
    if (d[0] as i32 != 104) { return 2; }   // h
    if (d[6] as i32 != 119) { return 3; }   // w
    if (d[10] as i32 != 100) { return 4; }  // d

    // Empty and multi-byte strings still round-trip.
    var w2 = iob.bytes_writer_new();
    w2 = w2.write_string("");
    if (w2.data.len() != 0) { return 5; }
    w2 = w2.write_string("é");
    if (w2.data.len() != 2) { return 6; }
    return 0;
}
`

func TestBytesWriterViewInterp(t *testing.T) {
	if got := runInterpExit(t, bytesWriterViewProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestBytesWriterViewX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, bytesWriterViewProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestBytesWriterViewArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, bytesWriterViewProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
