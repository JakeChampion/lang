package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A payloadless Option or Result (None, Ok(()), Err(())) is one shared static
// block per tag on the self-host compiler, so making one allocates nothing:
// a miss returned through a function, `?` propagating it, and an Ok of void
// cost no box. The blocks still behave as values beside allocated ones: held
// in an array, replaced, appended, matched and dropped, with x86-64's
// leakcheck balanced.
func TestSelfHostPayloadlessOptionIsStatic(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "payloadless.fern")
	if err := os.WriteFile(src, []byte(payloadlessOptionSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			env := []string{"FERN_STRICT_IR=1"}
			if target == "x86-64-linux" {
				env = append(env, "FERN_LEAKCHECK=1")
			}
			out, code := cli.exitOfFile(t, src, target, nil, env...)
			if code != 0 {
				t.Fatalf("exit %d, want 0 (20 means a payloadless value allocated):\n%s", code, out)
			}
			if target == "x86-64-linux" {
				e2eharness.CheckLeakcheckBalanced(t, out)
			}
		})
	}
}

const payloadlessOptionSrc = `
struct Pt { x: i32, y: i32 }

function find(xs: i32[], want: i32): Option[i32] {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == want) {
      return Some(i);
    }
    i = i + 1;
  }
  return None;
}

function check(n: i32): Result[void, string] {
  if (n < 0) {
    return Err("negative");
  }
  return Ok(());
}

function first_pt(ps: Pt[]): Option[Pt] {
  if (ps.len() == 0) {
    return None;
  }
  return Some(ps[0]);
}

function next_after(xs: i32[], want: i32): Option[i32] {
  let i: i32 = find(xs, want)?;
  return Some(i + 1);
}

function main(): i32 {
  let xs: i32[] = [1, 2, 3];
  let none_pts: Pt[] = [];
  let misses: i32 = 0;
  let oks: i32 = 0;
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    match (find(xs, 9)) {
      Some(_) => { return 10; },
      None => { misses = misses + 1; },
    }
    match (check(i)) {
      Ok(_) => { oks = oks + 1; },
      Err(_) => { return 11; },
    }
    match (first_pt(none_pts)) {
      Some(_) => { return 12; },
      None => {},
    }
    match (next_after(xs, 9)) {
      Some(_) => { return 13; },
      None => {},
    }
    i = i + 1;
  }
  if (__heap_alloc_count() - before != (0 as i64)) {
    return 20;
  }
  if (misses != 1000 || oks != 1000) {
    return 21;
  }

  // The shared blocks are ordinary values everywhere else: held in an array,
  // replaced, dropped, and next to boxes that do allocate.
  let opts: Option[Pt][] = [None, Some(Pt { x: 1, y: 2 }), None];
  opts = opts.with(0, Some(Pt { x: 3, y: 4 }));
  opts = opts.append(None);
  let sum: i32 = 0;
  for o in opts {
    match (o) {
      Some(p) => { sum = sum + p.x + p.y; },
      None => { sum = sum + 100; },
    }
  }
  if (sum != 210) {
    return 30;
  }
  match (next_after(xs, 2)) {
    Some(v) => { if (v != 2) { return 31; } },
    None => { return 32; },
  }
  match (check(0 - 1)) {
    Ok(_) => { return 33; },
    Err(e) => { if (e != "negative") { return 34; } },
  }
  return 0;
}
`
