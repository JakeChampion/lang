package ir

import (
	"fmt"
	"sort"
	"strings"
)

// Recognition of `std/array` combinator pipelines, so a chain like
// `xs.map(f).map(g).reduce(h)` appears in the IR as one dataflow shape with
// its stages rather than as three unrelated opaque calls (#9730).
//
// THIS PASS CHANGES NOTHING. It answers a question; it does not rewrite. That
// is deliberate and is the whole point of landing it separately from fusion
// (#9731): a recogniser with no optimisation attached can be proved to
// recognise the right things, and the performance delta that fusion then
// produces is attributable because this step produced none.
//
// docs/ARRAY-ALGEBRA.md settles what may later be done with a recognized
// pipeline; docs/ARRAY-PIPELINE-BASELINE-2026-09.md is what makes it worth
// recognizing. Neither is consulted here — this file only finds the shape.
//
// # How a pipeline appears, and why the matcher is shaped the way it is
//
// Not as a stack chain. The lowering stores each stage's result into a local
// and reloads it as the next stage's receiver, so `xs.map(f).map(g)` is
//
//	local.load  0          ; xs
//	make_closure …         ; f, parked in its own slot
//	local.store 4
//	local.load  4
//	call __method_Array_map__i64__i64 argc=2
//	…closure release…
//	local.store 3          ; the intermediate
//	local.load  3          ; …read straight back as the next receiver
//	make_closure …         ; g
//	…
//	call __method_Array_map__i64__i64 argc=2
//	local.load  3          ; and released, which is ALSO a read of slot 3
//	call __fern_arr_dec
//
// Two things follow, and the second is the one that theory gets wrong. Stages
// are linked by SLOT, not by stack position — so the matcher tracks slots and
// needs no operand-stack model. And the intermediate's slot is read a second
// time by its own reclaim, so "the intermediate is read once" is false as
// stated: it is read once by the next stage and once by `__fern_arr_dec`, and
// a matcher that did not know that would refuse every real pipeline.
//
// # What is refused, and why refusal is the safe direction
//
// A stage whose result slot is read by anything other than the next stage or a
// reclaim helper is not chained: the value is used elsewhere, so there is no
// single traversal to speak of. That is the "intervening binding used twice"
// case, and it is refused rather than approximated. Everything this file does
// not understand comes out as a shorter chain or no chain at all, never as a
// chain that is not there.

// arrayStageKind is the family a recognized combinator belongs to. The
// families are docs/ARRAY-ALGEBRA.md's, narrowed to the set #9730 asks for:
// elementwise, reduction and selection, which is what the three baseline
// pipelines of #9728 are built from.
type arrayStageKind int

const (
	stageElementwise arrayStageKind = iota // map
	stageSelection                         // filter
	stageReduction                         // fold, reduce
	stagePrefix                            // scan
)

func (k arrayStageKind) String() string {
	switch k {
	case stageElementwise:
		return "elementwise"
	case stageSelection:
		return "selection"
	case stageReduction:
		return "reduction"
	case stagePrefix:
		return "prefix"
	}
	return "unknown"
}

// cardinality is what a stage does to the element count. It is the fact a
// fusion pass needs first — a fixed trip count is what lets `map` fuse into
// its consumer, and `filter` not having one is why #9728 measured it as a
// separate pipeline.
type cardinality int

const (
	cardSame     cardinality = iota // out == in
	cardVariable                    // out <= in, not known until the traversal runs
	cardScalar                      // out is one value
)

func (c cardinality) String() string {
	switch c {
	case cardSame:
		return "n->n"
	case cardVariable:
		return "n->k"
	case cardScalar:
		return "n->1"
	}
	return "?"
}

