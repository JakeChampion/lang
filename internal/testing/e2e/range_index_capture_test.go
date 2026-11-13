package e2e

import "testing"

// rangeIndexCaptureSrc makes closures in range loops. Each turn of a range
// loop binds its own index, as a loop over an array binds its own element.
// A variable that the body assigns stays one shared cell.
const rangeIndexCaptureSrc = `import "std/array";
import "std/i32";
function shown(fs: (() => i32)[]): string {
  return fs.map((f: () => i32): string => f().to_string()).join(",");
}
function main(): i32 {
  let a: (() => i32)[] = [];
  for i in 0..3 {
    a = a.append((): i32 => i);
  }
  let b: (() => i32)[] = [];
  for i in 0..=2 {
    b = b.append((): i32 => i);
  }
  let c: (() => i32)[] = [];
  for i in 0..4 {
    if (i % 2 == 1) {
      continue;
    }
    c = c.append((): i32 => i);
  }
  let d: (() => i32)[] = [];
  let k = 0;
  for i in 0..3 {
    k = i;
    d = d.append((): i32 => k);
  }
  let e: (() => i32)[] = [];
  for i in 0..2 {
    for j in 0..2 {
      e = e.append((): i32 => i * 10 + j);
    }
  }
  let f: (() => i32)[] = [];
  outer: for i in 0..3 {
    for j in 0..3 {
      if (j == 1) {
        continue outer;
      }
      f = f.append((): i32 => i * 10 + j);
    }
  }
  print([shown(a), shown(b), shown(c), shown(d), shown(e), shown(f)].join("|"));
  return 0;
}
`

func TestRangeIndexCaptureIsPerTurn(t *testing.T) {
	const want = "0,1,2|0,1,2|0,2|2,2,2|0,1,10,11|0,10,20"
	if got := interpStdout(t, rangeIndexCaptureSrc); got != want {
		t.Fatalf("interpreter printed %q, want %q", got, want)
	}
	runBackendsAgainst(t, rangeIndexCaptureSrc, want, nil, nil)
}
