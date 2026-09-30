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

// The same payload handed to a container sink (#10696): `Array.append` and
// `Map.insert` retain an aliased element, key or value, so the container holds
// a count of its own and the arm releases the payload's. The program's exit
// carries `__rc_underflow_count()`, since admitting a sink that does not
// retain would be a double free.
func pairPayloadSinkSrc(decl, body, tail string) string {
	return `import "core/map";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    var b: u8[] = __alloc_u8(8);
    return Some(string_from_bytes_unchecked(b));
}
function main(): i32 {
    ` + decl + `
    var i: i32 = 0;
    while (i < 100) {
        match (mk(i)) { Some(v) => { ` + body + ` }, None => { return 1; } }
        i = i + 1;
    }
    return ` + tail + ` + __rc_underflow_count();
}
`
}

var pairPayloadSinkCases = []struct{ name, src string }{
	{"array-append", pairPayloadSinkSrc(`var xs: string[] = [];`, `xs = xs.append(v);`, `xs.len() - 100`)},
	{"map-value", pairPayloadSinkSrc(`var m: Map[string, string] = map_new(4);`, `m = m.insert("k", v);`, `m.len() - 1`)},
	{"map-key", pairPayloadSinkSrc(`var m: Map[string, i32] = map_new(4);`, `m = m.insert(v, i);`, `m.len() - 1`)},
}

func TestLeakCheckPairPayloadSinkX86_64(t *testing.T) {
	for _, tc := range pairPayloadSinkCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			allocs, frees, live := leakSummaryIn(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
			}
		})
	}
}

func TestLeakCheckPairPayloadSinkWasm(t *testing.T) {
	for _, tc := range pairPayloadSinkCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckWasm(t, tc.src, false)
			allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
			}
		})
	}
}