// ArrayStage is one recognized combinator call.
type ArrayStage struct {
	// Verb is the combinator as written: map, filter, fold, reduce, scan.
	Verb string
	// Callee is the mangled name the call actually targets, kept because the
	// two spellings (method form and free form) both reach here and a report
	// that hid which one ran would be hiding the thing a reader is checking.
	Callee string
	Kind   arrayStageKind
	Card   cardinality
	// Element names the function the stage applies per element, when the
	// lowering parked it in a slot this matcher could follow. Empty means the
	// stage was recognized but its element function was not — which is a
	// refusal to fuse under docs/ARRAY-ALGEBRA.md §1, not a failure to
	// recognize the stage.
	Element string
	// Op is the index of the call in the function's op stream.
	Op int
}

// ArrayPipeline is a maximal run of stages where each one consumes the
// previous one's result and nothing else does.
type ArrayPipeline struct {
	Func   string
	Line   int
	Col    int
	Stages []ArrayStage
	// Stop names the rule that ended the chain. docs/ARRAY-ALGEBRA.md §7: a
	// report that says only "did not fuse" is not the answer, because the
	// thing a reader is trying to find out is which rule to argue with.
	Stop ArrayRefusal
}

// ArrayRefusal is why a chain stopped where it did.
//
// A CLOSED set, deliberately, and #9732 asks for it in those words: the
// refusals are the work-remaining checklist for the fusion pass, and a
// checklist made of free-text strings cannot be counted. `FERN_ARRAY_REPORT=1`
// tallies them, which is the same property `FERN_SSA_REPORT` has — the set of
// things declined IS the list of what is left to build.
type ArrayRefusal int

const (
	// RefusalNone: the chain ended because it was finished, not because
	// anything declined it. A reduction produces a scalar, so there is
	// nothing left to chain and nothing to explain.
	RefusalNone ArrayRefusal = iota
	// RefusalUnboundResult: the stage's result was not bound to a local, so
	// no later stage in this function could name it.
	RefusalUnboundResult
	// RefusalConsumerNotInAlgebra: the next thing to read the result is not a
	// recognized std/array combinator.
	RefusalConsumerNotInAlgebra
	// RefusalIntermediateReadAgain: the intermediate is read by something
	// other than the next stage and its own reclaim, so the chain is not one
	// traversal and fusing it would remove an array something else needs.
	RefusalIntermediateReadAgain
)

func (r ArrayRefusal) String() string {
	switch r {
	case RefusalNone:
		return ""
	case RefusalUnboundResult:
		return "the stage's result is not bound to a local, so nothing here can consume it"
	case RefusalConsumerNotInAlgebra:
		return "the next consumer is not a recognized std/array combinator"
	case RefusalIntermediateReadAgain:
		return "the intermediate is read again, so this is not one traversal"
	}
	return "unknown"
}

// Tag is the short, stable name the histogram counts. Separate from String()
// because a tally wants a key that does not move when the prose is reworded.
func (r ArrayRefusal) Tag() string {
	switch r {
	case RefusalNone:
		return "complete"
	case RefusalUnboundResult:
		return "unbound-result"
	case RefusalConsumerNotInAlgebra:
		return "consumer-not-in-algebra"
	case RefusalIntermediateReadAgain:
		return "intermediate-read-again"
	}
	return "unknown"
}

// allRefusals is every reason, for the histogram to report a zero against
// rather than omitting a row nobody then notices is missing.
var allRefusals = []ArrayRefusal{
	RefusalNone, RefusalUnboundResult, RefusalConsumerNotInAlgebra, RefusalIntermediateReadAgain,
}

// Materializes counts the stages that build an array.
//
// Today that is every stage that does not reduce to a scalar, because nothing
// fuses yet — so this number IS what a fusion pass would remove, and printing
// it is how the report answers "why did this allocate" without claiming a
// fusion that did not happen (#9732). docs/ARRAY-PIPELINE-BASELINE-2026-09.md
// measured what one such intermediate costs: about 24.8 fresh bytes per input
// element on the cold pass, and an allocator call per geometric regrow.
func (p ArrayPipeline) Materializes() int {
	n := 0
	for _, s := range p.Stages {
		if s.Card != cardScalar {
			n++
		}
	}
	return n
}

