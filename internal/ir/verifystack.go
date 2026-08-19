// The IR verifier's stack half.
//
// verify.go checks the invariants a backend needs to emit anything at
// all. This file checks the one it needs to emit anything CORRECT: that
// the operand stack is discipline-obeying — every op finds its operands,
// every scope leaves what its block type promises, both arms of an `if`
// agree, a branch carries what its label expects, and the function ends
// holding exactly its result.
//
// It is the same algorithm wasm validation runs, including the
// polymorphic-stack treatment of unreachable code after a `br` or a
// `return`. That is not a coincidence: the IR is a wasm-shaped stack
// machine, so a stack-discipline break here is a module the wasm backend
// cannot emit and a register allocator will silently paper over on the
// natives — which is how the closure-dispatch cluster (#5001 / #5007 /
// #5009 / #5026) reached run time.
//
// What it deliberately does NOT check is operand WIDTH. A slot's class
// (integer-shaped vs float-shaped) is a property of the op; whether an
// integer slot holds an i32, an i64 or a pointer is not, because
// `WidthPtr` is resolved by the backend and the IR does not distinguish a
// pointer from an integer of the same width anywhere. Checking widths
// needs a pointer-aware type system the IR does not carry, and asserting
// them from the outside would report the IR's deliberate looseness as a
// defect.
//
// # Fail-soft, and why
//
// A verifier that reports a false problem gets switched off. Every
// construct this pass cannot model — an unrecognised op, a call whose
// callee's result shape is unknown — abandons the enclosing function and
// is COUNTED, never reported as a problem. Coverage is then a number the
// corpus gate can hold a floor under, so an unmodelled construct shows up
// as a coverage regression rather than as a spurious failure, and the
// table can grow monotonically.
package ir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

// valKind classifies one operand-stack slot. The IR's own distinction is
// exactly this coarse: an op either wants a float register or it does
// not.
type valKind uint8

const (
	kInt   valKind = iota // i32 / i64 / any pointer
	kFloat                // f32 / f64
	// kUnknown is what a pop yields in unreachable code, where the
	// stack is polymorphic. It matches every expectation.
	kUnknown
)

func (k valKind) String() string {
	switch k {
	case kInt:
		return "int"
	case kFloat:
		return "float"
	}
	return "unknown"
}

// Coverage records how much of a program the stack pass could model.
// Modelled + len(Skipped) == Funcs.
type Coverage struct {
	Funcs    int
	Modelled int
	// Skipped maps each unmodelled function's name to why it was
	// skipped. Per function rather than a count because the useful
	// question is which function went unchecked, not how many did.
	Skipped map[string]string
}

func (c *Coverage) skip(name, reason string) {
	if c.Skipped == nil {
		c.Skipped = map[string]string{}
	}
	c.Skipped[name] = reason
}

// Reasons tallies the skips by cause.
func (c Coverage) Reasons() map[string]int {
	out := map[string]int{}
	for _, r := range c.Skipped {
		out[r]++
	}
	return out
}

// typeSlots is how many operand-stack slots a value of type t occupies,
// and of what class. Every type is one slot except the two-word ones —
// see isTwoWord.
//
// A nil type means the lowering pass left the slot's type unrecorded
// (synthetic scratch slots are frequently untyped); those are always
// one integer-shaped word.
func (s *stackChecker) typeSlots(t ast.Type) []valKind {
	switch t.(type) {
	case nil:
		return []valKind{kInt}
	case ast.VoidType, ast.NeverType:
		return nil
	case ast.FloatType:
		return []valKind{kFloat}
	}
	if s.isTwoWord(t) {
		return []valKind{kInt, kInt}
	}
	return []valKind{kInt}
}

// erased reports whether t is a type parameter the lowering has not
// resolved. Such a value's slot shape depends on the instantiation — an
// erased `T` may arrive as one integer word, one float, or a two-word
// string — so nothing static can say what it puts on the stack, and a
// function that moves one is skipped rather than guessed at.
func erased(t ast.Type) bool {
	switch t.(type) {
	case ast.ParamType, ast.SelfType, ast.ProjType:
		return true
	}
	return false
}

