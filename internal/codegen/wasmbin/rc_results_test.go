package wasmbin

import (
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"

	"github.com/jakechampion/lang/internal/ir"
)

// Every runtime helper needs a decision on the RESULT axis, the same
// way TestRcSigsCoverEveryRuntimeHelper demands one on the argument
// axis.
//
// The two gates are deliberately separate loops over the same registry:
// a new helper now fails BOTH until someone says what it does to its
// arguments and what its return means, which are different questions
// with different answers for the same name — `__str_concat` borrows
// both its operands and hands back an owned string.
func TestRcResultsCoverEveryRuntimeHelper(t *testing.T) {
	var missing []string
	for name := range runtimeHelperSpecs {
		if !ir.RcHelperResultClassified(name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d runtime helper(s) have no result-axis classification: %v\n"+
			"Add each to one of rcResultOwned / rcResultImmortal / rcResultRaw / "+
			"rcResultBorrow / rcResultOperand / rcResultNonPointer in "+
			"internal/ir/rcresults.go, or to rcResultUnmodelled with a reason. "+
			"Read the helper's body: the names in this registry lie often enough "+
			"that the file's header lists three that do.", len(missing), missing)
	}
	if len(runtimeHelperSpecs) == 0 {
		t.Fatal("the runtime helper registry is empty — this gate is vacuous")
	}
}

// A helper that does not return a single i32 cannot return a pointer:
// on wasm32 an address IS an i32, so a void, f64 or i64 result settles
// the question without reading anything.
//
// This is the cheap half of the classification checking the expensive
// half. `verifyprovided.go`'s resultShape cannot do it — it files a
// heap pointer and a byte count as the same rWord — but the wasm
// registry carries the real valtype.
func TestRcResultPointerBucketsHoldOnlyI32Returns(t *testing.T) {
	pointerBucket := func(name string) (ir.RcResult, bool) {
		r, ok := ir.RcHelperResult(name)
		if !ok {
			return r, false
		}
		switch r {
		case ir.RcResultOwned, ir.RcResultImmortal, ir.RcResultRaw,
			ir.RcResultBorrow, ir.RcResultOperand:
			return r, true
		}
		return r, false
	}
	for name, spec := range runtimeHelperSpecs {
		r, isPointer := pointerBucket(name)
		if !isPointer {
			continue
		}
		if len(spec.results) == 0 {
			t.Errorf("%s is classified %v but returns nothing", name, r)
			continue
		}
		if spec.results[0] != encode.ValtypeI32 {
			t.Errorf("%s is classified %v but its first result is valtype %#x, not i32 — "+
				"a wasm32 pointer is an i32, so this cannot be an address",
				name, r, spec.results[0])
		}
	}
}

// The owned I/O family is `helperResultBoxCallers`, and runtime.go
// maintains that caller set for its own reasons (pulling the allocator
// into the helper set). Checking against it rather than trusting
// rcresults.go to be kept in step is the same argument
// verifyprovided.go makes for being a second record: a helper that
// starts or stops building a counted result box fails here instead of
// silently reading as the other class (#8398 / #8405).
func TestRcResultOwnedCoversEveryResultBoxCaller(t *testing.T) {
	for _, name := range helperResultBoxCallers {
		r, ok := ir.RcHelperResult(name)
		if !ok || r != ir.RcResultOwned {
			t.Errorf("%s builds its result box with __fern_alloc_rc1, so the caller "+
				"holds a unit and must release it — classified %v (known=%v), "+
				"want owned", name, r, ok)
		}
	}
	if len(helperResultBoxCallers) == 0 {
		t.Fatal("helperResultBoxCallers is empty — this gate is vacuous")
	}
	// The two shapes that stay sentinel-headered, and why: an IoError is
	// reachable from a result the caller may keep, and a Reader / Writer
	// handle outlives every call made through it. Releasing either would
	// be a no-op today and a use-after-free the moment the header went
	// live, so the class is pinned rather than left to drift.
	for _, name := range []string{
		"__build_io_error", "__fern_stdin", "__fern_stdout", "__fern_stderr",
	} {
		r, ok := ir.RcHelperResult(name)
		if !ok || r != ir.RcResultImmortal {
			t.Errorf("%s carries the static-sentinel rc header — classified %v "+
				"(known=%v), want immortal", name, r, ok)
		}
	}
}

// The two axes have to agree about aliasing: a helper whose result the
// ARGUMENT axis says is the operand renamed must be in the operand
// bucket here, and nothing else may be.
func TestRcResultOperandAgreesWithTheArgumentAxis(t *testing.T) {
	for name := range runtimeHelperSpecs {
		sig, known := ir.RcHelperSig(name)
		renames := false
		if known {
			for _, a := range sig.Args {
				if a.ResultIsOperand {
					renames = true
				}
			}
		}
		r, ok := ir.RcHelperResult(name)
		isOperand := ok && r == ir.RcResultOperand
		if renames != isOperand {
			t.Errorf("%s: argument axis says ResultIsOperand=%v, result axis says %v — "+
				"the two records disagree about whether the result is the operand renamed",
				name, renames, r)
		}
	}
}
