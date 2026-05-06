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
// where they appear). Control flow uses numeric labels rather than
// nested blocks so that lowering of `for`/`while`/`break`/`continue`
// produces a flat ops list that's easier to translate.
//
// What this PR introduces:
//   - The op set, IR Func and Program data types.
//   - A `Lower` pass: ast.Program + checker.Info → ir.Program.
//   - Lowering tests covering arithmetic, locals, control flow, and
//     function calls.
//
// What's NOT yet done:
//   - The WASM and ARM32 backends still walk the AST. Migrating them is
//     a follow-up; for now the IR is verified by tests rather than by
//     being on the production code path.
package ir

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
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
	OpNeg // unary -
	OpNot // logical !

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

	// String runtime calls.
	OpStrEq // (a-ptr, b-ptr)     → i32

	// Control flow. Labels are a sparse address space — the ops list
	// also contains OpLabel entries that "place" a label at a position.
	OpJump        // → unconditional jump to Label
	OpJumpIfFalse // (i32) → branch to Label when zero
	OpLabel       // declares a target

	// Calls.
	OpCallDirect   // (args...)        → result | ()
	OpCallIndirect // (args..., idx)   → result | ()

	OpDrop       // (T)               → ()
	OpReturn     // (T)               → unwinds the function
	OpReturnVoid // ()                → unwinds the function
)

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
	case OpNeg:
		return "neg"
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
	case OpStrEq:
		return "str.eq"
	case OpJump:
		return "jump"
	case OpJumpIfFalse:
		return "jump_if_false"
	case OpLabel:
		return "label"
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
	}
	return "<invalid>"
}

// Op is one instruction in a function's linear op list. Operands that
// don't apply to a given op are zero-valued.
type Op struct {
	Kind OpKind
	// I32 is the immediate for OpConstI32, the local index for
	// OpLoadLocal/OpStoreLocal, the label id for OpJump/OpJumpIfFalse/
	// OpLabel, the arg count for OpCallDirect/OpCallIndirect, and the
	// table index for OpConstFunc.
	I32 int32
	// F32 is the immediate for OpConstF32.
	F32 float32
	// Str carries OpConstStr's string value and OpCallDirect's callee
	// name. Empty otherwise.
	Str string
}

// Func is a single lowered function: parameter / local list, ops, and
// the return type.
type Func struct {
	Name       string
	Params     []ast.Param
	Locals     []*ast.Var
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
	case OpJump, OpJumpIfFalse, OpLabel:
		return fmt.Sprintf("%s L%d", op.Kind, op.I32)
	case OpCallDirect:
		return fmt.Sprintf("%s %s argc=%d", op.Kind, op.Str, op.I32)
	case OpCallIndirect:
		return fmt.Sprintf("%s argc=%d", op.Kind, op.I32)
	}
	return op.Kind.String()
}

