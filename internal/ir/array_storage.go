package ir

import "strconv"

// Where a pipeline stage's result buffer came from — the second of the three
// questions #9732 asks of an array expression — and, when it was freshly
// allocated, which rule of docs/REUSE-CONTRACT.md's R7 declined to donate one.
//
// The verdicts are read off the IR by the same planner that performs the
// in-place rewrite (`inPlaceVerdict`), so the report cannot claim a reuse the
// pass did not perform or a refusal it did not make. The set is CLOSED with
// stable tags, for the reason the fusion refusals are: the refusals are the
// coverage checklist for widening R7, and a checklist of free text cannot be
// tallied.
type ArrayStorage int

const (
	// StorageFused — nothing was materialized; the stage became part of a loop.
	StorageFused ArrayStorage = iota
	// StorageScalar — a reduction produces a value, not a buffer.
	StorageScalar
	// StorageReused — the buffer was written through the `own` parameter (R7).
	StorageReused
	// StorageNoInPlaceShape — the operator has no in-place shape; R7 covers map.
	StorageNoInPlaceShape
	// StorageReceiverNotOwnedParam — the receiver is not a parameter this
	// function consumes, so nothing licenses writing through it.
	StorageReceiverNotOwnedParam
	// StorageElementFunctionUnresolved — the element function is not a
	// statically resolved value.
	StorageElementFunctionUnresolved
	// StorageElementFunctionEffectful — the element function can reach an
	// observable effect (docs/ARRAY-ALGEBRA.md §1).
	StorageElementFunctionEffectful
	// StorageElementFunctionCaptures — the element function captures a value,
	// which could be the donor.
	StorageElementFunctionCaptures
	// StorageElementWidthUnsupported — only 8-byte elements in the first slice.
	StorageElementWidthUnsupported
	// StorageShapeChange — the stage changes the element type, so the donor's
	// buffer is the wrong shape even when it is owned.
	StorageShapeChange
	// StorageDonorNotReleasedHere — the donor's release is not in the call's
	// epilogue, so it is not dead at the call.
	StorageDonorNotReleasedHere
	// StorageScaleKernel — the stage is gone and a kernel wrote the buffer
	// instead of the scalar loop (#9735). Appended rather than grouped with
	// StorageReused so the existing values keep their numbers.
	StorageScaleKernel
)

// AllArrayStorage is every verdict, so a histogram prints a row per reason
// even at zero.
var AllArrayStorage = []ArrayStorage{
	StorageFused, StorageScalar, StorageReused, StorageScaleKernel,
	StorageNoInPlaceShape,
	StorageReceiverNotOwnedParam, StorageElementFunctionUnresolved,
	StorageElementFunctionEffectful, StorageElementFunctionCaptures,
	StorageElementWidthUnsupported, StorageShapeChange, StorageDonorNotReleasedHere,
}

// Tag is the stable one-word name a histogram counts under.
func (s ArrayStorage) Tag() string {
	switch s {
	case StorageFused:
		return "fused"
	case StorageScalar:
		return "scalar-result"
	case StorageReused:
		return "reused"
	case StorageNoInPlaceShape:
		return "no-in-place-shape"
	case StorageReceiverNotOwnedParam:
		return "receiver-not-own-param"
	case StorageElementFunctionUnresolved:
		return "element-fn-unresolved"
	case StorageElementFunctionEffectful:
		return "element-fn-effectful"
	case StorageElementFunctionCaptures:
		return "element-fn-captures"
	case StorageElementWidthUnsupported:
		return "element-width-unsupported"
	case StorageShapeChange:
		return "shape-change"
	case StorageDonorNotReleasedHere:
		return "donor-not-released-here"
	case StorageScaleKernel:
		return "scale-kernel"
	}
	return "unknown"
}

// Reason is the clause naming the rule, without saying what it decided: the
// report prefixes it with where the buffer came from, and E068 with the call
// it refuses.
func (s ArrayStorage) Reason() string {
	switch s {
	case StorageFused:
		return "the stage was fused into the loop"
	case StorageScalar:
		return "a reduction produces a value"
	case StorageReused:
		return "written through the `own` parameter (R7)"
	case StorageNoInPlaceShape:
		return "no in-place shape exists for this operator; R7 covers map"
	case StorageReceiverNotOwnedParam:
		return "the receiver is not an `own` parameter of this function, so nothing licenses writing through it"
	case StorageElementFunctionUnresolved:
		return "the element function is not statically resolved"
	case StorageElementFunctionEffectful:
		return "the element function can reach an observable effect"
	case StorageElementFunctionCaptures:
		return "the element function captures a value, which could be the donor"
	case StorageElementWidthUnsupported:
		return "only 8-byte elements are written in place in the first slice"
	case StorageShapeChange:
		return "the stage changes the element type, so the donor is the wrong shape"
	case StorageDonorNotReleasedHere:
		return "the donor's release is not in the call's epilogue, so it is not dead here"
	case StorageScaleKernel:
		return "__fern_scale_f64 replaced the map, so the kernel wrote it rather than the scalar loop (#9735)"
	}
	return "unknown"
}

// String is the clause a per-site report prints.
func (s ArrayStorage) String() string {
	switch s {
	case StorageFused, StorageScalar:
		return "no buffer: " + s.Reason()
	case StorageReused:
		return "donated buffer: " + s.Reason()
	}
	return "fresh buffer: " + s.Reason()
}

// ArrayStorageVerdicts answers the question for every stage of every
// recognized pipeline, keyed by function name and the stage's op index. A
// fused stage materializes nothing regardless of what its combinator would
// have done, and a reduction is answered first because "there was never a
// buffer" is the more informative answer than "fused".
func ArrayStorageVerdicts(prog *Program) map[string]ArrayStorage {
	out := map[string]ArrayStorage{}
	cx := newArrayContext(prog)
	fusion := ArrayFusionVerdicts(prog)
	byName := make(map[string]*Func, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	for _, fn := range prog.Funcs {
		if cx.isStdlibBody(fn) {
			continue
		}
		calls := collectArrayCalls(fn, cx)
		for _, pl := range recognizeInFunc(fn, cx) {
			fused := fusion[arrayPipelineKey(fn.Name, pl)].Why == FusionFused
			for _, s := range pl.Stages {
				var v ArrayStorage
				switch {
				case s.Kind == stageReduction:
					v = StorageScalar
				case fused:
					v = StorageFused
				default:
					v = StorageNoInPlaceShape
					for _, c := range calls {
						if c.op != s.Op {
							continue
						}
						_, v = inPlaceVerdict(fn, c, cx)
						// The battery runs the scale kernel AFTER R7, so a
						// stage R7 donates is never the kernel's; one it
						// declines may be. Asking scaleF64Verdict rather
						// than re-deriving is what keeps the report from
						// claiming a rewrite the pass did not perform.
						if v != StorageReused {
							if _, taken := scaleF64Verdict(byName, fn, c); taken {
								v = StorageScaleKernel
							}
						}
					}
				}
				out[arrayStageKey(fn.Name, s)] = v
			}
		}
	}
	return out
}

// arrayStageKey identifies a stage within a program.
func arrayStageKey(fnName string, s ArrayStage) string {
	return fnName + "#" + strconv.Itoa(s.Op)
}
