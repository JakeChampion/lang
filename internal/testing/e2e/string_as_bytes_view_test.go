package e2e

import "testing"

const asBytesViewProgram = `
import "std/utf8" as utf8;

function sum_view(b: [u8]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    return t;
}

function main(): i32 {
    let s: string = "hello";
    let b: [u8] = s.as_bytes();
    if (b.len() != 5) { return 1; }
    if (b[0] as i32 != 104) { return 2; }  // h
    if (b[4] as i32 != 111) { return 3; }  // o

    // The view sees the same bytes the copying constructor produces.
    let owned: u8[] = s.bytes();
    if (owned.len() != b.len()) { return 4; }
    let i: i32 = 0;
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
    let v: str = utf8.substring("hello world", 0, 5);
    let vb: [u8] = v.as_bytes();
    if (vb.len() != 5) { return 9; }
    if (vb[0] as i32 != 104) { return 10; }
    if (sum_view(vb) != sum_view(s.as_bytes())) { return 11; }

    // Multi-byte UTF-8 is bytes, not scalars: the view length is the
    // BYTE length, which is exactly why this type exists.
    let e: string = "é";
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

const bytesWriterViewProgram = `
import "std/io_buffered" as iob;

function main(): i32 {
    let w = iob.bytes_writer_new();
    w = w.write_string("hello ");
    w = w.write_string("world");
    let d: u8[] = w.data;
    if (d.len() != 11) { return 1; }
    if (d[0] as i32 != 104) { return 2; }   // h
    if (d[6] as i32 != 119) { return 3; }   // w
    if (d[10] as i32 != 100) { return 4; }  // d

    // Empty and multi-byte strings still round-trip.
    let w2 = iob.bytes_writer_new();
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

const asBytesInlineViewLifetimeProgram = `
function tail(s: string): [u8] {
    let a: [u8] = s.as_bytes();
    let b: [u8] = a[1:3];
    return b;
}
function main(): i32 {
    let s: string = "hello";
    let t: string = "abc";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: [u8] = s.as_bytes();
        let b: [u8] = a[1:3];
        a = t.as_bytes();
        if (b.len() != 2 || (b[0] as i32) != 101 || (b[1] as i32) != 108) { bad = bad + 1; }
        if ((a[0] as i32) != 97) { bad = bad + 2; }
        let r: [u8] = tail(s);
        if (r.len() != 2 || (r[1] as i32) != 108) { bad = bad + 4; }
        let q: [u8] = s.as_bytes()[1:4];
        if (q.len() != 3 || (q[0] as i32) != 101) { bad = bad + 8; }
        i = i + 1;
    }
    return bad + __rc_underflow_count();
}
`

func TestAsBytesInlineViewLifetimeInterp(t *testing.T) {
	if got := runInterpExit(t, asBytesInlineViewLifetimeProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestAsBytesInlineViewLifetimeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, asBytesInlineViewLifetimeProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestAsBytesInlineViewLifetimeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, asBytesInlineViewLifetimeProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestAsBytesInlineViewLifetimeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, asBytesInlineViewLifetimeProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
