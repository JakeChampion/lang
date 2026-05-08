// Package ir defines a small linear stack-machine intermediate
// representation that lives between the type-checked AST and the
// per-target code generators. It's the long-promised "third stage" —
// today the WASM and ARM32 backends both walk the AST directly, with
// a lot of duplicated logic for control flow and expression evaluation.
// Once they migrate to consuming IR, that logic moves here.
//
// The IR is an explicit-stack bytecode in the same family as WebAssembly
// itself: each Op pushes or pops 32-bit slots (the language's number,
// boolean, string-pointer, array-pointer, struct-pointer and function-
// table-index types are all i32 at the IR level; floats are still f32
// where they appear). Control flow is structured — `block`, `loop`,
// `if/else/end` scopes nest, and `br`/`br_if` take a relative depth
// rather than a label id — so emitting WAT or any structured target is
// a direct walk of the op list.
//
// Coverage today:
//   - The op set, IR Func and Program data types.
//   - A `Lower` pass: ast.Program + checker.Info → ir.Program.
//   - Lowering handles arithmetic, locals, control flow (if/while/for/
//     switch/break/continue), function calls (direct + indirect),
//     ternary, array literals + indexing, struct literals + field
//     access, string concatenation / equality / byte indexing,
//     and closure-converted nested functions: CaptureRef expressions
//     lower to env-relative loads and MakeClosure lowers to OpMakeClosure.
//
// What's NOT yet done:
//   - The WASM and ARM32 backends still walk the AST. Migrating them
//     is a follow-up; for now the IR is verified by tests rather than
//     by being on the production code path.
package ir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
)

// OpKind enumerates every IR instruction. The arity (consumed / produced
// stack slots) is documented per-op below.
type OpKind int

const (
	OpInvalid OpKind = iota

	// Constants
	OpConstI32  // (i32 imm)         → i32
	OpConstI64  // (i64 imm)         → i64
	OpConstF32  // (f32 imm)         → f32
	OpConstF64  // (f64 imm)         → f64
	OpConstStr  // (string-id imm)   → i32 (pointer)
	OpConstFunc // (func-id imm)     → i32 (table index)

	// Width-conversion ops between integer types. ExtendI32S
	// sign-extends an i32 to i64; WrapI64 truncates the low 32
	// bits. ExtendI32U zero-extends — used by `u32 as u64` /
	// any cast whose source is unsigned. More widths (8/16)
	// land in a follow-up alongside their underlying mask /
	// sign-extend story.
	OpExtendI32S   // (i32) → i64 (sign-extend)
	OpExtendI32U   // (i32) → i64 (zero-extend, for unsigned)
	OpWrapI64      // (i64) → i32
	OpFPromoteF32  // (f32) → f64
	OpFDemoteF64   // (f64) → f32
	OpSignExtend8  // (i32) → i32 (sign-extend low byte; lowers to i32.extend8_s)
	OpSignExtend16 // (i32) → i32 (sign-extend low halfword; lowers to i32.extend16_s)

	// Locals (parameter or var). Idx is the 0-based slot.
	OpLoadLocal  // ()                → T
	OpStoreLocal // (T)               → ()
	OpTeeLocal   // (T)               → T   (store + leave value on stack)

	// Integer / pointer arithmetic and comparison. All consume two i32
	// and produce one i32 except OpNeg / OpNot, which consume one.
	OpAdd
	OpSub
	OpMul
	OpDivS
	OpRemS
	OpAnd
	OpOr
	OpXor
	OpShl
	OpShrS
	OpNot // logical ! (i32.eqz)

	OpEq
	OpNe
	OpLtS
	OpLeS
	OpGtS
	OpGeS

	// Float counterparts. Consume / produce f32 instead of i32.
	OpFAdd
	OpFSub
	OpFMul
	OpFDiv
	OpFNeg
	OpFEq
	OpFNe
	OpFLt
	OpFLe
	OpFGt
	OpFGe

	// Memory (linear-byte addressed; arrays, strings, structs all live
	// here in the WASM model).
	OpLoadByte // (addr)             → i32 (zero-extended byte)
	OpLoad     // (addr)             → i32 (4-byte word)
	OpStore    // (addr, value)      → ()
	OpFLoad    // (addr)             → f32
	OpFStore   // (addr, value)      → ()
	OpStoreI8  // (addr, value)      → ()  (writes the low byte)

	// Heap allocation: pop a size in bytes and push the base pointer
	// the bump allocator returns.
	OpAlloc // (size i32)         → i32 (ptr)

	// String runtime calls.
	OpStrEq     // (a-ptr, b-ptr)   → i32
	OpStrConcat // (a-ptr, b-ptr)   → i32 (new ptr)

	// Structured control flow. Block / loop / if open a new lexical
	// scope on the validation-time control stack; OpEnd closes the
	// innermost. Branches address scopes by relative depth (0 =
	// innermost) so the op list is independent of any label-id
	// numbering. WASM exit semantics:
	//   - `block` is a forward-only target (br N exits scope N)
	//   - `loop` is a backward-only target (br N restarts scope N)
	//   - `if` runs its then-arm when the popped i32 is non-zero;
	//     OpElse switches to the else-arm; OpEnd closes the if.
	// Each of OpBlock / OpLoop / OpIf carries a BlockType immediate
	// (BlockTypeVoid / BlockTypeI32 / BlockTypeF32) describing the
	// value the scope leaves on the stack on normal fall-through.
	OpBlock // (BlockType) → opens a forward block
	OpLoop  // (BlockType) → opens a backward loop
	OpIf    // (i32) (BlockType) → opens a conditional block
	OpElse  // → switches the current if-scope to the else arm
	OpEnd   // → closes the innermost scope
	OpBr    // → unconditional branch to scope at relative depth I32
	OpBrIf  // (i32) → branch to relative depth I32 when value is non-zero

	// Calls.
	OpCallDirect   // (args...)        → result | ()
	OpCallIndirect // (args..., idx)   → result | ()

	OpDrop       // (T)               → ()
	OpReturn     // (T)               → unwinds the function
	OpReturnVoid // ()                → unwinds the function

	// Closure conversion. Hoisted local functions read captured outer
	// variables through a synthetic `__env` parameter (an i32 pointer
	// to a heap block where each capture sits at a fixed byte offset).
	// At the original def site the AST carries a *MakeClosure node that
	// allocates the env, packs current capture values into it, allocates
	// an 8-byte closure pair `{fn_idx, env_ptr}`, and yields the closure
	// pointer.
	OpMakeClosure // (cap_0 ... cap_{n-1}) → i32 (closure ptr)
)

// BlockType describes the type a block / loop / if leaves on the stack
// when control falls off its end normally. OpBlock, OpLoop, and OpIf
// stash the block type in their Op.I32 field.
const (
	BlockTypeVoid int32 = 0
	BlockTypeI32  int32 = 1
	BlockTypeF32  int32 = 2
	BlockTypeI64  int32 = 3
	BlockTypeF64  int32 = 4
)

// blockTypeName returns a short mnemonic for use in formatted output.
func blockTypeName(bt int32) string {
	switch bt {
	case BlockTypeI32:
		return "i32"
	case BlockTypeF32:
		return "f32"
	case BlockTypeI64:
		return "i64"
	case BlockTypeF64:
		return "f64"
	}
	return "void"
}

// String returns a short mnemonic for the op kind.
func (k OpKind) String() string {
	switch k {
	case OpConstI32:
		return "const.i32"
	case OpConstI64:
		return "const.i64"
	case OpConstF32:
		return "const.f32"
	case OpConstF64:
		return "const.f64"
	case OpExtendI32U:
		return "extend.i32_u"
	case OpExtendI32S:
		return "extend.i32_s"
	case OpWrapI64:
		return "wrap.i64"
	case OpFPromoteF32:
		return "promote.f32"
	case OpFDemoteF64:
		return "demote.f64"
	case OpSignExtend8:
		return "extend8_s"
	case OpSignExtend16:
		return "extend16_s"
	case OpConstStr:
		return "const.str"
	case OpConstFunc:
		return "const.func"
	case OpLoadLocal:
		return "local.load"
	case OpStoreLocal:
		return "local.store"
	case OpTeeLocal:
		return "local.tee"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpDivS:
		return "div_s"
	case OpRemS:
		return "rem_s"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpXor:
		return "xor"
	case OpShl:
		return "shl"
	case OpShrS:
		return "shr_s"
	case OpNot:
		return "not"
	case OpEq:
		return "eq"
	case OpNe:
		return "ne"
	case OpLtS:
		return "lt_s"
	case OpLeS:
		return "le_s"
	case OpGtS:
		return "gt_s"
	case OpGeS:
		return "ge_s"
	case OpFAdd:
		return "f.add"
	case OpFSub:
		return "f.sub"
	case OpFMul:
		return "f.mul"
	case OpFDiv:
		return "f.div"
	case OpFNeg:
		return "f.neg"
	case OpFEq:
		return "f.eq"
	case OpFNe:
		return "f.ne"
	case OpFLt:
		return "f.lt"
	case OpFLe:
		return "f.le"
	case OpFGt:
		return "f.gt"
	case OpFGe:
		return "f.ge"
	case OpLoadByte:
		return "load_u8"
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
	case OpFLoad:
		return "f.load"
	case OpFStore:
		return "f.store"
	case OpStoreI8:
		return "store_i8"
	case OpAlloc:
		return "alloc"
	case OpStrEq:
		return "str.eq"
	case OpStrConcat:
		return "str.concat"
	case OpBlock:
		return "block"
	case OpLoop:
		return "loop"
	case OpIf:
		return "if"
	case OpElse:
		return "else"
	case OpEnd:
		return "end"
	case OpBr:
		return "br"
	case OpBrIf:
		return "br_if"
	case OpCallDirect:
		return "call"
	case OpCallIndirect:
		return "call_indirect"
	case OpDrop:
		return "drop"
	case OpReturn:
		return "return"
	case OpReturnVoid:
		return "return_void"
	case OpMakeClosure:
		return "make_closure"
	}
	return "<invalid>"
}

