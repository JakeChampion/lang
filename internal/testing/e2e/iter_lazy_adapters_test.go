package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const iterLazyAdaptersSrc = `import "core/iter";
import "std/i32";
import "std/i64";

// A user's own iterator gets every adapter and sink from the trait.
pub struct Countdown { n: i32 }
impl iter.Iterator[i32] for Countdown {
  function next(self: Countdown): Option[(i32, Countdown)] {
    if (self.n <= 0) { return None; }
    return Some((self.n, Countdown { n: self.n - 1 }));
  }
}

function evens_total[I: iter.Iterator[i64]](it: I): i64 {
  return it.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function main(): i32 {
  let xs: i64[] = [1 as i64, 2 as i64, 3 as i64, 4 as i64, 5 as i64, 6 as i64];
  print(iter.of(xs)
    .filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
    .map((x: i64): i64 => x * (10 as i64))
    .fold(0 as i64, (a: i64, b: i64): i64 => a + b).to_string());
  print(iter.of(xs).take(4).len().to_string());
  print(iter.of(xs).take(0 - 2).len().to_string());
  let pairs: (i32, i64)[] = iter.of(xs).enumerate().take(2).collect();
  print(pairs[1].0.to_string() + ":" + pairs[1].1.to_string());
  let z: (i64, i32)[] = iter.of(xs).zip(iter.range(10, 13)).collect();
  print(z.len().to_string() + ":" + z[2].1.to_string());
  print(iter.range(0, 10).any((i: i32): boolean => i == 7).to_string());
  print(iter.range(0, 10).all((i: i32): boolean => i < 7).to_string());
  match (iter.range(0, 50).map((i: i32): i32 => i * 7).find((v: i32): boolean => v > 100)) {
    Some(v) => { print(v.to_string()); },
    None => { print("none"); }
  }
  print(iter.range(5, 100).take(4).map((i: i32): i32 => i * i).fold(0, (a: i32, b: i32): i32 => a + b).to_string());
  print(evens_total(iter.of(xs).map((x: i64): i64 => x + (1 as i64))).to_string());
  print(Countdown { n: 5 }.filter((i: i32): boolean => i != 3).map((i: i32): i32 => i * 2).fold(0, (a: i32, b: i32): i32 => a * 10 + b).to_string());
  let words: string[] = iter.of(["a", "bb", "ccc"]).map((w: string): string => w + "!").collect();
  print(words[2]);
  return 0;
}
`

const iterLazyAdaptersWant = "120\n4\n0\n1:2\n3:12\ntrue\nfalse\n105\n174\n12\n10842\nccc!\n"

func TestInterpIterLazyAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(iterLazyAdaptersSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != iterLazyAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, iterLazyAdaptersWant, errOut)
	}
}

func TestX86_64IterLazyAdapters(t *testing.T) {
	out, code := compileAndRunX86_64(t, iterLazyAdaptersSrc)
	if code != 0 || out != iterLazyAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, iterLazyAdaptersWant)
	}
}

func TestArm64IterLazyAdapters(t *testing.T) {
	out, code := compileAndRunArm64(t, iterLazyAdaptersSrc)
	if code != 0 || out != iterLazyAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, iterLazyAdaptersWant)
	}
}

func TestWasmIterLazyAdapters(t *testing.T) {
	if out := compileAndRunWasmCapture(t, iterLazyAdaptersSrc); out != iterLazyAdaptersWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, iterLazyAdaptersWant)
	}
}
