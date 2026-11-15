package e2e

import "testing"

// Each local is a struct literal, with or without a spread base, that goes to
// an own parameter. Each field value that it sets is fresh, an own parameter,
// a local at its last use, a scalar, a counted string copy, or such a literal.
// A borrowed base keeps no handle. The struct holds two open Readers for odd
// i, a counted string and a scalar. R holds an open Reader in each round.
const literalFieldOwnedSrc = `import "std/option";

struct H { r: Option[Reader], alt: Option[Reader], name: string, n: i32 }
struct K { h: H, n: i32 }
struct I { r: Reader, open: boolean, n: i32 }
struct R { inp: I, n: i32 }
struct B { r: Option[Reader], name: string, n: i32 }

@noinline function handle(i: i32): Option[Reader] {
  if (i % 2 == 0) {
    return None;
  }
  match (open_reader("/dev/null")) {
    Ok(r) => { return Some(r); },
    Err(_) => { return None; },
  }
}
@noinline function mk(i: i32): H {
  return H { r: handle(i), alt: handle(i), name: "name " + i.to_string(), n: i };
}
@noinline function eat(own h: H): i32 {
  let open = if (h.r.is_some()) { 7 } else { 0 };
  let other = if (h.alt.is_some()) { 5 } else { 0 };
  return h.n + h.name.len() as i32 + open + other;
}

@noinline function spread_fresh(own u: H, i: i32): i32 {
  let v = H { ...u, alt: handle(i) };
  return eat(v);
}
@noinline function spread_own_param(own u: H, own r: Option[Reader]): i32 {
  let v = H { ...u, alt: r };
  return eat(v);
}
@noinline function spread_local(own u: H, own r: Option[Reader]): i32 {
  let x = r;
  let v = H { ...u, alt: x };
  return eat(v);
}
@noinline function spread_scalar(own u: H, b: H): i32 {
  let v = H { ...u, n: b.n + 1 };
  return eat(v);
}
@noinline function spread_string(own u: H, b: H): i32 {
  let v = H { ...u, name: b.name };
  return eat(v);
}
@noinline function plain_own(own r: Option[Reader], own alt: Option[Reader], b: H): i32 {
  let v = H { r: r, alt: alt, name: b.name, n: b.n };
  return eat(v);
}
@noinline function plain_fresh(i: i32, b: H): i32 {
  let v = H { r: handle(i), alt: None, name: b.name + "!", n: 2 };
  return eat(v);
}
@noinline function eatk(own k: K): i32 {
  return k.n + eat(k.h);
}
@noinline function nested_own(own u: K, own h: H): i32 {
  let v = K { ...u, h: H { ...h, n: h.n + 1 } };
  return eatk(v);
}
@noinline function nested_local(own u: K, own h: H, i: i32): i32 {
  let x = H { ...h, name: h.name + "!" };
  let v = K { ...u, h: H { ...x, alt: handle(i) } };
  return eatk(v);
}
@noinline function mkr(i: i32): R {
  match (open_reader("/dev/null")) {
    Ok(r) => { return R { inp: I { r: r, open: true, n: i }, n: i }; },
    Err(_) => { return R { inp: I { r: stdin(), open: false, n: i }, n: i }; },
  }
}
@noinline function eatr(own s: R): i32 {
  let open = if (s.inp.open) { 3 } else { 0 };
  return s.n + s.inp.n + open;
}
@noinline function opened(own s: R, i: i32): i32 {
  let inp = I { ...s.inp, n: s.inp.n + i };
  let fresh = R { ...s, n: 0 };
  match (open_reader("/dev/null")) {
    Ok(r) => {
      let v = R { ...fresh, inp: I { ...inp, r: r, open: true } };
      if (i % 2 == 0) {
        return eatr(v) + 1;
      }
      return eatr(v);
    },
    Err(_) => { return eatr(fresh); },
  }
}
@noinline function eatb(own b: B): i32 {
  let open = if (b.r.is_some()) { 7 } else { 0 };
  return b.n + b.name.len() as i32 + open;
}
@noinline function kept_base(b: B, i: i32): i32 {
  let v = B { ...b, r: handle(i) };
  return eatb(v);
}

function main(): i32 {
  let t = 0;
  for i in 0..20 {
    let b = mk(i);
    t += spread_fresh(mk(i), i) + spread_own_param(mk(i), handle(i)) + spread_local(mk(i), handle(i));
    t += spread_scalar(mk(i), b) + spread_string(mk(i), b);
    t += plain_own(handle(i), handle(i), b) + plain_fresh(i, b);
    t += nested_own(K { h: mk(i), n: 1 }, mk(i + 1)) + nested_local(K { h: mk(i), n: 2 }, mk(i + 1), i);
    t += opened(mkr(i), i) + kept_base(B { r: handle(i), name: "b" + i.to_string(), n: i }, i);
    t += b.n;
  }
  return t % 256;
}
`

func TestLiteralFieldOwnedX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, literalFieldOwnedSrc, runInterpByte(t, literalFieldOwnedSrc), runSanitizeX86_64)
}

func TestLiteralFieldOwnedArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, literalFieldOwnedSrc, runInterpByte(t, literalFieldOwnedSrc), runSanitizeArm64)
}