// Op is one instruction in a function's linear op list. Operands that
// don't apply to a given op are zero-valued.
type Op struct {
	Kind OpKind
	// I32 is the immediate for OpConstI32, the local index for
	// OpLoadLocal/OpStoreLocal, the BlockType for OpBlock/OpLoop/OpIf,
	// the relative depth for OpBr/OpBrIf, the arg count for
	// OpCallDirect/OpCallIndirect/OpMakeClosure, and the table index
	// for OpConstFunc.
	I32 int32
	// I64 is the immediate for OpConstI64.
	I64 int64
	// F32 is the immediate for OpConstF32.
	F32 float32
	// F64 is the immediate for OpConstF64.
	F64 float64
	// Width is the operand bit-width for integer arithmetic /
	// comparison ops that exist in multiple widths (OpAdd, OpSub,
	// ..., OpEq, ..., OpNot, the load/store ops, and the local
	// load/stores via the operand type). Zero is treated as 32 so
	// existing emit paths keep producing i32 ops without code
	// changes; explicit `Width: 64` selects i64. Sub-i32 widths
	// (8, 16) are reserved — they ship with the unsigned-types
	// follow-up.
	Width int
	// Unsigned selects the `_u` variant of div / rem / shr /
	// comparison ops (OpDivS becomes i32.div_u, etc.) when the
	// checker has tagged the surrounding Binary as unsigned.
	// Default (false) keeps the existing signed-by-default
	// behaviour. Add / sub / mul / and / or / xor / eq / ne are
	// signedness-agnostic — the flag has no effect there.
	Unsigned bool
	// Str carries OpConstStr's string value and OpCallDirect's callee
	// name. Empty otherwise.
	Str string
	// Sig is set on OpCallIndirect to the static signature of the
	// function-typed local being dispatched through. Codegen uses it
	// to resolve the right `(type $tN)` clause in the WAT output.
	Sig *ast.FuncType
	// Pos points back at the source position the lowering pass was
	// processing when this op was emitted. Backends use it to drive
	// DWARF .loc / WASM debug-line info; the field is zero for ops
	// the lowering pass synthesised without an obvious source span
	// (e.g. trailing implicit returns).
	Pos ast.Position
}

// Func is a single lowered function: parameter / local list, ops, and
// the return type. ScratchTypes carries the type of each synthetic
// slot the lowering pass / inliner conjured (for ArrayLit / StructLit
// / Switch / closure helpers and for inlined callees' params,
// locals, scratches). Slots live at indices [len(Params)+len(Locals),
// …) and are addressed by OpLoadLocal / OpStoreLocal just like user
// vars; codegen reads ScratchTypes[i] to declare each slot with the
// right type (i32 / f32).
type Func struct {
	Name         string
	Params       []ast.Param
	Locals       []*ast.Var
	ScratchTypes []ast.Type
	ReturnType   ast.Type
	Ops          []Op
}

// Program is the lowered form of an entire ast.Program.
type Program struct {
	Funcs []*Func
}

// String prints the program in a textual form useful for tests and
// debugging. Indents instructions under each function header.
func (p *Program) String() string {
	var b strings.Builder
	for _, fn := range p.Funcs {
		fmt.Fprintf(&b, "func %s(", fn.Name)
		for i, p := range fn.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s: %s", p.Name, p.Type)
		}
		fmt.Fprintf(&b, ") -> %s\n", fn.ReturnType)
		for _, op := range fn.Ops {
			b.WriteString("  ")
			b.WriteString(formatOp(op))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func formatOp(op Op) string {
	switch op.Kind {
	case OpConstI32, OpLoadLocal, OpStoreLocal, OpTeeLocal:
		return fmt.Sprintf("%s %d", op.Kind, op.I32)
	case OpConstF32:
		return fmt.Sprintf("%s %g", op.Kind, op.F32)
	case OpConstF64:
		return fmt.Sprintf("%s %g", op.Kind, op.F64)
	case OpConstStr:
		return fmt.Sprintf("%s %q", op.Kind, op.Str)
	case OpConstFunc:
		return fmt.Sprintf("%s %s", op.Kind, op.Str)
	case OpBlock, OpLoop, OpIf:
		return fmt.Sprintf("%s %s", op.Kind, blockTypeName(op.I32))
	case OpBr, OpBrIf:
		return fmt.Sprintf("%s %d", op.Kind, op.I32)
	case OpCallDirect:
		return fmt.Sprintf("%s %s argc=%d", op.Kind, op.Str, op.I32)
	case OpCallIndirect:
		return fmt.Sprintf("%s argc=%d", op.Kind, op.I32)
	case OpMakeClosure:
		return fmt.Sprintf("%s %s caps=%d", op.Kind, op.Str, op.I32)
	}
	return op.Kind.String()
}

// Lower converts a checked AST program into IR. The Info argument
// supplies the local-by-function table, struct-decl map, and function
// signatures the lowering pass needs to resolve names.
//
// As a precondition, Lower runs closure conversion: any nested local
// FuncDecls are hoisted to the top-level Funcs list with a synthetic
// `__env` parameter, captured-variable references are rewritten as
// CaptureRef nodes, and original def sites are replaced with a
// MakeClosure-bearing Var. This means the per-function lowering only
// has to deal with a flat program of top-level functions.
func Lower(prog *ast.Program, info *checker.Info) (*Program, error) {
	if err := closureconv.Convert(prog, info); err != nil {
		return nil, err
	}
	out := &Program{}
	for _, fn := range prog.Funcs {
		f, err := lowerFunc(fn, info)
		if err != nil {
			return nil, err
		}
		out.Funcs = append(out.Funcs, f)
	}
	return out, nil
}

// builder is the per-function lowering state.
type builder struct {
	info *checker.Info
	fn   *ast.FuncDecl
	out  *Func
	// locals maps parameter and var names to their 0-based slot index.
	// Parameters are slots 0..len(params)-1; vars start at len(params).
	locals map[string]int32
	// scratchType records the declared type of synthetic scratch
	// slots the lowering pass introduces (closure helpers,
	// per-arm match bindings, struct/enum literal anchors). The
	// default is NumberType (i32); float-typed bindings record
	// FloatType so wasm declares the local as f32.
	scratchType map[int32]ast.Type
	// nextSlot is the index the next synthetic scratch slot
	// will use. Starts at len(params)+len(user locals) and only
	// ever grows, so reusing a binding name across two match
	// arms (which both write `b.locals[name] = slot` over the
	// same key) doesn't undercount the actual slot population.
	// `len(b.locals)` is no longer authoritative — always go
	// through allocSlot().
	nextSlot int32
	// depth is the current control-stack depth (number of open
	// block/loop/if scopes). Used to compute relative branch
	// distances for break/continue.
	depth int32
	// breakStack and contStack track the depth-after-open of the
	// scopes that `break` / `continue` should target. From a current
	// depth M, `br (M - stored)` lands at the right scope.
	breakStack []int32
	contStack  []int32
	// curPos is the source position of the AST node currently being
	// lowered. emit() stamps it onto every op so backends can drive
	// per-statement DWARF / .loc directives.
	curPos ast.Position
}

func lowerFunc(fn *ast.FuncDecl, info *checker.Info) (*Func, error) {
	out := &Func{
		Name:       fn.Name,
		Params:     fn.Params,
		Locals:     info.Locals[fn],
		ReturnType: fn.ReturnType,
	}
	b := &builder{info: info, fn: fn, out: out, locals: map[string]int32{}, scratchType: map[int32]ast.Type{}}
	for i, p := range fn.Params {
		b.locals[p.Name] = int32(i)
	}
	for i, v := range info.Locals[fn] {
		b.locals[v.Name] = int32(len(fn.Params) + i)
	}
	b.nextSlot = int32(len(fn.Params) + len(info.Locals[fn]))
	if err := b.stmt(fn.Body); err != nil {
		return nil, err
	}
	// Record the type of every synthetic slot the lowering pass
	// conjured beyond the user-visible params + locals — ArrayLit
	// / StructLit / Switch / closure helpers each added entries to
	// the locals map. Most are i32 (heap pointers or integer tags);
	// match-arm bindings of float-typed payloads register a
	// FloatType in scratchType so wasm declares the local as f32.
	//
	// Use the standalone nextSlot counter (rather than
	// `len(b.locals)`) so two match arms that share a binding
	// name don't fool the count by overwriting the same map
	// entry — both still consume distinct slot indices.
	scratchBase := int32(len(fn.Params) + len(info.Locals[fn]))
	scratchCount := int(b.nextSlot - scratchBase)
	if scratchCount < 0 {
		scratchCount = 0
	}
	out.ScratchTypes = make([]ast.Type, scratchCount)
	for i := range out.ScratchTypes {
		if t, ok := b.scratchType[scratchBase+int32(i)]; ok && t != nil {
			out.ScratchTypes[i] = t
		} else {
			out.ScratchTypes[i] = ast.NumberType{}
		}
	}
	// If the body falls off the end, emit an implicit return so the
	// downstream consumer doesn't have to check.
	if needsImplicitReturn(out.Ops) {
		if isVoid(fn.ReturnType) {
			b.emit(Op{Kind: OpReturnVoid})
		} else if isFloat(fn.ReturnType) {
			b.emit(Op{Kind: OpConstF32, F32: 0})
			b.emit(Op{Kind: OpReturn})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: 0})
			b.emit(Op{Kind: OpReturn})
		}
	}
	return out, nil
}