func anyErased(ts []ast.Type) bool {
	for _, t := range ts {
		if erased(t) {
			return true
		}
	}
	return false
}

func (s *stackChecker) slotCount(ts []ast.Type) int {
	n := 0
	for _, t := range ts {
		n += len(s.typeSlots(t))
	}
	return n
}

// blockSlots is what a scope of the given BlockType leaves on the stack.
func blockSlots(bt int32) ([]valKind, bool) {
	switch bt {
	case BlockTypeVoid:
		return nil, true
	case BlockTypeI32, BlockTypeI64:
		return []valKind{kInt}, true
	case BlockTypeF32, BlockTypeF64:
		return []valKind{kFloat}, true
	case BlockTypeStringPair:
		return []valKind{kInt, kInt}, true
	}
	return nil, false
}

// resultShape is the result of a callee the program does not define —
// a backend-provided builtin or runtime helper, whose signature lives in
// the backends rather than in the IR.
type resultShape uint8

const (
	rVoid   resultShape = iota // no result
	rWord                      // one integer-shaped word
	rFloat                     // one float
	rString                    // a string: one word, or two under the two-word ABI
	rPair                      // a (tag, payload) pair: two words
)

func (r resultShape) slots(twoWordStr bool) []valKind {
	switch r {
	case rWord:
		return []valKind{kInt}
	case rFloat:
		return []valKind{kFloat}
	case rString:
		if twoWordStr {
			return []valKind{kInt, kInt}
		}
		return []valKind{kInt}
	case rPair:
		return []valKind{kInt, kInt}
	}
	return nil
}

// ctrlFrame is one open structured-control scope, in the shape wasm
// validation needs: the stack height it started at, what a branch to it
// carries, what falling off its end must leave, and whether the code
// being validated is still reachable.
type ctrlFrame struct {
	kind        OpKind // OpBlock / OpLoop / OpIf, or OpInvalid for the function body
	at          int
	height      int
	labelSlots  []valKind // what a br to this scope carries: nil for a loop
	endSlots    []valKind
	unreachable bool
	sawElse     bool
}

type stackChecker struct {
	f          *Func
	known      map[string]*Func
	externs    map[string]*ExternFunc
	twoWordStr bool
	ptrW       int

	stack  []valKind
	frames []ctrlFrame

	problems []Problem
	// bail is set the moment something is unmodelled: the function is
	// abandoned rather than mis-reported.
	bail string
}

func (s *stackChecker) top() *ctrlFrame { return &s.frames[len(s.frames)-1] }

func (s *stackChecker) report(i int, k OpKind, format string, args ...any) {
	s.problems = append(s.problems, Problem{
		Func: s.f.Name, Op: i, Kind: k, Msg: fmt.Sprintf(format, args...),
	})
}

func (s *stackChecker) push(ks ...valKind) { s.stack = append(s.stack, ks...) }

func (s *stackChecker) pushN(n int, k valKind) {
	for j := 0; j < n; j++ {
		s.stack = append(s.stack, k)
	}
}

// strSlots is how many operand-stack slots a whole string value takes on
// this target: its (data, len) pair under the two-word ABI, otherwise a
// single heap pointer.
func (s *stackChecker) strSlots() int {
	if s.twoWordStr {
		return 2
	}
	return 1
}

// pop removes one slot and returns its class. In unreachable code the
// stack below the frame's entry height is polymorphic, so a pop that
// would underflow succeeds with kUnknown — exactly wasm's rule, and the
// reason `br` followed by dead operand code validates.
func (s *stackChecker) pop(i int, k OpKind, want valKind) valKind {
	fr := s.top()
	if len(s.stack) <= fr.height {
		if fr.unreachable {
			return kUnknown
		}
		s.report(i, k, "operand stack underflow — needs a value, but the enclosing scope holds none")
		// Keep going with a wildcard so one underflow does not
		// cascade into a report per following op.
		return kUnknown
	}
	got := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	if want != kUnknown && got != kUnknown && got != want {
		s.report(i, k, "wants a %s operand, but the stack holds a %s", want, got)
	}
	return got
}

