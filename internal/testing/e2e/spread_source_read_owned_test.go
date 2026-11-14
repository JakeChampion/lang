package e2e

import "testing"

// Each local is a struct literal that spreads an own parameter and reads it
// in a field value before the literal takes it. The local then goes to an own
// parameter. The struct holds counted strings, a string array, byte arrays
// and, for odd i, an open Reader.
const spreadSourceReadOwnedSrc = `import "std/array";
import "std/i32";
import "std/option";

struct R { a: string[], s: string, w: string, n: i32, m: i32, bs: u8[], cs: u8[], t: Option[Reader] }

@noinline function handle(i: i32): Option[Reader] {
  if (i % 2 == 0) {
    return None;
  }
  match (open_reader("/dev/null")) {
    Ok(r) => { return Some(r); },
    Err(_) => { return None; },
  }
}
@noinline function mk(i: i32): R {
  return R { a: ["a" + i.to_string(), "b"], s: "seed " + i.to_string(), w: "w" + i.to_string(), n: i, m: 1, bs: [1u8, 2u8], cs: [], t: handle(i) };
}
@noinline function eat(own r: R): i32 {
  let open = if (r.t.is_some()) { 7 } else { 0 };
  return r.n + r.m + r.a.len() as i32 + r.s.len() as i32 + r.w.len() as i32 + r.bs.len() as i32 + r.cs.len() as i32 + open;
}
@noinline function size(r: R): i32 { return r.a.len() as i32 + r.s.len() as i32; }
@noinline function count(xs: string[]): i32 { return xs.len() as i32; }
@noinline function view(xs: [string]): i32 { return xs.len() as i32; }
@noinline function has(t: Option[Reader]): i32 { return if (t.is_some()) { 3 } else { 4 }; }

@noinline function kept_method(own u: R): i32 {
  let v = R { ...u, n: u.a.len() as i32 };
  return eat(v);
}
@noinline function whole_borrow(own u: R): i32 {
  let v = R { ...u, n: size(u) };
  return eat(v);
}
@noinline function kept_arg(own u: R): i32 {
  let v = R { ...u, n: count(u.a) };
  return eat(v);
}
@noinline function kept_view(own u: R): i32 {
  let v = R { ...u, n: view(u.a) };
  return eat(v);
}
@noinline function kept_scalar(own u: R): i32 {
  let v = R { ...u, m: u.n };
  return eat(v);
}
@noinline function kept_string_op(own u: R): i32 {
  let v = R { ...u, s: u.w + "!" };
  return eat(v);
}
@noinline function kept_element(own u: R): i32 {
  let v = R { ...u, n: u.a[0].len() as i32 + if (u.a[1] == "b") { 1 } else { 2 } + u.w[0] as i32 };
  return eat(v);
}
@noinline function kept_bytes(own u: R): i32 {
  let v = R { ...u, cs: u.bs };
  return eat(v);
}
@noinline function handle_recv(own u: R): i32 {
  let v = R { ...u, n: if (u.t.is_some()) { 1 } else { 2 } };
  return eat(v);
}
@noinline function handle_arg(own u: R): i32 {
  let v = R { ...u, n: has(u.t) };
  return eat(v);
}
@noinline function replaced_append(own u: R): i32 {
  let v = R { ...u, a: u.a.append(u.w) };
  return eat(v);
}
@noinline function replaced_concat(own u: R, more: [u8]): i32 {
  let v = R { ...u, bs: u.bs.concat(more) };
  return eat(v);
}
@noinline function replaced_scalar(own run0: R): i32 {
  let run = R { ...run0, n: run0.n + 1 };
  return eat(run);
}
@noinline function mixed(own u: R): i32 {
  let v = R { ...u, n: u.a.len() as i32 + size(u), s: u.s + u.w, a: u.a.append(u.a[1]) };
  return eat(v);
}

function main(): i32 {
  let t = 0;
  let i = 0;
  while (i < 20) {
    t += kept_method(mk(i)) + whole_borrow(mk(i)) + kept_arg(mk(i)) + kept_view(mk(i));
    t += kept_scalar(mk(i)) + kept_string_op(mk(i)) + kept_element(mk(i)) + kept_bytes(mk(i));
    t += handle_recv(mk(i)) + handle_arg(mk(i)) + replaced_append(mk(i));
    t += replaced_concat(mk(i), [3u8]) + replaced_scalar(mk(i)) + mixed(mk(i));
    i += 1;
  }
  return t % 256;
}
`

func TestSpreadSourceReadOwnedX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, spreadSourceReadOwnedSrc, runInterpByte(t, spreadSourceReadOwnedSrc), runSanitizeX86_64)
}

func TestSpreadSourceReadOwnedArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, spreadSourceReadOwnedSrc, runInterpByte(t, spreadSourceReadOwnedSrc), runSanitizeArm64)
}