func needsImplicitReturn(ops []Op) bool {
	if len(ops) == 0 {
		return true
	}
	last := ops[len(ops)-1].Kind
	return last != OpReturn && last != OpReturnVoid
}

// lookupVariant looks for a variant with the given name across
// every enum in the program. Returns the owning enum's name, the
// variant index, and the payload count. Ambiguity (same variant
// name in two enums) was already rejected by the checker, so the
// first hit is authoritative.
func (b *builder) lookupVariant(name string) (enumName string, varIdx int, payloadCount int, ok bool) {
	for ename, ed := range b.info.Enums {
		for i, v := range ed.Variants {
			if v.Name == name {
				return ename, i, len(v.Payloads), true
			}
		}
	}
	return "", 0, 0, false
}

// emitEnumNew lowers a variant constructor: allocate a heap
// object whose first word is the runtime tag (varIdx) and whose
// subsequent words hold the evaluated payloads in order. The
// enum value (the heap pointer) lands on the operand stack.
//
// Layout: [tag : i32][payload0 : i32][payload1 : i32]...
//
// Total size is `4 + payloadCount * 4`. Payload-less variants
// still allocate 4 bytes for the tag — uniform layout simplifies
// match-side loads.
//
// `callNode` is the originating *ast.Call when this is a
// payload-carrying construction; nil otherwise. The checker
// stores type-substituted payload types under that key, which
// lets us pick OpFStore for an f32 payload even when the
// variant's declared payload was a type parameter (e.g.
// `Some(3.14)` on `Option[T]` with `T = float`).
func (b *builder) emitEnumNew(callNode *ast.Call, enumName string, varIdx int, payloadCount int, args []ast.Expr) error {
	size := int32(4 + payloadCount*4)
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__enum_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// Store tag at offset 0.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
	b.emit(Op{Kind: OpStore})
	// Resolve the payload's concrete type for op selection.
	// Prefer the checker-supplied substituted types so a generic
	// enum's `T = float` instantiation gets OpFStore; fall back
	// to the declared payload list (which contains ParamType
	// for generic variants — that's harmless because then the
	// arg can't be float anyway since the constraint failed).
	var payloadTypes []ast.Type
	if callNode != nil {
		if pts, ok := b.info.VariantCallPayloads[callNode]; ok {
			payloadTypes = pts
		}
	}
	if payloadTypes == nil {
		if ed, ok := b.info.Enums[enumName]; ok && varIdx < len(ed.Variants) {
			payloadTypes = ed.Variants[varIdx].Payloads
		}
	}
	for i, a := range args {
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: int32(4 + i*4)})
		b.emit(Op{Kind: OpAdd})
		if err := b.expr(a); err != nil {
			return err
		}
		if i < len(payloadTypes) && isFloat(payloadTypes[i]) {
			b.emit(Op{Kind: OpFStore})
		} else {
			b.emit(Op{Kind: OpStore})
		}
	}
	// Push the result pointer.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	return nil
}

// allocSlot reserves the next synthetic local slot index. Use
// this everywhere a fresh scratch / binding is needed; it
// stays accurate even when callers also rebind the same key
// in `b.locals`.
func (b *builder) allocSlot() int32 {
	s := b.nextSlot
	b.nextSlot++
	return s
}

func (b *builder) emit(op Op) {
	if op.Pos == (ast.Position{}) {
		op.Pos = b.curPos
	}
	b.out.Ops = append(b.out.Ops, op)
}

// openBlock / openLoop / openIf push a scope on the validation control
// stack. closeScope balances them. elseBranch toggles the if-scope to
// its else arm without changing depth.
func (b *builder) openBlock(bt int32) {
	b.emit(Op{Kind: OpBlock, I32: bt})
	b.depth++
}
func (b *builder) openLoop(bt int32) {
	b.emit(Op{Kind: OpLoop, I32: bt})
	b.depth++
}
func (b *builder) openIf(bt int32) {
	b.emit(Op{Kind: OpIf, I32: bt})
	b.depth++
}
func (b *builder) elseBranch()  { b.emit(Op{Kind: OpElse}) }
func (b *builder) closeScope() {
	b.emit(Op{Kind: OpEnd})
	b.depth--
}

// brTo emits an `OpBr` (or `OpBrIf` if cond is true) whose relative
// depth lands at the scope opened when depth-after-open was target.
func (b *builder) brTo(target int32, cond bool) {
	rel := b.depth - target
	if cond {
		b.emit(Op{Kind: OpBrIf, I32: rel})
	} else {
		b.emit(Op{Kind: OpBr, I32: rel})
	}
}

