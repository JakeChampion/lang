package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Each program calls a closure through a value whose target it reads out of
// its own environment, so ssa.slot_calls rewrites the call and the certifier
// reads the contract that it keeps.
var slotCallSrcs = map[string]string{
	// A `once` callee keeps its unit: the caller drops the closure after
	// the call, so the contract must not hand it over.
	"once-callee": `enum Step { Done(i32), Wait(i32, once (i32) => Step) }
function count_down(n: i32): Step {
  if (n == 0) { return Done(7); }
  return Wait(n, once (w: i32): Step => count_down(n - 1 + w));
}
function main(): i32 {
  let f = count_down(3);
  let steps = 0;
  while (steps < 10) {
    match (f) {
      Done(v) => { return v + steps - 10; },
      Wait(n, k) => { f = k(0); }
    }
    steps += 1;
  }
  return 1;
}
`,
	// The callee takes its operand as own, so the operand moves into the
	// call and the caller holds no unit of it after.
	"own-operand": `struct Box { v: i32[] }
struct Dev { present: (own Box, i32) => Box }
@noinline function keep(own b: Box, n: i32): Box { return Box { v: b.v.append(n) }; }
@noinline function three(): i32[] { return [1, 2, 3]; }
function main(): i32 {
  let d = Dev { present: keep };
  let b = Box { v: three() };
  b = d.present(b, 4);
  return b.v.len() as i32 - 4;
}
`,
}

func TestSelfHostCertifiesASlotCall(t *testing.T) {
	for name, src := range slotCallSrcs {
		for _, target := range selfHostFusionTargets {
			t.Run(name+"/"+target, func(t *testing.T) {
				bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_LEAKCHECK=1", "FERN_RC_CERTIFY_MEMO=0"})
				out, err := runScaleTarget(t, target, bin).CombinedOutput()
				if err != nil {
					t.Fatalf("%v:\n%s", err, out)
				}
				var allocs, frees, live int64
				if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("ownership: %v\n%s", err, out)
				}
			})
		}
	}
}
