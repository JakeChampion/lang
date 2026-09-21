package ir

import (
	"fmt"
	"sort"
	"strings"
)

// What the layout of an std/ndarray handle is at a site, derived from where
// the handle came from (#9734). Like the two recognisers beside it this
// CHANGES NOTHING; what it adds is the one fact the rest of the phase needs
// and could not ask for.
//
// docs/ARRAY-SHAPES.md §2 is a table of operations against storage, and its
// last three rows — `reshape`, `packed()`, `to_flat()` — branch at RUN TIME
// on `is_row_major()` / `is_packed()`. So two textually identical calls cost
// different amounts, and nothing in the IR could tell them apart:
//
//	var a = nd.from_flat(xs, s);   var f1 = a.to_flat();            // free
//	var t = a.transpose();         var f2 = t.to_flat();            // O(n)
//
// That is the avoidable O(n) copy #9734 says concise array code must not
// conceal, sitting inside the IR unremarked. §8's licence for an in-place
// elementwise kernel needs the same fact from the other direction: it reads
// "consumed, unique, and `is_packed()`", and a planner can take it only where
// the third conjunct is decided before the program runs.
//
// The predicates are decided by the metadata, and the metadata is decided by
// the chain of operations that built the handle — every one of which §2 names.
// So the layout is a property the IR can carry, and this is it.
//
// Recognition is by resolved identity, the boundary §6 sets: a user's own
// `NdArray` mangles differently and is not this module's.

// NdarrayLayout is what a handle's metadata is known to satisfy. The order is
// the lattice order, weakest first: a meet is a minimum.
type NdarrayLayout int

const (
	// NdarrayLayoutUnknown — the handle's provenance was not followed, so
	// nothing is claimed. A parameter, a struct field, the result of a call
	// this pass does not model.
	NdarrayLayoutUnknown NdarrayLayout = iota
	// NdarrayLayoutStrided — the provenance WAS followed, to one of §2's
	// metadata operations, and that operation does not preserve either
	// predicate. Nothing is claimed either; the difference from Unknown is
	// that the pass knows why, which is what makes the two separate rows of
	// a coverage checklist rather than one.
	NdarrayLayoutStrided
	// NdarrayLayoutRowMajor — `is_row_major()` is provably true, so
	// `reshape` takes its metadata branch. `offset` may be non-zero and the
	// storage may hold more than the handle reads, so `to_flat()` may still
	// copy.
	NdarrayLayoutRowMajor
	// NdarrayLayoutPacked — `is_packed()` is provably true: row-major from
	// element 0 with nothing else in the storage, so `data` IS the reading
	// order and all three of §2's branching rows take their free arm.
	NdarrayLayoutPacked
)

// AllNdarrayLayouts is every layout, strongest first, so the report prints a
// row per layout even at zero.
var AllNdarrayLayouts = []NdarrayLayout{
	NdarrayLayoutPacked,
	NdarrayLayoutRowMajor,
	NdarrayLayoutStrided,
	NdarrayLayoutUnknown,
}

// Tag is the stable one-word name a histogram counts under.
func (l NdarrayLayout) Tag() string {
	switch l {
	case NdarrayLayoutPacked:
		return "packed"
	case NdarrayLayoutRowMajor:
		return "row-major"
	case NdarrayLayoutStrided:
		return "strided"
	case NdarrayLayoutUnknown:
		return "unknown"
	}
	return "unknown"
}

// String is what the tag asserts.
func (l NdarrayLayout) String() string {
	switch l {
	case NdarrayLayoutPacked:
		return "`is_packed()` is provably true"
	case NdarrayLayoutRowMajor:
		return "`is_row_major()` is provably true, `is_packed()` is not proven"
	case NdarrayLayoutStrided:
		return "built by a metadata operation that preserves neither predicate"
	case NdarrayLayoutUnknown:
		return "the handle's provenance was not followed"
	}
	return "no layout recorded"
}

// ProvesPacked reports whether the layout decides `is_packed()`, which is the
// branch `to_flat()` and `packed()` take and the third conjunct of §8's
// in-place licence.
func (l NdarrayLayout) ProvesPacked() bool { return l == NdarrayLayoutPacked }