func (b *builder) stmt(s ast.Stmt) error {
	b.curPos = s.Pos()
	switch n := s.(type) {
	case *ast.Block:
		for _, ss := range n.Stmts {
			if err := b.stmt(ss); err != nil {
				return err
			}
		}
	case *ast.If:
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.openIf(BlockTypeVoid)
		if err := b.stmt(n.Then); err != nil {
			return err
		}
		if n.Else != nil {
			b.elseBranch()
			if err := b.stmt(n.Else); err != nil {
				return err
			}
		}
		b.closeScope()
	case *ast.LetElse:
		// Lower as: store source ptr, compare tag to varIdx.
		// On match (then-arm): bind payloads into locals declared
		// at the OUTER scope so they survive past the LetElse.
		// On mismatch: run the else block — checker has verified
		// the else diverges, so codegen-wise we don't need to
		// worry about the bindings being read on that path.
		_, varIdx, _, ok := b.lookupVariant(n.VariantName)
		if !ok {
			return fmt.Errorf("ir: let-else references unknown variant %q", n.VariantName)
		}
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__letelse_p_%d", ptrSlot)] = ptrSlot
		// Pre-allocate the binding slots BEFORE the if so the
		// stores inside the matched branch land in slots the
		// surrounding scope can read.
		bindingSlots := make([]int32, len(n.Bindings))
		for i, name := range n.Bindings {
			slot := b.allocSlot()
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			b.scratchType[slot] = bt
			b.locals[name] = slot
			bindingSlots[i] = slot
		}
		if err := b.expr(n.Source); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoad}) // tag at ptr+0
		b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
		b.emit(Op{Kind: OpEq})
		b.openIf(BlockTypeVoid)
		// Match: load each payload field into its pre-allocated
		// outer-scope slot.
		for i, slot := range bindingSlots {
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(4 + i*4)})
			b.emit(Op{Kind: OpAdd})
			if isFloat(bt) {
				b.emit(Op{Kind: OpFLoad})
			} else {
				b.emit(Op{Kind: OpLoad})
			}
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
		b.elseBranch()
		// Else block. The checker has verified divergence so
		// codegen doesn't need to do anything special.
		if err := b.stmt(n.Else); err != nil {
			return err
		}
		b.closeScope()
	case *ast.IfLet:
		// Lower `if let Variant(b1, b2, ...) = src { Then } [else
		// { Else }]` as: store the source pointer, compare its tag
		// to Variant's index, and on match bind payload fields
		// into Then-scope locals before running Then. On mismatch
		// run Else.
		_, varIdx, _, ok := b.lookupVariant(n.VariantName)
		if !ok {
			return fmt.Errorf("ir: if-let references unknown variant %q", n.VariantName)
		}
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__iflet_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Source); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		// tag at ptr+0; compare to varIdx → i32 0/1.
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoad})
		b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
		b.emit(Op{Kind: OpEq})
		b.openIf(BlockTypeVoid)
		// Match: bind payloads, run Then.
		for i, name := range n.Bindings {
			slot := b.allocSlot()
			b.locals[name] = slot
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			b.scratchType[slot] = bt
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(4 + i*4)})
			b.emit(Op{Kind: OpAdd})
			if isFloat(bt) {
				b.emit(Op{Kind: OpFLoad})
			} else {
				b.emit(Op{Kind: OpLoad})
			}
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
		if err := b.stmt(n.Then); err != nil {
			return err
		}
		if n.Else != nil {
			b.elseBranch()
			if err := b.stmt(n.Else); err != nil {
				return err
			}
		}
		b.closeScope()
	case *ast.While:
		// `block` carries break, `loop` carries continue. The body sits
		// inside both: br 1 exits the outer block (break); br 0 jumps
		// back to the loop top (continue / iteration).
		b.openBlock(BlockTypeVoid)
		breakD := b.depth
		b.openLoop(BlockTypeVoid)
		loopD := b.depth
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.emit(Op{Kind: OpNot}) // br_if exits when cond was false
		b.brTo(breakD, true)
		b.breakStack = append(b.breakStack, breakD)
		b.contStack = append(b.contStack, loopD)
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.contStack = b.contStack[:len(b.contStack)-1]
		b.brTo(loopD, false) // unconditional back-edge
		b.closeScope()       // close loop
		b.closeScope()       // close break-block
	case *ast.For:
		// Three-part for: init runs once, then `block`/`loop` carry
		// break/back-edge as in `while`, and an inner `block` (the
		// continue target) wraps the body so `continue` lands *before*
		// the step. Step + back-edge run after the inner block ends.
		if n.Init != nil {
			if err := b.stmt(n.Init); err != nil {
				return err
			}
		}
		b.openBlock(BlockTypeVoid)
		breakD := b.depth
		b.openLoop(BlockTypeVoid)
		loopD := b.depth
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.emit(Op{Kind: OpNot})
		b.brTo(breakD, true)
		b.openBlock(BlockTypeVoid)
		contD := b.depth
		b.breakStack = append(b.breakStack, breakD)
		b.contStack = append(b.contStack, contD)
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.contStack = b.contStack[:len(b.contStack)-1]
		b.closeScope() // close continue-block
		if n.Step != nil {
			if err := b.stmt(n.Step); err != nil {
				return err
			}
		}
		b.brTo(loopD, false)
		b.closeScope() // close loop
		b.closeScope() // close break-block
	case *ast.Break:
		if len(b.breakStack) == 0 {
			return fmt.Errorf("ir: break outside of a loop (compiler bug — should be checker-rejected)")
		}
		b.brTo(b.breakStack[len(b.breakStack)-1], false)
	case *ast.Continue:
		if len(b.contStack) == 0 {
			return fmt.Errorf("ir: continue outside of a loop (compiler bug — should be checker-rejected)")
		}
		b.brTo(b.contStack[len(b.contStack)-1], false)
	case *ast.Return:
		if n.Value == nil {
			b.emit(Op{Kind: OpReturnVoid})
			return nil
		}
		if err := b.expr(n.Value); err != nil {
			return err
		}
		b.emit(Op{Kind: OpReturn})
	case *ast.Var:
		if err := b.expr(n.Init); err != nil {
			return err
		}
		idx, ok := b.locals[n.Name]
		if !ok {
			return fmt.Errorf("ir: var %q has no slot (compiler bug)", n.Name)
		}
		b.emit(Op{Kind: OpStoreLocal, I32: idx})
	case *ast.ExprStmt:
		if err := b.expr(n.Expr); err != nil {
			return err
		}
		// If the expression leaves a value on the stack, drop it so the
		// stack stays balanced at statement boundaries.
		if exprLeavesValue(n.Expr, b.info) {
			b.emit(Op{Kind: OpDrop})
		}
	case *ast.Switch:
		// Lower switch with one outer block (break target / fallthrough
		// to default) and two nested blocks per case: the inner one is
		// the on-match jump target, the outer skips the body when no
		// value matched. Falling off any case body branches to the end
		// of the switch — no implicit fall-through.
		tagSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sw_%d", tagSlot)] = tagSlot
		if err := b.expr(n.Tag); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
		b.openBlock(BlockTypeVoid)
		switchEndD := b.depth
		b.breakStack = append(b.breakStack, switchEndD)
		for _, k := range n.Cases {
			b.openBlock(BlockTypeVoid)
			outerCaseD := b.depth
			b.openBlock(BlockTypeVoid)
			for _, v := range k.Values {
				b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
				if err := b.expr(v); err != nil {
					return err
				}
				b.emit(Op{Kind: OpEq})
				b.brTo(b.depth, true) // br 0: exit inner = match path
			}
			b.brTo(outerCaseD, false) // exit outer block: skip body
			b.closeScope()            // end of inner block (matched path lands here)
			if err := b.stmt(k.Body); err != nil {
				return err
			}
			b.brTo(switchEndD, false) // jump past the rest of the cases
			b.closeScope()            // end of outer per-case block
		}
		if n.Default != nil {
			if err := b.stmt(n.Default); err != nil {
				return err
			}
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.closeScope() // end of switch
	case *ast.Match:
		// Lower a `match` to: store the scrutinee pointer, load
		// its tag once, then for each arm test `tag == k` and
		// branch in. On match, the arm body runs with payload
		// fields loaded into freshly-bound locals; we then break
		// out of the whole match. The structure mirrors `switch`,
		// extended to bind payload positions.
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__match_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Tag); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		b.openBlock(BlockTypeVoid)
		matchEndD := b.depth
		b.breakStack = append(b.breakStack, matchEndD)
		for _, arm := range n.Arms {
			if arm.IsWildcard {
				// Guarded wildcard arm: the guard runs in the
				// outer scope (no bindings to introduce). On
				// false, fall through to the next arm via the
				// per-arm block; on true, run the body and
				// br to matchEnd.
				if arm.Guard != nil {
					b.openBlock(BlockTypeVoid)
					armEndD := b.depth
					if err := b.expr(arm.Guard); err != nil {
						return err
					}
					b.emit(Op{Kind: OpNot})
					b.brTo(armEndD, true) // skip if !guard
					if err := b.stmt(arm.Body); err != nil {
						return err
					}
					b.brTo(matchEndD, false)
					b.closeScope()
					continue
				}
				if err := b.stmt(arm.Body); err != nil {
					return err
				}
				b.brTo(matchEndD, false)
				continue
			}
			// Resolve variant index by name.
			_, varIdx, _, ok := b.lookupVariant(arm.VariantName)
			if !ok {
				return fmt.Errorf("ir: match arm references unknown variant %q", arm.VariantName)
			}
			// Outer per-arm block: skip body when tag mismatch.
			b.openBlock(BlockTypeVoid)
			outerArmD := b.depth
			// Inner block: matched-path target.
			b.openBlock(BlockTypeVoid)
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpLoad}) // tag at ptr+0
			b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
			b.emit(Op{Kind: OpEq})
			b.brTo(b.depth, true) // br 0 = exit inner = match
			b.brTo(outerArmD, false)
			b.closeScope() // end inner — matched path lands here
			// Bind payload locals from heap[ptr+4+i*4]. Float
			// payloads need an f32 load to keep stack types
			// consistent on WASM; arm32 ignores the choice.
			// arm.BindingTypes is filled by the checker with
			// the substituted concrete type (so generic enums
			// instantiated at `Option[number]` give `number`).
			// The binding's local also needs the right
			// declared type — recorded via b.scratchType so
			// the wasm backend declares it as f32 instead of
			// the default i32.
			for i, name := range arm.Bindings {
				slot := b.allocSlot()
				b.locals[name] = slot
				bt := ast.Type(ast.NumberType{})
				if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
					bt = arm.BindingTypes[i]
				}
				b.scratchType[slot] = bt
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpConstI32, I32: int32(4 + i*4)})
				b.emit(Op{Kind: OpAdd})
				if isFloat(bt) {
					b.emit(Op{Kind: OpFLoad})
				} else {
					b.emit(Op{Kind: OpLoad})
				}
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
			}
			// Optional guard: with bindings now in locals, run
			// the guard expression. On false, branch out of the
			// per-arm block (= fall through to the next arm) so
			// the pattern conceptually doesn't match this value.
			if arm.Guard != nil {
				if err := b.expr(arm.Guard); err != nil {
					return err
				}
				b.emit(Op{Kind: OpNot})
				b.brTo(outerArmD, true)
			}
			if err := b.stmt(arm.Body); err != nil {
				return err
			}
			// Bindings stay in b.locals after the arm finishes:
			// the IR only cares about slot indices (already
			// stamped into emitted ops), and `scratchCount` at
			// the end of lowerFunc must reflect every slot we
			// ever wrote. Two arms with overlapping binding names
			// won't clash at runtime — at most one arm body runs
			// per match.
			b.brTo(matchEndD, false) // jump past remaining arms
			b.closeScope()           // end outer per-arm block
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.closeScope() // end of match
	default:
		return fmt.Errorf("ir: unsupported statement %T", s)
	}
	return nil
}

