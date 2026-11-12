package e2ecompiler

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Each record of these values is held two times, so a copy or a store that
// follows each path does 2^40 steps (#15436).
const sharedValueProg = `import "core/map";
import "std/worker";

struct N { l: Option[N], r: Option[N] }

struct A { xs: i32[], l: Option[A], r: Option[A] }

struct Pair { name: string, both: (i32[], i32[]), by: Map[string, i32[]] }

function plain(levels: i32): N {
  let cur: N = N { l: None, r: None };
  for i in 0..levels {
    cur = N { l: Some(cur), r: Some(cur) };
  }
  return cur;
}

function plain_depth(n: N): i32 {
  return match (n.l) {
    Some(c) => plain_depth(c) + 1,
    None => 0,
  };
}

function arrays(levels: i32): A {
  let cur: A = A { xs: [1, 2], l: None, r: None };
  for i in 0..levels {
    cur = A { xs: [i], l: Some(cur), r: Some(cur) };
  }
  return cur;
}

function arrays_sum(n: A): i32 {
  return match (n.r) {
    Some(c) => arrays_sum(c) + n.xs.len(),
    None => n.xs.len(),
  };
}

function pair(): Pair {
  let xs: i32[] = [3, 4, 5];
  let by: Map[string, i32[]] = map_new(2);
  by = by.insert("a", xs);
  by = by.insert("b", xs);
  return Pair { name: "pair", both: (xs, xs), by: by };
}

function pair_sum(p: Pair): i32 {
  let none: i32[] = [];
  return p.both.0[0] + p.both.1[2] + p.by.get_or("a", none).len() + p.by.get_or("b", none)[1] + p.name.len();
}

function main(): i32 {
  let sent = worker.spawn((): N => plain(40));
  print(plain_depth(sent.join()).to_string());
  let held = worker.spawn((): A => arrays(40));
  print(arrays_sum(held.join()).to_string());
  let both = worker.spawn((): Pair => pair());
  print(pair_sum(both.join()).to_string());

  let cell: Cell[Option[A]] = cell_new(None);
  cell.set(Some(arrays(40)));
  match (cell.get()) {
    Some(a) => { print(arrays_sum(a).to_string()); },
    None => { print("none"); },
  }
  let stored: Cell[Pair] = cell_new(pair());
  print(pair_sum(stored.get()).to_string());
  stored.set(stored.get());
  print(pair_sum(stored.get()).to_string());

  return 0;
}
`

func TestSelfHostInterpretersKeepASharedValueShared(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "shared.fern")
	if err := os.WriteFile(src, []byte(sharedValueProg), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "40\n42\n19\n42\n19\n19\n"
	for _, engine := range []struct{ name, cli string }{{"go-interp", h.native}, {"primary-interp", h.cli}} {
		t.Run(engine.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, engine.cli, "-interp", src, h.stdlib)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v; stdout %q; stderr %.400s", err, &stdout, &stderr)
			}
			if stdout.String() != want {
				t.Fatalf("stdout %q, want %q", &stdout, want)
			}
		})
	}
}
