package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A record whose fields are constants is one static box even when a field is
// itself a static box, an empty array, a constant array or another such
// record, so `HeaderMap { names: [], values: [] }` costs nothing to build.
// The box behaves as one built per call: an update copies it, an append to a
// field grows a box of its own, and nested reads see the nested constants. A
// Some is not a record, so one around a constant record stays an Option box.
// x86-64's leakcheck stays balanced.
func TestSelfHostRecordOfStaticBoxesIsStatic(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "static_record.fern")
	if err := os.WriteFile(src, []byte(staticRecordSrc), 0o644); err != nil {
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
				t.Fatalf("exit %d, want 0 (20 means a constant record allocated):\n%s", code, out)
			}
			if target == "x86-64-linux" {
				e2eharness.CheckLeakcheckBalanced(t, out)
			}
		})
	}
}

const staticRecordSrc = `struct Pair { names: string[], vals: i32[] }
struct Outer { tag: i32, inner: Pair, more: Pair }

function fresh(): Pair {
  let names: string[] = [];
  let vals: i32[] = [];
  return Pair { names: names, vals: vals };
}

function nested(): Outer {
  return Outer { tag: 7, inner: Pair { names: [], vals: [] }, more: Pair { names: [], vals: [1, 2, 3] } };
}

function main(): i32 {
  let before: i64 = __heap_alloc_count();
  let n: i32 = 0;
  let i: i32 = 0;
  while (i < 100) {
    let p: Pair = fresh();
    let o: Outer = nested();
    n = n + p.names.len() + o.tag + o.more.vals[2] + o.inner.vals.len();
    i = i + 1;
  }
  if (__heap_alloc_count() - before != (0 as i64)) {
    return 20;
  }
  if (n != 1000) {
    return 10;
  }

  // An update and an append each build boxes of their own and leave the
  // constant as it was.
  let q: Pair = fresh();
  let q2: Pair = Pair { ...q, names: q.names.append("x"), vals: q.vals.append(5) };
  if (q2.names.len() != 1 || q2.names[0] != "x" || q2.vals[0] != 5) {
    return 11;
  }
  if (q.names.len() != 0 || fresh().vals.len() != 0) {
    return 12;
  }
  let o2: Outer = Outer { ...nested(), tag: 9 };
  let m: i32[] = o2.more.vals.append(4);
  if (o2.tag != 9 || m.len() != 4 || nested().more.vals.len() != 3 || nested().tag != 7) {
    return 13;
  }

  // An Option's box is a tag and a payload, not a shape and fields, so a
  // Some holding a constant record is built as an Option, not placed.
  let some: Option[Pair] = Some(Pair { names: [], vals: [8] });
  match (some) {
    Some(p) => { if (p.vals[0] != 8) { return 14; } },
    None => { return 15; },
  }
  return 0;
}
`
