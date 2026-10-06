package e2e

import "testing"

// A local bound to an append on a BORROWED array parameter is released
// (#11487).
//
// The receiver's borrow taint reached the binding, so its scope-exit release
// was the flat __fern_rc_dec, which never frees: the copy the append made
// leaked on every call, and with string elements the pushed element leaked
// with it. The grow helper counts its result on both arms, so the binding owns
// a reference of its own and reclaims it.
func TestAppendOnBorrowedParamIsReleased(t *testing.T) {
	shapes := []struct{ name, src string }{
		{"i32_elements", `
function k(xs: i32[]): i32 {
  let ys: i32[] = xs.append(7);
  return ys.len();
}

function main(): i32 {
  let base: i32[] = [1, 2];
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 3) {
    t = t + k(base);
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (base.len() != 2) { return 1; }
  return t - 9;
}`},
		{"string_elements", `
function k(xs: string[], name: string): i32 {
  let ys: string[] = xs.append(name + "-suffix-0123456789abcdef");
  return ys.len();
}

function main(): i32 {
  let base: string[] = ["a", "b"];
  let name: string = "element";
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 3) {
    t = t + k(base, name);
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (base.len() != 2) { return 1; }
  return t - 9;
}`},
		// The argument dies at the call, so nothing brackets it and the
		// append grows the caller's buffer in place: the binding shares it
		// at rc 2, and its release must leave the caller's count standing.
		{"in_place_on_a_dying_argument", `
import "std/i32";

@noinline
function grow_len(xs: string[], s: string): i32 {
  let ys: string[] = xs.append(s);
  return ys.len();
}

@noinline
function grow_ret(xs: string[], s: string): string[] {
  let ys: string[] = xs.append(s);
  return ys;
}

@noinline
function mk(pfx: string, i: i32): string[] {
  let out: string[] = [];
  out = out.append(pfx + "-first-0123456789abcdef");
  out = out.append(pfx + "-second-0123456789abcdef" + i.to_string());
  return out;
}

function main(): i32 {
  let pfx: string = "prefix-value";
  let elem: string = pfx + "-pushed-0123456789abcdef";
  let bad: i32 = 0;
  let i: i32 = 0;
  while (i < 5) {
    if (grow_len(mk(pfx, i), elem) != 3) { bad = bad + 1; }
    let r: string[] = grow_ret(mk(pfx, i), elem);
    if (r.len() != 3 || r[0].len() != 35 || r[2].len() != elem.len()) { bad = bad + 1; }
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (bad != 0) { return 1; }
  return 0;
}`},
	}

	checkCensusOnEveryTarget(t, shapes,
		"the append result bound on a borrowed parameter leaks once per call (#11487)")
}
