package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The callees live in another module and are never spliced, so every call
// crosses a function boundary: a record of scalar fields, an Option of one,
// and an Option of a scalar travel as their parts, through a free function,
// a method, a trait impl reached by a generic, and a parameter handed on.
// A split parameter is also the base of a record update whose result is
// kept whole: in a free function, a method, and a trait impl's self.
const scalarParamsLib = `import "std/time";

pub struct Mix { a: i32, b: i64, c: f64, d: boolean }

pub trait Waiter {
  function wait(self: Self, timeout: Option[Duration]): i64;
}

pub struct Real { k: i64 }

impl Waiter for Real {
  function wait(self: Real, timeout: Option[Duration]): i64 {
    match (timeout) {
      Some(d) => {
        return d.sec * 1000 + (d.nsec / 1000000) as i64 + self.k;
      },
      None => {
        return 0 - 1;
      }
    }
  }
}

@noinline pub function sum_mix(m: Mix): f64 {
  let flag: f64 = 0.0;
  if (m.d) {
    flag = 1.0;
  }
  return m.a as f64 + m.b as f64 + m.c + flag;
}

@noinline pub function hand_on(m: Mix, scale: i32): f64 {
  return sum_mix(m) * scale as f64;
}

@noinline pub function millis(t: Option[Duration]): i64 {
  match (t) {
    Some(d) => {
      return d.sec * 1000 + (d.nsec / 1000000) as i64;
    },
    None => {
      return 0 - 1;
    }
  }
}

@noinline pub function millis_plus(t: Option[Duration], extra: i64): i64 {
  return millis(t) + extra;
}

@noinline pub function doubled(t: Option[i64]): i64 {
  match (t) {
    None => {
      return 0 - 7;
    },
    Some(v) => {
      return v * 2;
    }
  }
}

@noinline pub function (d: Duration) whole_seconds(): i64 {
  return d.sec;
}

@noinline pub function later(now: Instant, d: Duration): Instant {
  let nsec: i64 = now.nsec as i64 + d.nsec as i64;
  return Instant { sec: now.sec + d.sec + nsec / 1000000000, nsec: (nsec % 1000000000) as i32 };
}

@noinline pub function wait_on[W: Waiter](w: W, t: Option[Duration]): i64 {
  return w.wait(t);
}

@noinline pub function moved_by(m: Mix, by: i64): Mix {
  return Mix { ...m, b: m.b + by };
}

pub struct Line { pos: i32, pend: i32, tab: boolean }

@noinline pub function (l: Line) space(by: i32): Line {
  if (l.tab) {
    return Line { ...l, pos: l.pos + by, pend: l.pend + 1 };
  }
  return Line { ...l, pos: l.pos + by };
}

pub trait Bump {
  function bumped(self: Self, by: f64): f64;
}

pub struct S0 { f0: f64, f1: boolean, f2: f64 }

@noinline function tg0(a: S0, b: S0, k: i32): (f64, S0) {
  return (a.f0 + a.f2 + k as f64, b);
}

impl Bump for S0 {
  function bumped(self: S0, by: f64): f64 {
    let (head, kept) = tg0(self, S0 { ...self, f2: self.f2 + by }, 2);
    let flag: f64 = 0.0;
    if (kept.f1) {
      flag = 1.0;
    }
    return head + kept.f2 * 2.0 + flag;
  }
}

@noinline pub function bump_on[B: Bump](b: B, by: f64): f64 {
  return b.bumped(by);
}
`

const scalarParamsMain = `import "std/bench";
import "std/time";
import "./lib";

function main(): i32 {
  let mixes: lib.Mix[] = [lib.Mix { a: 1, b: 20000000000, c: 0.5, d: true }, lib.Mix { a: 0 - 3, b: 0 - 4, c: 2.25, d: false }];
  let total: f64 = 0.0;
  let n: i64 = 0;
  let a0: i64 = bench.alloc_count();
  let i: i32 = 0;
  while (i < 100) {
    total = total + lib.sum_mix(lib.Mix { a: i, b: 7, c: 0.25, d: i % 2 == 0 });
    total = total + lib.hand_on(mixes[i % 2], 2);
    n = n + lib.millis(Some(time.duration_millis(i as i64)));
    n = n + lib.millis(None);
    n = n + lib.millis_plus(Some(time.duration_seconds(2)), 1);
    n = n + lib.millis_plus(None, 5);
    n = n + lib.doubled(Some(i as i64));
    n = n + lib.doubled(None);
    n = n + time.duration_millis(2500).whole_seconds();
    let t: Instant = lib.later(Instant { sec: 10, nsec: 900000000 }, time.duration_millis(250));
    n = n + t.sec + t.nsec as i64;
    n = n + lib.wait_on(lib.Real { k: 3 }, Some(time.duration_millis(40)));
    n = n + lib.wait_on(lib.Real { k: 3 }, None);
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + n.to_string() + " " + allocated.to_string());
  if (total != 2000000005500.0 || n != 15000220050) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  let kept: lib.Mix[] = [];
  let line: lib.Line = lib.Line { pos: 0, pend: 0, tab: false };
  let bumped: f64 = 0.0;
  i = 0;
  while (i < 10) {
    kept = kept.append(lib.moved_by(mixes[i % 2], i as i64));
    line = line.space(i).space(1);
    line = lib.Line { ...line, tab: i % 3 == 0 };
    bumped = bumped + lib.bump_on(lib.S0 { f0: 1.5, f1: i % 2 == 0, f2: 2.0 }, 0.5);
    i = i + 1;
  }
  let moved: f64 = 0.0;
  for m in kept {
    moved = moved + lib.sum_mix(m);
  }
  print(moved.to_string() + " " + line.pos.to_string() + " " + line.pend.to_string() + " " + bumped.to_string());
  if (moved != 100000000033.75 || line.pos != 55 || line.pend != 6 || bumped != 110.0) {
    return 3;
  }
  return 0;
}
`

func TestSelfHostScalarParams(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(scalarParamsLib), 0o644); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(scalarParamsMain), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 {
				t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 3: a wrong value after a record update)\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "live_bytes=0") {
				t.Errorf("%q, want every block freed", stderr)
			}
		})
	}
}
