package e2ecompiler

import "testing"

const returnedViewParameterProgram = `@noinline function generic[T](x: T, flag: boolean): T {
  if (flag) {
    if (!flag) { return x; }
  } else {
    loop { return x; break; }
  }
  return x;
}
@noinline function concrete(x: str, flag: boolean): str {
  if (flag) {
    if (!flag) { return x; }
  } else {
    loop { return x; break; }
  }
  return x;
}
@noinline function join(a: string, b: string): string { return a + b; }
function main(): i32 {
  let owner: string = join("a-wide-source-with-a-live-owner-", "and-a-tail-past-the-inline-boundary");
  let view: str = slice_unchecked(owner, 1, owner.len() - 1);
  for i in 0..16 {
    let flag = i % 2 == 0;
    let a: str = generic[str](view, flag);
    let b: str = concrete(view, flag);
    let literal: str = generic[str]("de", flag);
    if (a != view || b != view || literal != "de") { return 11; }
    if (owner[0] != b'a' || view.len() != owner.len() - 2) { return 12; }
  }
  if (__rc_underflow_count() != 0) { return 13; }
  return 0;
}
`

func TestSelfHostReturnedViewParameterAfterPhi(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, returnedViewParameterProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("returned view parameter: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
