// Ownership queries over the lifted form.
//
// This is the first piece of `docs/SSA-CUTOVER-PLAN.md`'s analysis-only
// route: reason about reference counting where values have names,
// def-use edges and a CFG, then map the answers back to the op stream
// through `Op.SrcOp`. Nothing here emits or rewrites anything — it
// answers questions, and the callers decide what to do with them.
//
// # Why the questions are asked here and not in internal/ir
//
// The flat IR has no CFG to ask, so the same questions get answered by
// hand there: `rc_analysis.go` says of its own last-use test that it is
// "TEXTUAL, and a name declared OUTSIDE the loop is read again by the
// next iteration, so its textually-last occurrence is not its last
// dynamic use" — which is #7544, and which needed a bespoke walk over
// While / Loop / For / ForEach bodies to repair. Measured over the
// corpus, 69% of reference-count operations act on a value whose last
// use is in a DIFFERENT block, so that is the majority case rather than
// a corner of it.
//
// # Run this on the UNOPTIMISED lift
//
// `Optimize` synthesises ops with no IR origin, so provenance stops
// being total the moment it runs and an answer produced here could not
// be mapped back. Lift, ask, map back; optimise only on the codegen
// path, which is untouched by any of this.
package ssa

// rcHelpers are the runtime reference-count entry points. The lift turns
// both spellings — the flat IR's dedicated OpRcInc / OpRcDec /
// OpRcIsUnique and the self-host's plain calls — into an OpCall carrying
// the helper's name, so one set covers both compilers.
var rcHelpers = map[string]bool{
	"__fern_rc_inc":       true,
	"__fern_rc_dec":       true,
	"__fern_rc_is_unique": true,
}

// RCSite is one reference-count operation and what the CFG says about
// the value it acts on.
type RCSite struct {
	Block   *Block
	Op      *Op
	Helper  string
	Operand Value

	// LiveAfter is true when some OTHER use of Operand is reachable from
	// this op — so the value is still wanted and this is not its last
	// use. A release here would be premature; a retain here is holding
	// the value for that later use.
	//
	// It is a question about SSA uses, and that is NOT the same question
	// as "is this reference count balanced". A retain can hand its count
	// to a data structure rather than to a later use, and then the value
	// is legitimately dead afterwards. `__map_own_key` in core/map.fern
	// is the worked example, and it is every one of the 40 retains the
	// corpus reports as dead:
	//
	//	__fern_rc_inc(__load_ptr(boxed));   // result discarded
	//	return boxed;
	//
	// The retain bumps the string's heap buffer while the function
	// returns the CELL. Nothing uses the retained pointer again, and
	// nothing is wrong: ownership leaves through the return, because the
	// buffer is reachable from `boxed` through memory.
	//
	// So "dead afterwards" is a filter, not a verdict. Separating a
	// genuine unbalanced retain from this shape needs reachability
	// THROUGH MEMORY — whether the retained pointer is reachable from a
	// value that escapes — which this analysis does not have.
	//
	// The other direction reads the same way. Of the 622 releases the
	// corpus reports as live afterwards, 540 are used by their block's
	// terminator and the top functions are all generated drop glue —
	// __drop_tuple_*, __drop_struct_*, __drop_enum_* — releasing a field
	// and then walking on through the container to reach the next one.
	// Also correct. Telling a premature release from a flat dec on a
	// value with other owners needs to know the count, which is the
	// callee ownership signature table (#7786).
	//
	// Both directions land in the same place: this pass classifies
	// STRUCTURE. It says where a value is still wanted, which is the
	// question the flat IR cannot ask without a bespoke walk. It does
	// not say whether the reference counting around it is right, and it
	// should not be read as if it did.
	LiveAfter bool

	// LaterUses are the uses that make LiveAfter true — every use of the
	// operand OR of one of its pass-through aliases that this op can
	// reach. Empty exactly when LiveAfter is false.
	//
	// It is here because the boolean alone invites a wrong follow-up
	// question. A caller wanting to know WHAT the later use is reaches
	// for uses.Of(Operand), gets nothing, and concludes the site has no
	// later use — because the uses are of the rc helper's RESULT, which
	// is the same object under another name. That mistake was made twice
	// within an hour of this analysis being written, both times by its
	// author. Handing back the sites the answer came from removes the
	// opportunity.
	LaterUses []UseSite

	// SrcOp is the source op index this site maps back to, and Mapped
	// says whether it has one. An unmapped site is one whose answer
	// could not be applied, which is why the provenance is total by
	// construction (see Op.SrcOp).
	SrcOp  int
	Mapped bool
}

