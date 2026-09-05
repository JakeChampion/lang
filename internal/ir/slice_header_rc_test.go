package ir

import "testing"

// A `[T]` slice is an rc1 view header (#8406): every consumer that used to
// leave a bare `__slice_make` block behind now releases it through
// __fern_closure_drop — the exit sweep for a local, the arg-temp release for
// `f(s.as_bytes())`, the data-pointer cast for `s.as_bytes() as usize`, and
// the parent of a sub-slice cut from a temp. Aliasing a header retains it,
// exactly like a tuple box.
//
// Counting note: every `var` of slice type carries TWO releases — the
// loop-body re-declaration drop at the binding (emitVarReinitDropOld,
// null-guarded on the first pass) and the exit sweep's — so a local counts
// as 2 and a temp as 1.

func sliceHeaderProgram(t *testing.T, src string, ptrW int) *Func {
	t.Helper()
	p := lowerSourceWith(t, src, ptrW)
	fn := findFunc(p, "main")
	if fn == nil {
		t.Fatalf("ptrW=%d: no main; ops:\n%v", ptrW, p)
	}
	return fn
}

func TestSliceLocalIsReleasedAtExit(t *testing.T) {
	const src = `
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var b: [u8] = s.as_bytes();
    return b.len();
}`
	for _, ptrW := range []int{4, 8} {
		fn := sliceHeaderProgram(t, src, ptrW)
		if n := countCallDirect(fn.Ops, "__fern_closure_drop"); n != 2 {
			t.Errorf("ptrW=%d: slice local b: want its reinit + exit header releases (2), got %d; ops:\n%v",
				ptrW, n, fn.Ops)
		}
		if n := countCallDirect(fn.Ops, "__fern_rc_dec"); n != 0 {
			t.Errorf("ptrW=%d: the header must be FREED (closure_drop), not merely dec'd (%d rc_dec); ops:\n%v",
				ptrW, n, fn.Ops)
		}
	}
}

func TestSliceArgTempIsReleasedAfterCall(t *testing.T) {
	const src = `
function total(b: [u8]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    return total(s.as_bytes());
}`
	for _, ptrW := range []int{4, 8} {
		fn := sliceHeaderProgram(t, src, ptrW)
		if n := countCallDirect(fn.Ops, "__fern_closure_drop"); n != 1 {
			t.Errorf("ptrW=%d: the fresh header handed to total() must be released once "+
				"after the call, got %d releases; ops:\n%v", ptrW, n, fn.Ops)
		}
		// The callee borrows its parameter: no release of b inside total.
		p := lowerSourceWith(t, src, ptrW)
		if callee := findFunc(p, "total"); callee != nil {
			if n := countCallDirect(callee.Ops, "__fern_closure_drop"); n != 0 {
				t.Errorf("ptrW=%d: total() borrows b and must not release it (%d releases); ops:\n%v",
					ptrW, n, callee.Ops)
			}
		}
	}
}

func TestSliceTempCastToUsizeReleasesHeader(t *testing.T) {
	const src = `
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var out: usize = __alloc(64);
    __memcpy(out, s.as_bytes() as usize, s.len());
    return __load_u8(out) as i32;
}`
	for _, ptrW := range []int{4, 8} {
		fn := sliceHeaderProgram(t, src, ptrW)
		if n := countCallDirect(fn.Ops, "__fern_closure_drop"); n != 1 {
			t.Errorf("ptrW=%d: `s.as_bytes() as usize` must release the header once its "+
				"data pointer is out, got %d releases; ops:\n%v", ptrW, n, fn.Ops)
		}
	}
}

