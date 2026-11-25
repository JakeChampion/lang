package checker

import "testing"

const ownReplacementPrelude = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct Holder { value: Handle, n: i32 }
function consume(own h: Holder): i32 { return h.value.close(); }
function fresh(): Holder { return Holder { value: Handle { fd: 1 }, n: 0 }; }
function same(h: Holder): Holder { return h; }
function bump(own h: Holder): Holder { return Holder { ...h, n: h.n + 1 }; }
struct Step { h: Holder, err: string }
function step(own h: Holder): Step { return Step { h: bump(h), err: "" }; }
enum Box { Full(Handle), Empty }
function main(): i32 { return 0; }
`

func TestOwnReplacementRejectsBorrowedValues(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"field", `function f(own value: Holder, borrowed: Handle): i32 { value = Holder { value: borrowed, n: 0 }; return consume(value); }`},
		{"read-only", `function f(own value: Holder, borrowed: Handle): i32 { value = Holder { value: borrowed, n: 0 }; return value.n; }`},
		{"alias", `function f(own value: Holder, other: Holder): i32 { value = other; return consume(value); }`},
		{"helper", `function f(own value: Holder, other: Holder): i32 { value = same(other); return consume(value); }`},
		{"beside-self", `function f(own value: Holder, borrowed: Handle): i32 { value = Holder { value: borrowed, n: value.n }; return consume(value); }`},
		{"spread-self", `function f(own value: Holder, borrowed: Handle): i32 { value = Holder { ...value, value: borrowed }; return consume(value); }`},
		{"match-binder", `function f(own b: Box, other: Handle): i32 { match (b) { Full(h) => { h = other; return h.fd; }, Empty => { return 0; } } return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantE051(t, tc.name, ownReplacementPrelude+tc.src)
		})
	}
}

func TestOwnReplacementAcceptsOwnedValues(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"fresh-literal", `function f(own value: Holder): i32 { value = Holder { value: Handle { fd: 2 }, n: 0 }; return consume(value); }`},
		{"fresh-call", `function f(own value: Holder): i32 { let n = value.n; value = fresh(); return consume(value) + n; }`},
		{"spread-self", `function f(own value: Holder): i32 { value = Holder { ...value, n: value.n + 1 }; return consume(value); }`},
		{"self-update", `function f(own value: Holder): i32 { value = bump(value); return consume(value); }`},
		{"other-own-parameter", `function f(own value: Holder, own other: Holder): i32 { let n = consume(value); value = other; return consume(value) + n; }`},
		{"local-last-use", `function f(own value: Holder): i32 { let n = consume(value); let next = fresh(); value = next; return consume(value) + n; }`},
		{"owned-sibling-field", `function f(own value: Holder): i32 { let s = step(value); value = s.h; if (s.err.len() > 0) { return 1; } return consume(value); }`},
		{"handle-free", "struct P { xs: i32[] }\nfunction g(own p: P, q: P): i32 { p = q; return p.xs.len(); }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, tc.name, ownReplacementPrelude+tc.src)
		})
	}
}

const spreadFieldPathPrelude = `
struct Hs { hs: Handle[], xs: i32[], n: i32 }
struct Rd { c: Hs, ok: boolean }
function rd(own c: Hs): Rd { let ok = c.n > 0; return Rd { c: c, ok: ok }; }
function eat(own c: Hs): i32 { return c.n; }
`

// A literal with base q.f takes the reads of q.f.g that it replaces.
func TestSpreadFieldPathTakesReplacedReads(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"argument", `function g(own c: Hs): i32 { let r = rd(c); return eat(Hs { ...r.c, xs: r.c.xs.with(0, 1) }); }`},
		{"result", `function g(own c: Hs): own Hs { let r = rd(c); return Hs { ...r.c, xs: r.c.xs.with(0, 1) }; }`},
		{"assignment", `function g(own c: Hs): i32 { let r = rd(c); c = Hs { ...r.c, xs: r.c.xs.with(0, 1) }; if (r.ok) { return 1; } return eat(c); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, tc.name, ownReplacementPrelude+spreadFieldPathPrelude+tc.src)
		})
	}
	wantE051(t, "read-again", ownReplacementPrelude+spreadFieldPathPrelude+`function g(own c: Hs): i32 { let r = rd(c); let n = eat(Hs { ...r.c, xs: r.c.xs.with(0, 1) }); return n + r.c.n; }`)
}