// Shape renders the pipeline the way the report prints it, e.g.
// "map(n->n) -> map(n->n) -> reduce(n->1)".
func (p ArrayPipeline) Shape() string {
	parts := make([]string, 0, len(p.Stages))
	for _, s := range p.Stages {
		parts = append(parts, fmt.Sprintf("%s(%s)", s.Verb, s.Card))
	}
	return strings.Join(parts, " -> ")
}

// arrayVerbs maps a recognized callee to its verb. Both spellings are listed
// because `Inline` may rewrite the one-line method delegate in std/array into
// a direct call to the free function, so which one a given build presents
// depends on whether the battery has run.
//
// Keyed on the mangled prefix rather than a bare name, which is what makes
// this std/array's `map` and not a user's: modload reserves the stdlib module
// prefixes first, so no user module can mangle to `array__`, and the `Array`
// method namespace admits one claimant per name (E006). docs/ARRAY-ALGEBRA.md
// §6 settles this as the recognition boundary, and records that a trait-based
// one is the better long-run answer.
var arrayVerbs = map[string]struct {
	verb string
	kind arrayStageKind
	card cardinality
}{
	"map":    {"map", stageElementwise, cardSame},
	"filter": {"filter", stageSelection, cardVariable},
	"fold":   {"fold", stageReduction, cardScalar},
	"reduce": {"reduce", stageReduction, cardScalar},
	"scan":   {"scan", stagePrefix, cardSame},
}

// arrayVerbOf returns the verb a callee names, or "" when it names none.
func arrayVerbOf(callee string) (string, bool) {
	var base string
	switch {
	case strings.HasPrefix(callee, "__method_Array_"):
		base = callee[len("__method_Array_"):]
	case strings.HasPrefix(callee, "array__"):
		base = callee[len("array__"):]
	default:
		return "", false
	}
	// Strip the monomorphiser's `__<type>` suffixes. A verb never contains
	// "__" itself, so the first one ends the name.
	if i := strings.Index(base, "__"); i >= 0 {
		base = base[:i]
	}
	if _, ok := arrayVerbs[base]; !ok {
		return "", false
	}
	return base, true
}

// reclaimCallees are the helpers that read a slot only to release what it
// holds. A read by one of these does not mean the value is used again, which
// is why the matcher has to know them by name rather than counting loads.
func isReclaimCallee(op Op) bool {
	if op.Kind != OpCallDirect {
		return false
	}
	if op.Runtime {
		// Every backend reclaim helper: __fern_arr_dec, __fern_str_dec, …
		return true
	}
	return strings.HasPrefix(op.Str, "__drop_")
}