// ProvesRowMajor reports whether the layout decides `is_row_major()`, which is
// the branch `reshape` takes.
func (l NdarrayLayout) ProvesRowMajor() bool {
	return l == NdarrayLayoutPacked || l == NdarrayLayoutRowMajor
}

func ndarrayLayoutMeet(a, b NdarrayLayout) NdarrayLayout {
	if a < b {
		return a
	}
	return b
}

// ndarrayLayoutOfCall is §2's table and §7's, read as transfer functions: what
// the layout of a call's RESULT is, given the layout of its receiver. ok is
// false when the callee does not produce a handle at all.
//
// Every arm is the operation's own code in internal/stdlib/std/ndarray.fern:
//
//   - `from_flat` aborts unless `count_of(shape) == data.len()`, then takes
//     the row-major strides at offset 0 — all three conjuncts, so packed.
//   - the metadata operations keep `data` and perturb the strides, the offset
//     or both; none preserves a predicate in a way that survives an unknown
//     rank, since §4 makes the rank a run-time property.
//   - `reshape` is row-major whichever branch it takes: the metadata branch
//     writes `row_major(shape)` outright, and the copying branch is a
//     `from_flat`. It is packed as well from a packed receiver, whose offset
//     is 0 and whose storage holds exactly the count the new shape must have.
//   - `packed()` returns the receiver when it is packed and a `from_flat` copy
//     otherwise, so it is packed from anything.
//   - every operation of §7 that returns a handle returns `from_flat` of a
//     buffer it just filled. `fold_all` returns a scalar and `to_flat` a
//     `T[]`, so neither is here.
func ndarrayLayoutOfCall(callee string, recv NdarrayLayout) (NdarrayLayout, bool) {
	if strings.HasPrefix(callee, ndarrayFromFlatPrefix) {
		return NdarrayLayoutPacked, true
	}
	verb, ok := ndarrayVerbOfAny(callee)
	if !ok {
		return NdarrayLayoutUnknown, false
	}
	switch verb {
	case "transpose", "permute", "reverse", "slice", "select", "broadcast_to":
		return NdarrayLayoutStrided, true
	case "reshape":
		if recv.ProvesPacked() {
			return NdarrayLayoutPacked, true
		}
		return NdarrayLayoutRowMajor, true
	case "packed":
		return NdarrayLayoutPacked, true
	case "map", "zip_with", "outer", "inner", "reduce_axis", "scan_axis", "map_rank":
		return NdarrayLayoutPacked, true
	}
	return NdarrayLayoutUnknown, false
}

const ndarrayFromFlatPrefix = "ndarray__from_flat__"

// ndarrayVerbOfAny is ndarrayVerbOf without the §7 filter: the receiver method
// of std/ndarray a callee names, whether or not it is one of the operations
// handed a function.
func ndarrayVerbOfAny(callee string) (string, bool) {
	if !strings.HasPrefix(callee, ndarrayMethodPrefix) {
		return "", false
	}
	base := callee[len(ndarrayMethodPrefix):]
	if i := strings.Index(base, "__"); i >= 0 {
		base = base[:i]
	}
	return base, base != ""
}

// ndarrayStorageSensitive is §2's three branching rows: the operations whose
// cost depends on the receiver's layout, and what the layout has to prove for
// them to move no elements.
var ndarrayStorageSensitive = map[string]NdarrayLayout{
	"to_flat": NdarrayLayoutPacked,
	"packed":  NdarrayLayoutPacked,
	"reshape": NdarrayLayoutRowMajor,
}

// NdarrayCopyVerdict is whether a storage-sensitive site is known to move no
// elements, or what the layout failed to prove. Closed and tagged, like the
// kernel sets beside it.
type NdarrayCopyVerdict int

const (
	NdarrayCopyMetadata NdarrayCopyVerdict = iota
	NdarrayCopyNotProvenPacked
	NdarrayCopyNotProvenRowMajor
)

// AllNdarrayCopyVerdicts is every verdict, so the report prints a row per
// verdict even at zero.
var AllNdarrayCopyVerdicts = []NdarrayCopyVerdict{
	NdarrayCopyMetadata,
	NdarrayCopyNotProvenPacked,
	NdarrayCopyNotProvenRowMajor,
}

