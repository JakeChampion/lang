package e2ecompiler

import (
	"strings"
	"testing"
)

// A record returned in words stays so when a caller builds a record of a
// different size from its fields: only a construction of the same size could
// take its box.
const scalarParamDonorProg = `import "std/bench";

struct Pair { a: i64, b: i32 }

struct Quad { w: i64, x: i64, y: string, z: i32 }

@noinline function made(n: i64): Pair {
  if (n < 0) {
    return Pair { a: 0, b: 0 };
  }
  return Pair { a: n * 3, b: (n % 7) as i32 };
}

@noinline function quad(n: i64): Quad {
  let p: Pair = made(n);
  return Quad { w: p.a, x: p.b as i64, y: "q", z: 1 };
}

function main(): i32 {
  let total: i64 = quad(5).w;
  let a0: i64 = bench.alloc_count();
  let i: i64 = 0;
  while (i < 50) {
    let p: Pair = made(i);
    total = total + p.a + p.b as i64;
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 3837) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostScalarParam(t *testing.T) {
	cli := buildSelfHostCLI(t)
	progs := []struct{ name, src string }{
		{"donor", scalarParamDonorProg},
	}
	for _, prog := range progs {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(prog.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, prog.src, target, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
				if code != 0 {
					t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 99: a count went under)\n%s", code, stderr)
				}
				if !strings.Contains(stderr, "live_bytes=0") {
					t.Errorf("%q, want every block freed", stderr)
				}
			})
		}
	}
}