func (b *builder) expr(e ast.Expr) error {
	b.curPos = e.Pos()
	switch n := e.(type) {
	case *ast.NumberLit:
		// The checker stamps Width on the literal once a concrete
		// type is known (i32 default, i64 / u32 / u64 from
		// expected-type context). Width=0 means "default i32" for
		// literals the checker never settled (e.g. unused-expression
		// statements, type-erased generic paths).
		if n.Width == 64 {
			b.emit(Op{Kind: OpConstI64, I64: n.Value})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: int32(n.Value)})
		}
	case *ast.CastExpr:
		if err := b.expr(n.Inner); err != nil {
			return err
		}
		srcInt, srcIsInt := n.InnerType.(ast.NumberType)
		dstInt, dstIsInt := n.Target.(ast.NumberType)
		srcFloat, srcIsFloat := n.InnerType.(ast.FloatType)
		dstFloat, dstIsFloat := n.Target.(ast.FloatType)
		switch {
		case srcIsInt && dstIsInt:
			sw := srcInt.NormalWidth()
			dw := dstInt.NormalWidth()
			switch {
			case sw == dw:
				// Same-width cast (signed ↔ unsigned). Bit-
				// identical at the wasm level.
			case sw <= 32 && dw <= 32:
				// Sub-i32 ↔ i32 / sub-i32 ↔ sub-i32 stays in i32
				// storage. Narrowing (32→16, 32→8, 16→8) needs a
				// mask to keep the upper bits clean. Widening
				// when SOURCE is signed needs an explicit
				// sign-extend so high bits become 1s if the
				// source's MSB was set.
				if dw < sw {
					// Narrowing: mask to keep dw bits.
					b.emit(Op{Kind: OpConstI32, I32: int32((1 << dw) - 1)})
					b.emit(Op{Kind: OpAnd})
				} else if srcInt.IsSigned() {
					// Widening signed: sign-extend via wasm's
					// dedicated `i32.extend8_s` / `i32.extend16_s`.
					switch sw {
					case 8:
						b.emit(Op{Kind: OpSignExtend8})
					case 16:
						b.emit(Op{Kind: OpSignExtend16})
					}
				}
				// Widening unsigned within i32 needs no op — the
				// source's narrow value already has zeros above
				// its width by construction (every store narrows).
			case sw <= 32 && dw == 64:
				// Sub-i32 → i64 first widens to i32 (with sign-
				// extend if the source was signed), then extends
				// to i64. The first step's mask was already
				// applied at the producing store, so for unsigned
				// sources nothing intermediate is needed.
				if srcInt.IsSigned() {
					switch sw {
					case 8:
						b.emit(Op{Kind: OpSignExtend8})
					case 16:
						b.emit(Op{Kind: OpSignExtend16})
					}
					b.emit(Op{Kind: OpExtendI32S})
				} else {
					b.emit(Op{Kind: OpExtendI32U})
				}
			case sw == 64 && dw <= 32:
				b.emit(Op{Kind: OpWrapI64})
				if dw < 32 {
					b.emit(Op{Kind: OpConstI32, I32: int32((1 << dw) - 1)})
					b.emit(Op{Kind: OpAnd})
				}
			default:
				return fmt.Errorf("ir: cast from %s to %s not yet supported", n.InnerType, n.Target)
			}
		case srcIsFloat && dstIsFloat:
			sw := srcFloat.NormalWidth()
			dw := dstFloat.NormalWidth()
			switch {
			case sw == dw:
				// f32→f32 / f64→f64 is identity.
			case sw == 32 && dw == 64:
				b.emit(Op{Kind: OpFPromoteF32})
			case sw == 64 && dw == 32:
				b.emit(Op{Kind: OpFDemoteF64})
			}
		default:
			return fmt.Errorf("ir: cast from %s to %s not yet supported", n.InnerType, n.Target)
		}
	case *ast.BoolLit:
		v := int32(0)
		if n.Value {
			v = 1
		}
		b.emit(Op{Kind: OpConstI32, I32: v})
	case *ast.StringLit:
		b.emit(Op{Kind: OpConstStr, Str: n.Value})
	case *ast.FloatLit:
		// The checker stamps `Width` on the literal once a
		// concrete float type is known; Width=0 means the literal
		// stayed at the f32 default (no expected-type pressure).
		if n.Width == 64 {
			b.emit(Op{Kind: OpConstF64, F64: n.Value})
		} else {
			b.emit(Op{Kind: OpConstF32, F32: float32(n.Value)})
		}
	case *ast.Ident:
		// A top-level function name in non-callee position is a function
		// reference; it materialises as a table index.
		if _, ok := b.info.FuncSigs[n.Name]; ok {
			if _, isLocal := b.locals[n.Name]; !isLocal {
				b.emit(Op{Kind: OpConstFunc, Str: n.Name})
				return nil
			}
		}
		// Payload-less variant in expression position (`Red`,
		// `EOF`). We construct an enum object containing just the
		// tag — no payloads to store.
		if enumName, varIdx, payloadCount, isVariant := b.lookupVariant(n.Name); isVariant && payloadCount == 0 {
			if _, isLocal := b.locals[n.Name]; !isLocal {
				return b.emitEnumNew(nil, enumName, varIdx, 0, nil)
			}
		}
		idx, ok := b.locals[n.Name]
		if !ok {
			return fmt.Errorf("ir: unresolved identifier %q (compiler bug)", n.Name)
		}
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
	case *ast.Unary:
		switch n.Op {
		case "-":
			if n.IsFloat {
				if err := b.expr(n.Operand); err != nil {
					return err
				}
				b.emit(Op{Kind: OpFNeg})
				return nil
			}
			// WASM has no i32.neg; emit `0 - operand`.
			b.emit(Op{Kind: OpConstI32, I32: 0})
			if err := b.expr(n.Operand); err != nil {
				return err
			}
			b.emit(Op{Kind: OpSub})
		case "!":
			if err := b.expr(n.Operand); err != nil {
				return err
			}
			b.emit(Op{Kind: OpNot})
		default:
			return fmt.Errorf("ir: unsupported unary %q", n.Op)
		}
	case *ast.Binary:
		return b.binary(n)
	case *ast.Call:
		return b.call(n)
	case *ast.Assign:
		return b.assign(n)
	case *ast.Ternary:
		// `cond ? a : b` lowers to a typed `if/else` whose arms each
		// push the result. The block-type tells consumers whether the
		// produced value is i32 or f32.
		bt := BlockTypeI32
		if n.IsFloat {
			bt = BlockTypeF32
		}
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.openIf(bt)
		if err := b.expr(n.Then); err != nil {
			return err
		}
		b.elseBranch()
		if err := b.expr(n.Else); err != nil {
			return err
		}
		b.closeScope()
	case *ast.Index:
		// Compile-time fold: `"literal"[const_idx]` collapses to
		// the byte at that index, as a single `OpConstI32`. Skips
		// the runtime address compute + byte load that the
		// general path emits, and lets the const propagate into
		// surrounding arithmetic. Out-of-range indices fall
		// through to the runtime path (the unchecked address
		// compute reads past the literal's NUL — undefined, but
		// the type checker forbids static OOB so we don't reach
		// here in practice).
		if n.IsString {
			if litStr, ok := n.Array.(*ast.StringLit); ok {
				if litIdx, ok := n.Idx.(*ast.NumberLit); ok {
					idx := int(litIdx.Value)
					if idx >= 0 && idx < len(litStr.Value) {
						b.emit(Op{Kind: OpConstI32, I32: int32(litStr.Value[idx])})
						return nil
					}
				}
			}
		}
		// `s[i]` and `a[i]` lower the same way at the IR level: push
		// base, push index, call into the bounds-checking helper
		// (modelled here as a runtime function call), and load the
		// byte / word at the resulting address.
		if err := b.expr(n.Array); err != nil {
			return err
		}
		if err := b.expr(n.Idx); err != nil {
			return err
		}
		if n.IsString {
			b.emit(Op{Kind: OpCallDirect, Str: "__str_idx", I32: 2})
			b.emit(Op{Kind: OpLoadByte})
		} else if n.IsSlice {
			b.emit(Op{Kind: OpCallDirect, Str: "__slice_idx", I32: 2})
			b.emit(Op{Kind: OpLoad})
		} else {
			b.emit(Op{Kind: OpCallDirect, Str: "__arr_idx", I32: 2})
			b.emit(Op{Kind: OpLoad})
		}
	case *ast.SliceExpr:
		// Lower `arr[low:high]` to:
		//   data_ptr = (arr or *slice) + low * 4
		//   len      = high - low
		//   slice    = __slice_make(data_ptr, len)
		// Both bounds default lazily — `low` falls back to 0,
		// `high` falls back to len(source). Bounds-check happens
		// at access time inside `__slice_idx`; constructing a
		// slice with `low > high` is allowed (the resulting
		// negative len just fails the next bounds check).

		// Push the source's underlying data pointer.
		if err := b.expr(n.Source); err != nil {
			return err
		}
		if n.SourceIsSlice {
			// For sub-slicing, dereference: data_ptr lives at
			// slice + 0.
			b.emit(Op{Kind: OpLoad})
		}
		// data_ptr += low * 4 (skip when low is 0/missing).
		if n.Low != nil {
			if err := b.expr(n.Low); err != nil {
				return err
			}
			b.emit(Op{Kind: OpConstI32, I32: 4})
			b.emit(Op{Kind: OpMul})
			b.emit(Op{Kind: OpAdd})
		}
		// Stash the data_ptr for later — we still need to push
		// the len before calling `$__slice_make`.
		dataSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_slice_data_%d", dataSlot)] = dataSlot
		b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})

		// Compute len = (High or source-len) - (Low or 0).
		if n.High != nil {
			if err := b.expr(n.High); err != nil {
				return err
			}
		} else {
			// Re-evaluate Source's length. Cheap when the source
			// is an identifier (the common case); a more
			// expensive source would benefit from evaluating
			// once into a slot, but we don't have that yet.
			if err := b.expr(n.Source); err != nil {
				return err
			}
			if n.SourceIsSlice {
				// len lives at slice + 4.
				b.emit(Op{Kind: OpConstI32, I32: 4})
				b.emit(Op{Kind: OpAdd})
				b.emit(Op{Kind: OpLoad})
			} else {
				// Owned arrays / strings carry their length at
				// data_ptr - 4 (the standard prefix).
				b.emit(Op{Kind: OpConstI32, I32: 4})
				b.emit(Op{Kind: OpSub})
				b.emit(Op{Kind: OpLoad})
			}
		}
		if n.Low != nil {
			if err := b.expr(n.Low); err != nil {
				return err
			}
			b.emit(Op{Kind: OpSub})
		}
		// Stack now: [len]. Push data, swap argument order via a
		// temp local, then call `$__slice_make(data, len)`.
		lenSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_slice_len_%d", lenSlot)] = lenSlot
		b.emit(Op{Kind: OpStoreLocal, I32: lenSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
		b.emit(Op{Kind: OpCallDirect, Str: "__slice_make", I32: 2})
	case *ast.ArrayLit:
		// Allocate len*4 + 4 bytes (length prefix + payload), store
		// the length at base+0, then store each element at
		// base+4+i*4 and leave the content pointer on the stack.
		// Codegen mirrors this layout in the live WASM emitter.
		const headerBytes = 4
		nElems := int32(len(n.Elems))
		b.emit(Op{Kind: OpConstI32, I32: headerBytes + nElems*4})
		b.emit(Op{Kind: OpAlloc})
		// Use a fresh local for the base. The caller's lowerFunc
		// already declared every Var in info.Locals; the synthetic
		// slot we need here lives at len(locals) and we extend
		// b.locals in place. The Func.Locals slice the codegen
		// consumer uses isn't updated, but the IR isn't currently
		// the production code path for backends; this is enough
		// for the IR's own tests.
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__arr_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// Length prefix at base+0.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: nElems})
		b.emit(Op{Kind: OpStore})
		// Each element at base + 4 + i*4.
		for i, el := range n.Elems {
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(headerBytes + i*4)})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(el); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStore})
		}
		// Push the *content* pointer (base + 4) so the value matches
		// what the rest of the language expects from an ArrayLit.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: headerBytes})
		b.emit(Op{Kind: OpAdd})
	case *ast.TupleLit:
		// Same shape as StructLit — alloc N words, store each
		// element at offset i*4. We don't have per-element type
		// info here, so fall back to integer / pointer stores
		// for everything; an i64 or f32 element would need
		// per-position widths (revisit when those land).
		b.emit(Op{Kind: OpConstI32, I32: int32(len(n.Elems) * 4)})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_tup_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		for i, elem := range n.Elems {
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(i * 4)})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(elem); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStore})
		}
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	case *ast.StructLit:
		sd, ok := b.info.Structs[n.TypeName]
		if !ok {
			return fmt.Errorf("ir: unknown struct %q (compiler bug)", n.TypeName)
		}
		// Allocate space for all fields, then store each at its
		// declared offset (4 bytes per field).
		b.emit(Op{Kind: OpConstI32, I32: int32(len(sd.Fields) * 4)})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// Map each FieldInit to its offset by scanning the decl.
		offs := map[string]int{}
		for i, f := range sd.Fields {
			offs[f.Name] = i * 4
		}
		for _, f := range n.Fields {
			off := offs[f.Name]
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(off)})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(f.Value); err != nil {
				return err
			}
			// Pick i32 vs f32 store based on the declared field
			// type. WASM rejects `i32.store` of an f32 operand,
			// and arm32 doesn't care which mnemonic we pick (32-
			// bit register store is untyped at the instruction
			// level), so this is enforced at the IR level.
			if isFloat(fieldType(sd.Fields, f.Name)) {
				b.emit(Op{Kind: OpFStore})
			} else {
				b.emit(Op{Kind: OpStore})
			}
		}
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	case *ast.CaptureRef:
		// A capture in a hoisted local function: load the captured
		// value at `__env + offset`. The synthetic __env parameter
		// closure conversion appended is just a regular local from
		// the IR's point of view, so we look it up by name.
		envIdx, ok := b.locals["__env"]
		if !ok {
			return fmt.Errorf("ir: capture %q in function without __env param (compiler bug)", n.Name)
		}
		b.emit(Op{Kind: OpLoadLocal, I32: envIdx})
		b.emit(Op{Kind: OpConstI32, I32: int32(n.Offset)})
		b.emit(Op{Kind: OpAdd})
		if _, isF := n.Type.(ast.FloatType); isF {
			b.emit(Op{Kind: OpFLoad})
		} else {
			b.emit(Op{Kind: OpLoad})
		}
	case *ast.MakeClosure:
		// Evaluate captures in declaration order so each one ends up
		// on the stack in slot-order. OpMakeClosure consumes them and
		// pushes the freshly-built closure pointer. The function name
		// is stored in Str so codegen can resolve the table index;
		// per-capture types live on the hoisted target's Captures
		// list, which a backend can fetch from the AST when packing
		// the env block.
		for _, capExpr := range n.Captures {
			if err := b.expr(capExpr); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpMakeClosure, Str: n.FuncName, I32: int32(len(n.Captures))})
	case *ast.FieldAccess:
		// Compute base + offset_of(field), then load the word.
		// Tuple field access uses a numeric selector (`pair.0`);
		// resolve the offset against the tuple's static element
		// list, otherwise fall through to the struct path.
		var ft ast.Type
		off := -1
		if tup, ok := b.targetTupleType(n.Target); ok {
			idx, err := strconv.Atoi(n.Field)
			if err != nil {
				return fmt.Errorf("ir: tuple field selector %q is not numeric", n.Field)
			}
			if idx < 0 || idx >= len(tup.Elems) {
				return fmt.Errorf("ir: tuple has %d elements; index %d out of range", len(tup.Elems), idx)
			}
			off = idx * 4
			ft = tup.Elems[idx]
		} else {
			st := b.fieldOwner(n.Target)
			sd, ok := b.info.Structs[st]
			if !ok {
				return fmt.Errorf("ir: field access on unresolved struct %q", st)
			}
			for i, f := range sd.Fields {
				if f.Name == n.Field {
					off = i * 4
					ft = f.Type
					break
				}
			}
			if off < 0 {
				return fmt.Errorf("ir: struct %s has no field %q", st, n.Field)
			}
		}
		if err := b.expr(n.Target); err != nil {
			return err
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(off)})
		b.emit(Op{Kind: OpAdd})
		if isFloat(ft) {
			b.emit(Op{Kind: OpFLoad})
		} else {
			b.emit(Op{Kind: OpLoad})
		}
	default:
		return fmt.Errorf("ir: unsupported expression %T", e)
	}
	return nil
}