func (s *stackChecker) popN(i int, k OpKind, n int, want valKind) {
	for j := 0; j < n; j++ {
		s.pop(i, k, want)
	}
}

// popSlots pops a value whose shape is known, deepest slot last.
func (s *stackChecker) popSlots(i int, k OpKind, ks []valKind) {
	for j := len(ks) - 1; j >= 0; j-- {
		s.pop(i, k, ks[j])
	}
}

// height is the stack height relative to the current frame's entry.
func (s *stackChecker) height() int { return len(s.stack) - s.top().height }

// setUnreachable marks the rest of the current scope dead and resets the
// stack to the frame's entry height, so the dead code validates
// polymorphically.
func (s *stackChecker) setUnreachable() {
	fr := s.top()
	fr.unreachable = true
	s.stack = s.stack[:fr.height]
}

// localType is the declared type of local slot idx. Locals are one flat
// index space — params, then declared locals, then the lowering pass's
// synthetic scratch slots — and one variable is one index however many
// stack slots its value occupies.
func (s *stackChecker) localType(idx int32) (ast.Type, bool) {
	i := int(idx)
	if i < 0 {
		return nil, false
	}
	if i < len(s.f.Params) {
		return s.f.Params[i].Type, true
	}
	i -= len(s.f.Params)
	if i < len(s.f.Locals) {
		return s.f.Locals[i].Type, true
	}
	i -= len(s.f.Locals)
	if i < len(s.f.ScratchTypes) {
		return s.f.ScratchTypes[i], true
	}
	return nil, false
}

// localSlots is the shape a local access moves.
//
// A local occupies two stack slots when its declared type is two-word on
// this target — that is what the backends key their paired `local.get` /
// `local.set` off — or when the op itself says so with
// `Width: WidthString`, which is how a pass that rewrote a slot's access
// records the pairing without changing the slot's type.
func (s *stackChecker) localSlots(op Op) ([]valKind, bool) {
	t, ok := s.localType(op.I32)
	if !ok {
		return nil, false
	}
	if op.Width == WidthString || s.isTwoWord(t) {
		return []valKind{kInt, kInt}, true
	}
	if _, isFloat := t.(ast.FloatType); isFloat {
		return []valKind{kFloat}, true
	}
	return []valKind{kInt}, true
}

// isTwoWord reports whether a value of type t rides two operand-stack
// slots on this target: a string under the two-word ABI (its data and
// length), and a `dyn Trait` on wasm, where the fat pointer stays inline
// rather than being boxed (OpBoxDyn is the native form).
func (s *stackChecker) isTwoWord(t ast.Type) bool {
	switch t.(type) {
	case ast.StringType:
		return s.twoWordStr
	case ast.DynTraitType:
		return s.ptrW == 4
	}
	return false
}

// callArgSlots is how many stack slots a call's arguments occupy, and
// whether that could be determined at all.
//
// Under the one-word string ABI the question is trivial: every argument
// is one slot, so the op's own count is the answer. Under the two-word
// ABI a string argument is a (data, len) pair, and the slot count is
// larger than the argument count by however many string arguments there
// are — which the op says only when the lowering pass recorded ArgTypes.
// Failing that, a defined callee's declared parameters answer it, and
// failing that the provided-signature table does. A generic builtin with
// none of the three (a Map method whose key width comes from the
// instantiation) is genuinely unanswerable, and skips the function.
func (s *stackChecker) callArgSlots(op Op) (int, bool) {
	n := int(op.I32)
	if !s.twoWordStr {
		return n, true
	}
	if at := op.ArgTypes(); len(at) == n {
		return s.slotCount(at), !anyErased(at)
	}
	if sig := op.Sig(); sig != nil && len(sig.Params) == n {
		return s.slotCount(sig.Params), !anyErased(sig.Params)
	}
	if callee, ok := s.known[op.Str]; ok && len(callee.Params) == n {
		total := 0
		for _, pm := range callee.Params {
			if erased(pm.Type) {
				return 0, false
			}
			total += len(s.typeSlots(pm.Type))
		}
		return total, true
	}
	if sig, ok := providedSigs[op.Str]; ok && sig.argSlots >= 0 {
		return sig.argSlots, true
	}
	return 0, false
}