func TestSliceAliasRetainsHeader(t *testing.T) {
	const src = `
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var a: [u8] = s.as_bytes();
    var b: [u8] = a;
    return a.len() + b.len();
}`
	for _, ptrW := range []int{4, 8} {
		fn := sliceHeaderProgram(t, src, ptrW)
		if n := countCallDirect(fn.Ops, "__fern_rc_inc"); n != 1 {
			t.Errorf("ptrW=%d: `b = a` must retain the shared header once, got %d incs; ops:\n%v",
				ptrW, n, fn.Ops)
		}
		if n := countCallDirect(fn.Ops, "__fern_closure_drop"); n != 4 {
			t.Errorf("ptrW=%d: both locals release the shared header (the exit sweep's second one frees it): "+
				"want 2 reinit + 2 exit releases, got %d; ops:\n%v", ptrW, n, fn.Ops)
		}
	}
}

func TestSubSliceOfTempReleasesParentHeader(t *testing.T) {
	const src = `
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var c: [u8] = s.as_bytes()[1:3];
    return c.len();
}`
	for _, ptrW := range []int{4, 8} {
		fn := sliceHeaderProgram(t, src, ptrW)
		// One for the parent temp once the child header is built, two for c.
		if n := countCallDirect(fn.Ops, "__fern_closure_drop"); n != 3 {
			t.Errorf("ptrW=%d: want the parent temp released after the sub-slice plus c's reinit + exit "+
				"(3 releases), got %d; ops:\n%v", ptrW, n, fn.Ops)
		}
	}
}

func TestSliceArrayDropWalksHeaders(t *testing.T) {
	const src = `
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var arr: [u8][] = [s.as_bytes(), s.as_bytes()];
    return arr[1].len();
}`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "main")
		if n := countCallDirect(fn.Ops, "__drop_arr_slice"); n == 0 {
			t.Errorf("ptrW=%d: a `[u8][]` local must drop through __drop_arr_slice; ops:\n%v", ptrW, fn.Ops)
		}
		drop := findFunc(p, "__drop_arr_slice")
		if drop == nil {
			t.Fatalf("ptrW=%d: __drop_arr_slice was routed to but never generated; program:\n%s", ptrW, p)
		}
		if n := countCallDirect(drop.Ops, "__fern_closure_drop"); n != 1 {
			t.Errorf("ptrW=%d: __drop_arr_slice must free each element header via __fern_closure_drop, got %d; ops:\n%v",
				ptrW, n, drop.Ops)
		}
	}
}

// The LENT view header (#8502) is released through the header's own protocol,
// not a raw `__free`.
//
// The lend reclaim landed against the pre-#8406 contract, where a header was a
// bare `__fern_alloc` block with no rc header, and freed it with
// `__free(header, 2*ptrW)`. Once the header carries an rc1 header that call is
// wrong on all three arguments — `__free` takes the block BASE and the data
// pointer is base+8, the block is 8 bytes longer than the payload, and an
// aliased header must be dec'd rather than freed. Freeing base+8 at the payload
// size does not merely strand the block: it pushes a block overlapping a live
// one onto that size class's freelist.
//
// Both producers are pinned — the synthesised full-range lend of an owned
// array, and `as_bytes` — since makesFreshViewHeader admits exactly those two.
func TestLentViewHeaderReleasedThroughHeaderDrop(t *testing.T) {
	cases := []struct{ name, src string }{
		{"lend of an owned array", `
function total(src: [u8], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    var b: u8[] = __alloc_u8(8);
    return total(b, 8);
}`},
		{"as_bytes at an argument position", `
function total(src: [u8], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    return total(s.as_bytes(), 5);
}`},
	}
	for _, c := range cases {
		for _, ptrW := range []int{4, 8} {
			fn := sliceHeaderProgram(t, c.src, ptrW)
			if n := countCallDirect(fn.Ops, "__free"); n != 0 {
				t.Errorf("%s (ptrW=%d): the lent header is released with a raw __free (%d call(s)) — "+
					"it is an rc1 block, so __free gets the wrong base, the wrong size and no rc gate; ops:\n%v",
					c.name, ptrW, n, fn.Ops)
			}
			if countCallDirect(fn.Ops, "__fern_closure_drop") == 0 {
				t.Errorf("%s (ptrW=%d): the lent header is never released; ops:\n%v", c.name, ptrW, fn.Ops)
			}
		}
	}
}