// fieldOwner returns the struct name of the value t produces. It
// supports the small set of expression shapes the IR needs to lower
// FieldAccess: identifiers (var / param), nested field access, and
// struct literals.
// exprType returns the static type of `e` for the limited set of
// shapes the IR layer needs to distinguish at lowering time. Falls
// back to nil when the expression's type can't be derived purely
// from local / param / argument metadata. Used by `len()` so we
// can pick the slice (`+4`) vs array / string (`-4`) offset.
func (b *builder) exprType(e ast.Expr) ast.Type {
	switch x := e.(type) {
	case *ast.Ident:
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == x.Name {
				return v.Type
			}
		}
		for _, p := range b.fn.Params {
			if p.Name == x.Name {
				return p.Type
			}
		}
	case *ast.SliceExpr:
		// Slice expressions always produce a SliceType — the
		// element type is the same as the source.
		src := b.exprType(x.Source)
		switch s := src.(type) {
		case ast.ArrayType:
			return ast.SliceType{Elem: s.Elem}
		case ast.SliceType:
			return s
		}
	}
	return nil
}

// targetTupleType returns the static TupleType of `e` when `e`
// resolves to one. Used by FieldAccess lowering to dispatch
// numeric-index access without going through the struct path.
func (b *builder) targetTupleType(e ast.Expr) (ast.TupleType, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == x.Name {
				if t, ok := v.Type.(ast.TupleType); ok {
					return t, true
				}
			}
		}
		for _, p := range b.fn.Params {
			if p.Name == x.Name {
				if t, ok := p.Type.(ast.TupleType); ok {
					return t, true
				}
			}
		}
	case *ast.TupleLit:
		elems := make([]ast.Type, 0, len(x.Elems))
		// The checker has already type-checked inner exprs, but
		// we don't have access to their resolved types from the
		// IR layer without re-checking. Skip the optimisation
		// for raw `(...).N` access by punting back to fieldOwner
		// (which won't find it either, surfacing a compile-time
		// error) — in practice nobody writes `(1,2).0` because
		// they could just write `1`. If this becomes a real
		// pattern, plumb expr types through checker.Info.
		_ = elems
	case *ast.FieldAccess:
		// Nested tuple access — need to walk down. Only one level
		// supported: `pair.0.field` where `pair.0` is a tuple.
		if outer, ok := b.targetTupleType(x.Target); ok {
			idx, err := strconv.Atoi(x.Field)
			if err == nil && idx >= 0 && idx < len(outer.Elems) {
				if t, ok := outer.Elems[idx].(ast.TupleType); ok {
					return t, true
				}
			}
		}
	}
	return ast.TupleType{}, false
}

func (b *builder) fieldOwner(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		// Look up via locals: vars carry a Var.Type; params live in
		// fn.Params. We cross-reference the checker's info.
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == x.Name {
				if st, ok := v.Type.(ast.StructType); ok {
					return st.Name
				}
			}
		}
		for _, p := range b.fn.Params {
			if p.Name == x.Name {
				if st, ok := p.Type.(ast.StructType); ok {
					return st.Name
				}
			}
		}
	case *ast.FieldAccess:
		owner := b.fieldOwner(x.Target)
		sd, ok := b.info.Structs[owner]
		if !ok {
			return ""
		}
		for _, f := range sd.Fields {
			if f.Name == x.Field {
				if st, ok := f.Type.(ast.StructType); ok {
					return st.Name
				}
			}
		}
	case *ast.StructLit:
		return x.TypeName
	}
	return ""
}

