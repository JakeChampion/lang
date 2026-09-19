package ir

import "github.com/jakechampion/lang/internal/ast"

// Why a pipeline stage's result buffer was freshly allocated rather than
// donated — the second of the three questions #9732 asks of an array
// expression.
//
// The reasons are `docs/REUSE-CONTRACT.md`'s, read off the IR rather than
// asserted: whether the combinator CONSUMES its array parameter is
// `Func.ParamConsumed`, and whether the element shape survives the stage is
// the two stages' argument types. Today every std/array combinator borrows its
// array, so every materializing stage answers the same way — which is itself
// worth printing, because "I own this array, why did mapping it allocate?" is
// exactly the question the report exists to answer, and the answer is a
// property of the signature rather than of the caller's code.
type ArrayStorage int

const (
	// StorageFused — nothing was materialized; the stage became part of a loop.
	StorageFused ArrayStorage = iota
	// StorageScalar — a reduction produces a value, not a buffer.
	StorageScalar
	// StorageBorrowedDonor — the combinator borrows its array, so there is no
	// owned donor to reuse whatever the caller holds.
	StorageBorrowedDonor
	// StorageShapeChange — the stage changes the element type, so the donor's
	// buffer is the wrong shape even when it is owned.
	StorageShapeChange
	// StorageReused — the buffer was donated rather than allocated.
	StorageReused
	// StorageUnknown — the stage's callee is not in the program, so nothing
	// can be said. Reported rather than guessed at.
	StorageUnknown
)

// AllArrayStorage is every verdict, so a histogram prints a row per reason
// even at zero.
var AllArrayStorage = []ArrayStorage{
	StorageFused, StorageScalar, StorageBorrowedDonor,
	StorageShapeChange, StorageReused, StorageUnknown,
}

// Tag is the stable one-word name a histogram counts under.
func (s ArrayStorage) Tag() string {
	switch s {
	case StorageFused:
		return "fused"
	case StorageScalar:
		return "scalar-result"
	case StorageBorrowedDonor:
		return "borrowed-donor"
	case StorageShapeChange:
		return "shape-change"
	case StorageReused:
		return "reused"
	}
	return "unknown"
}

// String is the clause a per-site report prints.
func (s ArrayStorage) String() string {
	switch s {
	case StorageFused:
		return "no buffer: the stage was fused into the loop"
	case StorageScalar:
		return "no buffer: a reduction produces a value"
	case StorageBorrowedDonor:
		return "fresh buffer: the combinator borrows its array, so there is no donor to reuse"
	case StorageShapeChange:
		return "fresh buffer: the stage changes the element type, so a donor would be the wrong shape"
	case StorageReused:
		return "donated buffer: reused from the stage before"
	}
	return "unknown: the combinator's body is not in this program"
}

// ArrayStageStorage answers the question for one stage of one pipeline.
// `fused` says whether the whole chain was rewritten, since a fused stage
// materializes nothing regardless of what its combinator would have done.
func ArrayStageStorage(prog *Program, pl ArrayPipeline, stage int, fused bool) ArrayStorage {
	// A reduction is answered first, fused or not: it produces a value, and
	// "fused into the loop" would be a less informative answer to "where did
	// this stage's buffer come from" than "there was never a buffer".
	s := pl.Stages[stage]
	if s.Kind == stageReduction {
		return StorageScalar
	}
	if fused {
		return StorageFused
	}
	fn := findFuncNamed(prog, s.Callee)
	if fn == nil {
		return StorageUnknown
	}
	// The array is the first argument. A combinator that does not consume it
	// has nothing to donate, whatever the caller owns.
	if len(fn.ParamConsumed) == 0 || !fn.ParamConsumed[0] {
		return StorageBorrowedDonor
	}
	if stageChangesElementType(prog, pl, stage) {
		return StorageShapeChange
	}
	return StorageReused
}

// stageChangesElementType compares what this stage reads with what the next
// one reads, which is what it wrote.
func stageChangesElementType(prog *Program, pl ArrayPipeline, stage int) bool {
	if stage+1 >= len(pl.Stages) {
		return false
	}
	in, okIn := stageElementType(prog, pl.Stages[stage])
	out, okOut := stageElementType(prog, pl.Stages[stage+1])
	return okIn && okOut && in != out
}

func stageElementType(prog *Program, s ArrayStage) (ast.Type, bool) {
	fn := findFuncNamed(prog, s.Callee)
	if fn == nil || len(fn.Params) == 0 {
		return nil, false
	}
	if at, isArr := fn.Params[0].Type.(ast.ArrayType); isArr {
		return at.Elem, true
	}
	return nil, false
}

func findFuncNamed(prog *Program, name string) *Func {
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}
