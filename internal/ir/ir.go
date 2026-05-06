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
	OpConstI32   // (i32 imm)         → i32
	OpConstF32   // (f32 imm)         → f32
	OpConstStr   // (string-id imm)   → i32 (pointer)
	OpConstFunc  // (func-id imm)     → i32 (table index)

	// Locals (parameter or var). Idx is the 0-based slot.
	OpLoadLocal  // ()                → T
	OpStoreLocal // (T)               → ()

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
)

// blockTypeName returns a short mnemonic for use in formatted output.
func blockTypeName(bt int32) string {
	switch bt {
	case BlockTypeI32:
		return "i32"
	case BlockTypeF32:
		return "f32"
	}
	return "void"
}

// String returns a short mnemonic for the op kind.
func (k OpKind) String() string {
	switch k {
	case OpConstI32:
		return "const.i32"
	case OpConstF32:
		return "const.f32"
	case OpConstStr:
		return "const.str"
	case OpConstFunc:
		return "const.func"
	case OpLoadLocal:
		return "local.load"
	case OpStoreLocal:
		return "local.store"
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
	// F32 is the immediate for OpConstF32.
	F32 float32
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
// the return type. NumScratch counts the synthetic i32 slots the
// lowering pass conjured for ArrayLit / StructLit / Switch / closure
// helpers — they live at indices [len(Params)+len(Locals), …) and are
// addressed by OpLoadLocal / OpStoreLocal just like user vars.
type Func struct {
	Name       string
	Params     []ast.Param
	Locals     []*ast.Var
	NumScratch int32
	ReturnType ast.Type
	Ops        []Op
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
	case OpConstI32, OpLoadLocal, OpStoreLocal:
		return fmt.Sprintf("%s %d", op.Kind, op.I32)
	case OpConstF32:
		return fmt.Sprintf("%s %g", op.Kind, op.F32)
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
	b := &builder{info: info, fn: fn, out: out, locals: map[string]int32{}}
	for i, p := range fn.Params {
		b.locals[p.Name] = int32(i)
	}
	for i, v := range info.Locals[fn] {
		b.locals[v.Name] = int32(len(fn.Params) + i)
	}
	if err := b.stmt(fn.Body); err != nil {
		return nil, err
	}
	// Record how many synthetic slots the lowering pass conjured beyond
	// the user-visible params + locals — ArrayLit / StructLit / Switch /
	// closure helpers each grew the locals map. Codegen needs the count
	// to declare matching WAT locals.
	out.NumScratch = int32(len(b.locals)) - int32(len(fn.Params)+len(info.Locals[fn]))
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
		tagSlot := int32(len(b.locals))
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
	default:
		return fmt.Errorf("ir: unsupported statement %T", s)
	}
	return nil
}

func (b *builder) expr(e ast.Expr) error {
	b.curPos = e.Pos()
	switch n := e.(type) {
	case *ast.NumberLit:
		b.emit(Op{Kind: OpConstI32, I32: int32(n.Value)})
	case *ast.BoolLit:
		v := int32(0)
		if n.Value {
			v = 1
		}
		b.emit(Op{Kind: OpConstI32, I32: v})
	case *ast.StringLit:
		b.emit(Op{Kind: OpConstStr, Str: n.Value})
	case *ast.FloatLit:
		b.emit(Op{Kind: OpConstF32, F32: float32(n.Value)})
	case *ast.Ident:
		// A top-level function name in non-callee position is a function
		// reference; it materialises as a table index.
		if _, ok := b.info.FuncSigs[n.Name]; ok {
			if _, isLocal := b.locals[n.Name]; !isLocal {
				b.emit(Op{Kind: OpConstFunc, Str: n.Name})
				return nil
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
		} else {
			b.emit(Op{Kind: OpCallDirect, Str: "__arr_idx", I32: 2})
			b.emit(Op{Kind: OpLoad})
		}
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
		baseSlot := int32(len(b.locals))
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
	case *ast.StructLit:
		sd, ok := b.info.Structs[n.TypeName]
		if !ok {
			return fmt.Errorf("ir: unknown struct %q (compiler bug)", n.TypeName)
		}
		// Allocate space for all fields, then store each at its
		// declared offset (4 bytes per field).
		b.emit(Op{Kind: OpConstI32, I32: int32(len(sd.Fields) * 4)})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := int32(len(b.locals))
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
			b.emit(Op{Kind: OpStore})
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
		// Resolving the offset needs the static type of the target;
		// we look it up via the local types in the funcs / params,
		// or via the assumed StructType the checker assigned.
		st := b.fieldOwner(n.Target)
		sd, ok := b.info.Structs[st]
		if !ok {
			return fmt.Errorf("ir: field access on unresolved struct %q", st)
		}
		off := -1
		for i, f := range sd.Fields {
			if f.Name == n.Field {
				off = i * 4
				break
			}
		}
		if off < 0 {
			return fmt.Errorf("ir: struct %s has no field %q", st, n.Field)
		}
		if err := b.expr(n.Target); err != nil {
			return err
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(off)})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoad})
	default:
		return fmt.Errorf("ir: unsupported expression %T", e)
	}
	return nil
}

// fieldOwner returns the struct name of the value t produces. It
// supports the small set of expression shapes the IR needs to lower
// FieldAccess: identifiers (var / param), nested field access, and
// struct literals.
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
		b.emit(Op{Kind: op})
		return nil
	}
	op, ok := intOp(n.Op)
	if !ok {
		return fmt.Errorf("ir: unsupported binary %q", n.Op)
	}
	b.emit(Op{Kind: op})
	return nil
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
	// `len(x)` on a string or array is inlined: both layouts carry a
	// 4-byte little-endian length prefix at `ptr - 4`. The checker
	// doesn't declare `len` as a function signature, so the call falls
	// here ahead of the FuncSigs / locals path.
	if id.Name == "len" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if _, isDeclared := b.info.FuncSigs[id.Name]; !isDeclared {
				if err := b.expr(n.Args[0]); err != nil {
					return err
				}
				b.emit(Op{Kind: OpConstI32, I32: 4})
				b.emit(Op{Kind: OpSub})
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
		// no-result discipline as index assignment.
		st := b.fieldOwner(t.Target)
		sd, ok := b.info.Structs[st]
		if !ok {
			return fmt.Errorf("ir: field assignment on unresolved struct %q", st)
		}
		off := -1
		for i, f := range sd.Fields {
			if f.Name == t.Field {
				off = i * 4
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
		b.emit(Op{Kind: OpStore})
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
