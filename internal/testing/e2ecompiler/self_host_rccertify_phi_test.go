package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Each test survives to the SSA lift, which folds it and leaves the join's
// phis one input each (#13166).
var foldedJoinSrcs = map[string]string{
	// The enum's tag and payload phis carry only zero; the certifier must
	// see them as zero for the field take out of `e` to be the take it is.
	"zero-phi-then-take": `struct S0 { f2: i32[] }
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
`,
	// The phi carries a fresh box across the edge; it is a value of its
	// own, and the box's unit moves into it.
	"fresh-phi": `struct Vec2 { x: f32, y: f32 }
function id[T](x: T): T { return x; }
@noinline function half(): f32 { return 1.5f32; }
function main(): i32 {
  let y: f32 = half();
  let v: Vec2 = id[Vec2](if (555i32 < (330i32 -| 36i32)) { id(Vec2 { x: 37.5f32, y: y }) } else { Vec2 { x: 72.5f32, y: y } });
  return v.x as i32 - 72;
}
`,
}

func TestSelfHostCertifiesAFoldedJoin(t *testing.T) {
	for name, src := range foldedJoinSrcs {
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
