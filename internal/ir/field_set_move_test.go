package ir_test

import "testing"

// The `.with` half of the superseded-field move: `h = H { ...h, buf:
// h.buf.with(i, v) }` hands `h.buf` to __fern_arr_cow_inplace as a move out
// of h's box — the is_unique(h) test that empties the slot on the unique
// branch and retains on the shared one — instead of the projection inc that
// forced the copy path on every call. Rebind and return forms, an `own`
// param, a plain param and a local, and a `.with` chain whose innermost
// receiver is the field.

const fieldSetMoveSrc = `struct H { buf: u8[], n: i32 }
function stepOwn(own h: H, b: u8): H { h = H { ...h, buf: h.buf.with(h.n, b), n: h.n + 1 }; return h; }
function stepRet(own h: H, b: u8): H { return H { ...h, buf: h.buf.with(h.n, b), n: h.n + 1 }; }
function stepParam(h: H, b: u8): H { h = H { ...h, buf: h.buf.with(h.n, b), n: h.n + 1 }; return h; }
function stepChain(own h: H, b: u8): H { return H { ...h, buf: h.buf.with(0, b).with(1, b), n: 2 }; }
function stepLocal(b: u8): i32 { var h: H = H { buf: __alloc_u8(4), n: 0 }; h = H { ...h, buf: h.buf.with(0, b), n: 1 }; return h.n; }
function main(): i32 { var h: H = H { buf: __alloc_u8(4), n: 0 }; h = stepOwn(h, 1 as u8); h = stepRet(h, 2 as u8); h = stepParam(h, 3 as u8); h = stepChain(h, 4 as u8); return h.n + stepLocal(5 as u8); }`

func TestFieldSetMoveTestsUniquenessBeforeCow(t *testing.T) {
	ip := lowerForTest(t, fieldSetMoveSrc)
	for _, fn := range []string{"stepOwn", "stepRet", "stepParam", "stepChain", "stepLocal"} {
		f := fnNamed(t, ip, fn)
		if !uniqueTestBeforeCall(f, "__fern_arr_cow_inplace") {
			t.Errorf("%s passes `h.buf` to __fern_arr_cow_inplace without the is_unique-gated move:\n%s", fn, ip)
		}
	}
}

// The refusals: a literal bound to a NEW name leaves the old box readable
// through its own field, and a second read of the field inside the literal
// evaluates against the pre-store box. Both keep the projection inc.
func TestFieldSetMoveRefusesReadableBox(t *testing.T) {
	ip := lowerForTest(t, `struct H { buf: u8[], n: i32 }
function bindNew(own h: H, b: u8): i32 { var g: H = H { ...h, buf: h.buf.with(0, b) }; return (h.buf[0] as i32) + (g.buf[0] as i32); }
function readTwice(own h: H, b: u8): H { return H { ...h, buf: h.buf.with(0, b), n: h.buf.len() }; }
function main(): i32 { var h: H = H { buf: __alloc_u8(4), n: 0 }; h = readTwice(h, 1 as u8); return h.n + bindNew(H { buf: __alloc_u8(4), n: 0 }, 2 as u8); }`)
	for _, fn := range []string{"bindNew", "readTwice"} {
		f := fnNamed(t, ip, fn)
		if uniqueTestBeforeCall(f, "__fern_arr_cow_inplace") {
			t.Errorf("%s moves `h.buf` out of a box that is still read:\n%s", fn, ip)
		}
		if retainsBeforeCall(f, "__fern_arr_cow_inplace") == 0 {
			t.Errorf("%s hands a still-owned field to __fern_arr_cow_inplace with no retain:\n%s", fn, ip)
		}
	}
}
