package e2e

import "testing"

// A pair-form match payload handed to a callee that only BORROWS it (#10669):
// the callee retains what it keeps, so the payload's own count stays the
// caller's, and the arm has to release it. std/http's header parser is the
// shape — `Some(v) => { h = h.append(name, v); }` over the field value's
// Option, with `HeaderMap.append` pushing a retained `value` into the map —
// and it leaked one value string per header line. Binding the payload to a
// var first balanced, which is what hid it.
const pairPayloadBorrowedArgSrc = `import "std/headers";
import "std/string";
function bytes_string(buf: u8[], from: i32, end: i32): string {
    var out: u8[] = __alloc_u8(end - from);
    var i: i32 = 0;
    while (from + i < end) { out = out.with(i, buf[from + i]); i = i + 1; }
    return string_from_bytes_unchecked(out);
}
function find_byte(buf: u8[], from: i32, end: i32, b: u8): i32 {
    var i: i32 = from;
    while (i < end) { if (buf[i] == b) { return i; } i = i + 1; }
    return -1;
}
function field_value(buf: u8[], from: i32, end: i32): Option[string] {
    if (end <= from) { return None; }
    return Some(bytes_string(buf, from, end));
}
function parse(buf: u8[]): Option[HeaderMap] {
    var h: HeaderMap = headers.header_map_new();
    var at: i32 = 0;
    while (at < buf.len()) {
        var e: i32 = find_byte(buf, at, buf.len(), 10 as u8);
        if (e < 0) { e = buf.len(); }
        var col: i32 = find_byte(buf, at, e, 58 as u8);
        if (col < 0) { return None; }
        match (field_value(buf, col + 1, e)) {
            Some(v) => { h = h.append(bytes_string(buf, at, col), v); },
            None => { return None; }
        }
        at = e + 1;
    }
    return Some(h);
}
function main(): i32 {
    var block: u8[] = "Host: localhost.localdomain\nX-A: one-two-three-four\nX-B: two-three-four-five\n".bytes();
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        match (parse(block)) {
            Some(h) => { n = n + h.len(); },
            None => { return 1; }
        }
        i = i + 1;
    }
    return n - 300;
}
`

func TestLeakCheckPairPayloadBorrowedArgX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, pairPayloadBorrowedArgSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the payload handed to the borrowing append is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckPairPayloadBorrowedArgArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, pairPayloadBorrowedArgSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the payload handed to the borrowing append is not released", code, allocs, frees, live)
	}
}
