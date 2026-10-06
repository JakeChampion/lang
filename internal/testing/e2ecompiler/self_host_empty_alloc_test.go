package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// `__alloc_u8(0)` is one shared immortal empty array on the self-host
// compiler, as it is on the Go compiler, so a zero-length buffer costs no
// allocation (#11507). The array behaves as any empty one: a push grows it
// into a box of its own, a later zero-length allocation is still empty, and
// a map, which starts from two of them, fills and reads back. x86-64's
// leakcheck stays balanced.
func TestSelfHostEmptyAllocIsStatic(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "empty_u8.fern")
	if err := os.WriteFile(src, []byte(emptyAllocSrc), 0o644); err != nil {
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
				t.Fatalf("exit %d, want 0 (20 means a zero-length allocation allocated):\n%s", code, out)
			}
			if target == "x86-64-linux" {
				e2eharness.CheckLeakcheckBalanced(t, out)
			}
		})
	}
}

const emptyAllocSrc = `import "core/map";

function main(): i32 {
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 100) {
    let z: u8[] = __alloc_u8(0);
    if (z.len() != 0) {
      return 10;
    }
    i = i + 1;
  }
  if (__heap_alloc_count() - before != (0 as i64)) {
    return 20;
  }

  // The shared empty array is an ordinary empty array: a push grows it into
  // a box of its own, and the next zero-length allocation is still empty.
  let a: u8[] = __alloc_u8(0);
  a = a.append(7 as u8);
  a = a.append(9 as u8);
  let b: u8[] = __alloc_u8(0);
  if (a.len() != 2 || a[0] != (7 as u8) || a[1] != (9 as u8) || b.len() != 0) {
    return 30;
  }
  let c: u8[] = __alloc_u8(3);
  if (c.len() != 3 || c[2] != (0 as u8)) {
    return 31;
  }

  // A map starts from empty key and value arrays.
  let m: Map[string, i32] = Map {};
  let n: Map[string, i32] = Map {};
  m = m.insert("a", 1);
  m = m.insert("b", 2);
  if (!m.has("a") || !m.has("b") || n.len() != 0 || m.len() != 2) {
    return 40;
  }
  return 0;
}
`
