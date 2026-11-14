package e2e

import "testing"

// Each element read of a fresh call result goes to an own parameter. The read
// must take its element and release the other element. The tuple holds a
// string beside a struct with a counted string, an array and, for odd i, an
// open Reader.
const tupleProjectionOwnedSrc = `import "std/i32";

struct R { a: string[], s: string, n: i32, t: Option[Reader] }
struct W { r: R, k: i32 }

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
  return R { a: ["a" + i.to_string()], s: "seed " + i.to_string(), n: i, t: handle(i) };
}
@noinline function bump(own r: R): (string, R) {
  return ("tag " + r.n.to_string(), R { ...r, n: r.n + 1 });
}
@noinline function swap(own r: R): (R, string) {
  return (R { ...r, a: r.a.append("b" + r.s), n: r.n + 2 }, "left " + r.s);
}
@noinline function nest(own r: R): (string, (string, R)) {
  return ("outer " + r.s, ("inner " + r.s, R { ...r, n: r.n + 3 }));
}
@noinline function opt(own r: R): (string, Option[R]) {
  return ("opt " + r.s, Some(r));
}
@noinline function eat(own r: R): i32 {
  return r.n + r.a.len() as i32 + r.s.len() as i32;
}
@noinline function takes(own s: string): i32 {
  return s.len() as i32;
}
@noinline function step(own run: R): i32 {
  run = bump(run).1;
  return eat(run);
}
@noinline function direct(own run: R): i32 {
  return eat(bump(run).1);
}
@noinline function first(own run: R): i32 {
  return takes(bump(run).0);
}
@noinline function zero(own run0: R): i32 {
  let run = R { ...run0, s: "local" };
  run = swap(run).0;
  return eat(run);
}
@noinline function letproj(own run0: R): i32 {
  let run = R { ...run0, s: "local" };
  let r1 = bump(run).1;
  return eat(r1);
}
@noinline function nested(own run0: R): i32 {
  let run = R { ...run0, s: "local" };
  run = nest(run).1.1;
  return eat(run);
}
@noinline function looped(own run0: R): i32 {
  let run = R { ...run0, n: 0 };
  let i = 0;
  while (i < 5) {
    run = bump(run).1;
    i += 1;
  }
  return eat(run);
}

@noinline function result_own(own r: R): own R {
  return bump(r).1;
}
@noinline function eatw(own w: W): i32 {
  return w.k + eat(w.r);
}
@noinline function in_literal(own r: R): i32 {
  return eatw(W { r: bump(r).1, k: 3 });
}
@noinline function scrutinee(own r: R): i32 {
  match (opt(r).1) {
    Some(x) => { return eat(x); },
    None => { return 0; },
  }
  return 0;
}
@noinline function let_else(own r: R): i32 {
  let Some(x) = opt(r).1 else { return 0; };
  return eat(x);
}

function main(): i32 {
  let t = 0;
  let i = 0;
  while (i < 20) {
    t += step(mk(i)) + direct(mk(i)) + first(mk(i)) + zero(mk(i));
    t += letproj(mk(i)) + nested(mk(i)) + looped(mk(i));
    t += eat(result_own(mk(i))) + in_literal(mk(i)) + scrutinee(mk(i)) + let_else(mk(i));
    i += 1;
  }
  return t % 256;
}
`

func TestTupleProjectionOwnedX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, tupleProjectionOwnedSrc, runInterpByte(t, tupleProjectionOwnedSrc), runSanitizeX86_64)
}

func TestTupleProjectionOwnedArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, tupleProjectionOwnedSrc, runInterpByte(t, tupleProjectionOwnedSrc), runSanitizeArm64)
}
