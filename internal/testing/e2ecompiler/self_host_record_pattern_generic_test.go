package e2ecompiler

import "testing"

// A record pattern on a value of a generic struct names the clone of its
// type, whether a parameter, an annotation or a literal gives that type.
func TestSelfHostRecordPatternOnGenericStructLocal(t *testing.T) {
	selfHostRunOnEveryTarget(t, recordPatternOnGenericStructSource)
}

const recordPatternOnGenericStructSource = `struct Pair[T] { a: T, b: T }

function sum(p: Pair[i32]): i32 {
  let Pair { a, b } = p;
  return a + b;
}

function main(): i32 {
  let p: Pair[i32] = Pair { a: 1, b: 2 };
  let Pair { a: x, b: y } = p;
  if (x + y != 3) {
    return 1;
  }
  let q = Pair { a: 10, b: 20 };
  let Pair { a: u, b: v } = q;
  if (u + v != 30) {
    return 2;
  }
  let ps: Pair[i32][] = [];
  ps = ps.append(p);
  ps = ps.append(q);
  let total = 0;
  for Pair { a, b } in ps {
    total += a * b;
  }
  if (total != 202) {
    return 3;
  }
  let w: Pair[string] = Pair { a: "x", b: "yz" };
  let Pair { a: s, b: t } = w;
  if (s.len() + t.len() != 3) {
    return 4;
  }
  return sum(p) - 3;
}
`
