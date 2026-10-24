package e2ecompiler

import "testing"

const fnLocalCallProgram = `import "std/array";

struct P { bytes: u8[], bits: i32 }

@noinline function first(ps: () => P[]): i32 {
  let n: i32 = 0;
  for p in ps() {
    n = n + p.bytes.take(p.bits).len() * 10 + p.bits;
  }
  return n;
}

function main(): i32 {
  let ps: P[] = [P { bytes: [1, 2, 3], bits: 2 }, P { bytes: [4], bits: 1 }];
  print(first((): P[] => ps).to_string());
  return 0;
}
`

func TestSelfHostFnLocalCallInfersItsResult(t *testing.T) {
	want := "33\n"
	runSemanticProgram(t, "fnlocalcall", fnLocalCallProgram, []string{"first"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