// verifyStack runs the pass over one function. It appends to problems
// only for genuine violations; anything unmodelled sets bail and the
// function's findings are discarded.
func verifyStack(f *Func, known map[string]*Func, externs map[string]*ExternFunc, ptrW int) ([]Problem, string) {
	s := &stackChecker{f: f, known: known, externs: externs, ptrW: ptrW, twoWordStr: ast.UseTwoWordStrings(ptrW)}
	if erased(f.ReturnType) {
		return nil, "result type is an unresolved type parameter"
	}
	retSlots := s.typeSlots(f.ReturnType)
	s.frames = []ctrlFrame{{kind: OpInvalid, at: -1, height: 0, labelSlots: retSlots, endSlots: retSlots}}

	for i, op := range f.Ops {
		s.step(i, op)
		if s.bail != "" {
			return nil, s.bail
		}
	}

	// The structural pass already reports an unclosed scope; reporting
	// the height consequences of one too would be noise.
	if len(s.frames) != 1 {
		return nil, ""
	}
	fr := s.top()
	if !fr.unreachable {
		if got := s.height(); got != len(retSlots) {
			s.report(len(f.Ops)-1, OpInvalid,
				"function ends holding %d stack slots, but its result needs %d", got, len(retSlots))
		}
	}
	return s.problems, ""
}

