package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gap_add and counted build every value they return. One caller keeps each
// result whole, and the loop must still get the results in words.
const pairKeptWholeProg = `import "std/bench";

enum FieldError { Invalid, End }

struct Gap { sec: i64, nsec: i32 }

struct Mark { sec: i64, nsec: i32 }

@noinline function gap_add(a: Gap, b: Gap): Gap {
  let n = a.nsec + b.nsec;
  if (n >= 1000) {
    return Gap { sec: a.sec + b.sec + 1 as i64, nsec: n - 1000 };
  }
  return Gap { sec: a.sec + b.sec, nsec: n };
}

@noinline function mark_add(m: Mark, d: Gap): Mark {
  let r = gap_add(Gap { sec: m.sec, nsec: m.nsec }, d);
  return Mark { sec: r.sec, nsec: r.nsec };
}

@noinline function counted(r: i32): Result[i32, i32] {
  if (r < 0) {
    return Err(0 - r);
  }
  return Ok(r);
}

@noinline function field(i: i32): Result[(i64, u32), FieldError] {
  if (i < 0) { return Err(Invalid); }
  return Ok((5000000000i64 + i as i64, i as u32 + 1u32));
}

@noinline function store(own items: (i64, u32)[], item: (i64, u32)): (i64, u32)[] {
  return items.append(item);
}

function split_payload(): i32 {
  let Ok(pair) = field(17) else { return 1; };
  let empty: (i64, u32)[] = [];
  let kept = store(empty, pair);
  let before = bench.alloc_count();
  let total = 0i64;
  for i in 0..100 {
    let Ok((a, b)) = field(i) else { return 2; };
    total += a + b as i64;
  }
  let count = bench.allocs_after(before);
  let (left, right) = kept[0];
  if (total != 500000010000i64 || left != 5000000017i64 || right != 18u32) { return 3; }
  if let Err(FieldError.Invalid) = field(-1) {} else { return 4; }
  if (count != 0i64) { return 5; }
  return 0;
}

function main(): i32 {
  let rc = split_payload();
  if (rc != 0) { return rc; }
  let step = Gap { sec: 1 as i64, nsec: 600 };
  let marks: Option[Mark][] = [];
  marks = marks.append(Some(mark_add(Mark { sec: 0 as i64, nsec: 0 }, step)));
  let results: Result[i32, i32][] = [];
  results = results.append(counted(3));
  results = results.append(counted(-1));
  let total = Gap { sec: 0 as i64, nsec: 0 };
  let n = 0;
  let a0 = bench.alloc_count();
  for i in 0..100 {
    total = gap_add(total, step);
    match (counted(i)) {
      Ok(v) => {
        n = n + v;
      },
      Err(_) => {}
    }
  }
  let a1 = bench.alloc_count();
  let Some(m) = marks[0] else {
    return 1;
  };
  let Ok(three) = results[0] else {
    return 1;
  };
  let Err(why) = results[1] else {
    return 1;
  };
  if (total.sec != 160 as i64 || total.nsec != 0 || n != 4950 || m.sec != 1 as i64 || m.nsec != 600 || three != 3 || why != 1) {
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

func TestSelfHostPairKeptWholeByOneCaller(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "pair_kept_whole.fern")
	if err := os.WriteFile(src, []byte(pairKeptWholeProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a value came back wrong, 2: the loop allocated, 99: a count went under)", tg.target, got)
		}
	}
}
