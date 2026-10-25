package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The i64 test survives to the SSA lift, which folds it and leaves the
// join's phis one input each; the certifier must read those as copies for the
// field take out of `e` to be the take it is (#13166).
const foldedJoinTakeSrc = `struct S0 { f2: i32[] }
struct G { v: S0 }
function (g: G) get(): S0 { return g.v; }
enum O { Nil, Has(S0) }
function (o: O) or(d: S0): S0 { match (o) { Has(x) => { return x; }, Nil => { return d; } } }
@noinline function three(): i32[] { return [535, 330, 350]; }
function main(): i32 {
  let a: G = G { v: S0 { f2: three() } };
  let o: O = Nil;
  if (1i64 == 2i64) { o = Has(a.get()); }
  let e: S0 = o.or(a.get());
  return e.f2[0] - 535;
}
`

func TestSelfHostCertifiesATakeAfterAFoldedJoin(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, foldedJoinTakeSrc, []string{"FERN_LEAKCHECK=1", "FERN_RC_CERTIFY_MEMO=0"})
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