// RecognizeArrayPipelines finds every std/array combinator chain in the
// program, longest first within a function, in a deterministic order.
func RecognizeArrayPipelines(p *Program) []ArrayPipeline {
	var out []ArrayPipeline
	for _, fn := range p.Funcs {
		// std/array's own bodies are skipped. The method forms are one-line
		// delegates — `(xs: T[]) map(f) { return map(xs, f); }` — so each is
		// a recognized call inside a recognized function, and reporting them
		// would bury a user's three-stage chain under one entry per stdlib
		// wrapper their program happened to instantiate. A combinator
		// implemented in terms of another is the library's business.
		if _, isStdlibBody := arrayVerbOf(fn.Name); isStdlibBody {
			continue
		}
		out = append(out, recognizeInFunc(fn)...)
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

// call describes one recognized combinator call and the slots around it.
type arrayCall struct {
	op      int
	verb    string
	callee  string
	recv    int32 // slot the receiver was loaded from, -1 if not a slot load
	result  int32 // slot the result was stored into, -1 if not stored
	element string
	// fnSlot is where the element function was parked, which fusion needs in
	// order to load it; -1 when the matcher could not follow it. The NAME
	// alone is not enough — a fused loop calls the value, not the symbol.
	fnSlot int32
}

func recognizeInFunc(fn *Func) []ArrayPipeline {
	calls := collectArrayCalls(fn)
	if len(calls) == 0 {
		return nil
	}
	loads := countSlotLoads(fn)

	// Chain: stage n+1's receiver slot is stage n's result slot, and that
	// slot is read exactly twice — once by the next stage, once by the
	// reclaim that releases the intermediate. A third read means the value
	// went somewhere else and there is no single traversal.
	used := make([]bool, len(calls))
	var out []ArrayPipeline
	for i := range calls {
		if used[i] {
			continue
		}
		chain := []int{i}
		stop := RefusalNone
		for {
			last := calls[chain[len(chain)-1]]
			if last.result < 0 {
				stop = RefusalUnboundResult
				break
			}
			next := -1
			for j := range calls {
				if !used[j] && j > chain[len(chain)-1] && calls[j].recv == last.result {
					next = j
					break
				}
			}
			if next < 0 {
				stop = RefusalConsumerNotInAlgebra
				break
			}
			if loads[last.result].other > 0 {
				// The intermediate is read by something that is neither the
				// next stage nor a reclaim. Refuse rather than guess.
				stop = RefusalIntermediateReadAgain
				break
			}
			chain = append(chain, next)
		}
		stages := make([]ArrayStage, 0, len(chain))
		for _, k := range chain {
			used[k] = true
			c := calls[k]
			v := arrayVerbs[c.verb]
			stages = append(stages, ArrayStage{
				Verb: v.verb, Callee: c.callee, Kind: v.kind, Card: v.card,
				Element: c.element, Op: c.op,
			})
		}
		pos := fn.Ops[calls[chain[0]].op].Pos
		if len(stages) > 0 && stages[len(stages)-1].Card == cardScalar {
			// A reduction ends the pipeline by producing a scalar; there is
			// nothing left to chain and no refusal to report.
			stop = RefusalNone
		}
		out = append(out, ArrayPipeline{
			Func: fn.Name, Line: pos.Line, Col: pos.Col, Stages: stages, Stop: stop,
		})
	}
	return out
}

// collectArrayCalls walks the op stream once, picking out the recognized
// calls and reading the slots on either side of each.
//
// The receiver is the FIRST slot loaded in the run of ops since the previous
// call; the callback is parked in a slot of its own and loaded last, so the
// first load in that window is the receiver and the last is the callback.
// The result is the first slot stored after the call.
func collectArrayCalls(fn *Func) []arrayCall {
	var out []arrayCall
	// closureOf records which lambda a slot was last given, so a callback
	// parked in a slot can be named.
	closureOf := map[int32]string{}
	windowStart := 0
	for i, op := range fn.Ops {
		if op.Kind == OpMakeClosure || op.Kind == OpConstFunc {
			// The store that parks it is the next op in practice; record
			// against whatever slot takes it.
			if i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpStoreLocal {
				closureOf[fn.Ops[i+1].I32] = op.Str
			}
			continue
		}
		if op.Kind != OpCallDirect {
			continue
		}
		verb, ok := arrayVerbOf(op.Str)
		if !ok || op.Runtime {
			windowStart = i + 1
			continue
		}
		c := arrayCall{op: i, verb: verb, callee: op.Str, recv: -1, result: -1, fnSlot: -1}
		for k := windowStart; k < i; k++ {
			if fn.Ops[k].Kind != OpLoadLocal {
				continue
			}
			// The last load before the call is the callback slot.
			if name, isClosure := closureOf[fn.Ops[k].I32]; isClosure {
				c.element = name
				c.fnSlot = fn.Ops[k].I32
			}
		}
		c.recv = receiverSlotOf(fn, i, op.I32, c.fnSlot)
		for k := i + 1; k < len(fn.Ops); k++ {
			if fn.Ops[k].Kind == OpStoreLocal {
				c.result = fn.Ops[k].I32
				break
			}
			if fn.Ops[k].Kind == OpCallDirect && !isReclaimCallee(fn.Ops[k]) {
				break
			}
		}
		out = append(out, c)
		windowStart = i + 1
	}
	return out
}

// slotLoads counts how a slot is read: by a reclaim helper, or by anything
// else. A pipeline's intermediate is expected to be read once as the next
// stage's receiver and once by its reclaim; `other` counting more than the
// receiver read is what says the value escaped the chain.
type slotLoads struct{ reclaim, other int }

func countSlotLoads(fn *Func) map[int32]*slotLoads {
	out := map[int32]*slotLoads{}
	get := func(s int32) *slotLoads {
		if out[s] == nil {
			out[s] = &slotLoads{}
		}
		return out[s]
	}
	for i, op := range fn.Ops {
		if op.Kind != OpLoadLocal {
			continue
		}
		// Look ahead for the call this load feeds, shallowly: a reclaim
		// reads its operand and calls within a couple of ops.
		reclaim := false
		for k := i + 1; k < len(fn.Ops) && k <= i+3; k++ {
			if fn.Ops[k].Kind == OpCallDirect {
				reclaim = isReclaimCallee(fn.Ops[k])
				break
			}
		}
		if reclaim {
			get(op.I32).reclaim++
		} else {
			get(op.I32).other++
		}
	}
	// The receiver read of the next stage is an `other`, and every chained
	// intermediate has exactly one. Discount it so `other > 0` means a read
	// beyond the chain.
	for _, v := range out {
		if v.other > 0 {
			v.other--
		}
	}
	return out
}

// FormatArrayPipelineHistogram tallies the program's pipelines by how they
// ended, and by how much they materialize.
//
// The shape is FERN_SSA_REPORT's, for the reason #9732 gives: the set of
// refusals is the coverage checklist for the fusion pass, so it has to be
// countable. Every reason gets a row even at zero — a reason that vanished
// from the output when it stopped firing is one nobody notices is missing.
func FormatArrayPipelineHistogram(p *Program) string {
	pipes := RecognizeArrayPipelines(p)
	verdicts := ArrayFusionVerdicts(p)
	counts := map[ArrayRefusal]int{}
	fusionCounts := map[FusionRefusal]int{}
	stages, materialize, chained, fused := 0, 0, 0, 0
	for _, pl := range pipes {
		counts[pl.Stop]++
		stages += len(pl.Stages)
		materialize += pl.Materializes()
		if len(pl.Stages) > 1 {
			chained++
		}
		why := verdicts[arrayPipelineKey(pl.Func, pl)]
		fusionCounts[why]++
		if why == FusionFused {
			fused++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "array pipelines: %d (%d with more than one stage), %d stages, %d materializing\n",
		len(pipes), chained, stages, materialize)
	fmt.Fprintf(&b, "fused: %d (#9731)\n", fused)
	for _, r := range AllFusionRefusals {
		fmt.Fprintf(&b, "  %-26s %d\n", r.Tag(), fusionCounts[r])
	}
	fmt.Fprintf(&b, "chains stopped by (#9730):\n")
	for _, r := range allRefusals {
		fmt.Fprintf(&b, "  %-26s %d\n", r.Tag(), counts[r])
	}
	return b.String()
}

// FormatArrayPipelines renders what the program's array pipelines are, in the
// shape `-append-report` established: one line per site, naming the rule that
// decided, and a summary. docs/ARRAY-ALGEBRA.md §7 is why the rule is printed
// rather than just the verdict.
func FormatArrayPipelines(p *Program) string {
	pipes := RecognizeArrayPipelines(p)
	if len(pipes) == 0 {
		return "no std/array pipelines\n"
	}
	posOf := make([]string, len(pipes))
	posW := 0
	for i, pl := range pipes {
		posOf[i] = fmt.Sprintf("%s:%d:%d", pl.Func, pl.Line, pl.Col)
		if len(posOf[i]) > posW {
			posW = len(posOf[i])
		}
	}
	verdicts := ArrayFusionVerdicts(p)
	var b strings.Builder
	chained := 0
	for i, pl := range pipes {
		if len(pl.Stages) > 1 {
			chained++
		}
		fmt.Fprintf(&b, "%-*s  %s\n", posW, posOf[i], pl.Shape())
		// "Did it fuse?" is the first question #9732 asks, so it is answered
		// per site rather than once at the bottom: a reader checking one
		// expression should not have to know that the absence of a word means
		// no.
		fmt.Fprintf(&b, "%-*s    %s; %d stage(s) materialize unfused\n",
			posW, "", verdicts[arrayPipelineKey(pl.Func, pl)], pl.Materializes())
		if pl.Stop != RefusalNone {
			fmt.Fprintf(&b, "%-*s    chain ends here: %s\n", posW, "", pl.Stop)
		}
		for _, s := range pl.Stages {
			elem := s.Element
			if elem == "" {
				elem = "(element function not statically resolved)"
			}
			fmt.Fprintf(&b, "%-*s    %-10s %-12s %s\n", posW, "", s.Verb, s.Kind, elem)
		}
	}
	fmt.Fprintf(&b, "\n%d pipeline(s), %d with more than one stage\n", len(pipes), chained)
	return b.String()
}

// elementFuncStart returns the index at which the element function's push
// sequence begins — the ops from there up to the call are exactly what put the
// function on the stack, whether the chain built a closure here or loaded one
// already built.
//
// Callers use it to walk the call's arguments backwards. The alternative, and
// what this replaced, is to scan forward for the first local load since the
// previous call: inside a `while` that finds the loop condition's counter
// rather than the array, and a fusion built on that answer traverses the wrong
// slot.
func elementFuncStart(fn *Func, callIdx int, fnSlot int32) (int, bool) {
	if fnSlot < 0 {
		return 0, false
	}
	k := -1
	for i := callIdx - 1; i >= 0; i-- {
		if fn.Ops[i].Kind == OpLoadLocal && fn.Ops[i].I32 == fnSlot {
			k = i
			break
		}
	}
	if k < 0 {
		return 0, false
	}
	for k > 0 {
		switch prev := fn.Ops[k-1]; prev.Kind {
		case OpStoreLocal:
			if prev.I32 != fnSlot {
				return k, true
			}
			k--
		case OpConstFunc:
			k--
		case OpMakeClosure:
			k--
			// A closure's captures are pushed ahead of it. Only single-op
			// captures are stepped over; an expression has no fixed width to
			// skip, so the caller is told nothing rather than a wrong index.
			for n := prev.I32; n > 0; n-- {
				if k == 0 || !isSingleOpPush(fn.Ops[k-1]) {
					return 0, false
				}
				k--
			}
		default:
			return k, true
		}
	}
	return k, true
}

// receiverSlotOf returns the slot holding the array a combinator call is
// applied to, or -1. The receiver is the call's FIRST argument, so it is found
// by starting at the element function (the last) and stepping back over the
// arguments between them.
func receiverSlotOf(fn *Func, callIdx int, argc, fnSlot int32) int32 {
	k, ok := elementFuncStart(fn, callIdx, fnSlot)
	if !ok {
		return -1
	}
	// `fold(xs, seed, f)` and `scan(xs, seed, f)` put one argument between the
	// receiver and the function; `map(xs, f)` and the rest put none.
	for n := argc - 2; n > 0; n-- {
		if k == 0 || !isSingleOpPush(fn.Ops[k-1]) {
			return -1
		}
		k--
	}
	if k == 0 || fn.Ops[k-1].Kind != OpLoadLocal {
		return -1
	}
	return fn.Ops[k-1].I32
}

// isSingleOpPush reports whether op pushes one value and consumes none.
func isSingleOpPush(op Op) bool {
	switch op.Kind {
	case OpLoadLocal, OpConstI32, OpConstI64, OpConstFunc:
		return true
	}
	return false
}
