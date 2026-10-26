package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The records live in another module. Each round builds four records whose
// Option and scalar-record fields are laid out in the record's own box, so
// building them costs one allocation each (and one for Wait's set) and
// nothing for the Options, Durations and Instants inside them. The fields are
// built from Somes, Nones, calls and other fields, read apart, updated, and
// handed on; Two has two Options that each need a branch to take apart.
// Whole.d is used whole and so stays a box.
const laidFieldsLib = `import "std/time";

pub struct Wait { task: i32, set: i32[], timeout: Option[Duration] }

pub struct Sent { pn: i64, at: Instant, size: i32 }

pub struct Two { a: Option[i64], b: Option[Duration], c: i32 }

pub struct Whole { d: Duration, n: i32 }

pub function from_nanos(ns: i64): Instant {
  return Instant { sec: ns / 1000000000, nsec: (ns % 1000000000) as i32 };
}

pub function nanos_of(i: Instant): i64 {
  return i.sec * 1000000000 + i.nsec as i64;
}

@noinline function timeout_of(ms: i32): Option[Duration] {
  if (ms < 0) {
    return None;
  }
  return Some(time.duration_millis(ms as i64));
}

@noinline function maybe(n: i64): Option[i64] {
  if (n % 3 == 0) {
    return None;
  }
  return Some(n);
}

@noinline pub function parked(id: i32, ms: i32): Wait {
  return Wait { task: id, set: [id, 1], timeout: timeout_of(ms) };
}

@noinline pub function due(w: Wait, now: i64): i64 {
  let Some(t) = w.timeout else {
    return 0 - 1;
  };
  return now + t.as_nanos();
}

@noinline pub function sent(pn: i64, now: i64): Sent {
  return Sent { pn: pn, at: from_nanos(now), size: 1200 };
}

@noinline pub function age(s: Sent, now: i64): i64 {
  return now - nanos_of(s.at);
}

@noinline pub function two(i: i64): Two {
  return Two { a: maybe(i), b: timeout_of((i % 4) as i32 - 1), c: i as i32 };
}

@noinline pub function sum_two(t: Two): i64 {
  let n: i64 = t.c as i64;
  if let Some(a) = t.a {
    n = n + a;
  }
  if let Some(b) = t.b {
    n = n + b.as_millis();
  }
  return n;
}

@noinline pub function bumped(t: Two): Two {
  return Two { ...t, c: t.c + 1 };
}

@noinline pub function keep(d: Duration): Duration[] {
  return [d];
}
`

const laidFieldsMain = `import "std/bench";
import "std/time";
import "./lib";

function main(): i32 {
  let n: i64 = 0;
  let a0: i64 = bench.alloc_count();
  for i in 0..1000 {
    let w: lib.Wait = lib.parked(i, i % 5 - 1);
    n = n + lib.due(w, 7) + w.set.len() as i64;
    let s: lib.Sent = lib.sent(i as i64, 1000000);
    n = n + lib.age(s, 3000000) + s.pn;
    n = n + lib.sum_two(lib.bumped(lib.two(i as i64)));
  }
  let allocated: i64 = bench.alloc_count() - a0;
  let kept: lib.Sent[] = [];
  for i in 0..10 {
    kept = kept.append(lib.sent(i as i64, i as i64 * 5));
  }
  for s in kept {
    n = n + lib.nanos_of(s.at);
  }
  let wh: lib.Whole = lib.Whole { d: time.duration_seconds(3), n: 4 };
  n = n + lib.keep(wh.d)[0].as_seconds() + wh.n as i64;
  print(n.to_string() + " " + allocated.to_string());
  if (n != 3201341049) {
    return 1;
  }
  if (allocated > 4000) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostLaidFields(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(laidFieldsLib), 0o644); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(laidFieldsMain), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 {
				t.Errorf("exit %d, want 0 (1: a wrong value, 2: a field was boxed)\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "live_bytes=0") {
				t.Errorf("%q, want every block freed", stderr)
			}
		})
	}
}
