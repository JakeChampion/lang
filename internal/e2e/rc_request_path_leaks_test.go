package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// Three shapes the serve loop and the request parser are built from, each
// of which stranded one allocation per request on the Go compiler while the
// self-host build was flat: a fresh buffer handed to a pair-form parser whose
// payload holds a copy of it, a local seeded from an element of a parameter's
// array field and then reassigned, and a match payload passed to a method
// that copies its bytes out. Each program's answer is checked, and the
// census must balance on every backend.
func TestRequestPathTempsAreReleased(t *testing.T) {
	cases := []struct {
		name string
		want int
		src  string
	}{
		{"fresh-buffer-to-a-pair-form-parser", 20, `struct S { data: u8[] }
struct R { body: S }
function copy2(buf: u8[]): u8[] { var out: u8[] = __alloc_u8(2); out = out.with(0, buf[0]); out = out.with(1, buf[1]); return out; }
function wrap(bs: u8[]): S { return S { data: bs }; }
function parse(buf: u8[]): Option[R] { if (buf.len() < 2) { return None; } return Some(R { body: wrap(copy2(buf)) }); }
function mk(n: i32): u8[] { var out: u8[] = __alloc_u8(n); return out; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        match (parse(mk(3 + i))) { Some(r) => { t = t + r.body.data.len(); }, None => { t = t + 100; } }
        i = i + 1;
    }
    return t;
}`},
		{"element-of-a-parameter-field-then-reassigned", 33, `struct C { bufs: u8[][] }
function read(c: C, at: i32, extra: u8[]): i32 {
    var buf: u8[] = c.bufs[at];
    var i: i32 = 0;
    while (i < extra.len()) { buf = buf.append(extra[i]); i = i + 1; }
    return buf.len();
}
function main(): i32 {
    var c: C = C { bufs: [[1 as u8, 2 as u8], [3 as u8]] };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        t = t + read(c, i % 2, [9 as u8, 9 as u8]);
        i = i + 1;
    }
    return t - 2;
}`},
		{"payload-passed-to-a-copying-method", 30, `struct S { data: u8[] }
struct R { path: string, body: S }
function mkr(n: i32): Option[R] { return Some(R { path: "abc", body: S { data: __alloc_u8(n) } }); }
function (r: R) body_string(): string { return string_from_bytes_unchecked(r.body.data); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        match (mkr(i + 8)) { Some(r) => { t = t + r.body_string().len() - i - 5; }, None => { return 1; } }
        i = i + 1;
    }
    return t;
}`},
		// A pair-form callee's pointer payload arrives counted by the return
		// ABI, so the caller owns it whether the callee built it or retained
		// an alias of a parameter's element (`Some(h.values[i])`, the shape
		// every HeaderMap lookup takes). Each consumer of that count: the
		// statement match, the expression match, a local, a `?` binding, and
		// a return from inside the arm.
		{"pair-form-alias-payload-consumed-five-ways", 40, `struct H { names: string[], values: string[] }
function heap(i: i32): string { var s: string = "keep-alive-"; return s + ("!" + "!") + (if (i % 2 == 0) { "x" } else { "yy" }); }
function get(h: H, name: string): Option[string] {
    var i: i32 = 0;
    while (i < h.names.len()) { if (h.names[i] == name) { return Some(h.values[i]); } i = i + 1; }
    return None;
}
function via_match(h: H): i32 { match (get(h, "connection")) { Some(v) => { return v.len(); }, None => {} } return 0; }
function via_expr(h: H): i32 { return match (get(h, "connection")) { Some(v) => v.len(), None => 0 }; }
function via_local(h: H): i32 { var o: Option[string] = get(h, "connection"); match (o) { Some(v) => { return v.len(); }, None => {} } return 0; }
function via_try(h: H): Option[i32] { var v: string = get(h, "connection")?; return Some(v.len()); }
function via_return(h: H): Option[string] { match (get(h, "connection")) { Some(v) => { return Some(v); }, None => { return None; } } return None; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        var h: H = H { names: ["connection"], values: [heap(i)] };
        t = t + via_match(h) + via_expr(h) + via_local(h);
        match (via_try(h)) { Some(n) => { t = t + n; }, None => { return 1; } }
        match (via_return(h)) { Some(v) => { t = t + v.len(); }, None => { return 2; } }
        i = i + 1;
    }
    return t - 540;
}`},
		// A fresh array handed to a callee that reassigns and returns its
		// parameter comes back one count heavy when the callee hands it
		// straight back (no rebind, or a push that kept the pointer): the
		// bare return carries the transfer inc. The serve loop's
		// `__with_backlog(conns, drv.wait(...))` is this shape on every wait.
		{"fresh-array-returned-unchanged-by-a-threading-callee", 40, `function grow(a: i32[], more: boolean): i32[] { if (more) { a = a.append(7); } return a; }
function mk(n: i32): i32[] { var v: i32[] = []; var i: i32 = 0; while (i < n) { v = v.append(i); i = i + 1; } return v; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var a: i32[] = grow([1, 2, 3], i % 2 == 0);
        var b: i32[] = grow(mk(2), i % 3 == 0);
        t = t + a.len() + b.len();
        i = i + 1;
    }
    return t - 19;
}`},
	}
	for _, c := range cases {
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, c.src, c.want, runSanitizeX86_64)
		})
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, c.src, c.want, runSanitizeArm64)
		})
		t.Run("wasm-leakcheck/"+c.name, func(t *testing.T) {
			stdout, stderr, _ := runLeakCheckWasm(t, c.src, true)
			if got := strings.TrimSpace(stdout); got != strconv.Itoa(c.want) {
				t.Fatalf("result=%q, want %d\n%s", got, c.want, stderr)
			}
			if strings.Contains(stderr, "fern-sanitizer:") {
				t.Errorf("sanitizer finding: %q", stderr)
			}
			allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
}