// Tag is the stable one-word name a histogram counts under.
func (v NdarrayCopyVerdict) Tag() string {
	switch v {
	case NdarrayCopyMetadata:
		return "metadata"
	case NdarrayCopyNotProvenPacked:
		return "not-proven-packed"
	case NdarrayCopyNotProvenRowMajor:
		return "not-proven-row-major"
	}
	return "unknown"
}

// String is the reason clause, without a verdict in front of it.
func (v NdarrayCopyVerdict) String() string {
	switch v {
	case NdarrayCopyMetadata:
		return "the receiver's layout proves the free branch, so no element moves"
	case NdarrayCopyNotProvenPacked:
		return "the receiver is not proven packed, so the call may copy its elements"
	case NdarrayCopyNotProvenRowMajor:
		return "the receiver is not proven row-major, so the call may copy its elements"
	}
	return "no reason recorded"
}

// NdarrayLayoutSite is one call whose cost depends on its receiver's layout.
type NdarrayLayoutSite struct {
	Func string
	Line int
	Col  int
	// Verb is the operation as written: to_flat, packed or reshape.
	Verb string
	// Callee is the mangled, monomorphised name the call targets.
	Callee string
	// Receiver is what the pass proved about the handle the call was made
	// on.
	Receiver NdarrayLayout
	// Verdict is whether that is enough for the call to move no elements.
	Verdict NdarrayCopyVerdict
	// Op is the index of the call in the function's op stream.
	Op int
}

