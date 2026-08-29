package e2e

import (
	"sort"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/ssa"
)

// Provenance on the SSA lift, and why it is a gate rather than a detail.
//
// docs/SSA-CUTOVER-PLAN.md's route runs the ownership analysis over the
// LIFTED form — where every value has a name, a def-use edge and a
// dominance relation, and where the flat IR's anonymous operand-stack
// values do not exist — and then maps the decisions back onto positions in
// the op stream the emitters consume. `ssa.Op.SrcOp` is that mapping.
//
// The mapping has to be TOTAL. A decision about an op with no source index
// is a decision that cannot be applied, and the failure mode is silent: the
// analysis reports an answer, the emitter never receives it, and the RC
// operation simply does not appear. So this asserts total coverage rather
// than measuring a percentage.
//
// The one legitimate exception is a phi. A phi is synthesised at a join by
// the SSA construction, not lifted from any op, so it has no source index
// and never will. Anything ssa.Optimize creates is the same, which is why
// the plan's analysis runs on the unoptimised lift — this test therefore
// lifts and does NOT optimise, matching what the analysis would do.
func TestSSALiftProvenanceIsTotal(t *testing.T) {
	var funcs, lifted, ops, withSrc, phis int
	var bad []string
	bail := map[string]int{}

	corpusPrograms(t, func(name string, cfg verifyConfig, ip *ir.Program) {
		for _, f := range ip.Funcs {
			funcs++
			sf, err := ssa.LiftFromIR(f)
			if err != nil {
				bail[err.Error()]++
				continue
			}
			lifted++
			for _, b := range sf.Blocks {
				for _, o := range b.Ops {
					ops++
					src, ok := o.SourceOp()
					switch {
					case o.Kind == ssa.OpPhi:
						phis++
					case !ok:
						if len(bad) < 10 {
							bad = append(bad, f.Name+": "+o.Kind.String()+" has no source op")
						}
					case src < 0 || src >= len(f.Ops):
						if len(bad) < 10 {
							bad = append(bad, f.Name+": "+o.Kind.String()+" names source op "+
								strconv.Itoa(src)+", outside the function's "+strconv.Itoa(len(f.Ops))+" ops")
						}
					default:
						withSrc++
					}
				}
			}
		}
	})

	if funcs == 0 {
		t.Fatal("no functions lifted — the corpus walk selected nothing")
	}
	t.Logf("lifted %d/%d functions; %d ops, %d with a source op, %d phis (no origin by construction)",
		lifted, funcs, ops, withSrc, phis)
	if len(bad) > 0 {
		t.Errorf("%d op(s) in the lifted corpus carry no usable source index — an analysis "+
			"decision about one of these could not be applied to the op stream, and would be "+
			"lost silently. First few:\n  %s", len(bad), joinLines(bad))
	}
	if withSrc+phis != ops {
		t.Errorf("%d ops, but %d with a source op + %d phis = %d — the accounting does not close",
			ops, withSrc, phis, withSrc+phis)
	}
	// The lift's own coverage is a separate property, held where it is
	// measured rather than asserted here; a collapse would show up as a
	// sharp drop in the logged ratio.
	if lifted*100/funcs < 95 {
		var keys []string
		for k := range bail {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return bail[keys[a]] > bail[keys[b]] })
		t.Errorf("only %d of %d corpus functions lift (%.1f%%) — the analysis route in "+
			"docs/SSA-CUTOVER-PLAN.md is proportional to this. Most common bails:\n  %s",
			lifted, funcs, 100*float64(lifted)/float64(funcs), joinLines(keys[:min(3, len(keys))]))
	}
}

func joinLines(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "\n  "
		}
		out += x
	}
	return out
}
