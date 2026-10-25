package e2ecompiler

import (
	"strings"
	"testing"
)

// A record of scalars, or an Option of one, handed to a function the inliner
// leaves as a call passes in its fields, so building the argument allocates
// nothing.
const scalarParamProg = `import "std/bench";
import "std/time";

struct Gap { lo: i64, hi: i32 }

struct Held { span: Gap, n: i32 }

@noinline function width(s: Gap, k: i64): i64 {
  if (s.hi < 0) {
    return k;
  }
  return s.lo * k + s.hi as i64;
}

@noinline function forward(s: Gap, k: i64): i64 {
  if (k > 1000) {
    return 0;
  }
  return width(s, k + 1) + width(s, k);
}

@noinline function count(s: Gap, n: i32): i64 {
  if (n <= 0) {
    return s.lo;
  }
  return count(s, n - 1) + s.hi as i64;
}

@noinline function (s: Gap) scaled(k: i32): i64 {
  return s.lo * k as i64 - s.hi as i64;
}

@noinline function limit(t: Option[Gap], k: i64): i64 {
  let Some(s) = t else {
    return 0 - k;
  };
  return width(s, k) + s.lo;
}

@noinline function tested(t: Option[Gap]): i64 {
  if let Some(s) = t {
    return s.lo + s.hi as i64;
  }
  return 7;
}

@noinline function matched(t: Option[Gap], k: i64): i64 {
  match (t) {
    Some(s) => {
      return s.lo - k;
    },
    None => {
      return k;
    }
  }
  return 0;
}

@noinline function passed(t: Option[Gap], k: i64): i64 {
  return limit(t, k) + tested(t);
}

@noinline function small(t: Option[i64]): i64 {
  match (t) {
    Some(v) => {
      return v * 2;
    },
    None => {
      return 0 - 1;
    }
  }
  return 0;
}

@noinline function waited(t: Option[Duration]): i64 {
  let Some(d) = t else {
    return 0 - 1;
  };
  return d.as_millis();
}

@noinline function elapsed(a: Instant, b: Instant): i64 {
  return a.since(b).as_nanos();
}

@noinline function many(a: Gap, b: Gap, c: Gap, d: Gap, k: i64): i64 {
  return a.lo + b.lo * 2 + c.lo * 3 + d.lo * 4 + (a.hi + b.hi + c.hi + d.hi) as i64 + k;
}

@noinline function choose(n: i64): Option[Gap] {
  if (n % 3 == 0) {
    return None;
  }
  return Some(Gap { lo: n, hi: 2 });
}

function main(): i32 {
  let h: Held = Held { span: Gap { lo: 11, hi: 4 }, n: 3 };
  let base: Instant = Instant { sec: 0, nsec: 1000 };
  let total: i64 = 0;
  let a0: i64 = bench.alloc_count();
  let i: i64 = 0;
  while (i < 20) {
    let s: Gap = Gap { lo: i, hi: (i % 5) as i32 };
    total = total + width(s, 3) + forward(Gap { lo: i * 2, hi: 1 }, i) + count(s, 3) + s.scaled(2);
    total = total + width(h.span, i) + many(Gap { lo: i, hi: 1 }, Gap { lo: 2, hi: 2 }, s, h.span, i);
    let t: Option[Gap] = None;
    if (i % 2 == 0) {
      t = Some(Gap { lo: i + 1, hi: 0 - 1 });
    }
    total = total + limit(t, i) + tested(t) + matched(t, i) + passed(t, 2);
    total = total + limit(Some(Gap { lo: 3, hi: 3 }), i) + tested(None) + matched(choose(i), i);
    let o: Option[i64] = None;
    if (i % 3 == 1) {
      o = Some(i * 5);
    }
    total = total + small(o) + small(Some(i));
    total = total + waited(Some(time.duration_millis(i * 7))) + waited(None);
    total = total + elapsed(Instant { sec: i / 7, nsec: (i * 1000000 + 5000) as i32 }, base);
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 19190099710 as i64) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

// Slots the rewrite must leave whole: a record kept past the call, an Option
// read from a field, and a function named as a value. Only the answer and the
// census are pinned.
const scalarParamKeptProg = `struct Gap { lo: i64, hi: i32 }

struct Box { t: Option[Gap], s: Gap }

@noinline function keep(s: Gap, into: Gap[]): Gap[] {
  return into.append(s);
}

@noinline function stored(s: Gap, n: i32): Box {
  return Box { t: Some(s), s: s };
}

@noinline function first(t: Option[Gap]): i64 {
  let Some(s) = t else {
    return 0 - 1;
  };
  return s.lo;
}

@noinline function apply(s: Gap): i64 {
  return s.lo + s.hi as i64;
}

function call(f: (Gap) => i64, s: Gap): i64 {
  return f(s);
}

function main(): i32 {
  let kept: Gap[] = [];
  let total: i64 = 0;
  let i: i64 = 0;
  while (i < 10) {
    kept = keep(Gap { lo: i, hi: 1 }, kept);
    let b: Box = stored(Gap { lo: i * 3, hi: 2 }, 1);
    total = total + first(b.t) + first(None) + b.s.lo + call(apply, Gap { lo: i, hi: 9 });
    i = i + 1;
  }
  for s in kept {
    total = total + s.lo * s.hi as i64;
  }
  print(total.to_string());
  if (total != 440) {
    return 1;
  }
  return 0;
}
`

// A record returned in words stays so when a caller builds a record of a
// different size from its fields: only a construction of the same size could
// take its box.
const scalarParamDonorProg = `import "std/bench";

struct Pair { a: i64, b: i32 }

struct Quad { w: i64, x: i64, y: string, z: i32 }

@noinline function made(n: i64): Pair {
  if (n < 0) {
    return Pair { a: 0, b: 0 };
  }
  return Pair { a: n * 3, b: (n % 7) as i32 };
}

@noinline function quad(n: i64): Quad {
  let p: Pair = made(n);
  return Quad { w: p.a, x: p.b as i64, y: "q", z: 1 };
}

function main(): i32 {
  let total: i64 = quad(5).w;
  let a0: i64 = bench.alloc_count();
  let i: i64 = 0;
  while (i < 50) {
    let p: Pair = made(i);
    total = total + p.a + p.b as i64;
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 3837) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostScalarParam(t *testing.T) {
	cli := buildSelfHostCLI(t)
	progs := []struct{ name, src string }{
		{"lanes", scalarParamProg},
		{"kept", scalarParamKeptProg},
		{"donor", scalarParamDonorProg},
	}
	for _, prog := range progs {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(prog.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, prog.src, target, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
				if code != 0 {
					t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 99: a count went under)\n%s", code, stderr)
				}
				if !strings.Contains(stderr, "live_bytes=0") {
					t.Errorf("%q, want every block freed", stderr)
				}
			})
		}
	}
}