func (s *stackChecker) step(i int, op Op) {
	switch op.Kind {
	// Constants.
	case OpConstI32, OpConstI64, OpConstFunc, OpConstVtable, OpEnumSentinel:
		s.push(kInt)
	case OpConstF32, OpConstF64:
		s.push(kFloat)
	case OpConstStr:
		s.pushN(s.strSlots(), kInt)

	// Width and representation conversions.
	case OpExtendI32S, OpExtendI32U, OpWrapI64:
		s.pop(i, op.Kind, kInt)
		s.push(kInt)
	case OpFPromoteF32, OpFDemoteF64:
		s.pop(i, op.Kind, kFloat)
		s.push(kFloat)
	case OpFConvertI32, OpFConvertI64, OpReinterpretF32I32, OpReinterpretF64I64:
		s.pop(i, op.Kind, kInt)
		s.push(kFloat)
	case OpITruncF32, OpITruncF64, OpReinterpretI32F32, OpReinterpretI64F64:
		s.pop(i, op.Kind, kFloat)
		s.push(kInt)

	// Locals.
	case OpLoadLocal:
		ks, ok := s.localSlots(op)
		if !ok {
			// verify.go reports the out-of-range index; continuing
			// here would add a second report for one defect.
			s.bail = "local index outside the frame"
			return
		}
		s.push(ks...)
	case OpStoreLocal, OpTeeLocal:
		ks, ok := s.localSlots(op)
		if !ok {
			s.bail = "local index outside the frame"
			return
		}
		s.popSlots(i, op.Kind, ks)
		if op.Kind == OpTeeLocal {
			s.push(ks...)
		}

	// Integer arithmetic, comparison and bit counting.
	case OpAdd, OpSub, OpMul, OpDivS, OpRemS, OpAnd, OpOr, OpXor, OpShl, OpShrS,
		OpEq, OpNe, OpLtS, OpLeS, OpGtS, OpGeS:
		s.popN(i, op.Kind, 2, kInt)
		s.push(kInt)
	case OpNot, OpClz, OpCtz, OpPopcount:
		s.pop(i, op.Kind, kInt)
		s.push(kInt)

	// Float arithmetic and comparison. Comparisons yield a bool word.
	case OpFAdd, OpFSub, OpFMul, OpFDiv:
		s.popN(i, op.Kind, 2, kFloat)
		s.push(kFloat)
	case OpFNeg:
		s.pop(i, op.Kind, kFloat)
		s.push(kFloat)
	case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		s.popN(i, op.Kind, 2, kFloat)
		s.push(kInt)

	// Memory. A load or store of a two-word string moves the
	// (data, len) pair, which the op announces with WidthString.
	case OpLoadByte, OpLoad, OpAlloc, OpMatchTag, OpRcInc, OpRcDec, OpRcIsUnique:
		s.pop(i, op.Kind, kInt)
		if op.Width == WidthString {
			s.push(kInt, kInt)
		} else {
			s.push(kInt)
		}
	case OpFLoad:
		s.pop(i, op.Kind, kInt)
		s.push(kFloat)
	case OpStore, OpStoreI8:
		if op.Width == WidthString {
			s.popN(i, op.Kind, 3, kInt)
		} else {
			s.popN(i, op.Kind, 2, kInt)
		}
	case OpFStore:
		s.pop(i, op.Kind, kFloat)
		s.pop(i, op.Kind, kInt)

	// Strings. These take whole string VALUES, so their operands are
	// pairs wherever the target's ABI makes a string a pair.
	case OpStrEq, OpStrCmp:
		s.popN(i, op.Kind, 2*s.strSlots(), kInt)
		s.push(kInt)
	case OpStrConcat:
		s.popN(i, op.Kind, 2*s.strSlots(), kInt)
		s.pushN(s.strSlots(), kInt)
	case OpStrLen:
		s.popN(i, op.Kind, s.strSlots(), kInt)
		s.push(kInt)

	// Structured control flow.
	case OpBlock, OpLoop, OpIf:
		if op.Kind == OpIf {
			s.pop(i, op.Kind, kInt)
		}
		end, ok := blockSlots(op.I32)
		if !ok {
			s.bail = fmt.Sprintf("unknown block type %d", op.I32)
			return
		}
		label := end
		if op.Kind == OpLoop {
			// A branch to a loop restarts it, so it carries the
			// loop's parameters — always none here.
			label = nil
		}
		s.frames = append(s.frames, ctrlFrame{
			kind: op.Kind, at: i, height: len(s.stack), labelSlots: label, endSlots: end,
		})

	case OpElse:
		fr := s.top()
		if fr.kind != OpIf || fr.sawElse {
			// verify.go reports both; nothing useful to add.
			s.bail = "malformed else"
			return
		}
		if !fr.unreachable {
			if got := s.height(); got != len(fr.endSlots) {
				s.report(i, op.Kind, "the then-arm leaves %d stack slots, but the if promises %d",
					got, len(fr.endSlots))
			}
		}
		fr.sawElse = true
		fr.unreachable = false
		s.stack = s.stack[:fr.height]

	case OpEnd:
		if len(s.frames) == 1 {
			s.bail = "end with no open scope"
			return
		}
		fr := s.top()
		if !fr.unreachable {
			if got := s.height(); got != len(fr.endSlots) {
				s.report(i, op.Kind, "the %s opened at op %d leaves %d stack slots, but its block type promises %d",
					fr.kind, fr.at, got, len(fr.endSlots))
			}
		}
		// An if with a result and no else can only be well-formed
		// when the fall-through carries the value, which an
		// else-less if cannot do.
		if fr.kind == OpIf && !fr.sawElse && len(fr.endSlots) > 0 {
			s.report(i, op.Kind, "the if opened at op %d promises %d stack slots but has no else arm",
				fr.at, len(fr.endSlots))
		}
		end := fr.endSlots
		s.stack = s.stack[:fr.height]
		s.frames = s.frames[:len(s.frames)-1]
		s.push(end...)

	case OpBr, OpBrIf:
		if op.Kind == OpBrIf {
			s.pop(i, op.Kind, kInt)
		}
		d := int(op.I32)
		if d < 0 || d >= len(s.frames) {
			// verify.go reports the depth; the target is unknown so
			// nothing further can be said.
			s.bail = "branch depth has no target"
			return
		}
		target := &s.frames[len(s.frames)-1-d]
		if !s.top().unreachable {
			if got := s.height(); got < len(target.labelSlots) {
				s.report(i, op.Kind, "branch to depth %d needs %d stack slots, but only %d are available",
					d, len(target.labelSlots), got)
			}
		}
		if op.Kind == OpBr {
			s.setUnreachable()
		}

	// Calls.
	case OpCallDirect, OpCallDirectPair, OpCallClosureDirect, OpCallIndirect, OpCallDyn:
		if !s.call(i, op) {
			return
		}

	case OpDrop:
		if op.Width == WidthString {
			s.popN(i, op.Kind, 2, kUnknown)
		} else {
			s.pop(i, op.Kind, kUnknown)
		}

	case OpReturn:
		s.popSlots(i, op.Kind, s.typeSlots(s.f.ReturnType))
		s.setUnreachable()
	case OpReturnVoid:
		s.setUnreachable()
	case OpReturnPair:
		s.popN(i, op.Kind, 2, kInt)
		s.setUnreachable()

	// Register-form Option / Result construction.
	case OpMakeSomeI32, OpMakeOkI32, OpMakeErrI32:
		s.pop(i, op.Kind, kInt)
		s.push(kInt, kInt)
	case OpMakeNoneI32:
		s.push(kInt, kInt)

	case OpBoxDyn:
		s.popN(i, op.Kind, 2, kInt)
		s.push(kInt)

	case OpMakeClosure, OpMakeEnv:
		s.popN(i, op.Kind, int(op.I32), kUnknown)
		s.push(kInt)

	case OpLine:
		// No stack effect by construction.

	default:
		s.bail = "unmodelled op " + op.Kind.String()
	}
}

