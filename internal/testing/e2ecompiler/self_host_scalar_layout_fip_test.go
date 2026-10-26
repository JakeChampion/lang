package e2ecompiler

import "testing"

func checkScalarLayout(t *testing.T, steps string) {
	t.Helper()
	source := `struct Row { a: i32, b: i32, c: i32 }
struct State { row: Row, retained: i32[] }
` + steps + `function main(): i32 {
  let state = State { row: Row { a: 0, b: 1, c: 2 }, retained: [7] };
  state = step(state, 0);
  let mark = __heap_alloc_count();
  let n: i32 = 0;
  while (n < 100) { state = step(state, n); n = n + 1; }
  if (__heap_alloc_count() != mark) { return 1; }
  if (state.row.a != 99 || state.row.b != 100 || state.row.c != 101) { return 2; }
  if (state.retained[0] != 7) { return 3; }
  return 0;
}
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want zero\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostScalarLayoutPreservesFIP(t *testing.T) {
	checkScalarLayout(t, `@noinline fbip function decode(own row: Row, n: i32): Row {
  row = Row { ...row, a: n };
  row = Row { ...row, b: n + 1 };
  return Row { ...row, c: n + 2 };
}
@noinline fbip function step(own state: State, n: i32): State {
  return State { ...state, row: decode(state.row, n) };
}
`)
}

func TestSelfHostScalarLayoutFactoryReturn(t *testing.T) {
	checkScalarLayout(t, `@noinline fip function decode(n: i32): Row {
  return Row { a: n, b: n + 1, c: n + 2 };
}
@noinline fbip function step(own state: State, n: i32): State {
  return State { ...state, row: decode(n) };
}
`)
}
