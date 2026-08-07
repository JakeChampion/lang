package wasmbin

import (
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The IR verifier keeps its own record of what the runtime helpers
// consume and produce (internal/ir/verifyprovided.go), because a
// verifier that read the emitter's own table would agree with it by
// construction and check nothing. The cost of a second record is that it
// can drift, and a drifted entry is worse than no entry: it makes the
// verifier report a defect in correct IR, or stay quiet on broken IR.
//
// This is the seam that keeps the two honest. Every helper both know
// about must agree on how many operand-stack slots the call consumes and
// how many it leaves, at wasm's two-word string ABI. A helper whose
// signature changes here fails until the verifier's copy is updated.
func TestProvidedSigsAgreeWithWasmRuntime(t *testing.T) {
	var names []string
	for name := range runtimeHelperSpecs {
		names = append(names, name)
	}
	sort.Strings(names)

	var known int
	for _, name := range names {
		spec := runtimeHelperSpecs[name]
		argSlots, resultSlots, ok := ir.ProvidedCallee(name, true)
		if !ok {
			// Not every helper reaches the IR as a named callee; the
			// verifier only records the ones that do, and an absent
			// entry costs coverage rather than correctness.
			continue
		}
		known++
		if argSlots >= 0 && argSlots != len(spec.params) {
			t.Errorf("%s: the verifier expects %d argument slots, the wasm runtime declares %d",
				name, argSlots, len(spec.params))
		}
		if resultSlots != len(spec.results) {
			t.Errorf("%s: the verifier expects %d result slots, the wasm runtime declares %d",
				name, resultSlots, len(spec.results))
		}
	}
	if known == 0 {
		t.Fatal("no helper was cross-checked — the two tables are no longer being compared")
	}
	t.Logf("cross-checked %d of %d wasm runtime helpers", known, len(names))
}