// Lower converts a checked AST program into IR. The Info argument
// supplies the local-by-function table, struct-decl map, and function
// signatures the lowering pass needs to resolve names.
func Lower(prog *ast.Program, info *checker.Info) (*Program, error) {
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
	info   *checker.Info
	fn     *ast.FuncDecl
	out    *Func
	labels int32 // next label id
	// locals maps parameter and var names to their 0-based slot index.
	// Parameters are slots 0..len(params)-1; vars start at len(params).
	locals map[string]int32
	// breakStack and contStack track jump labels for nested loops /
	// switches so `break` and `continue` resolve to the innermost.
	breakStack []int32
	contStack  []int32
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

func (b *builder) emit(op Op) { b.out.Ops = append(b.out.Ops, op) }

func (b *builder) freshLabel() int32 {
	b.labels++
	return b.labels
}

func (b *builder) stmt(s ast.Stmt) error {
	switch n := s.(type) {
	case *ast.Block:
		for _, ss := range n.Stmts {
			if err := b.stmt(ss); err != nil {
				return err
			}
		}
	case *ast.If:
		elseL := b.freshLabel()
		endL := b.freshLabel()
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJumpIfFalse, I32: elseL})
		if err := b.stmt(n.Then); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJump, I32: endL})
		b.emit(Op{Kind: OpLabel, I32: elseL})
		if n.Else != nil {
			if err := b.stmt(n.Else); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpLabel, I32: endL})
	case *ast.While:
		topL := b.freshLabel()
		endL := b.freshLabel()
		b.emit(Op{Kind: OpLabel, I32: topL})
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJumpIfFalse, I32: endL})
		b.breakStack = append(b.breakStack, endL)
		b.contStack = append(b.contStack, topL)
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.contStack = b.contStack[:len(b.contStack)-1]
		b.emit(Op{Kind: OpJump, I32: topL})
		b.emit(Op{Kind: OpLabel, I32: endL})
	case *ast.For:
		topL := b.freshLabel()
		stepL := b.freshLabel()
		endL := b.freshLabel()
		if n.Init != nil {
			if err := b.stmt(n.Init); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpLabel, I32: topL})
		if err := b.expr(n.Cond); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJumpIfFalse, I32: endL})
		b.breakStack = append(b.breakStack, endL)
		b.contStack = append(b.contStack, stepL)
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.contStack = b.contStack[:len(b.contStack)-1]
		b.emit(Op{Kind: OpLabel, I32: stepL})
		if n.Step != nil {
			if err := b.stmt(n.Step); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpJump, I32: topL})
		b.emit(Op{Kind: OpLabel, I32: endL})
	case *ast.Break:
		if len(b.breakStack) == 0 {
			return fmt.Errorf("ir: break outside of a loop (compiler bug — should be checker-rejected)")
		}
		b.emit(Op{Kind: OpJump, I32: b.breakStack[len(b.breakStack)-1]})
	case *ast.Continue:
		if len(b.contStack) == 0 {
			return fmt.Errorf("ir: continue outside of a loop (compiler bug — should be checker-rejected)")
		}
		b.emit(Op{Kind: OpJump, I32: b.contStack[len(b.contStack)-1]})
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
	default:
		return fmt.Errorf("ir: unsupported statement %T", s)
	}
	return nil
}

func (b *builder) expr(e ast.Expr) error {
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
		if err := b.expr(n.Operand); err != nil {
			return err
		}
		switch n.Op {
		case "-":
			if n.IsFloat {
				b.emit(Op{Kind: OpFNeg})
			} else {
				b.emit(Op{Kind: OpNeg})
			}
		case "!":
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
	default:
		return fmt.Errorf("ir: unsupported expression %T", e)
	}
	return nil
}

func (b *builder) binary(n *ast.Binary) error {
	// Short-circuit operators don't fit the "both sides then op" pattern;
	// lower them as small if/else chains over the IR.
	switch n.Op {
	case "&&":
		falseL := b.freshLabel()
		endL := b.freshLabel()
		if err := b.expr(n.Left); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJumpIfFalse, I32: falseL})
		if err := b.expr(n.Right); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJump, I32: endL})
		b.emit(Op{Kind: OpLabel, I32: falseL})
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.emit(Op{Kind: OpLabel, I32: endL})
		return nil
	case "||":
		trueL := b.freshLabel()
		falseL := b.freshLabel()
		endL := b.freshLabel()
		if err := b.expr(n.Left); err != nil {
			return err
		}
		// JumpIfFalse falsey-path; otherwise the value is already truthy
		// but we want a normalised 1.
		b.emit(Op{Kind: OpJumpIfFalse, I32: falseL})
		b.emit(Op{Kind: OpJump, I32: trueL})
		b.emit(Op{Kind: OpLabel, I32: falseL})
		if err := b.expr(n.Right); err != nil {
			return err
		}
		b.emit(Op{Kind: OpJump, I32: endL})
		b.emit(Op{Kind: OpLabel, I32: trueL})
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpLabel, I32: endL})
		return nil
	}
	if err := b.expr(n.Left); err != nil {
		return err
	}
	if err := b.expr(n.Right); err != nil {
		return err
	}
	if n.IsStringConcat {
		// Lowering string concat into IR is left for a follow-up; the
		// existing backends emit it directly from the AST.
		return fmt.Errorf("ir: string concatenation not yet lowered")
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
	// table index and dispatch indirectly.
	idx, ok := b.locals[id.Name]
	if !ok {
		return fmt.Errorf("ir: cannot resolve callee %q", id.Name)
	}
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args))})
	return nil
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
	}
	return fmt.Errorf("ir: assignment target %T not yet lowered", n.Target)
}

func exprLeavesValue(e ast.Expr, info *checker.Info) bool {
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
