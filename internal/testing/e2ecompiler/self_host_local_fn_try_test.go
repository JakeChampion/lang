package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A local function lifts to a synthesised declaration, which returns `Ok(...)`
// of a value block that uses `?`. `Ok(x)` alone infers `Result[i32, never]`,
// and the declared `Result[i32, i32]` must stay the body's result for the
// `?` to have an `Err` to return.
var localFnTrySrcs = map[string]string{
	"value-block": `function main(): i32 {
  function g(): Result[i32, i32] {
    let x: Result[i32, i32] = Ok(763i32);
    return Ok({ let w: Result[i32, i32] = x; w? });
  }
  match (g()) { Ok(v) => { return v - 763; }, Err(x) => { return 1; } }
}
`,
	"at-binding": `function main(): i32 {
  let e: Result[i32, i32] = Err(5i32);
  function g(): Result[i32, i32] {
    return Ok(match (e) { w @ Ok(v) => (w?), Err(x) => x });
  }
  match (g()) { Ok(v) => { return v - 5; }, Err(x) => { return 1; } }
}
`,
	"err-propagates": `function main(): i32 {
  function g(x: Result[i32, i32]): Result[i32, i32] {
    let y: i32 = { let w: Result[i32, i32] = x; w? };
    return Ok(y + 1);
  }
  match (g(Err(9i32))) { Ok(v) => { return 1; }, Err(x) => { return x - 9; } }
}
`,
}

func TestSelfHostLocalFnTryKeepsTheDeclaredResult(t *testing.T) {
	for name, src := range localFnTrySrcs {
		for _, target := range selfHostFusionTargets {
			t.Run(name+"/"+target, func(t *testing.T) {
				bin := e2eharness.CompileSelfHostSource(t, target, src, nil)
				if out, err := runScaleTarget(t, target, bin).CombinedOutput(); err != nil {
					t.Fatalf("%v:\n%s", err, out)
				}
			})
		}
	}
}