// call handles every call-shaped op. It returns false when the callee's
// result shape is unknown, having set bail.
func (s *stackChecker) call(i int, op Op) bool {
	if op.I32 < 0 {
		s.bail = "negative argument count"
		return false
	}
	// A dyn call's I32 is the METHOD'S VTABLE SLOT, not an argument
	// count, so its arguments come from the signature instead — which is
	// receiver-first, and the receiver is popped separately below.
	var args int
	if op.Kind == OpCallDyn {
		sig := op.Sig()
		if sig == nil || len(sig.Params) == 0 {
			s.bail = "dyn call with no receiver-first signature"
			return false
		}
		if anyErased(sig.Params) {
			s.bail = "dyn call through an erased parameter type"
			return false
		}
		args = s.slotCount(sig.Params[1:])
	} else {
		var ok bool
		args, ok = s.callArgSlots(op)
		if !ok {
			s.bail = "unknown argument shape for callee " + op.Str
			return false
		}
	}

	switch op.Kind {
	case OpCallIndirect, OpCallDyn:
		sig := op.Sig()
		if sig == nil {
			s.bail = "indirect call with no signature"
			return false
		}
		// The receiver word of a dyn call is pushed below the args,
		// the vtable word above them; an indirect call carries the
		// table index on top.
		s.pop(i, op.Kind, kInt) // vtable / table index
		s.popN(i, op.Kind, args, kUnknown)
		if op.Kind == OpCallDyn {
			s.pop(i, op.Kind, kInt) // receiver data
		}
		if erased(sig.Result) {
			s.bail = "call through an erased result type"
			return false
		}
		s.push(s.typeSlots(sig.Result)...)
		return true

	case OpCallClosureDirect:
		s.pop(i, op.Kind, kInt) // env pointer
		s.popN(i, op.Kind, args, kUnknown)
	default:
		s.popN(i, op.Kind, args, kUnknown)
	}

	if op.Kind == OpCallDirectPair {
		s.push(kInt, kInt)
		return true
	}

	if callee, ok := s.known[op.Str]; ok {
		if erased(callee.ReturnType) {
			s.bail = "call to " + op.Str + ", whose result type is an unresolved type parameter"
			return false
		}
		s.push(s.typeSlots(callee.ReturnType)...)
		return true
	}
	if e, ok := s.externs[op.Str]; ok {
		s.push(s.typeSlots(e.ReturnType)...)
		return true
	}
	if sig, ok := providedSigs[op.Str]; ok {
		s.push(sig.result.slots(s.twoWordStr)...)
		return true
	}
	s.bail = "unknown result shape for callee " + op.Str
	return false
}