func (b *builder) binary(n *ast.Binary) error {
	// Short-circuit operators don't fit the "both sides then op" pattern;
	// lower them as small if/else chains over the IR.
	switch n.Op {
	case "&&":
		// `a && b` → `if a then b else 0`. The branch pushes b only
		// when a is truthy, else a normalised 0.
		if err := b.expr(n.Left); err != nil {
			return err
		}
		b.openIf(BlockTypeI32)
		if err := b.expr(n.Right); err != nil {
			return err
		}
		b.elseBranch()
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.closeScope()
		return nil
	case "||":
		// `a || b` → `if a then 1 else b`. The truthy branch
		// normalises a to 1 the way the AST emitter does.
		if err := b.expr(n.Left); err != nil {
			return err
		}
		b.openIf(BlockTypeI32)
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.elseBranch()
		if err := b.expr(n.Right); err != nil {
			return err
		}
		b.closeScope()
		return nil
	}
	// Integer arithmetic identities + strength reduction. Only
	// applied when neither side is a string-concat / float —
	// floats sidestep because they need NaN / signed-zero
	// handling we don't want to bake in here, and string +
	// has its own handling below.
	if !n.IsStringConcat && !n.IsStringCmp && !n.IsFloat {
		if folded, ok := b.maybeFoldArithIdentity(n); ok {
			return folded
		}
		if folded, ok := b.maybeFoldSelfIdentity(n); ok {
			return folded
		}
	}
	if n.IsStringConcat {
		// Compile-time fold: `"foo" + "bar"` collapses to the
		// concatenated literal `"foobar"` as a single OpConstStr.
		// Skips the runtime `__lang_strcat` allocation + 2x
		// memcpy entirely; the literal lands in .rodata once
		// (deduped via internString on either backend). Chains
		// fold left-associatively because the AST is built
		// that way: `"a" + "b" + "c"` becomes
		// `("a" + "b") + "c"`, the inner pair folds first,
		// then the outer pair folds against the result.
		if litL, lOK := n.Left.(*ast.StringLit); lOK {
			if litR, rOK := n.Right.(*ast.StringLit); rOK {
				b.emit(Op{Kind: OpConstStr, Str: litL.Value + litR.Value})
				return nil
			}
		}
	}
	if n.IsStringCmp {
		if folded, ok := b.maybeFoldStringEq(n); ok {
			return folded
		}
	}
	if err := b.expr(n.Left); err != nil {
		return err
	}
	if err := b.expr(n.Right); err != nil {
		return err
	}
	if n.IsStringConcat {
		// Both operands have been pushed; concatenation is a
		// single dedicated op that mirrors the WASM runtime helper.
		b.emit(Op{Kind: OpStrConcat})
		return nil
	}
	if n.IsStringCmp {
		// Same shape as concat but for content equality. `!=` is
		// the negation of `==`.
		b.emit(Op{Kind: OpStrEq})
		if n.Op == "!=" {
			b.emit(Op{Kind: OpNot})
		}
		return nil
	}
	if n.IsFloat {
		op, ok := floatOp(n.Op)
		if !ok {
			return fmt.Errorf("ir: unsupported float binary %q", n.Op)
		}
		// FloatWidth=0 means "unannotated by the checker", which
		// happens for IR-test inputs that bypass the checker.
		// Treat as f32 so existing tests pass; checker-produced
		// trees set it explicitly for f64 ops.
		w := n.FloatWidth
		if w == 0 {
			w = 32
		}
		b.emit(Op{Kind: op, Width: w})
		return nil
	}
	op, ok := intOp(n.Op)
	if !ok {
		return fmt.Errorf("ir: unsupported binary %q", n.Op)
	}
	// Width=0 means "unannotated by the checker", which happens
	// for IR-test inputs that bypass the checker entirely. Treat
	// as i32 so existing tests pass; checker-produced trees set
	// IntWidth explicitly.
	w := n.IntWidth
	if w == 0 {
		w = 32
	}
	b.emit(Op{Kind: op, Width: w, Unsigned: n.IsUnsigned})
	return nil
}

// maybeFoldStringEq attempts to compile-time-fold a string
// equality / inequality comparison. Two shapes:
//
//  1. lit == lit  →  OpConstI32 0/1   (and the inverse for !=)
//  2. ident == lit (or lit == ident) →
//        len(ident) == lit.length
//          ? <ident == lit at byte level via OpStrEq>
//          : 0
//     The length-mismatch path skips the strcmp call entirely;
//     the length-match path falls through to the existing
//     `__lang_strcmp` runtime. For the common HTTP-routing
//     pattern `path == "/health"` (where most paths have
//     different lengths from the literal) this saves the
//     function call + the strcmp's internal length compare.
//
// Returns (nil, true) on success — the IR has been emitted in
// place. Returns (_, false) when the shape doesn't apply, in
// which case the caller falls back to the standard OpStrEq.
// maybeFoldArithIdentity recognises right- and left-identity
// arithmetic / bitwise patterns plus power-of-two strength
// reduction, applied only when the *other* side is a non-
// literal — the all-const case is left for fold.go's binary
// fold to collapse into a single OpConstI32.
//
//	x + 0   → x          0 + x  → x
//	x - 0   → x
//	x * 1   → x          1 * x  → x
//	x * 2^k → x << k     2^k * x → x << k        (k > 0)
//	x | 0   → x          0 | x  → x
//	x ^ 0   → x          0 ^ x  → x
//	x & -1  → x         -1 & x  → x        (all bits set)
//	x << 0  → x          x >> 0 → x
//
// Comparisons and division / modulo are intentionally not
// folded: comparisons against 0/1 still need to produce a
// 0/1 result, and `/` / `%` would change observable behaviour
// when the divisor is zero.
func (b *builder) maybeFoldArithIdentity(n *ast.Binary) (error, bool) {
	numL, lok := constNumber(n.Left)
	numR, rok := constNumber(n.Right)
	// Only one side may be a literal — leave the all-const case
	// to fold.go.
	if lok && rok {
		return nil, false
	}
	switch n.Op {
	case "+", "|", "^":
		if lok && numL == 0 {
			return b.expr(n.Right), true
		}
		if rok && numR == 0 {
			return b.expr(n.Left), true
		}
	case "-", "<<", ">>":
		if rok && numR == 0 {
			return b.expr(n.Left), true
		}
	case "&":
		if lok && numL == -1 {
			return b.expr(n.Right), true
		}
		if rok && numR == -1 {
			return b.expr(n.Left), true
		}
	case "*":
		if lok && numL == 1 {
			return b.expr(n.Right), true
		}
		if rok && numR == 1 {
			return b.expr(n.Left), true
		}
		if lok {
			if k, ok := powerOfTwo(numL); ok && k > 0 {
				if err := b.expr(n.Right); err != nil {
					return err, true
				}
				b.emit(Op{Kind: OpConstI32, I32: k})
				b.emit(Op{Kind: OpShl})
				return nil, true
			}
		}
		if rok {
			if k, ok := powerOfTwo(numR); ok && k > 0 {
				if err := b.expr(n.Left); err != nil {
					return err, true
				}
				b.emit(Op{Kind: OpConstI32, I32: k})
				b.emit(Op{Kind: OpShl})
				return nil, true
			}
		}
	}
	return nil, false
}

// constNumber peels back the small set of AST shapes that
// resolve to a compile-time integer constant. Currently:
//   - NumberLit       (e.g. `5`)
//   - Unary("-", num) (e.g. `-1`, which the parser builds as
//                      a unary minus on a positive literal)
// Returns the int32 value and true on a hit.
func constNumber(e ast.Expr) (int32, bool) {
	if n, ok := e.(*ast.NumberLit); ok {
		return int32(n.Value), true
	}
	if u, ok := e.(*ast.Unary); ok && u.Op == "-" {
		if inner, ok := u.Operand.(*ast.NumberLit); ok {
			return -int32(inner.Value), true
		}
	}
	return 0, false
}

// maybeFoldSelfIdentity recognises self-on-both-sides patterns
// for an Ident — the result is independent of the value and
// can be replaced with the appropriate constant. Inspired by
// Cranelift's icmp.isle / arithmetic.isle rules.
//
//	x - x   → 0
//	x ^ x   → 0
//	x | x   → x
//	x & x   → x
//	x == x  → 1
//	x != x  → 0
//	x <  x  → 0
//	x <= x  → 1
//	x >  x  → 0
//	x >= x  → 1
//
// Skipped:
//
//	x + x  — not a self-identity (would be `x * 2`); already
//	         strength-reduces via the existing pow2 fold when
//	         one side is `2`.
//	x * x  — not an identity, real square computation.
//	x / x  — would be 1, but x might be 0 → runtime trap; we
//	         can't fold without changing observable behaviour.
//	x % x  — same divisor-zero concern.
//	String / float ops — caller already gates on integer.
//
// Restricted to plain identifiers (n.Left and n.Right both
// `*ast.Ident` with the same name) so we don't double-evaluate
// expressions with side effects.
func (b *builder) maybeFoldSelfIdentity(n *ast.Binary) (error, bool) {
	idL, ok := n.Left.(*ast.Ident)
	if !ok {
		return nil, false
	}
	idR, ok := n.Right.(*ast.Ident)
	if !ok || idL.Name != idR.Name {
		return nil, false
	}
	switch n.Op {
	case "-", "^":
		b.emit(Op{Kind: OpConstI32, I32: 0})
		return nil, true
	case "|", "&":
		return b.expr(n.Left), true
	case "==", "<=", ">=":
		b.emit(Op{Kind: OpConstI32, I32: 1})
		return nil, true
	case "!=", "<", ">":
		b.emit(Op{Kind: OpConstI32, I32: 0})
		return nil, true
	}
	return nil, false
}

// powerOfTwo returns (k, true) when n == 1 << k for some
// 0 <= k < 31. Used by the multiplication strength reduction.
func powerOfTwo(n int32) (int32, bool) {
	if n <= 0 {
		return 0, false
	}
	if n&(n-1) != 0 {
		return 0, false
	}
	for k := int32(0); k < 31; k++ {
		if n == 1<<k {
			return k, true
		}
	}
	return 0, false
}