// RecognizeNdarrayLayouts finds every storage-sensitive call in the program,
// in a deterministic order, with what the pass proved about its receiver.
func RecognizeNdarrayLayouts(p *Program) []NdarrayLayoutSite {
	sigs := buildFuncSigs(p)
	shapes := NewCallShapes(p)
	var out []NdarrayLayoutSite
	for _, fn := range p.Funcs {
		if isNdarrayBody(fn) {
			continue
		}
		recv := ndarrayReceiverLayouts(fn, shapes, sigs)
		for i, op := range fn.Ops {
			if op.Kind != OpCallDirect || op.Runtime {
				continue
			}
			verb, ok := ndarrayVerbOfAny(op.Str)
			if !ok {
				continue
			}
			need, sensitive := ndarrayStorageSensitive[verb]
			if !sensitive {
				continue
			}
			at := recv[i]
			verdict := NdarrayCopyMetadata
			switch {
			case need == NdarrayLayoutPacked && !at.ProvesPacked():
				verdict = NdarrayCopyNotProvenPacked
			case need == NdarrayLayoutRowMajor && !at.ProvesRowMajor():
				verdict = NdarrayCopyNotProvenRowMajor
			}
			out = append(out, NdarrayLayoutSite{
				Func: fn.Name, Line: op.Pos.Line, Col: op.Pos.Col,
				Verb: verb, Callee: op.Str, Receiver: at, Verdict: verdict, Op: i,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Func != out[j].Func {
			return out[i].Func < out[j].Func
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// NdarrayReceiverLayouts is the layout of the receiver at every std/ndarray
// call in fn, keyed by op index. The algebra recogniser reads it too: §6 says
// a reduction along the last axis walks contiguous storage and one along any
// other axis strides, and that is only the whole question when the storage IS
// contiguous — a strided receiver strides along every axis.
func NdarrayReceiverLayouts(p *Program, fn *Func) map[int]NdarrayLayout {
	return ndarrayReceiverLayouts(fn, NewCallShapes(p), buildFuncSigs(p))
}

// ndarrayReceiverLayouts runs the layout analysis over one function.
//
// Slots are flow-INSENSITIVE: a slot's layout is the meet of every layout
// stored into it anywhere in the function, and a parameter claims nothing. A
// slot assigned a packed handle and later a transposed one therefore reads
// strided at both, which costs precision and needs no reasoning about which
// store reaches which load — the answer is correct whatever the control flow
// does. The fixpoint is what makes a loop-carried slot settle.
//
// The operand stack is tracked only through ops whose effect is known. A
// control-flow op or an unresolved call empties it, and a pop from an empty
// stack yields Unknown, so what is tracked is always a suffix of what is
// really there.
func ndarrayReceiverLayouts(fn *Func, shapes *CallShapes, sigs map[string]funcSig) map[int]NdarrayLayout {
	// Each round starts every slot at the weakest layout and can only
	// strengthen one, so a slot moves at most three times and the whole map
	// settles within three rounds per slot plus the one that confirms it.
	// The bound is a bound, not a limit the ascent reaches: it is here so
	// that a transfer function made non-monotone later stops and claims
	// nothing rather than spinning.
	rounds := 3*(len(fn.Params)+len(fn.Locals)+len(fn.ScratchTypes)) + 2
	slots := map[int32]NdarrayLayout{}
	var recv map[int]NdarrayLayout
	for round := 0; round < rounds; round++ {
		next := map[int32]NdarrayLayout{}
		recv = ndarrayLayoutWalk(fn, shapes, sigs, slots, next)
		if ndarrayLayoutsEqual(slots, next) {
			return recv
		}
		slots = next
	}
	return ndarrayLayoutWalk(fn, shapes, sigs, map[int32]NdarrayLayout{}, map[int32]NdarrayLayout{})
}

func ndarrayLayoutsEqual(a, b map[int32]NdarrayLayout) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// ndarrayLayoutWalk is one pass: it reads slot layouts from `in`, records the
// layouts stored into each slot in `out`, and returns the receiver layout at
// every std/ndarray call it could resolve.
func ndarrayLayoutWalk(fn *Func, shapes *CallShapes, sigs map[string]funcSig, in, out map[int32]NdarrayLayout) map[int]NdarrayLayout {
	recv := map[int]NdarrayLayout{}
	stack := []ndarrayVal{}
	// A parameter's layout is whatever the caller had, which this pass does
	// not follow across a call boundary.
	for i := range fn.Params {
		out[int32(i)] = NdarrayLayoutUnknown
	}
	push := func(v ndarrayVal, n int) {
		for ; n > 0; n-- {
			stack = append(stack, v)
		}
	}
	// pop removes n operands and returns the DEEPEST of them, which for a
	// call is the receiver: it was pushed first. Running out means the
	// operand came from before the walk could see it.
	pop := func(n int) ndarrayVal {
		deepest := ndarrayVal{}
		for ; n > 0; n-- {
			if len(stack) == 0 {
				return ndarrayVal{}
			}
			deepest = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		}
		return deepest
	}
	store := func(slot int32, v ndarrayVal) {
		// A constant is the null the lowering writes into a slot it has
		// moved the handle out of, and no constant is a handle. The claim
		// this analysis makes is about what a slot holds WHEN IT IS LOADED
		// AS A RECEIVER, so a store that cannot have produced a handle
		// contributes nothing to it.
		if v.konst {
			return
		}
		if have, seen := out[slot]; seen {
			out[slot] = ndarrayLayoutMeet(have, v.layout)
			return
		}
		out[slot] = v.layout
	}
	for i, op := range fn.Ops {
		switch op.Kind {
		case OpLine, OpCoverPoint:
			continue
		case OpConstI32, OpConstI64, OpConstF32, OpConstF64:
			push(ndarrayVal{konst: true}, 1)
			continue
		case OpRcInc, OpRcDec:
			// Pass-through: a retained handle is the same handle.
			push(pop(1), 1)
			continue
		case OpLoadLocal:
			if op.Width == WidthString {
				push(ndarrayVal{}, 2)
				continue
			}
			push(ndarrayVal{layout: in[op.I32]}, 1)
			continue
		case OpStoreLocal, OpTeeLocal:
			if op.Width == WidthString {
				pop(2)
				store(op.I32, ndarrayVal{})
				continue
			}
			v := pop(1)
			store(op.I32, v)
			if op.Kind == OpTeeLocal {
				push(v, 1)
			}
			continue
		}
		pops, pushes, ok := ndarrayStackEffect(op, shapes, sigs)
		if !ok {
			// A control-flow op, or a call whose shape is not known here.
			// What is on the real stack is no longer known, so track
			// nothing; a later pop then reads Unknown.
			stack = stack[:0]
			continue
		}
		if isCallShaped(op) {
			at := pop(pops)
			if _, isNd := ndarrayVerbOfAny(op.Str); isNd && !op.Runtime {
				recv[i] = at.layout
			}
			l, produces := ndarrayLayoutOfCall(op.Str, at.layout)
			if !produces || op.Runtime {
				l = NdarrayLayoutUnknown
			}
			push(ndarrayVal{layout: l}, pushes)
			continue
		}
		pop(pops)
		push(ndarrayVal{}, pushes)
	}
	return recv
}

// ndarrayVal is one entry of the tracked operand stack.
type ndarrayVal struct {
	layout NdarrayLayout
	// konst marks a value a constant op pushed, which no handle ever is.
	konst bool
}

func isCallShaped(op Op) bool {
	switch op.Kind {
	case OpCallDirect, OpCallDirectPair, OpCallIndirect, OpCallDyn, OpCallClosureDirect:
		return true
	}
	return false
}

// ndarrayStackEffect is how many entries an op takes and leaves. A call's
// answer comes from CallShapes, which is the project's one definition of it
// and the only one that covers the runtime helpers the lowering threads
// between a call and the store of its result — `__fern_arr_dec` sits there,
// and giving up on it would lose the handle that is still on the stack.
func ndarrayStackEffect(op Op, shapes *CallShapes, sigs map[string]funcSig) (pops, pushes int, ok bool) {
	if isCallShaped(op) {
		args, bail := shapes.ArgSlots(op)
		if bail != "" {
			return 0, 0, false
		}
		results, bail := shapes.ResultSlots(op)
		if bail != "" {
			return 0, 0, false
		}
		if op.Kind == OpCallClosureDirect {
			args++ // the env pointer the IR appends as the last operand
		}
		return args, results, true
	}
	return opStackEffect(op, sigs)
}

// FormatNdarrayLayouts renders the storage-sensitive calls, or "" when the
// program has none, so the array report can leave the section out.
func FormatNdarrayLayouts(p *Program) string {
	sites := RecognizeNdarrayLayouts(p)
	if len(sites) == 0 {
		return ""
	}
	posOf := make([]string, len(sites))
	posW, verbW := 0, 0
	for i, s := range sites {
		posOf[i] = fmt.Sprintf("%s:%d:%d", s.Func, s.Line, s.Col)
		if len(posOf[i]) > posW {
			posW = len(posOf[i])
		}
		if len(s.Verb) > verbW {
			verbW = len(s.Verb)
		}
	}
	var b strings.Builder
	b.WriteString("std/ndarray storage-sensitive operations, and what their receiver is proved to be:\n")
	for i, s := range sites {
		fmt.Fprintf(&b, "%-*s  %-*s  over %-9s  -> %s\n",
			posW, posOf[i], verbW, s.Verb, s.Receiver.Tag(), s.Verdict.Tag())
	}
	return b.String()
}

// FormatNdarrayLayoutHistogram tallies the storage-sensitive calls by whether
// their receiver's layout decides them, or "" when the program has none.
func FormatNdarrayLayoutHistogram(p *Program) string {
	sites := RecognizeNdarrayLayouts(p)
	if len(sites) == 0 {
		return ""
	}
	verdicts := map[NdarrayCopyVerdict]int{}
	layouts := map[NdarrayLayout]int{}
	free := 0
	for _, s := range sites {
		verdicts[s.Verdict]++
		layouts[s.Receiver]++
		if s.Verdict == NdarrayCopyMetadata {
			free++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "std/ndarray storage-sensitive operations (#9734): %d, %d proved to move no elements\n", len(sites), free)
	for _, v := range AllNdarrayCopyVerdicts {
		fmt.Fprintf(&b, "  %-26s %d\n", v.Tag(), verdicts[v])
	}
	b.WriteString("their receivers' layouts:\n")
	for _, l := range AllNdarrayLayouts {
		fmt.Fprintf(&b, "  %-26s %d\n", l.Tag(), layouts[l])
	}
	return b.String()
}
