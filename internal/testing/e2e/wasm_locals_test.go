package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWasmManyTemporariesInOneFunction is #12078: the lowering gives every
// temporary a slot of its own, and before the wasm backend coalesced them each
// slot was a local, so a long enough function declared more than the 50,000
// locals wasmtime accepts and the module failed to load. These 1,600
// statements made about 54,000.
func TestWasmManyTemporariesInOneFunction(t *testing.T) {
	const n = 1600
	var b strings.Builder
	b.WriteString("import \"std/i32\";\n")
	b.WriteString("function h(a: i32): i32 { return a + 1; }\n")
	b.WriteString("function g(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, q: i32, r: i32, " +
		"i: i32, j: i32, k: i32, l: i32, m: i32, n: i32, o: i32, p: i32): i32 {\n" +
		"  return a + b + c + d + e + f + q + r + i + j + k + l + m + n + o + p;\n}\n")
	b.WriteString("function main(): i32 {\n  let s: i32 = 0;\n")
	want := 0
	for i := range n {
		fmt.Fprintf(&b, "  s = s + g(h(%d), h(2), h(3), h(4), h(5), h(6), h(7), h(8), "+
			"h(9), h(10), h(11), h(12), h(13), h(14), h(15), h(16));\n", i)
		want += i + 1 + 150
	}
	b.WriteString("  print(s.to_string());\n  return 0;\n}\n")
	if got := strings.TrimSpace(compileAndRunWasmCapture(t, b.String())); got != fmt.Sprint(want) {
		t.Fatalf("printed %q, want %d", got, want)
	}
}

// wasmLocalsProbeSrc puts a value in each position where sharing a local
// would be wrong if a live range were cut short: carried around a loop, read
// on a path that skipped its store, live into a nested loop, of each wasm
// type, and in the consecutive slots a dyn dispatch reads by offset. The
// functions are @noinline so n stays a runtime value: inlined into main with a
// constant argument, their loops fold away.
const wasmLocalsProbeSrc = `import "std/i32";
import "std/i64";
import "std/f64";

trait Shape { function area(self: Self, k: i32): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self, k: i32): i32 { return self.s * self.s + k; } }
struct Ci { r: i32 }
impl Shape for Ci { function area(self: Self, k: i32): i32 { return self.r * 3i32 - k; } }

@noinline
function carried(n: i32): i32 {
  let acc: i32 = 1;
  let i: i32 = 0;
  while (i < n) {
    let t: i32 = acc * 3;
    if (i % 2 == 0) {
      t = t + i;
    }
    acc = t % 1009;
    i = i + 1;
  }
  return acc;
}

@noinline
function skipped(n: i32): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let x: i32 = 7;
    if (i % 3 == 0) {
      x = i * 2;
    }
    let y: i32 = 0;
    if (i % 4 == 1) {
      y = x + 1;
    } else {
      y = x - 1;
    }
    total = total + x * y;
    i = i + 1;
  }
  return total;
}

@noinline
function nested(n: i32): i64 {
  let before: i64 = (n as i64) * 3i64 + 5i64;
  let sum: i64 = 0i64;
  let i: i32 = 0;
  while (i < n) {
    let j: i32 = 0;
    while (j < i) {
      if (j == 7) {
        break;
      }
      let w: i64 = (j as i64) * before;
      let z: i64 = w + (i as i64) * 11i64;
      sum = sum + z;
      j = j + 1;
    }
    let f: f64 = (i as f64) * 0.5;
    sum = sum + (f as i64);
    i = i + 1;
  }
  return sum;
}

@noinline
function shapes(n: i32): i32 {
  let out: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let d: dyn Shape = Sq { s: i };
    if (i % 2 == 1) {
      d = Ci { r: i };
    }
    let a: i32 = i + 1;
    let b: i32 = a * 2;
    out = out + d.area(b - a);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  print(carried(50).to_string());
  print(skipped(40).to_string());
  print(nested(30).to_string());
  print(shapes(20).to_string());
  let x: f64 = 1.5;
  let k: i32 = 0;
  while (k < 10) {
    let y: f64 = x * 1.25;
    x = y - 0.125;
    k = k + 1;
  }
  print(f64_bits(x).to_string());
  return 0;
}
`

// TestWasmLocalsProbeMatchesInterp runs wasmLocalsProbeSrc on wasm and on the
// interpreter, which has no locals to share, and wants the same output.
func TestWasmLocalsProbeMatchesInterp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.fern")
	if err := os.WriteFile(path, []byte(wasmLocalsProbeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, want, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 {
		t.Fatalf("interp exited %d\nstdout: %s\nstderr: %s", code, want, errOut)
	}
	if got := compileAndRunWasmCapture(t, wasmLocalsProbeSrc); got != want {
		t.Fatalf("wasm printed\n%s\ninterp printed\n%s", got, want)
	}
}
