package checker

import "testing"

func TestScopedBlockResultPreservesBorrowedInputs(t *testing.T) {
	t.Run("reader-borrowed-then", func(t *testing.T) {
		wantE051(t, "reader-borrowed-then", `struct Box { r: Reader, n: i32 }
function wrap(r: Reader): Box { return Box { r: r, n: 1 }; }
function take(own x: Box): i32 { return x.n; }
function f(c: boolean, r: Reader): i32 { let x = if (c) { wrap(r) } else { wrap(stdin()) }; return take(x); }
function main(): i32 { return 0; }
`)
	})
	t.Run("reader-retained-valid", func(t *testing.T) {
		wantE051(t, "reader-retained-valid", `struct Box { r: Reader, n: i32 }
function wrap(r: Reader): Box { return Box { r: r, n: 1 }; }
function take(own x: Box): i32 { return x.n; }
function observe(r: Reader): i32 { return 0; }
function f(c: boolean): i32 {
 let r = stdin();
 let x = if (c) { wrap(r) } else { wrap(stdin()) };
 let n = take(x);
 return n + observe(r);
}
function main(): i32 { return 0; }
`)
	})
	t.Run("captured-direct-public-gap", func(t *testing.T) {
		wantE051(t, "captured-direct-public-gap", `struct Handle { data: i32[] }
function (own h: Handle) close(): i32 { return h.data[0]; }
struct Box { h: Handle, n: i32 }
function make(n: i32): Handle { return Handle { data: [n] }; }
function wrap(h: Handle): Box { return Box { h: h, n: 1 }; }
function identity(b: Box): Box { return b; }
function take(own b: Box): i32 { return b.h.data[0] + b.n; }
function f(c: boolean, b: Box): i32 { let get = (): Box => b; let x = if (c) { get() } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }
`)
	})
}

func TestScopedBlockResultPreservesOwnedInputs(t *testing.T) {
	t.Run("checked-callback-arm", func(t *testing.T) {
		wantOK(t, "checked-callback-arm", `struct H { fd: i32 }
function (own h: H) close(): i32 { return h.fd; }
struct Box { h: H }
function make(): H { return H { fd: 7 }; }
function wrap(h: H): Box { return Box { h: h }; }
function take(own b: Box): i32 { return b.h.fd; }
function f(c: boolean): i32 { let get = (): own Box => Box { h: make() }; let x = if (c) { get() } else { wrap(make()) }; return take(x); } function main(): i32 { return 0; }`)
	})
	t.Run("owned-input-arms", func(t *testing.T) {
		wantOK(t, "owned-input-arms", `struct H { fd: i32 }
function (own h: H) close(): i32 { return h.fd; }
struct Box { h: H }
function make(): H { return H { fd: 7 }; }
function wrap(h: H): Box { return Box { h: h }; }
function take(own b: Box): i32 { return b.h.fd; }
function move(own h: H): Box { return Box { h: h }; } function f(c: boolean, own h: H): i32 { let x = if (c) { move(h) } else { move(make()) }; return take(x); } function main(): i32 { return 0; }`)
	})
}
