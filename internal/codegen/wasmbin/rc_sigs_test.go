package wasmbin

import (
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// internal/ir/rcsigs.go records what each runtime helper does to a
// reference count, because the helpers have no body in the IR and every
// ownership analysis over the op stream has to be told.
//
// A second record drifts, and the interesting drift here is silent: a
// new helper that releases its argument is simply absent from the
// table, so an analysis reports no release and the count it produces
// reads low with nothing to say so. This is the seam that makes that
// loud. Every helper in the wasm registry has to land in one of the
// three buckets — a signature, "moves counts in a shape the table
// cannot express", or "moves none" — and the choice is a line of code
// someone has to write.
func TestRcSigsCoverEveryRuntimeHelper(t *testing.T) {
	var missing []string
	for name := range runtimeHelperSpecs {
		if !ir.RcHelperClassified(name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these runtime helpers have no reference-count classification in "+
			"internal/ir/rcsigs.go — add each to rcRuntimeSigs, rcUnmodelled or "+
			"rcInert: %v", missing)
	}

	var stale []string
	for _, name := range ir.RcClassifiedRuntimeNames() {
		if _, ok := runtimeHelperSpecs[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("internal/ir/rcsigs.go classifies helpers the wasm runtime no longer "+
			"defines: %v", stale)
	}

	if len(runtimeHelperSpecs) == 0 {
		t.Fatal("the registry is empty — nothing was compared")
	}
	t.Logf("%d wasm runtime helpers, %d unclassified, %d stale",
		len(runtimeHelperSpecs), len(missing), len(stale))
}

// The operand index a signature names has to be an argument the helper
// actually takes. An out-of-range index would make an analysis read a
// neighbouring argument — a stride or a size — as the counted pointer.
func TestRcSigOperandIsWithinTheHelpersArguments(t *testing.T) {
	checked := 0
	for name, spec := range runtimeHelperSpecs {
		sig, ok := ir.RcHelperSig(name)
		if !ok {
			continue
		}
		checked++
		if sig.Operand < 0 || sig.Operand >= len(spec.params) {
			t.Errorf("%s: signature names operand %d, but the helper takes %d arguments",
				name, sig.Operand, len(spec.params))
		}
	}
	if checked == 0 {
		t.Fatal("no signature was checked against a helper's arguments")
	}

	// A helper that hands back the pointer it was given has to return
	// something. `__free` is the entry this catches if it is ever given
	// the flag by copy-paste from its neighbours.
	for name, spec := range runtimeHelperSpecs {
		sig, ok := ir.RcHelperSig(name)
		if ok && sig.ResultIsOperand && len(spec.results) == 0 {
			t.Errorf("%s: the signature says the result is the operand, but the helper returns nothing", name)
		}
	}
	t.Logf("checked %d helper signatures against their argument lists", checked)
}
