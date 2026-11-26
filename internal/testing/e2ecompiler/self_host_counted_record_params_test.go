package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A record with a counted field crosses a call as its parts: the callee
// changes the array in place, and the record needs no box. A record whose
// callee returns a record of as many fields keeps its box, which the plan
// reuses for the result.
func TestSelfHostCountedRecordParams(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(countedRecordParamsLib), 0o644); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(countedRecordParamsMain), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 {
				t.Errorf("exit %d, want 0 (1: a wrong value, 2: the record took a box, 3: the result did not reuse the box)\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "live_bytes=0") {
				t.Errorf("%q, want every block freed", stderr)
			}
		})
	}
}

const countedRecordParamsLib = `pub enum Kind { Plain, Sealed }

pub struct Rec { kind: Kind, version: i32, body: u8[] }

pub struct Held { body: u8[], seen: i32 }

pub function at(wire: [u8], from: i32): Result[Rec, string] {
  if (from + 3 > wire.len()) {
    return Err("short");
  }
  let body: u8[] = [];
  body = body.append(wire[from]).append(wire[from + 1]).append(wire[from + 2]);
  return Ok(Rec { kind: Plain, version: wire[from] as i32, body: body });
}

@noinline pub function marked(own r: Rec): i32 {
  let n = r.body.len();
  let b = r.body.with(0, 7 as u8);
  match (r.kind) {
    Plain => {
      return b[0] as i32 + r.version + n;
    },
    Sealed => {
      return 0;
    }
  }
  return 0;
}

@noinline pub function seen(own h: Held, x: u8): Held {
  return Held { body: h.body.with(0, x), seen: h.seen + 1 };
}
`

const countedRecordParamsMain = `import "std/bench";
import "./lib";

function main(): i32 {
  let wire: u8[] = [1 as u8, 2 as u8, 3 as u8, 4 as u8];
  let total = 0;
  let a0: i64 = bench.alloc_count();
  for i in 0..1000 {
    let Ok(r) = lib.at(wire, i % 2) else {
      return 1;
    };
    total += lib.marked(r);
  }
  let made: i64 = bench.alloc_count() - a0;
  let h = lib.seen(lib.Held { body: [0 as u8, 0 as u8], seen: 0 }, 0 as u8);
  let a1: i64 = bench.alloc_count();
  for i in 0..1000 {
    h = lib.seen(h, (i % 200) as u8);
  }
  let kept: i64 = bench.alloc_count() - a1;
  print(total.to_string() + " " + made.to_string() + " " + h.seen.to_string() + " " + h.body[0].to_string() + " " + kept.to_string());
  if (total != 11500 || h.seen != 1001 || h.body[0] != 199 as u8) {
    return 1;
  }
  if (made != 1000) {
    return 2;
  }
  if (kept != 0) {
    return 3;
  }
  return 0;
}
`
