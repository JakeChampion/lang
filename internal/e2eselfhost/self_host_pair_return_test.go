package e2eselfhost

import (
	"strings"
	"testing"
)

// A function returning a variant whose payloads fit a word, and whose every
// caller takes the variant apart, returns its tag and the payload beside it
// (sempair). The program covers a declared enum, Option and Result; returns
// of constructions, of a parameter, of another paired call and of a recursive
// one; and functions that stay boxed: one a caller keeps the variant of, one
// used as a value and one with an i64 payload.
//
// The exit code: 1 for a wrong value, 2 for an allocation in the loop, which
// only matches on what the paired functions return, 99 for a count that went
// under.
const pairReturnProg = `import "std/bench";

enum Three { A(string), B, C(i32) }
struct Box2 { n: i32, s: string }

@noinline function pick(n: i32, s: string): Three {
  if (n % 3 == 0) {
    return A(s);
  }
  if (n % 3 == 1) {
    return B;
  }
  return C(n * 2);
}

@noinline function kept_pick(n: i32, s: string): Three {
  if (n % 3 == 0) {
    return A(s);
  }
  if (n % 3 == 1) {
    return B;
  }
  return C(n * 2);
}

@noinline function through(t: Three): Three {
  return t;
}

@noinline function opt(n: i32, s: string): Option[string] {
  if (n % 2 == 0) {
    return Some(s);
  }
  return None;
}

@noinline function res(n: i32, b: Box2): Result[Box2, i32] {
  if (n % 2 == 0) {
    return Ok(b);
  }
  return Err(n);
}

@noinline function fwd(n: i32, b: Box2): Result[Box2, i32] {
  return res(n + 1, b);
}

function depth(n: i32): Option[i32] {
  if (n == 0) {
    return Some(0);
  }
  match (depth(n - 1)) {
    Some(d) => {
      return Some(d + 1);
    },
    None => {
      return None;
    }
  }
}

@noinline function valued(n: i32): Option[i32] {
  if (n > 0) {
    return Some(n);
  }
  return None;
}

@noinline function wide(n: i32): Option[i64] {
  if (n > 0) {
    return Some(n as i64 * 4000000000 as i64);
  }
  return None;
}

function score(t: Three): i32 {
  match (t) {
    A(x) => {
      return x.len();
    },
    B => {
      return 100;
    },
    C(k) => {
      return k;
    }
  }
}

function main(): i32 {
  let s: string = "hey" + "".to_string();
  let b: Box2 = Box2 { n: 7, s: s };
  let total: i32 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    match (pick(i, s)) {
      A(x) => {
        total = total + x.len();
      },
      B => {
        total = total + 100;
      },
      C(k) => {
        total = total + k;
      }
    }
    match (opt(i, s)) {
      Some(x) => {
        total = total + x.len();
      },
      None => {
        total = total + 1000;
      }
    }
    match (fwd(i, b)) {
      Ok(x) => {
        total = total + x.n + x.s.len();
      },
      Err(e) => {
        total = total + e * 10000;
      }
    }
    match (depth(i)) {
      Some(d) => {
        total = total + d;
      },
      None => {}
    }
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  match (through(kept_pick(5, s))) {
    C(k) => {
      total = total + k;
    },
    _ => {}
  }
  let kept: Three[] = [];
  i = 0;
  while (i < 4) {
    kept = kept.append(kept_pick(i, s));
    i = i + 1;
  }
  let f: (i32) => Option[i32] = valued;
  match (f(5)) {
    Some(v) => {
      total = total + v;
    },
    None => {}
  }
  match (wide(3)) {
    Some(w) => {
      total = total + (w / 1000000000 as i64) as i32;
    },
    None => {}
  }
  for t in kept {
    total = total + score(t);
  }
  print(total.to_string());
  if (total != 366745) {
    return 1;
  }
  if (a1 != a0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

func TestSelfHostPairReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, pairReturnProg, target, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 {
				t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 99: a count went under)\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "live_bytes=0") {
				t.Errorf("%q, want every block freed", stderr)
			}
		})
	}
}