func (b *builder) maybeFoldStringEq(n *ast.Binary) (error, bool) {
	litL, lOK := n.Left.(*ast.StringLit)
	litR, rOK := n.Right.(*ast.StringLit)
	if lOK && rOK {
		// Both literals — compute the answer at compile time.
		var v int32 = 0
		if (n.Op == "==") == (litL.Value == litR.Value) {
			v = 1
		}
		b.emit(Op{Kind: OpConstI32, I32: v})
		return nil, true
	}
	// One side literal, the other a pure identifier (no side
	// effects to worry about double-evaluating).
	var lit *ast.StringLit
	var ident *ast.Ident
	if lOK {
		lit = litL
		if id, ok := n.Right.(*ast.Ident); ok {
			ident = id
		}
	} else if rOK {
		lit = litR
		if id, ok := n.Left.(*ast.Ident); ok {
			ident = id
		}
	}
	if lit == nil || ident == nil {
		return nil, false
	}
	// Don't apply the fold to identifiers that resolve to top-
	// level functions or aren't local — the codegen path for
	// non-local strings is more delicate (currently nothing
	// breaks, but the language has no string globals yet, so
	// we keep the optimization scoped to locals + params).
	if _, ok := b.locals[ident.Name]; !ok {
		return nil, false
	}
	// Emit: <ident> ; const 4 ; sub ; load  (i.e. len(ident))
	if err := b.expr(ident); err != nil {
		return err, true
	}
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpConstI32, I32: int32(len(lit.Value))})
	b.emit(Op{Kind: OpEq})
	b.openIf(BlockTypeI32)
	if err := b.expr(ident); err != nil {
		return err, true
	}
	b.emit(Op{Kind: OpConstStr, Str: lit.Value})
	b.emit(Op{Kind: OpStrEq})
	b.elseBranch()
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.closeScope()
	if n.Op == "!=" {
		b.emit(Op{Kind: OpNot})
	}
	return nil, true
}

func intOp(s string) (OpKind, bool) {
	switch s {
	case "+":
		return OpAdd, true
	case "-":
		return OpSub, true
	case "*":
		return OpMul, true
	case "/":
		return OpDivS, true
	case "%":
		return OpRemS, true
	case "&":
		return OpAnd, true
	case "|":
		return OpOr, true
	case "^":
		return OpXor, true
	case "<<":
		return OpShl, true
	case ">>":
		return OpShrS, true
	case "==":
		return OpEq, true
	case "!=":
		return OpNe, true
	case "<":
		return OpLtS, true
	case "<=":
		return OpLeS, true
	case ">":
		return OpGtS, true
	case ">=":
		return OpGeS, true
	}
	return 0, false
}

func floatOp(s string) (OpKind, bool) {
	switch s {
	case "+":
		return OpFAdd, true
	case "-":
		return OpFSub, true
	case "*":
		return OpFMul, true
	case "/":
		return OpFDiv, true
	case "==":
		return OpFEq, true
	case "!=":
		return OpFNe, true
	case "<":
		return OpFLt, true
	case "<=":
		return OpFLe, true
	case ">":
		return OpFGt, true
	case ">=":
		return OpFGe, true
	}
	return 0, false
}

func (b *builder) call(n *ast.Call) error {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return fmt.Errorf("ir: indirect call from non-identifier expression")
	}
	// Variant constructor: lower to a heap-allocated tagged-union
	// object [tag, payload0, payload1, ...]. The checker already
	// type-checked the args; we just emit the storage.
	if enumName, varIdx, payloads, isVariant := b.lookupVariant(id.Name); isVariant {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			return b.emitEnumNew(n, enumName, varIdx, payloads, n.Args)
		}
	}
	// `len(x)` on a string or array is inlined: both layouts carry a
	// 4-byte little-endian length prefix at `ptr - 4`. The checker
	// doesn't declare `len` as a function signature, so the call falls
	// here ahead of the FuncSigs / locals path.
	if id.Name == "len" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if _, isDeclared := b.info.FuncSigs[id.Name]; !isDeclared {
				// Compile-time fold: when the arg is a literal whose
				// length is statically known, collapse the whole
				// `<ptr>; const 4; sub; load` sequence to a single
				// const. Saves the runtime alloc + prefix-load that
				// the unfolded shape would force, and lets the
				// const propagate into surrounding arithmetic.
				switch lit := n.Args[0].(type) {
				case *ast.StringLit:
					b.emit(Op{Kind: OpConstI32, I32: int32(len(lit.Value))})
					return nil
				case *ast.ArrayLit:
					b.emit(Op{Kind: OpConstI32, I32: int32(len(lit.Elems))})
					return nil
				}
				if err := b.expr(n.Args[0]); err != nil {
					return err
				}
				// Slice values carry the length at slice+4
				// (after the data_ptr); arrays / strings carry
				// it at base-4 (the standard prefix). Pick the
				// offset based on the static type.
				if _, isSlice := b.exprType(n.Args[0]).(ast.SliceType); isSlice {
					b.emit(Op{Kind: OpConstI32, I32: 4})
					b.emit(Op{Kind: OpAdd})
				} else {
					b.emit(Op{Kind: OpConstI32, I32: 4})
					b.emit(Op{Kind: OpSub})
				}
				b.emit(Op{Kind: OpLoad})
				return nil
			}
		}
	}
	for _, a := range n.Args {
		if err := b.expr(a); err != nil {
			return err
		}
	}
	// Direct call if the name is a top-level / builtin function and not
	// shadowed by a local of the same name.
	if _, isFunc := b.info.FuncSigs[id.Name]; isFunc {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: id.Name, I32: int32(len(n.Args))})
			return nil
		}
	}
	// Otherwise it's a call through a function-typed local: push the
	// table index and dispatch indirectly. The local's static type is
	// the FuncType codegen needs to resolve a `(type $tN)` clause.
	idx, ok := b.locals[id.Name]
	if !ok {
		return fmt.Errorf("ir: cannot resolve callee %q", id.Name)
	}
	sig, err := b.localFuncType(id.Name)
	if err != nil {
		return err
	}
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: sig})
	return nil
}

// localFuncType returns the static FuncType of the named local. Calls
// through a non-function-typed local are a checker bug, but we surface
// them as IR errors rather than panicking.
func (b *builder) localFuncType(name string) (*ast.FuncType, error) {
	for _, p := range b.fn.Params {
		if p.Name == name {
			ft, ok := p.Type.(*ast.FuncType)
			if !ok {
				return nil, fmt.Errorf("ir: indirect call through non-function-typed param %q", name)
			}
			return ft, nil
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			ft, ok := v.Type.(*ast.FuncType)
			if !ok {
				return nil, fmt.Errorf("ir: indirect call through non-function-typed var %q", name)
			}
			return ft, nil
		}
	}
	return nil, fmt.Errorf("ir: indirect call through unknown local %q", name)
}

func (b *builder) assign(n *ast.Assign) error {
	switch t := n.Target.(type) {
	case *ast.Ident:
		if err := b.expr(n.Value); err != nil {
			return err
		}
		idx, ok := b.locals[t.Name]
		if !ok {
			return fmt.Errorf("ir: cannot assign to %q (no slot)", t.Name)
		}
		// Tee semantics: leave a copy on the stack for callers that
		// use the assignment as an expression. Plain ExprStmts drop it
		// via exprLeavesValue + OpDrop.
		b.emit(Op{Kind: OpStoreLocal, I32: idx})
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		return nil
	case *ast.Index:
		// `a[i] = v` lowers to a bounds-checked address compute via
		// __arr_idx, then a regular i32.store. Doesn't leave a value
		// on the stack — exprLeavesValue special-cases this shape so
		// no drop is emitted by the surrounding ExprStmt.
		if err := b.expr(t.Array); err != nil {
			return err
		}
		if err := b.expr(t.Idx); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallDirect, Str: "__arr_idx", I32: 2})
		if err := b.expr(n.Value); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStore})
		return nil
	case *ast.FieldAccess:
		// `p.field = v` lowers to base + offset; value; store. Same
		// no-result discipline as index assignment. Float-typed
		// fields require an f32 store; everything else is i32.
		st := b.fieldOwner(t.Target)
		sd, ok := b.info.Structs[st]
		if !ok {
			return fmt.Errorf("ir: field assignment on unresolved struct %q", st)
		}
		off := -1
		var ft ast.Type
		for i, f := range sd.Fields {
			if f.Name == t.Field {
				off = i * 4
				ft = f.Type
				break
			}
		}
		if off < 0 {
			return fmt.Errorf("ir: struct %s has no field %q", st, t.Field)
		}
		if err := b.expr(t.Target); err != nil {
			return err
		}
		if off > 0 {
			b.emit(Op{Kind: OpConstI32, I32: int32(off)})
			b.emit(Op{Kind: OpAdd})
		}
		if err := b.expr(n.Value); err != nil {
			return err
		}
		if isFloat(ft) {
			b.emit(Op{Kind: OpFStore})
		} else {
			b.emit(Op{Kind: OpStore})
		}
		return nil
	}
	return fmt.Errorf("ir: assignment target %T not yet lowered", n.Target)
}

func exprLeavesValue(e ast.Expr, info *checker.Info) bool {
	if a, ok := e.(*ast.Assign); ok {
		// Ident assignment leaves the assigned value on the stack
		// (tee semantics). Index and FieldAccess assignments don't —
		// they store and finish, so no drop is needed afterwards.
		switch a.Target.(type) {
		case *ast.Ident:
			return true
		}
		return false
	}
	if c, ok := e.(*ast.Call); ok {
		if id, ok := c.Callee.(*ast.Ident); ok {
			if sig, ok := info.FuncSigs[id.Name]; ok {
				return !ast.Equal(sig.Result, ast.VoidType{})
			}
		}
		return true
	}
	return true
}

func isVoid(t ast.Type) bool {
	_, ok := t.(ast.VoidType)
	return ok
}

func isFloat(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
}

// fieldType returns the declared type of `name` in fields, or nil
// if no such field exists. Used by codegen sites that need to
// pick i32 vs f32 ops based on the field's declared type.
func fieldType(fields []ast.Param, name string) ast.Type {
	for _, f := range fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}