// RCSites reports every reference-count operation in f, with the
// liveness of its operand at that point.
//
// The operand is Args[0] throughout: each rc helper takes the pointer it
// acts on first, and the lift preserves argument order.
func RCSites(f *Func) []RCSite {
	uses := BuildUses(f)
	reach := reachableBlocks(f)
	var out []RCSite
	for _, b := range f.Blocks {
		for oi, o := range b.Ops {
			if o.Kind != OpCall || !rcHelpers[o.Str] || len(o.Args) == 0 {
				continue
			}
			src, mapped := o.SourceOp()
			later := usesAfter(uses, reach, b, oi, aliasesOf(f, uses, o.Args[0]), o)
			out = append(out, RCSite{
				Block:     b,
				Op:        o,
				Helper:    o.Str,
				Operand:   o.Args[0],
				LaterUses: later,
				LiveAfter: len(later) > 0,
				SrcOp:     src,
				Mapped:    mapped,
			})
		}
	}
	return out
}

// aliasesOf returns v together with every value that is v under another
// name.
//
// `__fern_rc_inc` and `__fern_rc_dec` hand back the pointer they were
// given, so the lift gives their result a fresh SSA value that denotes
// the same object. Code after the call reads the RESULT, not the
// operand — so asking only about uses of the operand reports almost
// every retain as having no later use, which is an artifact of the
// representation rather than a fact about the program. Following the
// pass-through closure is what makes the question mean what it says.
//
// `__fern_rc_is_unique` is not in the closure: it returns a boolean, not
// the pointer.
func aliasesOf(f *Func, uses *Uses, v Value) []Value {
	out := []Value{v}
	seen := map[int32]bool{v.ID: true}
	for i := 0; i < len(out); i++ {
		for _, u := range uses.Of(out[i]) {
			o := u.Op
			if o == nil || o.Kind != OpCall || o.Result.ID == 0 {
				continue
			}
			if o.Str != "__fern_rc_inc" && o.Str != "__fern_rc_dec" {
				continue
			}
			if len(o.Args) == 0 || o.Args[0].ID != out[i].ID || seen[o.Result.ID] {
				continue
			}
			seen[o.Result.ID] = true
			out = append(out, o.Result)
		}
	}
	return out
}

// usesAfter returns every use of vs (a value and its pass-through
// aliases) other than `self` that can be reached from position oi in
// block b.
//
// Two ways a use can be after this point: later in the same block, or
// anywhere in a block reachable from this one — INCLUDING b itself,
// which is what makes a loop work. A use textually earlier in a loop
// body is reached again on the next iteration, and that is exactly the
// case a textual scan gets wrong.
func usesAfter(uses *Uses, reach map[*Block]map[*Block]bool, b *Block, oi int, vs []Value, self *Op) []UseSite {
	var out []UseSite
	for _, v := range vs {
		for _, u := range uses.Of(v) {
			if u.Op == self {
				continue
			}
			if u.Block == b && !reach[b][b] {
				// Straight-line block: only a later position counts.
				if indexOfOp(b, u.Op) > oi {
					out = append(out, u)
				}
				continue
			}
			if reach[b][u.Block] {
				out = append(out, u)
			}
		}
	}
	return out
}

// indexOfOp returns the position of op in b, or -1 for a terminator
// operand use (Op is nil), which always counts as after the ops.
func indexOfOp(b *Block, op *Op) int {
	if op == nil {
		return len(b.Ops)
	}
	for i, o := range b.Ops {
		if o == op {
			return i
		}
	}
	return -1
}

// reachableBlocks builds, for every block, the set of blocks reachable
// from it along CFG edges. A block reaches ITSELF only through a cycle,
// which is the property the loop case above rests on.
func reachableBlocks(f *Func) map[*Block]map[*Block]bool {
	out := make(map[*Block]map[*Block]bool, len(f.Blocks))
	for _, b := range f.Blocks {
		seen := map[*Block]bool{}
		stack := append([]*Block(nil), b.Succs()...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == nil || seen[n] {
				continue
			}
			seen[n] = true
			stack = append(stack, n.Succs()...)
		}
		out[b] = seen
	}
	return out
}
