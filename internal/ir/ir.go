// Package ir defines a small linear stack-machine intermediate
// representation that lives between the type-checked AST and the
// per-target code generators. It's the long-promised "third stage" —
// today the WASM backend walks the AST directly. Once it migrates
// to consuming IR, that logic moves here.
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
//   - The WASM backend still walks the AST. Migrating it is a
//     follow-up; for now the IR is verified by tests rather than
//     by being on the production code path for that target.
package ir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
	"github.com/jakechampion/lang/internal/shadowrename"
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
	// Int ↔ float conversions. Width is the SOURCE width
	// for I→F (32 or 64) and the DESTINATION width for F→I.
	// `Unsigned` flags signed vs unsigned reading of the
	// integer side (per wasm's _s / _u suffix).
	OpFConvertI32 // (i32) → f32 / f64; Width=32 → f32, =64 → f64; Unsigned for _u variant
	OpFConvertI64 // (i64) → f32 / f64; Width=32 → f32, =64 → f64; Unsigned for _u variant
	OpITruncF32   // (f32) → i32 / i64; Width=32 → i32, =64 → i64
	OpITruncF64   // (f64) → i32 / i64; Width=32 → i32, =64 → i64

	// Bit-level reinterpret between f32 and i32 (no value
	// conversion). Lowers the `f32_bits` / `f32_from_bits`
	// builtins. On the native backends the operand stack
	// stores both as raw 32-bit slots so these emit zero
	// instructions; wasm distinguishes typed stack slots and
	// needs `i32.reinterpret_f32` / `f32.reinterpret_i32`.
	OpReinterpretI32F32 // (f32) → i32 (bits, IEEE-754 layout)
	OpReinterpretF32I32 // (i32) → f32 (bits, IEEE-754 layout)

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
	OpLoadI8S  // (addr)             → i32 (sign-extended low byte)
	OpLoadI16U // (addr)             → i32 (zero-extended halfword)
	OpLoadI16S // (addr)             → i32 (sign-extended halfword)
	OpStoreI16 // (addr, value)      → ()  (writes the low halfword)

	// Heap allocation: pop a size in bytes and push the base pointer
	// the bump allocator returns.
	OpAlloc // (size i32)         → i32 (ptr)

	// String runtime calls.
	OpStrEq     // (a-ptr, b-ptr)   → i32
	OpStrConcat // (a-ptr, b-ptr)   → i32 (new ptr)
	// OpStrLen reads the length of a string. Today every backend
	// emits an `[ptr - 4]` load (4-byte little-endian length prefix);
	// the dedicated op gives small-string-optimisation work a single
	// seam to change instead of hunting down ~20 open-coded length
	// reads scattered across the runtime helpers. Pops a string
	// pointer, pushes its i32 length.
	OpStrLen // (str-ptr) → i32

	// OpEnumSentinel pushes the address of a shared static
	// 4-byte sentinel containing the i32 tag value carried in
	// the op's I32 immediate. Emitted by emitEnumNew for any
	// payloadless variant — the sentinel's byte at offset 0
	// is the tag, so every existing match / try site reads it
	// with the same `[ptr + 0]` load it used for a heap-
	// allocated `[tag=N]` box. Zero-alloc construction for
	// Option.None (tag=1), IoError.Interrupted / .Unsupported,
	// JsonValue.JNull, user enums, etc. First step toward
	// register-based Option/Result returns; future PRs extend
	// the encoding to payloaded variants.
	OpEnumSentinel // (I32: tag value) → i64 (sentinel ptr)

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

	// OpCallClosureDirect is the defunctionalised form of an
	// OpCallIndirect whose receiver slot was provably mono-
	// morphic (single `MakeClosure` flow source). The caller
	// has already pushed (args..., env_ptr) onto the stack;
	// emit issues a direct `call $name` to the hoisted target
	// without the function-table indirection or the
	// `i32.const 0` env-stub that OpCallDirect adds when the
	// callee is in the closure table.
	OpCallClosureDirect // (args..., env_ptr) → result | ()

	OpDrop       // (T)               → ()
	OpReturn     // (T)               → unwinds the function
	OpReturnVoid // ()                → unwinds the function

	// Register-based Result[T, E] / Option[T] returns. Today
	// every Option/Result returned from a function lives on the
	// heap as a `{tag:i32 @0, payload @8}` box; allocating +
	// loading the tag back at the match site is the bulk of the
	// per-call overhead for short-lived fallible calls.
	//
	// The pair-form lowering returns the (tag, payload) pair on
	// the operand stack instead — multi-value `(result tag_t
	// payload_t)` on wasm, rax + rdx on x86-64 SysV, x0 + x1 on
	// AAPCS64. Heap-boxed Option/Result still exists for storing
	// in struct fields / Map values / state slots; the optimisation
	// fires only when the value flows call-return-style.
	//
	// First-pass IR ops:
	//   OpMakeSomeI32 — (i32 payload)  → (tag=0, payload)
	//   OpMakeNoneI32 — ()             → (tag=1, payload=0)
	//   OpMakeOkI32   — (i32 payload)  → (tag=0, payload)
	//   OpMakeErrI32  — (i32 payload)  → (tag=1, payload)
	//   OpReturnPair  — (tag, payload) → unwinds the function
	//
	// Scoped to i32 payloads for the first PR — covers
	// `Option[i32]` / `Result[i32, i32]` and pointer-typed
	// payloads (string/Map/T[]/struct) that already live in 4-
	// or 8-byte slots. Wider payloads (i64 / f64) follow in a
	// future PR alongside the per-instantiation calling-
	// convention work.
	OpMakeSomeI32 // (i32 payload) → (i32 tag=0, i32 payload)
	OpMakeNoneI32 // ()            → (i32 tag=1, i32 payload=0)
	OpMakeOkI32   // (i32 payload) → (i32 tag=0, i32 payload)
	OpMakeErrI32  // (i32 payload) → (i32 tag=1, i32 payload)
	OpReturnPair  // (tag, payload)→ unwinds the function

	// OpCallDirectPair invokes a pair-returning function. Same
	// Str/I32 conventions as OpCallDirect (target name + arg
	// count), but the callee returns (tag, payload) — two i32
	// values pushed onto the operand stack. Used by AST→IR when
	// the caller knows the callee was lowered with OpReturnPair.
	OpCallDirectPair // (args...) → (i32 tag, i32 payload)

	// OpMatchTag pops a value-position scrutinee from the
	// operand stack and pushes its i32 variant tag. Today the
	// backend handlers lower it to a heap-pointer `[ptr+0]`
	// load (same byte-for-byte behaviour as an OpLoad at
	// offset 0), keeping every existing match / if-let /
	// let-else dispatch site working unchanged. Once the
	// pair-form caller-side rewrite lands (step 4 of the
	// Option/Result arc), OpMatchTag will be the abstraction
	// point that hides the difference between "scrutinee is
	// a heap pointer, deref tag@0" and "scrutinee is a
	// (tag, payload) pair, the tag is already in a register".
	OpMatchTag // (ptr) → i32 tag

	// Closure conversion. Hoisted local functions read captured outer
	// variables through a synthetic `__env` parameter (an i32 pointer
	// to a heap block where each capture sits at a fixed byte offset).
	// At the original def site the AST carries a *MakeClosure node that
	// allocates the env, packs current capture values into it, allocates
	// an 8-byte closure pair `{fn_idx, env_ptr}`, and yields the closure
	// pointer.
	OpMakeClosure // (cap_0 ... cap_{n-1}) → i32 (closure ptr)

	// OpMakeEnv is the defunctionalised-closure form of
	// OpMakeClosure. Allocates and populates only the env
	// block (skipping the 8-byte {fn_idx, env_ptr} pair),
	// pushing env_ptr directly. Synthesised by the
	// elide-closure-pair pass when every reader of the
	// closure slot ended up as a defunctionalised
	// OpCallClosureDirect — the pair's fn_idx field
	// becomes dead, the pair allocation goes away with it.
	OpMakeEnv // (cap_0 ... cap_{n-1}) → i32 (env ptr or 0)
)

// BlockType describes the type a block / loop / if leaves on the stack
// when control falls off its end normally. OpBlock, OpLoop, and OpIf
// stash the block type in their Op.I32 field.
const (
	BlockTypeVoid       int32 = 0
	BlockTypeI32        int32 = 1
	BlockTypeF32        int32 = 2
	BlockTypeI64        int32 = 3
	BlockTypeF64        int32 = 4
	BlockTypeStringPair int32 = 5
)

// blockTypeName returns a short mnemonic for use in formatted output.
func blockTypeName(bt int32) string {
	switch bt {
	case BlockTypeI32:
		return "i32"
	case BlockTypeStringPair:
		return "i32 i32"
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
	case OpFConvertI32:
		return "convert.i32"
	case OpFConvertI64:
		return "convert.i64"
	case OpITruncF32:
		return "trunc.f32"
	case OpITruncF64:
		return "trunc.f64"
	case OpReinterpretI32F32:
		return "i32.reinterpret_f32"
	case OpReinterpretF32I32:
		return "f32.reinterpret_i32"
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
	case OpStoreI16:
		return "store_i16"
	case OpLoadI8S:
		return "load_i8_s"
	case OpLoadI16U:
		return "load_i16_u"
	case OpLoadI16S:
		return "load_i16_s"
	case OpAlloc:
		return "alloc"
	case OpStrEq:
		return "str.eq"
	case OpStrConcat:
		return "str.concat"
	case OpStrLen:
		return "str.len"
	case OpEnumSentinel:
		return "enum.sentinel"
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
	case OpCallClosureDirect:
		return "call_closure_direct"
	case OpDrop:
		return "drop"
	case OpReturn:
		return "return"
	case OpReturnVoid:
		return "return_void"
	case OpReturnPair:
		return "return_pair"
	case OpMakeSomeI32:
		return "make_some.i32"
	case OpMakeNoneI32:
		return "make_none.i32"
	case OpMakeOkI32:
		return "make_ok.i32"
	case OpMakeErrI32:
		return "make_err.i32"
	case OpCallDirectPair:
		return "call_direct_pair"
	case OpMatchTag:
		return "match_tag"
	case OpMakeClosure:
		return "make_closure"
	case OpMakeEnv:
		return "make_env"
	}
	return "<invalid>"
}

// WidthPtr is the sentinel `Op.Width` value meaning "pointer-
// width"; each backend resolves it to its native heap-pointer
// size (4 on wasm32, 8 on arm64). Used to size OpStore /
// OpLoad of heap-pointer values without forcing the IR layer
// to know the target. -1 keeps the existing 0 = i32 / 64 =
// i64 encoding intact.
const WidthPtr = -1

// WidthString is the sentinel `Op.Width` value meaning "two-
// word string slot" — `(data, len)` packed into two consecutive
// pointer-width slots. OpStore / OpLoad with this width fan out
// to two stores / loads at offset +0 and +ptrW. Used for struct
// fields, variant payloads, tuple elements, and array elements
// of string type so the heap layout matches the operand-stack
// shape every other string consumer expects. -2 stays clear of
// 0 (i32 default), 64 (i64), and -1 (WidthPtr).
const WidthString = -2

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
	// follow-up. `WidthPtr` (-1) is a backend-resolved sentinel
	// meaning "pointer-width" — wasm interprets it as 4-byte
	// (i32.store / i32.load); arm64 as 8-byte (str x / ldr x).
	// Used for OpStore / OpLoad of heap-pointer-typed values
	// (string / array / struct / enum / closure) so the high
	// 32 bits of arm64-darwin's >4 GiB heap addresses survive.
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
	// ArgTypes is set on OpCallDirect / OpCallDirectPair to the
	// static parameter types of the callee, in the same order the
	// IR pushes them. Backends consume it to compute operand-stack
	// slot counts under the two-word string ABI (each string arg
	// occupies 2 slots → 2 registers). The lowering pass populates
	// it from FuncSigs at the central call-emission site and from
	// the surrounding code's static knowledge at synthesised emit
	// sites (e.g. `__str_slice` knows it takes (string, i32, i32)).
	// Nil is allowed for callees that take no string args — the
	// backend then treats every arg as 1 slot.
	ArgTypes []ast.Type
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
	// PairForm is the set of function names lowered with the
	// register-based (tag, payload) return ABI. Populated once
	// per program by findPairFormFuncs during `LowerWith`.
	// Backends consult this to decide:
	//   - what signature to emit for the function (e.g. wasm
	//     `(result i32 i32)` instead of `(result i32)` once
	//     the multi-value return ABI is wired), and
	//   - whether a call site needs to rebox / extract the
	//     pair (consumer-side scrutinee vs generic context).
	// Nil/missing entries mean heap-form (default ABI).
	PairForm map[string]bool
	// PtrW is the target's pointer width in bytes (4 on wasm32,
	// 8 on natives). Recorded once by `LowerWith` so post-Lower
	// passes (Inline / FlattenBranches / codegen) don't have to
	// re-derive target-awareness from configuration.
	PtrW int
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
	case OpCallDirect, OpCallClosureDirect, OpCallDirectPair:
		return fmt.Sprintf("%s %s argc=%d", op.Kind, op.Str, op.I32)
	case OpCallIndirect:
		return fmt.Sprintf("%s argc=%d", op.Kind, op.I32)
	case OpMakeClosure, OpMakeEnv:
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
	return LowerWith(prog, info, 4)
}

// LowerWith is the pointer-width-aware variant. `ptrW` is 4 on
// wasm32 and 8 on arm64; it sizes pointer-typed enum payloads,
// struct fields, array elements, and closure captures so heap
// addresses survive arm64-darwin's >= 4 GiB heap.
func LowerWith(prog *ast.Program, info *checker.Info, ptrW int) (*Program, error) {
	// Rename shadowed local variables so each Var declaration
	// in a function carries a name that's globally unique
	// within the function. The IR's per-name `b.locals` slot
	// lookup is otherwise blind to scoping — two nested
	// `var x: i64` declarations would collapse onto a single
	// slot and the outer reads would silently see the inner
	// store's value. Runs before closureconv so the closure
	// pass sees post-rename names everywhere.
	shadowrename.Rename(prog, info)
	if err := closureconv.ConvertWith(prog, info, ptrW); err != nil {
		return nil, err
	}
	// Identify pair-form-eligible functions up-front. A function
	// returns its `Option[i32]` value via the (tag, payload) pair
	// ABI instead of a heap-allocated `{tag, payload}` box when
	// every `return` in its body produces `Some(EXPR)` or `None`
	// directly. Captured here so callers know to consume two
	// stack values from a OpCallDirectPair instead of one.
	pairForm := findPairFormFuncs(prog, info, ptrW)
	out := &Program{PairForm: pairForm, PtrW: ptrW}
	for _, fn := range prog.Funcs {
		f, err := lowerFunc(fn, info, ptrW, pairForm)
		if err != nil {
			return nil, err
		}
		out.Funcs = append(out.Funcs, f)
	}
	return out, nil
}

// findPairFormFuncs scans every top-level function in prog and
// returns the names of those eligible for the register-based
// (tag, payload) return ABI. Eligibility today is conservative:
//
//   - Return type is `Option[T]` or `Result[T, E]` where every
//     type argument is i32-stack-shaped (i32 / u32 / boolean —
//     see isPairFormPayloadShape; pointer-shaped values like string /
//     struct / T[] are deliberately excluded today).
//   - Every `return` statement in the body (including those
//     inside if / for / while / match arms) returns one of:
//       (a) a `Some(EXPR)` / `None` literal (for Option) or
//           `Ok(EXPR)` / `Err(EXPR)` literal (for Result)
//           directly,
//       (b) a direct call `helper()` where `helper` is itself
//           in the pair-form set — the call's heap-box result
//           flows out unchanged, and `helper`'s callers got
//           the pair-form treatment too, or
//       (c) a ternary `cond ? Then : Else` whose Then and Else
//           are each themselves an (a) / (b) / (c) shape
//           (recursive). Each arm constructs a heap-box pair
//           independently; consumers still apply
//           `OpCallDirectPair` to the join.
//
// (b) is determined by fixpoint iteration: each pass adds
// functions whose returns now resolve under (a) / (b) / (c)
// using the previous pass's set. Converges in linear passes
// over the function graph.
//
// Tightening the analysis further (e.g. to accept
// pointer-shaped payloads) is tracked as a follow-up.
func findPairFormFuncs(prog *ast.Program, info *checker.Info, ptrW int) map[string]bool {
	out := map[string]bool{}
	for {
		grew := false
		for _, fn := range prog.Funcs {
			if out[fn.Name] {
				continue
			}
			if !isPairFormEligible(fn, info, ptrW, out) {
				continue
			}
			out[fn.Name] = true
			grew = true
		}
		if !grew {
			break
		}
	}
	return out
}

// isPairFormEligible returns true if fn can be lowered with
// the register-based pair-form return ABI. See findPairFormFuncs
// for the eligibility rules; `pairForm` is the previous
// fixpoint pass's known-pair-form set, used to authorise
// tail-call returns into it.
//
// Supported return-type shapes:
//   - `Option[T]` where T is i32-stack-shaped — body must
//     produce only `Some(EXPR)` / `None` literals, tail calls
//     to other pair-form `Option[T]` returners, or ternaries
//     `cond ? A : B` whose arms are themselves one of these
//     shapes.
//   - `Result[T, E]` where T and E are both i32-stack-shaped —
//     body must produce only `Ok(EXPR)` / `Err(EXPR)` literals,
//     tail calls to other pair-form `Result[T, E]` returners,
//     or ternaries `cond ? A : B` whose arms are themselves
//     one of these shapes.
//
// Other shapes (pointer-typed payloads, wider numeric
// payloads, mixed-shape Result) require either the native
// pair-form lowering to support wider slots or the per-
// instantiation rebox machinery — both tracked as follow-ups.
func isPairFormEligible(fn *ast.FuncDecl, info *checker.Info, ptrW int, pairForm map[string]bool) bool {
	if fn.IsLocal {
		// Hoisted closures take an extra __env i32 param and
		// have a fixed-shape body; pair-form lowering for them
		// is a future PR.
		return false
	}
	// `__map_get_impl` is the call-target codegen's alias-
	// rewrite path uses for `__method_Map_get`. The IR-side
	// pair-form check at the call site keys off the user-
	// visible name (`__method_Map_get`), which isn't a real
	// FuncDecl in pairForm — so OpCallDirect (heap-box ABI)
	// is emitted regardless. If the called function uses
	// pair-form, the caller drops the payload (x1 / rdx)
	// and reads garbage at the heap-box pointer dereference,
	// segfaulting at runtime on natives. Force the Map
	// runtime's Option-returning helpers to the heap-box
	// path so the call-site ABI and the function's actual
	// return shape agree.
	if fn.Name == "__map_get_impl" {
		return false
	}
	enumT, ok := fn.ReturnType.(ast.EnumType)
	if !ok {
		return false
	}
	variantNames := pairFormVariantsFor(enumT, info, ptrW)
	if variantNames == nil {
		return false
	}
	if fn.Body == nil {
		return false
	}
	return allReturnsArePairFormShape(fn.Body, variantNames, pairForm)
}

// pairFormVariantsFor returns the set of valid variant
// constructor names for fn's return type when the type is
// eligible for pair-form lowering. Returns nil if the type
// doesn't match a known shape or if any payload type isn't
// pair-form-shaped on this target (see
// `isPairFormPayloadShape` — i32-fitting on every backend,
// pointer-shaped values only on wasm32 today).
//
// Built-in `Option[T]` and `Result[T, E]` are recognised by
// name (so the variant names are sourced from the package-
// level `optionVariants` / `resultVariants` constants). Any
// user-declared enum is also eligible if it matches the
// canonical shape:
//   - exactly two variants,
//   - variant 0 carries exactly one payload that's
//     pair-form-shaped (after substituting the enum's
//     type-parameter bindings, if any), and
//   - variant 1 is nullary.
//
// The canonical-order requirement is load-bearing: the IR's
// pair-form construction reuses `OpMakeSomeI32` (tag=0) for
// the payload-carrying variant and `OpMakeNoneI32` (tag=1)
// for the nullary one, and the consumer-side tag dispatch
// reads the variant's `varIdx` from the enum decl, so the
// two must agree.
func pairFormVariantsFor(t ast.EnumType, info *checker.Info, ptrW int) map[string]bool {
	switch t.Name {
	case "Option":
		if len(t.Args) != 1 || !isPairFormPayloadShape(t.Args[0], ptrW) {
			return nil
		}
		return optionVariants
	case "Result":
		if len(t.Args) != 2 || !isPairFormPayloadShape(t.Args[0], ptrW) || !isPairFormPayloadShape(t.Args[1], ptrW) {
			return nil
		}
		// Mixed-width variants (e.g. `Result[i32, MyStruct]`)
		// break the heap-box rebox the call-site uses to
		// integrate pair-form returns with non-pair-form
		// consumers: the maker stores payload at the variant-
		// specific offset (+4 for i32, +8 for pointer-shape),
		// the rebox picks a single offset, and the match-arm
		// reader reads at the variant-specific offset. With
		// same-width variants those three line up; with mixed
		// widths they don't. Fall back to the heap-box path —
		// `Result[i32, MyStruct]` then flows as a single i32
		// pointer and the match-arm consumer dispatches on
		// `[ptr+0]` tag + reads the payload at its declared
		// offset. Slower (one alloc per construction) but
		// correct end-to-end.
		w0 := pairPayloadWidth(t.Args[0])
		w1 := pairPayloadWidth(t.Args[1])
		if w0 != w1 {
			return nil
		}
		return resultVariants
	}
	if info == nil {
		return nil
	}
	ed := info.Enums[t.Name]
	if ed == nil || len(ed.Variants) != 2 {
		return nil
	}
	v0, v1 := ed.Variants[0], ed.Variants[1]
	if len(v0.Payloads) != 1 || len(v1.Payloads) != 0 {
		return nil
	}
	payloadType := resolveTypeParam(v0.Payloads[0], ed.TypeParams, t.Args)
	if !isPairFormPayloadShape(payloadType, ptrW) {
		return nil
	}
	return map[string]bool{v0.Name: true, v1.Name: true}
}

// resolveTypeParam substitutes a ParamType reference with its
// binding from `args`, looking up the index by name in `params`.
// Used when checking a user enum's variant payload shape: a
// generic enum like `MyOption[T]` declares its payload as
// `ParamType{Name: "T"}`, and the eligibility check needs the
// concrete type (e.g. `i32`) to decide `isPairFormPayloadShape`.
// Non-ParamType inputs and unbound names fall through unchanged.
func resolveTypeParam(t ast.Type, params []string, args []ast.Type) ast.Type {
	pt, ok := t.(ast.ParamType)
	if !ok {
		return t
	}
	if len(params) != len(args) {
		return t
	}
	for i, name := range params {
		if name == pt.Name {
			return args[i]
		}
	}
	return t
}

// Shared variant-name sets returned by pairFormVariantsFor.
// Treat as read-only — they're handed out to every pair-form
// eligibility check.
var (
	optionVariants = map[string]bool{"Some": true, "None": true}
	resultVariants = map[string]bool{"Ok": true, "Err": true}
)

// isPairFormPayloadShape reports whether t is a payload type
// the pair-form ABI can carry. Narrow numeric (i32 / u32 /
// boolean) shapes fit a single 4-byte operand-stack slot on
// every target; pointer-shaped values (string / struct / T[]
// / [T] / tuple / usize) also fit because the maker / rebox
// emit paths size their stores per the payload width
// (`pairPayloadWidth`) — 4-byte on wasm32, 8-byte on natives.
//
// The `ptrW` parameter is unused today but kept on the
// signature so future tightenings (e.g. f64 / i64 payloads,
// which still need wider slots even on wasm32) have an
// obvious place to dispatch.
func isPairFormPayloadShape(t ast.Type, ptrW int) bool {
	switch x := t.(type) {
	case ast.NumberType:
		w := x.NormalWidth()
		if w >= 0 && w <= 32 && !x.IsPointerWidth() {
			return true
		}
		if x.IsPointerWidth() {
			return true
		}
		return false
	case ast.BoolType:
		return true
	case ast.StringType:
		// Two-word ABI on wasm32: strings are `(data, len)` —
		// two operand-stack slots, not one. The pair-form
		// return shape carries only ONE i32 payload slot per
		// variant, so a string payload can't fit. Reject so
		// the heap-box fallback (single i32 return holding a
		// `(tag, payload)` cell pointer) handles it. Natives
		// still use the LSB-tagged single-pointer slot — keep
		// them eligible until the native two-word flip extends
		// `useTwoWordStrings`.
		return !useTwoWordStrings(ptrW)
	case ast.ArrayType, ast.SliceType, ast.StructType, ast.TupleType:
		return true
	}
	return false
}


// allReturnsArePairFormShape walks every Return statement
// reachable from s and reports whether each one is one of
// the pair-form-compatible shapes:
//
//   - A named variant-constructor literal — `Some(EXPR)` /
//     `None` / `Ok(EXPR)` / `Err(EXPR)` (filtered by `names`).
//   - A tail call to a function already in `pairForm` (the
//     callee returns a heap-box pair the caller can flow
//     through unchanged, and *its* callers still get the
//     `OpCallDirectPair` consumer-side optimization).
//
// Stops at the first non-conforming return.
func allReturnsArePairFormShape(s ast.Stmt, names map[string]bool, pairForm map[string]bool) bool {
	if s == nil {
		return true
	}
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			if !allReturnsArePairFormShape(st, names, pairForm) {
				return false
			}
		}
		return true
	case *ast.If:
		return allReturnsArePairFormShape(x.Then, names, pairForm) && allReturnsArePairFormShape(x.Else, names, pairForm)
	case *ast.IfLet:
		return allReturnsArePairFormShape(x.Then, names, pairForm) && allReturnsArePairFormShape(x.Else, names, pairForm)
	case *ast.While:
		return allReturnsArePairFormShape(x.Body, names, pairForm)
	case *ast.For:
		return allReturnsArePairFormShape(x.Body, names, pairForm)
	case *ast.Switch:
		for _, c := range x.Cases {
			if !allReturnsArePairFormShape(c.Body, names, pairForm) {
				return false
			}
		}
		return x.Default == nil || allReturnsArePairFormShape(x.Default, names, pairForm)
	case *ast.Match:
		for _, arm := range x.Arms {
			if !allReturnsArePairFormShape(arm.Body, names, pairForm) {
				return false
			}
		}
		return true
	case *ast.LetElse:
		return allReturnsArePairFormShape(x.Else, names, pairForm)
	case *ast.Arena:
		return allReturnsArePairFormShape(x.Body, names, pairForm)
	case *ast.Return:
		return isPairFormReturnExpr(x.Value, names, pairForm)
	}
	return true
}

// isPairFormReturnExpr reports whether e (the right-hand side
// of a `return` statement) is one of the shapes that lets the
// surrounding function stay pair-form-eligible. The accepted
// shapes are:
//
//   - A named variant-constructor literal — `Some(EXPR)` /
//     `None` / `Ok(EXPR)` / `Err(EXPR)` (filtered by `names`).
//   - A direct call to a function already in `pairForm` —
//     the callee returns a heap-box pair that the caller flows
//     through unchanged.
//   - A ternary `cond ? Then : Else` whose Then and Else are
//     each themselves pair-form return shapes (recursive).
//
// All three shapes leave the caller emitting a heap pointer
// at the i32 return position, so consumers can still apply the
// `OpCallDirectPair` optimization at the call site.
func isPairFormReturnExpr(e ast.Expr, names map[string]bool, pairForm map[string]bool) bool {
	if isVariantLiteralExpr(e, names) {
		return true
	}
	if isPairFormTailCall(e, pairForm) {
		return true
	}
	if ie, ok := e.(*ast.IfExpr); ok {
		return isPairFormReturnExpr(ie.Then, names, pairForm) &&
			isPairFormReturnExpr(ie.Else, names, pairForm)
	}
	return false
}

// isPairFormTailCall reports whether e is a direct Call to a
// top-level function already known to be pair-form. A pair-
// form callee returns a heap-box (the function-side ABI stays
// heap-box today — see the OpReturnPair codegen path), so a
// caller's `return helper()` just lets that heap pointer flow
// through to its own caller, which is free to apply the
// `OpCallDirectPair` consumer-side optimization on the outer
// call.
func isPairFormTailCall(e ast.Expr, pairForm map[string]bool) bool {
	c, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	return pairForm[id.Name]
}

// isPairFormScrutinee reports whether e is a direct Call to
// a pair-form function (per b.pairForm). The match-style
// dispatch sites (IfLet / Match / LetElse) check this to
// decide whether to consume the call result as (tag, payload)
// directly (zero-alloc) or to fall back to the heap-box rebox
// path the generic Call lowering uses.
// pairFormPayloadType returns the payload type carried by the
// named variant on this builder's enclosing pair-form function.
// Used to size `Op.Width` on the maker ops so backends can pick
// the right alloc / store widths for pointer-shaped payloads.
// Returns nil for payloadless variants and for the unrecognised
// case (caller still emits the appropriate maker op; nil maps
// to Width=0 via `pairPayloadWidth`).
func (b *builder) pairFormPayloadType(variantName string) ast.Type {
	if b.fn == nil {
		return nil
	}
	enumT, ok := b.fn.ReturnType.(ast.EnumType)
	if !ok {
		return nil
	}
	switch enumT.Name {
	case "Option":
		if variantName == "Some" && len(enumT.Args) >= 1 {
			return enumT.Args[0]
		}
	case "Result":
		switch variantName {
		case "Ok":
			if len(enumT.Args) >= 1 {
				return enumT.Args[0]
			}
		case "Err":
			if len(enumT.Args) >= 2 {
				return enumT.Args[1]
			}
		}
	default:
		if b.info == nil {
			return nil
		}
		ed := b.info.Enums[enumT.Name]
		if ed == nil {
			return nil
		}
		for _, v := range ed.Variants {
			if v.Name == variantName && len(v.Payloads) == 1 {
				return resolveTypeParam(v.Payloads[0], ed.TypeParams, enumT.Args)
			}
		}
	}
	return nil
}

func (b *builder) isPairFormScrutinee(e ast.Expr) bool {
	c, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	if _, isLocal := b.locals[id.Name]; isLocal {
		return false
	}
	return b.pairForm[id.Name]
}

// pairFormVariantOf inspects a pair-form-variant literal AST
// (`Some(EXPR)` / `None` / `Ok(EXPR)` / `Err(EXPR)`) and
// returns the variant name + payload expression (nil for the
// payloadless `None`). Caller is responsible for confirming the
// literal is a recognised variant shape via
// `isVariantLiteralExpr` first — the two helpers share the
// arity expectations (call form ⇒ exactly one arg; ident form
// ⇒ payloadless variant).
func pairFormVariantOf(e ast.Expr) (name string, payload ast.Expr) {
	if c, ok := e.(*ast.Call); ok {
		if id, ok := c.Callee.(*ast.Ident); ok && len(c.Args) == 1 {
			return id.Name, c.Args[0]
		}
		return "", nil
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, nil
	}
	return "", nil
}

// isVariantLiteralExpr returns true if e is the AST shape of
// one of the named variant constructors (`Some(EXPR)`,
// `None`, `Ok(EXPR)`, `Err(EXPR)`). Used by the pair-form
// eligibility analysis to confirm a return statement
// constructs an Option/Result variant directly rather than
// (for example) forwarding the result of another function
// call. Payloadless variants parse as a bare Ident; payload-
// carrying ones must have exactly one argument (the checker
// rejects other arities upstream, but the guard is defensive —
// it pins the invariant that `pairFormVariantOf` can decode
// whatever this accepts).
func isVariantLiteralExpr(e ast.Expr, names map[string]bool) bool {
	c, ok := e.(*ast.Call)
	if !ok {
		// Bare payloadless variant (`None`) parses as an Ident
		// the checker resolves to the variant constructor.
		if id, ok := e.(*ast.Ident); ok {
			return names[id.Name]
		}
		return false
	}
	if len(c.Args) != 1 {
		return false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	return names[id.Name]
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
	// defers is the in-source-order list of every Defer
	// statement in the function body, collected by a pre-walk
	// before any IR emission. deferSlots holds the synthetic
	// "active" flag local for each defer; lowering the Defer
	// statement sets the flag to 1, and the cleanup blocks
	// emitted before each return run the deferred expression
	// only when the flag is set.
	defers     []*ast.Defer
	deferSlots []int32
	// ptrW is the target's heap-pointer width in bytes — 4 on
	// wasm32, 8 on arm64. Sizes enum payload slots, struct
	// field offsets, array element strides, and closure
	// captures so pointer-typed values survive arm64-darwin's
	// >= 4 GiB heap.
	ptrW int
	// pairForm maps function name → whether that function is
	// lowered with the pair-form (tag, payload) return ABI.
	// Populated once per program by findPairFormFuncs, shared
	// across every builder. Callers consult it to decide
	// between OpCallDirect and OpCallDirectPair.
	pairForm map[string]bool
	// thisIsPair is `pairForm[fn.Name]` cached on the builder
	// for the common path in Return / Some / None / Ok / Err
	// lowering. `pairVariants` is the variant-name set for the
	// function's pair-form return type — `{"Some", "None"}` for
	// `Option[T]`, `{"Ok", "Err"}` for `Result[T, E]`. nil when
	// thisIsPair is false.
	thisIsPair   bool
	pairVariants map[string]bool
	// suppressPairRebox tells the Call lowering to skip the
	// `emitRepackPairAsHeapBox` step after OpCallDirectPair —
	// the caller (IfLet / Match / LetElse scrutinee path)
	// will consume (tag, payload) directly. Default false
	// (rebox to heap so existing consumers see the legacy
	// shape).
	suppressPairRebox bool
}

// twoWordStrings reports whether the current target carries
// strings on the operand stack as a `(data, len)` two-word
// pair (vs the legacy single LSB-tagged pointer slot). Today
// that's true exactly for wasm32 (`ptrW == 4`). The arm64
// native flip (in progress; see `docs/SSO-NATIVE-FLIP-STATUS.md`)
// will extend this to arm64; x86_64 follows in a separate arc.
// Routes through the canonical `ast.UseTwoWordStrings` so
// the eventual flip happens in one place and propagates to
// every consumer.
func (b *builder) twoWordStrings() bool {
	return ast.UseTwoWordStrings(b.ptrW)
}

// collectDefers walks `s` recursively and appends every
// `*ast.Defer` it finds (in source-declaration order) to
// `out`. Function-bodies of nested local FuncDecls are NOT
// traversed — those have their own defer scope handled by
// lowerFunc when they're lowered separately.
func collectDefers(s ast.Stmt, out *[]*ast.Defer) {
	if s == nil {
		return
	}
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			collectDefers(st, out)
		}
	case *ast.If:
		collectDefers(x.Then, out)
		collectDefers(x.Else, out)
	case *ast.IfLet:
		collectDefers(x.Then, out)
		collectDefers(x.Else, out)
	case *ast.LetElse:
		collectDefers(x.Else, out)
	case *ast.While:
		collectDefers(x.Body, out)
	case *ast.For:
		collectDefers(x.Init, out)
		collectDefers(x.Step, out)
		collectDefers(x.Body, out)
	case *ast.Switch:
		for _, k := range x.Cases {
			collectDefers(k.Body, out)
		}
		// x.Default is `*ast.Block` (concrete pointer); when
		// the user wrote no default it's a typed-nil pointer.
		// Boxing into the Stmt interface makes `s == nil` at
		// the top of this function FALSE (interface has a
		// non-nil type-tag), and we'd crash on x.Stmts in the
		// *ast.Block case below. Guard explicitly.
		if x.Default != nil {
			collectDefers(x.Default, out)
		}
	case *ast.Match:
		for _, arm := range x.Arms {
			collectDefers(arm.Body, out)
		}
	case *ast.Defer:
		*out = append(*out, x)
	case *ast.Arena:
		// Defers inside an arena block still register with
		// the enclosing function — arena's cursor snap is
		// independent of defer's per-function cleanup.
		collectDefers(x.Body, out)
	}
}

// emitDeferCleanup walks the registered defers in reverse
// source order and emits `if active[i] { <expr>; }` for each.
// Called from `Return` lowering and from the implicit-return
// path at the end of `lowerFunc`.
func (b *builder) emitDeferCleanup() error {
	for i := len(b.defers) - 1; i >= 0; i-- {
		b.emit(Op{Kind: OpLoadLocal, I32: b.deferSlots[i]})
		b.openIf(BlockTypeVoid)
		// Evaluate the deferred expression. Drop the result
		// — defer's expression value is unused.
		if err := b.expr(b.defers[i].Expr); err != nil {
			return err
		}
		if exprLeavesValue(b.defers[i].Expr, b.info) {
			b.emit(Op{Kind: OpDrop})
		}
		b.closeScope()
	}
	return nil
}

func lowerFunc(fn *ast.FuncDecl, info *checker.Info, ptrW int, pairForm map[string]bool) (*Func, error) {
	out := &Func{
		Name:       fn.Name,
		Params:     fn.Params,
		Locals:     info.Locals[fn],
		ReturnType: fn.ReturnType,
	}
	b := &builder{
		info:        info,
		fn:          fn,
		out:         out,
		locals:      map[string]int32{},
		scratchType: map[int32]ast.Type{},
		ptrW:        ptrW,
		pairForm:    pairForm,
		thisIsPair:  pairForm[fn.Name],
	}
	if b.thisIsPair {
		if enumT, ok := fn.ReturnType.(ast.EnumType); ok {
			b.pairVariants = pairFormVariantsFor(enumT, info, ptrW)
		}
	}
	for i, p := range fn.Params {
		b.locals[p.Name] = int32(i)
	}
	for i, v := range info.Locals[fn] {
		b.locals[v.Name] = int32(len(fn.Params) + i)
	}
	b.nextSlot = int32(len(fn.Params) + len(info.Locals[fn]))
	// Pre-walk the function body to find every Defer
	// statement. Each gets an "active" flag local: 0 by
	// default; the IR sets it to 1 when the Defer statement
	// is reached at runtime, and the per-exit cleanup block
	// runs the deferred expression only when the flag is set.
	// That makes a defer reached inside a conditional a
	// no-op when the conditional didn't fire.
	collectDefers(fn.Body, &b.defers)
	for i := range b.defers {
		slot := b.allocSlot()
		b.deferSlots = append(b.deferSlots, slot)
		b.locals[fmt.Sprintf("__defer_%d_active", i)] = slot
	}
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
	// downstream consumer doesn't have to check. Run any
	// registered defers first — same shape as the explicit
	// Return path.
	if needsImplicitReturn(out.Ops) {
		if err := b.emitDeferCleanup(); err != nil {
			return nil, err
		}
		switch {
		case isVoid(fn.ReturnType):
			b.emit(Op{Kind: OpReturnVoid})
		case b.thisIsPair:
			// Pair-form fns return (tag, payload). The
			// type-checker should reject programs that
			// actually reach this fallthrough, but the IR
			// still needs to satisfy the backend's stack
			// shape — emit zero pair so wasm's typed-stack
			// validation passes.
			b.emit(Op{Kind: OpConstI32, I32: 0})
			b.emit(Op{Kind: OpConstI32, I32: 0})
			b.emit(Op{Kind: OpReturnPair})
		case isFloat(fn.ReturnType):
			b.emit(Op{Kind: OpConstF32, F32: 0})
			b.emit(Op{Kind: OpReturn})
		default:
			// String-typed return on wasm32 fans to two i32
			// slots `(data, len)`; emit zeros for both so the
			// trailing OpReturn pops the right shape. Natives
			// stay on the single-pointer-slot LSB-tagged ABI
			// for now.
			if _, isString := fn.ReturnType.(ast.StringType); isString && b.twoWordStrings() {
				b.emit(Op{Kind: OpConstI32, I32: 0})
				b.emit(Op{Kind: OpConstI32, I32: 0})
				b.emit(Op{Kind: OpReturn})
			} else {
				b.emit(Op{Kind: OpConstI32, I32: 0})
				b.emit(Op{Kind: OpReturn})
			}
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
// emitRepackPairAsHeapBox consumes (tag, payload) from the
// operand stack and synthesises a heap-allocated box matching
// `payloadLayout(Option[T])` / `payloadLayout(Result[T, E])`'s
// shape: tag at +0, payload at +4 (i32 payload) or +8
// (pointer-shape payload — `payloadWidth == WidthPtr` —
// box-size 16 with 8-byte alignment on natives). Result is
// the box pointer on the operand stack. Used at
// OpCallDirectPair sites where the consumer expects the
// heap-form Option/Result (the legacy shape that existing
// match / var-assignment / struct-field code handles).
//
// The match-style scrutinee path in IfLet / Match / LetElse
// sets `suppressPairRebox` so the pair flows straight from
// the call into the dispatch without going through this rebox.
func (b *builder) emitRepackPairAsHeapBox(payloadWidth int) error {
	boxSize := int32(8)
	payloadOff := int32(4)
	storeOp := Op{Kind: OpStore}
	if payloadWidth == WidthPtr {
		// Pointer-shape payloads use the target's pointer width:
		// on natives (ptrW=8), pad to 8-byte alignment → box-size
		// 16, payload at +8 (matches `payloadLayout`'s emit for
		// Option[T] / Result[T, …] when T is pointer-shaped). On
		// wasm32 (ptrW=4), pointer = i32, so the box stays at the
		// 8-byte / +4 i32 layout. Without this branch the wasm
		// path would write payload at +8 (the native layout) but
		// every reader — TryOp's success-path load, Match/IfLet's
		// scrutinee-dispatch read, OpMatchTag's box-shape callers
		// — pulls from the +4 layout, so the read returns 0 and
		// downstream `print(payload)` etc. trap on `[0 - 4]`.
		if b.ptrW == 8 {
			boxSize = 16
			payloadOff = 8
			storeOp = Op{Kind: OpStore, Width: WidthPtr}
		}
	}
	// Stack: [tag, payload] — top is payload. Stash payload in
	// a scratch local so we can alloc + store the box without
	// shuffling values around.
	payloadSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_payload_%d", payloadSlot)] = payloadSlot
	b.emit(Op{Kind: OpStoreLocal, I32: payloadSlot})
	tagSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_tag_%d", tagSlot)] = tagSlot
	b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
	b.emit(Op{Kind: OpConstI32, I32: boxSize})
	b.emit(Op{Kind: OpAlloc})
	boxSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_box_%d", boxSlot)] = boxSlot
	b.emit(Op{Kind: OpStoreLocal, I32: boxSlot})
	// Store tag at box+0 (always 4-byte i32).
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
	b.emit(Op{Kind: OpStore})
	// Store payload at the right offset (4 for i32, 8 for
	// pointer-shape) and width.
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: payloadOff})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
	b.emit(storeOp)
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	return nil
}

// payloadWidthForCalleeReturn derives the pair-form payload
// width for the rebox at an OpCallDirectPair site: read the
// callee's return type (always `Option[T]` / `Result[T, E]` /
// a user pair-form enum at this point — verified by
// `b.pairForm[callee]`), pick which variant carries the
// payload, return `pairPayloadWidth(payloadType)`.
//
// Both variants of `Result[T, E]` may carry different-width
// payloads (e.g. `Result[i32, string]`); the rebox uses the
// WIDER one so either branch fits — the consumer's
// `match`-side payload-load width then handles narrowing per
// arm via `payloadLoadOp`.
func (b *builder) payloadWidthForCalleeReturn(retType ast.Type) int {
	enumT, ok := retType.(ast.EnumType)
	if !ok {
		return 0
	}
	switch enumT.Name {
	case "Option":
		if len(enumT.Args) >= 1 && pairPayloadWidth(enumT.Args[0]) == WidthPtr {
			return WidthPtr
		}
	case "Result":
		for _, a := range enumT.Args {
			if pairPayloadWidth(a) == WidthPtr {
				return WidthPtr
			}
		}
	default:
		if b.info == nil {
			return 0
		}
		ed := b.info.Enums[enumT.Name]
		if ed == nil {
			return 0
		}
		for _, v := range ed.Variants {
			if len(v.Payloads) != 1 {
				continue
			}
			pt := resolveTypeParam(v.Payloads[0], ed.TypeParams, enumT.Args)
			if pairPayloadWidth(pt) == WidthPtr {
				return WidthPtr
			}
		}
	}
	return 0
}

func (b *builder) emitEnumNew(callNode *ast.Call, enumName string, varIdx int, payloadCount int, args []ast.Expr) error {
	// Payloadless variants return a shared static 4-byte
	// `[tag=varIdx]` sentinel instead of allocating a fresh
	// box per construction. Match / try sites still read the
	// tag with the same `[ptr + 0]` load they used for heap-
	// allocated boxes, so this is a constructor-side rewrite
	// only. Catches Option.None, IoError.Interrupted /
	// .Unsupported, JsonValue.JNull, and any user-defined enum
	// variant with no payload. Sentinels are shared across
	// distinct enums with the same tag value — only the tag
	// matters at the match site.
	if payloadCount == 0 {
		b.emit(Op{Kind: OpEnumSentinel, I32: int32(varIdx)})
		return nil
	}
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
	offsets, size := payloadLayout(payloadTypes, payloadCount, b.ptrW)
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__enum_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// Store tag at offset 0.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
	b.emit(Op{Kind: OpStore})
	for i, a := range args {
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
		b.emit(Op{Kind: OpAdd})
		if err := b.expr(a); err != nil {
			return err
		}
		var pt ast.Type
		if i < len(payloadTypes) {
			pt = payloadTypes[i]
		}
		b.emit(payloadStoreOpFor(pt, b.ptrW))
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
func (b *builder) elseBranch() { b.emit(Op{Kind: OpElse}) }
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
		// the else diverges.
		//
		// Pair-form scrutinee fast path mirrors IfLet's: consume
		// (tag, payload) from the operand stack into scratch
		// locals, dispatch on tag local, bind payload from
		// payload local — zero alloc end-to-end.
		_, varIdx, _, ok := b.lookupVariant(n.VariantName)
		if !ok {
			return fmt.Errorf("ir: let-else references unknown variant %q", n.VariantName)
		}
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
		if b.isPairFormScrutinee(n.Source) {
			tagSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__letelse_tag_%d", tagSlot)] = tagSlot
			payloadSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__letelse_pay_%d", payloadSlot)] = payloadSlot
			prev := b.suppressPairRebox
			b.suppressPairRebox = true
			if err := b.expr(n.Source); err != nil {
				return err
			}
			b.suppressPairRebox = prev
			b.emit(Op{Kind: OpStoreLocal, I32: payloadSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
			b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
			b.emit(Op{Kind: OpEq})
			b.openIf(BlockTypeVoid)
			// Pair-form is scoped to Option[i32]: 0 or 1
			// bindings. For 0 (None arm) the bindingSlots
			// loop is empty; for 1 (Some(v)) bind from
			// payloadSlot directly.
			for _, slot := range bindingSlots {
				b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
			}
			b.elseBranch()
			if err := b.stmt(n.Else); err != nil {
				return err
			}
			b.closeScope()
			break
		}
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__letelse_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Source); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpMatchTag})
		b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
		b.emit(Op{Kind: OpEq})
		b.openIf(BlockTypeVoid)
		// Match: load each payload field into its pre-allocated
		// outer-scope slot.
		offsets, _ := payloadLayout(n.BindingTypes, len(bindingSlots), b.ptrW)
		for i, slot := range bindingSlots {
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
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
		// { Else }]`. Two shapes:
		//
		//   - Heap-form scrutinee (legacy default): eval source
		//     to a heap-box pointer; load tag from `[ptr+0]`;
		//     dispatch + bind payload fields by offset.
		//
		//   - Pair-form scrutinee (Option[i32] match-style
		//     consumer fast path): the source is a direct call
		//     to a pair-form function. Suppress the call's
		//     rebox, consume (tag, payload) from the operand
		//     stack into two scratch locals, dispatch on the
		//     tag local, and bind the payload from the payload
		//     local. ZERO heap alloc end-to-end.
		_, varIdx, _, ok := b.lookupVariant(n.VariantName)
		if !ok {
			return fmt.Errorf("ir: if-let references unknown variant %q", n.VariantName)
		}
		if b.isPairFormScrutinee(n.Source) {
			tagSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__iflet_tag_%d", tagSlot)] = tagSlot
			payloadSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__iflet_pay_%d", payloadSlot)] = payloadSlot
			prev := b.suppressPairRebox
			b.suppressPairRebox = true
			if err := b.expr(n.Source); err != nil {
				return err
			}
			b.suppressPairRebox = prev
			// Operand stack: [tag, payload]; top is payload.
			b.emit(Op{Kind: OpStoreLocal, I32: payloadSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
			b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
			b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
			b.emit(Op{Kind: OpEq})
			b.openIf(BlockTypeVoid)
			// Pair-form is scoped to Option[i32] today; there's
			// always exactly one binding (the payload).
			for i, name := range n.Bindings {
				slot := b.allocSlot()
				b.locals[name] = slot
				bt := ast.Type(ast.NumberType{})
				if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
					bt = n.BindingTypes[i]
				}
				b.scratchType[slot] = bt
				b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
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
			break
		}
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__iflet_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Source); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		// tag at ptr+0; compare to varIdx → i32 0/1.
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpMatchTag})
		b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
		b.emit(Op{Kind: OpEq})
		b.openIf(BlockTypeVoid)
		// Match: bind payloads, run Then.
		offsets, _ := payloadLayout(n.BindingTypes, len(n.Bindings), b.ptrW)
		for i, name := range n.Bindings {
			slot := b.allocSlot()
			b.locals[name] = slot
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			b.scratchType[slot] = bt
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
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
		// Cleanup-before-return: replay every active defer in
		// LIFO order. Evaluate the return value first into a
		// temp local so cleanup runs after the value is fixed
		// (matches Go's "defer sees the return value" only at
		// the cost of pushing the value through a slot — the
		// language doesn't have named return values for
		// defers to mutate, so this is just for correctness
		// when the return expression has side effects).
		if n.Value == nil {
			if err := b.emitDeferCleanup(); err != nil {
				return err
			}
			b.emit(Op{Kind: OpReturnVoid})
			return nil
		}
		// Pair-form return: this function was marked eligible for
		// the (tag, payload) ABI by findPairFormFuncs, and the
		// return value is one of the pair-form variant literals
		// (`Some(EXPR)` / `None` for Option, `Ok(EXPR)` /
		// `Err(EXPR)` for Result). Emit the matching OpMake*I32
		// + OpReturnPair instead of the heap-box construction
		// the generic path would emit. Defers fall back to the
		// heap-box path — pair-form is scoped to the no-defer
		// subset for now.
		if b.thisIsPair && len(b.defers) == 0 && isVariantLiteralExpr(n.Value, b.pairVariants) {
			variantName, payload := pairFormVariantOf(n.Value)
			payloadType := b.pairFormPayloadType(variantName)
			payloadW := pairPayloadWidth(payloadType)
			// Built-in Option/Result variants keep their named
			// ops for readability; user-defined two-variant
			// enums reuse OpMakeSomeI32 / OpMakeNoneI32 for the
			// payload-carrying / nullary variants since the
			// backends treat all four maker ops as a single
			// "push (tag, payload)" pair (tag=0 for the
			// payload-carrying ones, tag=1 for nullary). The
			// `Width` operand carries the payload width so
			// native backends pick the right alloc size + store
			// (4-byte at +4 for i32 payloads, 8-byte at +8 for
			// pointer-shaped payloads).
			switch variantName {
			case "Some":
				if err := b.expr(payload); err != nil {
					return err
				}
				b.emit(Op{Kind: OpMakeSomeI32, Width: payloadW})
			case "None":
				b.emit(Op{Kind: OpMakeNoneI32})
			case "Ok":
				if err := b.expr(payload); err != nil {
					return err
				}
				b.emit(Op{Kind: OpMakeOkI32, Width: payloadW})
			case "Err":
				if err := b.expr(payload); err != nil {
					return err
				}
				b.emit(Op{Kind: OpMakeErrI32, Width: payloadW})
			default:
				// User enum variant: payload-carrying → tag 0
				// (OpMakeSomeI32), nullary → tag 1
				// (OpMakeNoneI32). The canonical-order check
				// in `pairFormVariantsFor` ensures the user's
				// variant order matches this tag mapping.
				if payload != nil {
					if err := b.expr(payload); err != nil {
						return err
					}
					b.emit(Op{Kind: OpMakeSomeI32, Width: payloadW})
				} else {
					b.emit(Op{Kind: OpMakeNoneI32})
				}
			}
			b.emit(Op{Kind: OpReturnPair})
			return nil
		}
		// Tail-call to a pair-form callee inside a pair-form
		// fn: forward the (tag, payload) pair through. Without
		// `suppressPairRebox` the generic call lowering would
		// rebox into a heap pointer, then OpReturn would return
		// a single i32 — mismatching the wasm-side
		// `(result i32 i32)` signature once it gates real
		// multi-value returns on PairForm. Setting the flag
		// keeps the pair on the operand stack so OpReturnPair
		// handles it directly.
		if b.thisIsPair && len(b.defers) == 0 && isPairFormTailCall(n.Value, b.pairForm) {
			save := b.suppressPairRebox
			b.suppressPairRebox = true
			err := b.expr(n.Value)
			b.suppressPairRebox = save
			if err != nil {
				return err
			}
			b.emit(Op{Kind: OpReturnPair})
			return nil
		}
		if err := b.expr(n.Value); err != nil {
			return err
		}
		// Stash the value in a synthetic local so we can run
		// defers after it's evaluated.
		if len(b.defers) > 0 {
			slot := b.allocSlot()
			b.scratchType[slot] = b.fn.ReturnType
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
			if err := b.emitDeferCleanup(); err != nil {
				return err
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
		}
		b.emit(Op{Kind: OpReturn})
	case *ast.Defer:
		// Find this Defer's index in the pre-collected list
		// (pointer identity — collectDefers walked the same
		// AST). Set the corresponding active flag.
		for i, d := range b.defers {
			if d == n {
				b.emit(Op{Kind: OpConstI32, I32: 1})
				b.emit(Op{Kind: OpStoreLocal, I32: b.deferSlots[i]})
				return nil
			}
		}
		return fmt.Errorf("ir: Defer node not registered (compiler bug)")
	case *ast.Arena:
		// arena_save → store cursor → body → load cursor →
		// arena_restore. The cursor lives in a fresh scratch
		// slot for the duration of the block. No defer-style
		// machinery needed — block exit always reaches the
		// restore (early returns are handled by the function-
		// level epilogue, which lives further out and doesn't
		// know about per-block arenas; nested arenas reset
		// only the inner cursor).
		slot := b.allocSlot()
		b.locals[fmt.Sprintf("__arena_%d", slot)] = slot
		b.emit(Op{Kind: OpCallDirect, Str: "arena_save", I32: 0})
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.emit(Op{Kind: OpLoadLocal, I32: slot})
		b.emit(Op{Kind: OpCallDirect, Str: "arena_restore", I32: 1})
		return nil
	case *ast.Var:
		if err := b.expr(n.Init); err != nil {
			return err
		}
		idx, ok := b.locals[n.Name]
		if !ok {
			return fmt.Errorf("ir: var %q has no slot (compiler bug)", n.Name)
		}
		b.emit(Op{Kind: OpStoreLocal, I32: idx})
	case *ast.Destructure:
		// Evaluate Init once into the synthesised temp slot,
		// then per-name: load temp + offs[i] + load (with the
		// right width — `payloadLoadOpFor` covers i32 / f32 /
		// i64 / f64 / pointer-width / two-word string) and
		// store into the name's slot.
		tempIdx, ok := b.locals[n.TempName]
		if !ok {
			return fmt.Errorf("ir: destructure temp %q has no slot (compiler bug)", n.TempName)
		}
		if err := b.expr(n.Init); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: tempIdx})
		// Recover the tuple element types from the synthetic
		// temp so we can pick the right per-element load op +
		// offset.
		var tup ast.TupleType
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == n.TempName {
				if t, ok := v.Type.(ast.TupleType); ok {
					tup = t
				}
				break
			}
		}
		if len(tup.Elems) != len(n.Names) {
			return fmt.Errorf("ir: destructure arity mismatch (compiler bug)")
		}
		offs, _ := tupleElemLayout(tup.Elems, b.ptrW)
		for i, name := range n.Names {
			nameIdx, ok := b.locals[name]
			if !ok {
				return fmt.Errorf("ir: destructure name %q has no slot (compiler bug)", name)
			}
			b.emit(Op{Kind: OpLoadLocal, I32: tempIdx})
			b.emit(Op{Kind: OpConstI32, I32: offs[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(tup.Elems[i], b.ptrW))
			b.emit(Op{Kind: OpStoreLocal, I32: nameIdx})
		}
	case *ast.ExprStmt:
		if err := b.expr(n.Expr); err != nil {
			return err
		}
		// If the expression leaves a value on the stack, drop it so the
		// stack stays balanced at statement boundaries. String-typed
		// expressions on wasm32 produce two stack values under the
		// two-word ABI; mark the drop with `Width: WidthString` so the
		// wasm codegen fans it out to two `drop`s.
		if exprLeavesValue(n.Expr, b.info) {
			w := 0
			if _, isString := b.exprType(n.Expr).(ast.StringType); isString && b.twoWordStrings() {
				w = WidthString
			}
			b.emit(Op{Kind: OpDrop, Width: w})
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
		//
		// Pair-form scrutinee fast path mirrors IfLet's / LetElse's:
		// consume (tag, payload) from the operand stack into two
		// scratch locals, dispatch on the tag local, bind the
		// payload from the payload local — zero alloc end-to-end.
		// Scoped to Option[i32] today (the pair-form set's only
		// shape); any arm with multiple bindings or pointer-
		// shaped payload skips through to the heap-form path,
		// but pair-form-eligibility already excludes those cases
		// upstream so the fast path always covers Option[i32]
		// matches end-to-end.
		pairFormScrutinee := b.isPairFormScrutinee(n.Tag)
		var (
			ptrSlot, tagSlot, payloadSlot int32
		)
		if pairFormScrutinee {
			tagSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__match_tag_%d", tagSlot)] = tagSlot
			payloadSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__match_pay_%d", payloadSlot)] = payloadSlot
			prev := b.suppressPairRebox
			b.suppressPairRebox = true
			if err := b.expr(n.Tag); err != nil {
				return err
			}
			b.suppressPairRebox = prev
			b.emit(Op{Kind: OpStoreLocal, I32: payloadSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
		} else {
			ptrSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__match_p_%d", ptrSlot)] = ptrSlot
			if err := b.expr(n.Tag); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		}
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
			if pairFormScrutinee {
				b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
			} else {
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpMatchTag}) // tag (see OpMatchTag doc)
			}
			b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
			b.emit(Op{Kind: OpEq})
			b.brTo(b.depth, true) // br 0 = exit inner = match
			b.brTo(outerArmD, false)
			b.closeScope() // end inner — matched path lands here
			// Bind payload locals. arm.BindingTypes is filled by
			// the checker with the substituted concrete type (so
			// generic enums instantiated at `Option[number]` give
			// `number`). The binding's local also needs the right
			// declared type — recorded via b.scratchType so the
			// wasm backend declares it as f32 instead of the
			// default i32. Pair-form scrutinees bind from the
			// payload local directly (no heap load); heap-form
			// scrutinees load from `[ptr+offset]`.
			offsets, _ := payloadLayout(arm.BindingTypes, len(arm.Bindings), b.ptrW)
			for i, name := range arm.Bindings {
				slot := b.allocSlot()
				b.locals[name] = slot
				bt := ast.Type(ast.NumberType{})
				if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
					bt = arm.BindingTypes[i]
				}
				b.scratchType[slot] = bt
				if pairFormScrutinee {
					b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
				} else {
					b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
					b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
					b.emit(Op{Kind: OpAdd})
					b.emit(payloadLoadOpFor(bt, b.ptrW))
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
		//
		// IsFloat takes precedence — set by settleFloat when a
		// polymorphic literal lands in float context (`var r:
		// f32 = 0`, `r * 2`, `r <= 0` against an f32 r). Emit
		// the f-const path with the integer Value cast to float.
		if n.IsFloat {
			if n.FloatWidth == 64 {
				b.emit(Op{Kind: OpConstF64, F64: float64(n.Value)})
			} else {
				b.emit(Op{Kind: OpConstF32, F32: float32(n.Value)})
			}
		} else if n.Width == 64 || (n.Width == ast.WidthPtr && b.ptrW == 8) {
			// usize literals on natives (ptrW=8) emit as i64
			// so the full pointer-width value survives — a
			// settled usize literal whose value exceeds 32 bits
			// would otherwise truncate to OpConstI32. On wasm32
			// (ptrW=4) usize stays i32-sized and the i32 const
			// path is correct.
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
			// Resolve the target-aware `usize` (NormalWidth = -1)
			// to a concrete width here so the int↔int cast table
			// below can keep its existing 32/64 cases. usize is
			// 8 bytes on natives (ptrW=8) and 4 bytes on wasm32
			// (ptrW=4); the matrix collapses to a same-width or
			// i32↔i64 hop depending on the target.
			if srcInt.IsPointerWidth() {
				sw = b.ptrW * 8
			}
			if dstInt.IsPointerWidth() {
				dw = b.ptrW * 8
			}
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
		case srcIsInt && dstIsFloat:
			// int → float. Width on the resulting Op is the
			// destination's float width; Unsigned is the
			// SOURCE side's signed-ness.
			dw := dstFloat.NormalWidth()
			sw := srcInt.NormalWidth()
			if srcInt.IsPointerWidth() {
				sw = b.ptrW * 8 // resolve usize to target's ptr width
			}
			if sw == 64 {
				b.emit(Op{Kind: OpFConvertI64, Width: dw, Unsigned: !srcInt.IsSigned()})
			} else {
				// Sub-i32 widths already live in i32 storage at
				// the wasm level; the cast op reads the i32 and
				// converts to the requested float width.
				b.emit(Op{Kind: OpFConvertI32, Width: dw, Unsigned: !srcInt.IsSigned()})
			}
		case srcIsFloat && dstIsInt:
			// float → int (truncate-toward-zero). Width on the
			// op is the destination's int width; Unsigned chosen
			// per the destination's signed-ness.
			dw := dstInt.NormalWidth()
			if dw < 32 {
				dw = 32
			}
			if srcFloat.NormalWidth() == 64 {
				b.emit(Op{Kind: OpITruncF64, Width: dw, Unsigned: !dstInt.IsSigned()})
			} else {
				b.emit(Op{Kind: OpITruncF32, Width: dw, Unsigned: !dstInt.IsSigned()})
			}
		default:
			// Any owned array / slice / string / struct → i32:
			// surface the data / wrapper pointer for the bulk-
			// memory primitives.
			if _, ok := n.Target.(ast.NumberType); ok {
				switch n.InnerType.(type) {
				case ast.SliceType:
					// slice value is a pointer to a
					// `{data_ptr, len}` header — load the
					// data_ptr at offset 0.
					b.emit(Op{Kind: OpLoad})
					return nil
				case ast.StringType:
					if b.twoWordStrings() {
						// Two-word ABI: the operand stack
						// has `[..., data, len]` (inner
						// already evaluated at the top of
						// this case). Casting to usize / i32
						// can't keep both halves on the
						// stack — box them into a fresh
						// 16-byte cell so the resulting
						// pointer survives a round-trip
						// through the integer slot. The
						// inverse cast `usize → string`
						// reads back via OpLoad{WidthString}.
						lenSlot := b.allocSlot()
						b.locals[fmt.Sprintf("__str_to_usize_len_%d", lenSlot)] = lenSlot
						dataSlot := b.allocSlot()
						b.locals[fmt.Sprintf("__str_to_usize_data_%d", dataSlot)] = dataSlot
						cellSlot := b.allocSlot()
						b.locals[fmt.Sprintf("__str_to_usize_cell_%d", cellSlot)] = cellSlot
						// Save (data, len) from the stack
						// into scratch locals.
						b.emit(Op{Kind: OpStoreLocal, I32: lenSlot})
						b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})
						// Allocate the cell.
						b.emit(Op{Kind: OpConstI32, I32: stringSlotSize(b.ptrW)})
						b.emit(Op{Kind: OpAlloc})
						b.emit(Op{Kind: OpStoreLocal, I32: cellSlot})
						// Re-stage [cell, data, len] for OpStore{WidthString}.
						b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
						b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
						b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
						b.emit(Op{Kind: OpStore, Width: WidthString})
						// Push the cell pointer as the cast's result.
						b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
						return nil
					}
					return nil
				case ast.ArrayType, ast.StructType:
					// owned-array / struct values ARE the
					// data / wrapper pointer; the length
					// prefix (arrays) or fields (structs) live
					// at known offsets. No ops needed.
					return nil
				}
			}
			// Reverse direction: i32 / usize → T[] / string / struct.
			// Pure type-level reinterpret; the runtime value
			// is the same pointer, just exposed under a typed
			// handle. usize widens the i32 hop to the target's
			// native pointer width — necessary on arm64-darwin
			// where heap addresses exceed 32 bits.
			//
			// On wasm32, string-typed values live as a two-word
			// `(data, len)` pair on the operand stack. An i32 / usize
			// cast to `string` therefore can't stay a no-op: the
			// single stack value gets reinterpreted as a pointer to
			// an 8-byte `(data, len)` cell and fanned out via
			// `OpLoad{Width:WidthString}` (two `i32.load`s at +0
			// and +4). This is the cell-pointer convention the
			// Map runtime uses for string-typed K/V slots — see
			// the boxing dispatch at the Map call sites in
			// `callBody`. On natives strings stay a single pointer,
			// so the cast remains a no-op.
			if nt, ok := n.InnerType.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
				switch n.Target.(type) {
				case ast.StringType:
					if b.twoWordStrings() {
						b.emit(Op{Kind: OpLoad, Width: WidthString})
					}
					return nil
				case ast.ArrayType, ast.StructType:
					return nil
				}
			}
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
	case *ast.FString:
		// f-strings keep an `n.Desugared` expression (built by
		// the checker, type-checked, method-dispatch-resolved)
		// which IS the equivalent `+`-chain ready to lower. The
		// node only stays in the AST so the formatter can
		// rebuild the f"..." surface syntax on round-trip; the
		// IR / codegen path looks at Desugared.
		if n.Desugared == nil {
			b.emit(Op{Kind: OpConstStr, Str: ""})
		} else if err := b.expr(n.Desugared); err != nil {
			return err
		}
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
	case *ast.IfExpr:
		// `if (c) { a } else { b }` lowers to a typed `if/else`
		// whose arms each push the result. The block-type tells
		// consumers whether the produced value is i32, i64, f32,
		// f64, or the two-i32 (data, len) pair for string-typed
		// arms. Without the i64 / f64 branches, wasm's if-block
		// validator rejected `local.get $i64_var; ...` inside an
		// `(if (result i32))` with "type mismatch: expected
		// i32, found i64" — native targets ignore the block
		// type so the failure was wasm-only.
		bt := BlockTypeI32
		if n.IsFloat {
			bt = BlockTypeF32
		}
		// Promote to i64 / f64 when the arm bodies resolve to
		// a concrete wide numeric. Mirrors the MatchExpr
		// scratch-slot fix from #550 — the arm-body settle
		// (#534 / #545) already pins the width; this just
		// consumes it.
		thenT := b.exprType(n.Then)
		if nt, ok := thenT.(ast.NumberType); ok && nt.NormalWidth() == 64 {
			bt = BlockTypeI64
		}
		if ft, ok := thenT.(ast.FloatType); ok && ft.NormalWidth() == 64 {
			bt = BlockTypeF64
		}
		if _, isString := b.exprType(n).(ast.StringType); isString && b.twoWordStrings() {
			bt = BlockTypeStringPair
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
	case *ast.MatchExpr:
		// Lower expression-form `match`. Same per-arm structure
		// as stmt-form Match but each arm body is an Expr — we
		// stash its value in a scratch slot keyed off the unified
		// arm type, then load that slot after the outer block.
		// Using a scratch slot avoids the wasm "block (result T)
		// must fall through with T on stack" trap; exhaustiveness
		// (the checker's job) guarantees the slot is written
		// exactly once before the post-block load.
		resultType := ast.Type(ast.NumberType{})
		if n.IsFloat {
			resultType = ast.FloatType{}
		}
		// Promote the slot's width when an arm body resolves
		// to a concrete i64 / f64 — the scratch type drives
		// the local's declared width on wasm, and a default
		// polymorphic NumberType lands as i32 there. An i64
		// arm body stored into an i32 slot fails the wasm
		// validator with "type mismatch: expected i32, found
		// i64". The checker's MatchExpr settle (#534 / #545)
		// plus exprType (#530 / #532) already resolve each
		// arm body's width; the first arm body that returns
		// a non-polymorphic type wins.
		for _, arm := range n.Arms {
			if arm == nil {
				continue
			}
			t := b.exprType(arm.Body)
			if nt, ok := t.(ast.NumberType); ok && !nt.Polymorphic {
				resultType = nt
				break
			}
			if ft, ok := t.(ast.FloatType); ok && !ft.Polymorphic {
				resultType = ft
				break
			}
			// String arm bodies need the two-word
			// `(data, len)` slot shape on wasm32; the
			// default i32 NumberType{} maps to a single
			// i32 local and validates wrong when the arm
			// pushes a (data, len) pair. Carrying the
			// StringType through makes the wasm codegen
			// declare `<slot>_data` + `<slot>_len`.
			if _, ok := t.(ast.StringType); ok {
				resultType = ast.StringType{}
				break
			}
		}
		resultSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__matchexpr_r_%d", resultSlot)] = resultSlot
		b.scratchType[resultSlot] = resultType
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__matchexpr_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Tag); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		b.openBlock(BlockTypeVoid)
		matchEndD := b.depth
		for _, arm := range n.Arms {
			if arm.IsWildcard {
				if arm.Guard != nil {
					b.openBlock(BlockTypeVoid)
					armEndD := b.depth
					if err := b.expr(arm.Guard); err != nil {
						return err
					}
					b.emit(Op{Kind: OpNot})
					b.brTo(armEndD, true)
					if err := b.expr(arm.Body); err != nil {
						return err
					}
					b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
					b.brTo(matchEndD, false)
					b.closeScope()
					continue
				}
				if err := b.expr(arm.Body); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
				b.brTo(matchEndD, false)
				continue
			}
			_, varIdx, _, ok := b.lookupVariant(arm.VariantName)
			if !ok {
				return fmt.Errorf("ir: match-expression arm references unknown variant %q", arm.VariantName)
			}
			b.openBlock(BlockTypeVoid)
			outerArmD := b.depth
			b.openBlock(BlockTypeVoid)
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpLoad})
			b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
			b.emit(Op{Kind: OpEq})
			b.brTo(b.depth, true)
			b.brTo(outerArmD, false)
			b.closeScope() // matched path lands here
			offsets, _ := payloadLayout(arm.BindingTypes, len(arm.Bindings), b.ptrW)
			for i, name := range arm.Bindings {
				slot := b.allocSlot()
				b.locals[name] = slot
				bt := ast.Type(ast.NumberType{})
				if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
					bt = arm.BindingTypes[i]
				}
				b.scratchType[slot] = bt
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
				b.emit(Op{Kind: OpAdd})
				b.emit(payloadLoadOpFor(bt, b.ptrW))
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
			}
			if arm.Guard != nil {
				if err := b.expr(arm.Guard); err != nil {
					return err
				}
				b.emit(Op{Kind: OpNot})
				b.brTo(outerArmD, true)
			}
			if err := b.expr(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(matchEndD, false)
			b.closeScope()
		}
		b.closeScope()
		b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	case *ast.TryOp:
		// Lower `expr?` as: stash the source pointer, branch on
		// the failure tag, take the success path otherwise. The
		// failure branch differs by Kind:
		//
		//   - Option: build a fresh None of the function's return
		//     type and OpReturn it. None has no payload so the
		//     allocation is a single tag word; cheap.
		//   - Result: forward the SOURCE pointer as the return
		//     value. The source already carries tag=Err and the E
		//     payload at +4, and the checker has verified the E
		//     matches the enclosing return's E, so the same heap
		//     object satisfies both types — no reallocation.
		//
		// Both share the success-path payload load at ptr+4; the
		// width comes from the checker-stamped Type.
		ptrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__try_p_%d", ptrSlot)] = ptrSlot
		if err := b.expr(n.Inner); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpLoad}) // tag at ptr+0
		b.emit(Op{Kind: OpConstI32, I32: 1}) // failure variant idx is 1 for both Option and Result
		b.emit(Op{Kind: OpEq})
		b.openIf(BlockTypeVoid)
		// Failure-path return shape has to match the enclosing
		// function's ABI: pair-form fns return (tag, payload)
		// via OpReturnPair, heap-form fns return a single
		// heap-box pointer via OpReturn.
		switch n.Kind {
		case ast.TryKindOption:
			if b.thisIsPair {
				b.emit(Op{Kind: OpMakeNoneI32})
				b.emit(Op{Kind: OpReturnPair})
			} else {
				if err := b.emitEnumNew(nil, "Option", 1, 0, nil); err != nil {
					return err
				}
				b.emit(Op{Kind: OpReturn})
			}
		case ast.TryKindResult:
			if b.thisIsPair {
				// Forward the source heap-box's (tag,
				// payload) onto the operand stack so
				// OpReturnPair has the right shape.
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpLoad}) // tag at ptr+0
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpConstI32, I32: 4})
				b.emit(Op{Kind: OpAdd})
				b.emit(Op{Kind: OpLoad}) // payload at ptr+4
				b.emit(Op{Kind: OpReturnPair})
			} else {
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpReturn})
			}
		default:
			return fmt.Errorf("ir: TryOp with unstamped Kind")
		}
		b.closeScope()
		// Success path: load payload at the same offset
		// emitEnumNew stored it. `payloadLayout` aligns
		// 8-byte payloads (i64 / f64 / two-word strings) to
		// offset 8 because the tag occupies offset 0..3 —
		// the previous unconditional `ptr + 4` load read
		// padding bytes for any wide payload (Option[i64]'s
		// success path returned junk high bits).
		offsets, _ := payloadLayout([]ast.Type{n.Type}, 1, b.ptrW)
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpConstI32, I32: offsets[0]})
		b.emit(Op{Kind: OpAdd})
		b.emit(payloadLoadOpFor(n.Type, b.ptrW))
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
		// byte / word at the resulting address. Element width
		// changes the load op + which helper picks up the stride;
		// `__str_idx` already does (i*1 + bounds-check) so we
		// reuse it for sub-i32 owned arrays.
		if err := b.expr(n.Array); err != nil {
			return err
		}
		if err := b.expr(n.Idx); err != nil {
			return err
		}
		// Resolve the element type to pick stride + load width.
		// loadWidth is non-zero only for the 64-bit case; the
		// wasm codegen's intPrefix / floatPrefix honour it on
		// OpLoad / OpFLoad.
		elemType := n.ElemType
		stride := int32(4)
		loadOp := OpLoad
		loadWidth := 0
		if elemType != nil {
			stride = int32(ast.ElemSizeBytesFor(elemType, b.ptrW))
			if nt, ok := elemType.(ast.NumberType); ok {
				switch nt.NormalWidth() {
				case 8:
					if nt.IsSigned() {
						loadOp = OpLoadI8S
					} else {
						loadOp = OpLoadByte
					}
				case 16:
					if nt.IsSigned() {
						loadOp = OpLoadI16S
					} else {
						loadOp = OpLoadI16U
					}
				case 64:
					loadWidth = 64
				}
			}
			if ft, ok := elemType.(ast.FloatType); ok {
				loadOp = OpFLoad
				if ft.NormalWidth() == 64 {
					loadWidth = 64
				}
			}
			// Pointer-typed elements: emit a ptr-width load so
			// arm64's 8-byte heap pointers don't truncate.
			if ast.IsPointerType(elemType) {
				loadWidth = WidthPtr
			}
			// String elements (two-word ABI): the wasm OpLoad
			// handler fans out a WidthString load to two i32.load
			// calls (data @ +0, len @ +4). On natives this stays
			// as a single ptr-width load via the LSB-tagged form.
			if _, isString := elemType.(ast.StringType); isString && b.twoWordStrings() {
				loadWidth = WidthString
			}
		}
		if n.IsString {
			b.emit(Op{Kind: OpCallDirect, Str: "__str_idx", I32: 2})
			b.emit(Op{Kind: OpLoadByte})
		} else if n.IsSlice {
			// Slice-index variants per stride: __slice_idx_1
			// for byte slices, __slice_idx_2 for halfwords,
			// __slice_idx (= 4) for the historical layout,
			// __slice_idx_8 for i64/f64.
			sliceHelper := "__slice_idx"
			switch stride {
			case 1:
				sliceHelper = "__slice_idx_1"
			case 2:
				sliceHelper = "__slice_idx_2"
			case 8:
				sliceHelper = "__slice_idx_8"
			case 16:
				sliceHelper = "__slice_idx_16"
			}
			b.emit(Op{Kind: OpCallDirect, Str: sliceHelper, I32: 2})
			b.emit(Op{Kind: loadOp, Width: loadWidth})
		} else {
			// Per-stride helper: __arr_idx_1 for byte arrays
			// (stride=1, address arithmetic identical to
			// __arr_idx but without the *4 multiplier),
			// __arr_idx for the historical i32-stride layout,
			// __arr_idx_2 for u16/i16, __arr_idx_8 for i64/f64.
			// String indexing routes through __str_idx (above);
			// the byte-array case is plain `base + i`.
			helper := "__arr_idx"
			switch stride {
			case 1:
				helper = "__arr_idx_1"
			case 2:
				helper = "__arr_idx_2"
			case 8:
				helper = "__arr_idx_8"
			case 16:
				helper = "__arr_idx_16"
			}
			b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
			b.emit(Op{Kind: loadOp, Width: loadWidth})
		}
	case *ast.SliceExpr:
		// String slicing: copy into a fresh length-prefixed
		// string. Owns its bytes (matches the rest of the
		// language's string semantics — no separate view type
		// for strings yet). Bounds-check happens inside the
		// helper.
		if n.IsString {
			if err := b.expr(n.Source); err != nil {
				return err
			}
			if n.Low != nil {
				if err := b.expr(n.Low); err != nil {
					return err
				}
			} else {
				b.emit(Op{Kind: OpConstI32, I32: 0})
			}
			if n.High != nil {
				if err := b.expr(n.High); err != nil {
					return err
				}
			} else {
				// Fall back to source length: re-evaluate
				// Source then route through OpStrLen for the
				// SSO-aware length read (inline / heap branch).
				// The two-word ABI's length lives on the operand
				// stack as the second i32 of `(data, len)` — the
				// legacy `[ptr - 4]` array-shape prefix-load no
				// longer applies.
				if err := b.expr(n.Source); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStrLen})
			}
			// I32 is the IR-level operand-stack pop count (one
			// per source argument), so it stays at 3 on both
			// targets. On wasm32 the OpLoadLocal-of-string
			// fans to two wasm-stack i32s automatically, so
			// the runtime helper's 4-param signature is fed
			// the right number of wasm-stack slots without the
			// IR caller knowing about the fan-out.
			//
			// ArgTypes stamped explicitly because `__str_slice`
			// isn't in FuncSigs (it's a synthesised helper, not
			// a user-callable name) — the arm64 two-word ABI
			// needs `(string, i32, i32)` to count the string
			// arg as 2 operand-stack slots.
			b.emit(Op{Kind: OpCallDirect, Str: "__str_slice", I32: 3, ArgTypes: []ast.Type{ast.StringType{}, ast.NumberType{}, ast.NumberType{}}})
			break
		}
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
		// data_ptr += low * stride (skip when low is 0/missing).
		// Stride defaults to 4 for the historical i32 layout but
		// drops to 1 / 2 / 8 for byte / halfword / wide-element
		// slices per ast.ElemSizeBytes. Skip the multiply
		// entirely when stride == 1 — `low * 1` is just `low`.
		stride := int32(4)
		if n.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(n.ElemType, b.ptrW))
		}
		if n.Low != nil {
			if err := b.expr(n.Low); err != nil {
				return err
			}
			if stride != 1 {
				b.emit(Op{Kind: OpConstI32, I32: stride})
				b.emit(Op{Kind: OpMul})
			}
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
		// Allocate len*stride + 4 bytes (length prefix + payload),
		// store the length at base+0, then store each element at
		// base+4+i*stride and leave the content pointer on the
		// stack. Stride defaults to 4 (the historical i32 / pointer
		// layout) but drops to 1 / 2 for byte / halfword arrays
		// per ast.ElemSizeBytes.
		// Header layout: a 4-byte length prefix lives at
		// `data - 4`, so `len(arr)` always loads from a fixed
		// offset. For stride > 4 we still need the FIRST
		// element to be stride-aligned (Apple Silicon enforces
		// alignment for some 8-byte LDR/STR sequences); pad
		// the header up to `stride` so element 0 sits at a
		// stride-aligned offset from base. For stride <= 4 the
		// 4-byte header is already aligned.
		nElems := int32(len(n.Elems))
		stride := int32(4)
		if n.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(n.ElemType, b.ptrW))
		}
		headerBytes := int32(4)
		if stride > 4 {
			headerBytes = stride
		}
		// Pick the element-store op via `arrayElemStoreOpFor`,
		// which is the central place that knows about WidthString
		// (two-word strings on wasm32), WidthPtr (pointer-width
		// stores on arm64), the i8 / i16 / i64 / f32 / f64 lanes,
		// and the default i32 fallback.
		storeOpAndWidth := arrayElemStoreOpFor(n.ElemType, b.ptrW)
		b.emit(Op{Kind: OpConstI32, I32: headerBytes + nElems*stride})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__arr_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// Length prefix at base + headerBytes - 4 (so callers
		// can always reach it via `data - 4`).
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		if headerBytes != 4 {
			b.emit(Op{Kind: OpConstI32, I32: headerBytes - 4})
			b.emit(Op{Kind: OpAdd})
		}
		b.emit(Op{Kind: OpConstI32, I32: nElems})
		b.emit(Op{Kind: OpStore})
		// Each element at base + headerBytes + i*stride.
		for i, el := range n.Elems {
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: headerBytes + int32(i)*stride})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(el); err != nil {
				return err
			}
			b.emit(storeOpAndWidth)
		}
		// Push the *content* pointer (base + headerBytes) so the
		// value matches what the rest of the language expects
		// from an ArrayLit.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: headerBytes})
		b.emit(Op{Kind: OpAdd})
	case *ast.MapLit:
		// Lower `Map { k1: v1, k2: v2, ... }` to:
		//   var __m = map_new(N, keyKind, valKind);
		//   __m.set(k1, v1);
		//   __m.set(k2, v2);
		//   ...
		//   __m
		// where N is the entry count (so no resize on the
		// initial fill), `keyKind` tags K (0 = i32-scalar,
		// 1 = string), and `valKind` tags V (0 = i32-scalar,
		// 1 = pointer-shaped). The runtime uses keyKind to
		// pick i32.eq vs strcmp on lookup, and valKind to
		// size the .values() snapshot array's element stride.
		// Stash the constructed Map handle in a fresh local so
		// each `set` call can reload it.
		b.emit(Op{Kind: OpConstI32, I32: int32(len(n.Entries))})
		b.emit(Op{Kind: OpConstI32, I32: mapKeyKindTag(n.KeyType, b.ptrW)})
		b.emit(Op{Kind: OpConstI32, I32: mapValKindTag(n.ValueType)})
		b.emit(Op{Kind: OpCallDirect, Str: "map_new", I32: 3})
		mapSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_maplit_%d", mapSlot)] = mapSlot
		b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})
		// Boxing path: each K and/or V gets a fresh 8-byte cell
		// whose pointer goes into the entries array — matches the
		// emitWideMapSet shape used at user `m.set(k, v)` call
		// sites. Triggers for wide V (i64 / u64 / f64) on every
		// target, and string K / V on wasm32 (where the two-word
		// ABI doesn't fit the helper's i32 K/V slot).
		boxK := isStringForBoxing(n.KeyType, b.ptrW) || mapKeyKindTag(n.KeyType, b.ptrW) == 2
		boxV := isWideScalar(n.ValueType) || isStringForBoxing(n.ValueType, b.ptrW)
		for _, ent := range n.Entries {
			b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
			if err := b.pushMapMethodArg(ent.Key, n.KeyType, boxK, "__maplit_k"); err != nil {
				return err
			}
			if err := b.pushMapMethodArg(ent.Value, n.ValueType, boxV, "__maplit_v"); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_set", I32: 3})
		}
		b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	case *ast.TupleLit:
		// Same shape as StructLit — alloc enough heap for the
		// elements at their packed offsets, store each at
		// `offs[i]` with a width that matches the element's
		// type (so pointer-typed elements get pointer-width
		// slots on arm64).
		elemTypes := make([]ast.Type, len(n.Elems))
		for i, elem := range n.Elems {
			elemTypes[i] = b.exprType(elem)
		}
		offs, size := tupleElemLayout(elemTypes, b.ptrW)
		b.emit(Op{Kind: OpConstI32, I32: size})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_tup_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		for i, elem := range n.Elems {
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: offs[i]})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(elem); err != nil {
				return err
			}
			b.emit(payloadStoreOpFor(elemTypes[i], b.ptrW))
		}
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	case *ast.StructLit:
		sd, ok := b.info.Structs[n.TypeName]
		if !ok {
			return fmt.Errorf("ir: unknown struct %q (compiler bug)", n.TypeName)
		}
		// Per-field layout — pointer-typed fields widen to
		// ptrW bytes on arm64 so heap addresses survive the
		// store/load round-trip. Wide / pointer fields are
		// 8-byte-aligned within the heap object.
		offs, size := structFieldLayout(sd.Fields, b.ptrW)
		b.emit(Op{Kind: OpConstI32, I32: size})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		for _, f := range n.Fields {
			off := offs[f.Name]
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: off})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(f.Value); err != nil {
				return err
			}
			// Reuse payloadStoreOp so the store is correctly
			// sized for the field's declared type: i32 / f32
			// / sub-i32 → 4 bytes, i64 / f64 → 8 bytes, and
			// pointer types (string / array / struct / enum
			// / slice / closure) → WidthPtr (4 on wasm32, 8
			// on arm64).
			b.emit(payloadStoreOpFor(fieldType(sd.Fields, f.Name), b.ptrW))
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
		// Reuse the per-payload-type load picker: i32.load for
		// pointer / 4-byte types, f32.load for f32, i64.load /
		// f64.load for the wide variants. Without the width
		// dispatch a captured i64 or f64 would silently
		// truncate / mis-decode at the load site.
		b.emit(payloadLoadOpFor(n.Type, b.ptrW))
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
		// Compute base + offset_of(field), then load the value
		// at its declared width (4-byte for i32 / f32 / sub-i32,
		// 8-byte for i64 / f64, ptr-width for pointer types so
		// arm64-darwin's high heap addresses survive).
		// Tuple field access uses a numeric selector (`pair.0`);
		// resolve the offset against the tuple's static element
		// list, otherwise fall through to the struct path.
		var ft ast.Type
		off := int32(-1)
		if tup, ok := b.targetTupleType(n.Target); ok {
			idx, err := strconv.Atoi(n.Field)
			if err != nil {
				return fmt.Errorf("ir: tuple field selector %q is not numeric", n.Field)
			}
			if idx < 0 || idx >= len(tup.Elems) {
				return fmt.Errorf("ir: tuple has %d elements; index %d out of range", len(tup.Elems), idx)
			}
			offs, _ := tupleElemLayout(tup.Elems, b.ptrW)
			off = offs[idx]
			ft = tup.Elems[idx]
		} else {
			st := b.fieldOwner(n.Target)
			sd, ok := b.info.Structs[st]
			if !ok {
				return fmt.Errorf("ir: field access on unresolved struct %q", st)
			}
			offs, _ := structFieldLayout(sd.Fields, b.ptrW)
			for _, f := range sd.Fields {
				if f.Name == n.Field {
					off = offs[f.Name]
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
		b.emit(Op{Kind: OpConstI32, I32: off})
		b.emit(Op{Kind: OpAdd})
		b.emit(payloadLoadOpFor(ft, b.ptrW))
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
		if slot, ok := b.locals[x.Name]; ok {
			if t, ok := b.scratchType[slot]; ok {
				return t
			}
		}
	case *ast.CaptureRef:
		// Captured variable references carry their resolved
		// outer-scope type on the AST node — needed when the
		// closure body asks "what struct/tuple is this?" for
		// field-access offset resolution.
		return x.Type
	case *ast.SliceExpr:
		// A slice expression's static type follows the source:
		// `string[a:b]` is still a string (handled by __str_slice
		// at runtime), array[a:b] is a SliceType over the element.
		src := b.exprType(x.Source)
		switch s := src.(type) {
		case ast.ArrayType:
			return ast.SliceType{Elem: s.Elem}
		case ast.SliceType:
			return s
		case ast.StringType:
			return ast.StringType{}
		}
	case *ast.Binary:
		// `+` between strings produces a string. Returning the
		// right type here matters for the `len()` lowering: a
		// `len(a + b)` with a / b both strings must route through
		// OpStrLen rather than the array-shape `const 4; sub;
		// load` fallback — otherwise small-string-optimisation
		// inline-tagged returns from `__lang_strcat` go through
		// the wrong length-read path and the load tries to read
		// memory at `inline_value - 4`. Latent bug since the
		// OpStrLen seam landed; surfaced once strcat started
		// producing inline outputs.
		if x.IsStringConcat {
			return ast.StringType{}
		}
		if x.IsStringCmp {
			return ast.BoolType{}
		}
		// Comparison ops (`==`, `!=`, `<`, `<=`, `>`, `>=`)
		// produce booleans regardless of operand width. The
		// checker stamps IntWidth / FloatWidth on the Binary
		// to drive codegen's i64.eq / f64.lt etc. selection,
		// but the RESULT type is bool. Without this guard,
		// the IntWidth=64 case below incorrectly typed
		// `(a > b)` inside a `(bool, i64)` tuple as i64,
		// the slot stride doubled, and wasm rejected the
		// i32 load (the bool was an i32 0/1 on the stack)
		// against an i64-shaped slot.
		switch x.Op {
		case "==", "!=", "<", "<=", ">", ">=":
			return ast.BoolType{}
		}
		// Numeric binaries — return a NumberType / FloatType
		// shaped by the checker's stamps so the IR can size
		// payload slots correctly for tuple / struct elements.
		// Without this, `(a + b)` in a tuple literal lands
		// with type nil → payloadSlotSize defaults to 4 →
		// tupleElemLayout packs i64 / f64 elements at 4-byte
		// offsets → both stores clobber each other's high
		// bits.
		if x.FloatWidth != 0 {
			return ast.FloatType{Width: x.FloatWidth}
		}
		if x.IntWidth != 0 {
			return ast.NumberType{Width: x.IntWidth, Signed: !x.IsUnsigned}
		}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.NumberLit:
		// The checker stamps `Width` on the literal once
		// the destination type is known. Mirror it back as
		// a `NumberType` so callers (TupleLit slot sizing
		// especially) see the resolved width rather than
		// the nil-default i32.
		if x.IsFloat {
			return ast.FloatType{Width: x.FloatWidth}
		}
		if x.Width != 0 {
			return ast.NumberType{Width: x.Width, Signed: !x.IsUnsigned}
		}
	case *ast.FloatLit:
		// Mirror the NumberLit handling above for float-
		// literal source forms (`3.14`, `1.5e10`). The
		// checker stamps `Width` once the destination
		// commits; without this, a tuple-literal `(3.14,
		// 42)` against `(f64, i32)` saw payloadSlotSize
		// fall back to its 4-byte default and the f64
		// store/load mis-aligned its operand-stack slot.
		if x.Width != 0 {
			return ast.FloatType{Width: x.Width}
		}
	case *ast.FieldAccess:
		// Tuple field access (`pair.0`) — resolve the static
		// tuple type, parse the numeric selector, and look up
		// the element type. Without this the struct path
		// below falls through to `fieldOwner` (which returns
		// "" for tuples) and exprType returns nil — the
		// surrounding TupleLit slot-sizing then defaulted to
		// 4 bytes and truncated wide elements.
		if tup, ok := b.targetTupleType(x.Target); ok {
			if idx, err := strconv.Atoi(x.Field); err == nil && idx >= 0 && idx < len(tup.Elems) {
				return tup.Elems[idx]
			}
		}
		// Struct field access. `r.body` on `r: HttpRequest`
		// needs to resolve to `string` so `len(r.body)` routes
		// through OpStrLen for the SSO seam. Look up the
		// struct decl and find the field.
		owner := b.fieldOwner(x.Target)
		if owner == "" {
			return nil
		}
		sd, ok := b.info.Structs[owner]
		if !ok {
			return nil
		}
		for _, f := range sd.Fields {
			if f.Name == x.Field {
				return f.Type
			}
		}
	case *ast.Index:
		// `a[i]` returns the element type. The checker stamps
		// ElemType on array / slice indexing once the element is
		// resolved; for string indexing the result is a single
		// byte (modelled as i32 zero-extended), which means
		// `len(s[i])` would be a type error in lang — but
		// `len(arr_of_strings[i])` MUST route through OpStrLen
		// so the SSO seam handles the inline / heap branch.
		// Same family of latent bug as the *ast.Binary case
		// above: without this dispatch, `argT` comes back nil
		// and the `len()` fallback open-codes a `[ptr - 4]`
		// load, which traps on inline-form strings produced
		// by $args / $string_from_bytes / $__str_concat / etc.
		if x.IsString {
			// `s[i]` produces a single byte zero-extended into
			// an i32. Treat as a generic number type — falls
			// through the `len()` fallback the way arrays do
			// (and `len(byte)` is rejected by the checker
			// upstream regardless).
			return ast.NumberType{}
		}
		return x.ElemType
	case *ast.Call:
		// `len(f(...))` where `f` returns a string must route
		// through OpStrLen so the SSO seam handles the inline
		// vs heap branch. Without this dispatch, the lowering
		// falls through to the array-shape `[ptr - 4]; load`
		// fallback, which traps on inline-form strings
		// produced by string-returning helpers — most
		// importantly `int_to_string`, whose 1..3-digit / -1..-99
		// outputs cascade through `$string_from_bytes`'s
		// inline-output path. The callee's return type comes
		// off `info.FuncSigs` (populated by the checker for
		// every user fn + every prelude / builtin signature).
		if id, ok := x.Callee.(*ast.Ident); ok {
			// Generic Map methods carry TypeArgs (K, V) on the
			// Call. The FuncSigs entry stores the generic
			// signature whose Result is a ParamType (V); we
			// substitute V from TypeArgs so callers consuming
			// the result (`len(m.get_or(...))`) see the
			// concrete type rather than the unresolved param.
			switch id.Name {
			case "__method_Map_get_or", "__method_MapIter_value":
				if len(x.TypeArgs) >= 2 {
					return x.TypeArgs[1]
				}
			case "__method_Map_get":
				// Returns Option[V] — but the inner V is what
				// matters for the boxing-aware load. Caller's
				// match-arm reads the payload directly; this
				// path is only consulted for `len()` etc.
				// applied to V, which isn't the typical shape.
				// Leave the generic for now.
			}
			if sig, ok := b.info.FuncSigs[id.Name]; ok && sig != nil {
				return sig.Result
			}
			// Closure-typed local / param: `len(f())` where f is
			// a Var or param of type `() => string`. Without this
			// dispatch `exprType` returns nil and the surrounding
			// `len()` lowering falls through to the array-shape
			// `[ptr - 4]; load` fallback — which traps on inline-
			// form strings (SSO) returned by the closure.
			for _, p := range b.fn.Params {
				if p.Name == id.Name {
					if ft, ok := p.Type.(*ast.FuncType); ok {
						return ft.Result
					}
				}
			}
			for _, v := range b.info.Locals[b.fn] {
				if v.Name == id.Name {
					if ft, ok := v.Type.(*ast.FuncType); ok {
						return ft.Result
					}
				}
			}
		}
		// CaptureRef callee: `len(capF())` inside a closure body
		// where `capF` is a captured outer function value. The
		// captured Type is *FuncType — return its Result so the
		// surrounding `len()` lowering picks the right load shape.
		if cr, ok := x.Callee.(*ast.CaptureRef); ok {
			if ft, ok := cr.Type.(*ast.FuncType); ok {
				return ft.Result
			}
		}
	case *ast.IfExpr:
		// `len(if cond { a } else { b })` where both arms are
		// strings must route through OpStrLen — same SSO-seam
		// reason as the *ast.Call / *ast.Index cases above.
		// Without this dispatch, the lowering falls through to
		// the array-shape `[ptr - 4]; load` fallback, which
		// traps when one of the arms produces an inline-form
		// string (e.g. `if cond { int_to_string(n) } else { s }`).
		//
		// The checker has already unified the two arms to a
		// single type; recursing on `Then` is enough — `Else`
		// must match.
		return b.exprType(x.Then)
	case *ast.MatchExpr:
		// `len(match e { Variant => a, _ => b })` parallels the
		// IfExpr case: every arm body shares a unified type so
		// recursing on the first arm body that resolves is
		// sufficient.
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			if t := b.exprType(arm.Body); t != nil {
				return t
			}
		}
	case *ast.Assign:
		// Assignment-as-expression returns the assigned value's
		// type — that's the target's type. The IR's `b.assign`
		// emits a store-then-load pair to leave the value on the
		// stack for downstream consumers; the surrounding
		// ExprStmt drops it. Knowing the type here lets the drop
		// fan correctly for two-word strings on wasm32.
		return b.exprType(x.Target)
	case *ast.CastExpr:
		// `n as i64` resolves to the cast's target type.
		// Without this, a tuple literal whose element is a
		// cast (`(n as i64, 0 as i64)`) saw exprType→nil →
		// payloadSlotSize defaulted to 4 → both elements
		// packed into 4-byte slots and the i64 value
		// truncated.
		return x.Target
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
		if slot, ok := b.locals[x.Name]; ok {
			if t, ok := b.scratchType[slot]; ok {
				if tt, ok := t.(ast.TupleType); ok {
					return tt, true
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
	case *ast.Index:
		// `arr[i].N` where arr is an array of tuples — the
		// Index result's static type is the element type of
		// the array. Without this, the IR's FieldAccess
		// lowering falls through to the struct path,
		// `fieldOwner` returns "", and codegen errors with
		// `field access on unresolved struct ""`.
		if t, ok := b.exprType(x).(ast.TupleType); ok {
			return t, true
		}
	case *ast.CaptureRef:
		// A captured tuple value: closure conversion stamps
		// the resolved outer-scope type on `x.Type`. Without
		// this case the IR's FieldAccess lowering for `t.N`
		// (where `t` is a captured tuple in the closure body)
		// falls through to the struct path and codegen errors
		// with `field access on unresolved struct ""`. Mirror
		// of the CaptureRef case in `fieldOwner` for structs.
		if t, ok := x.Type.(ast.TupleType); ok {
			return t, true
		}
	case *ast.Call:
		// `f().N` where `f` returns a tuple — either a top-level
		// function (resolved through FuncSigs) or a closure value
		// (function-typed local / param / capture). callReturnType
		// covers all three shapes; we just peel back the TupleType
		// when it lands.
		if t := b.callReturnType(x); t != nil {
			if tt, ok := t.(ast.TupleType); ok {
				return tt, true
			}
		}
	}
	return ast.TupleType{}, false
}

func (b *builder) fieldOwner(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		// Look up via locals: vars carry a Var.Type; params live in
		// fn.Params. We cross-reference the checker's info. Match-
		// arm bindings (and other IR-introduced locals) only show
		// up in b.scratchType, so consult that as a third source.
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
		if slot, ok := b.locals[x.Name]; ok {
			if t, ok := b.scratchType[slot]; ok {
				if st, ok := t.(ast.StructType); ok {
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
	case *ast.Index:
		// Field access through an array index — `xs[i].field`
		// where xs is T[]. Walk the array expression's static
		// type, peel the ArrayType down to its element. Works
		// recursively, so `xss[i][j].field` resolves through
		// nested arrays.
		if t := b.exprStaticType(x.Array); t != nil {
			if at, ok := t.(ast.ArrayType); ok {
				if st, ok := at.Elem.(ast.StructType); ok {
					return st.Name
				}
			}
		}
	case *ast.Call:
		// Field access on a function call's struct return —
		// `foo().field` where foo returns a struct. Look up the
		// callee's return type via funcs / FuncSigs.
		if t := b.callReturnType(x); t != nil {
			if st, ok := t.(ast.StructType); ok {
				return st.Name
			}
		}
	case *ast.StructLit:
		return x.TypeName
	case *ast.CaptureRef:
		// A captured struct value: the env loads the heap
		// pointer; the struct decl is named on x.Type.
		if st, ok := x.Type.(ast.StructType); ok {
			return st.Name
		}
	}
	return ""
}

// exprStaticType returns the checker-recorded static type of an
// expression, or nil if the IR can't resolve it locally. Used by
// fieldOwner to peel through ArrayType for `xs[i].field`-style
// access. Covers the same surface as fieldOwner (Ident, FieldAccess,
// Index) but returns the full ast.Type rather than just a struct
// name, so callers can handle composite return shapes (arrays of
// structs, structs containing arrays of structs, etc.).
func (b *builder) exprStaticType(e ast.Expr) ast.Type {
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
		if slot, ok := b.locals[x.Name]; ok {
			if t, ok := b.scratchType[slot]; ok {
				return t
			}
		}
	case *ast.FieldAccess:
		owner := b.fieldOwner(x.Target)
		if sd, ok := b.info.Structs[owner]; ok {
			for _, f := range sd.Fields {
				if f.Name == x.Field {
					return f.Type
				}
			}
		}
	case *ast.Index:
		if t := b.exprStaticType(x.Array); t != nil {
			if at, ok := t.(ast.ArrayType); ok {
				return at.Elem
			}
		}
	case *ast.Call:
		return b.callReturnType(x)
	}
	return nil
}

// callReturnType returns the return type of a Call expression
// by looking up the callee in the checker's FuncSigs map. The
// callee can be either an Ident (most common — direct call to
// a named function), a closure-typed Ident (a Var / param of
// FuncType), or a *ast.CaptureRef whose stamped Type carries
// the captured outer-scope FuncType. Used by fieldOwner /
// exprStaticType / targetTupleType to resolve `foo().field`
// and `foo().0` patterns where the call returns a struct /
// tuple — and crucially when `foo` is a closure value, not a
// top-level function.
func (b *builder) callReturnType(c *ast.Call) ast.Type {
	if cr, ok := c.Callee.(*ast.CaptureRef); ok {
		if ft, ok := cr.Type.(*ast.FuncType); ok {
			return ft.Result
		}
		return nil
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	if b.info != nil {
		if sig, ok := b.info.FuncSigs[id.Name]; ok && sig != nil {
			return sig.Result
		}
	}
	// Function-typed local / param: `foo` is bound as a
	// closure value (a Var of FuncType) or arrived as a param
	// of FuncType. Its Result is what callers see post-call.
	for _, p := range b.fn.Params {
		if p.Name == id.Name {
			if ft, ok := p.Type.(*ast.FuncType); ok {
				return ft.Result
			}
		}
	}
	if b.info != nil {
		for _, v := range b.info.Locals[b.fn] {
			if v.Name == id.Name {
				if ft, ok := v.Type.(*ast.FuncType); ok {
					return ft.Result
				}
			}
		}
	}
	return nil
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
//     len(ident) == lit.length
//     ? <ident == lit at byte level via OpStrEq>
//     : 0
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
				b.emitShlByConst(n, k)
				return nil, true
			}
		}
		if rok {
			if k, ok := powerOfTwo(numR); ok && k > 0 {
				if err := b.expr(n.Left); err != nil {
					return err, true
				}
				b.emitShlByConst(n, k)
				return nil, true
			}
		}
	}
	return nil, false
}

// emitShlByConst pushes a constant shift count `k` of the
// width matching `n`'s resolved integer width, then emits
// OpShl with that width. For i64 binaries the count needs
// to be i64 on wasm (`i64.shl` requires `i64` operands) —
// the previous shape emitted OpConstI32 and OpShl{Width:0},
// which wasm rejected with "type mismatch: expected i64,
// found i32" any time the strength-reduction rewrote
// `i64-expr * 2^k` into a shift. Native targets ignored
// the width on the const, so the failure was wasm-only.
func (b *builder) emitShlByConst(n *ast.Binary, k int32) {
	if n.IntWidth == 64 {
		b.emit(Op{Kind: OpConstI64, I64: int64(k)})
		b.emit(Op{Kind: OpShl, Width: 64})
		return
	}
	b.emit(Op{Kind: OpConstI32, I32: k})
	b.emit(Op{Kind: OpShl})
}

// constNumber peels back the small set of AST shapes that
// resolve to a compile-time integer constant. Currently:
//   - NumberLit       (e.g. `5`)
//   - Unary("-", num) (e.g. `-1`, which the parser builds as
//     a unary minus on a positive literal)
//
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
		// `x - x` / `x ^ x` collapse to 0. Width must match
		// the binary's resolved IntWidth — emitting i32 0
		// into an i64 context breaks the wasm validator
		// ("type mismatch: expected i64, found i32").
		b.emitZeroConstForWidth(n.IntWidth)
		return nil, true
	case "|", "&":
		return b.expr(n.Left), true
	case "==", "<=", ">=":
		// Comparisons produce booleans (i32) regardless of
		// operand width — leave at i32 0/1.
		b.emit(Op{Kind: OpConstI32, I32: 1})
		return nil, true
	case "!=", "<", ">":
		b.emit(Op{Kind: OpConstI32, I32: 0})
		return nil, true
	}
	return nil, false
}

// emitZeroConstForWidth pushes a zero constant whose IR
// width matches `intWidth` — i64 when 64, i32 otherwise.
// Used by the strength-reduction / self-identity folds to
// avoid mixing i32 and i64 on the operand stack on wasm.
func (b *builder) emitZeroConstForWidth(intWidth int) {
	if intWidth == 64 {
		b.emit(Op{Kind: OpConstI64, I64: 0})
		return
	}
	b.emit(Op{Kind: OpConstI32, I32: 0})
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
	// Emit: <ident> ; OpStrLen  (i.e. len(ident)). Routed
	// through the SSO seam so the inline-tag bit is honoured —
	// the old `const 4; sub; load` shape read from
	// `inline_value - 4` (garbage) for inline-tagged strings.
	if err := b.expr(ident); err != nil {
		return err, true
	}
	b.emit(Op{Kind: OpStrLen})
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

// mapKeyKindTag returns the runtime tag for the Map[K, V]
// instantiation's key type:
//
//	0 = i32-sized scalar (i32, u32, sub-i32 widths) — and
//	    wide scalars (i64 / u64 / f64) on natives where
//	    `usize` is 8 bytes so the key fits raw.
//	1 = string.
//	2 = wide-scalar-boxed: an i64 / u64 / f64 key on a
//	    target whose `usize` is narrower than the key
//	    (wasm32, ptrW=4). The IR boxes the key into a
//	    heap cell; the runtime's __map_hash / __map_lookup
//	    branches dereference the cell to hash / compare
//	    the underlying 8-byte value.
//
// Other key types (struct / enum / float-on-narrow-ptr)
// still aren't supported; they'd need their own runtime
// branches.
func mapKeyKindTag(t ast.Type, ptrW int) int32 {
	switch t.(type) {
	case ast.StringType:
		return 1
	}
	if isWideScalar(t) && ptrW < 8 {
		return 2
	}
	return 0
}

// mapValKindTag is mapKeyKindTag's V-side counterpart. 0 =
// i32-sized scalar (i32 / u32 / sub-i32); 1 = pointer-shaped
// (string / array / struct / enum / slice / tuple). Stored at
// buf+12 by map_new so `__map_values_impl` can size its
// snapshot array's element stride correctly on arm64 (4-byte
// for i32-V, 8-byte for pointer-V — surviving arm64-darwin's
// high heap). i64 / f64 V types still use the boxed-cell
// codepath (emitWideMapSet / emitWideMapGet) and never reach
// the value-column snapshot directly.
func mapValKindTag(t ast.Type) int32 {
	if ast.IsPointerType(t) {
		return 1
	}
	return 0
}

// callArgTypesFromSig builds the ArgTypes slice for an
// OpCallDirect / OpCallDirectPair from a callee's parameter
// list, padding with ast.NumberType{} when the IR pushed more
// args than the declared signature (e.g. `map_new`'s injected
// keyKind / valKind tags). Returns nil when sig has no params
// AND argc == 0 — the backend's nil-fast-path treats every
// arg as 1 slot, which is correct for the no-args case.
func callArgTypesFromSig(params []ast.Type, argc int) []ast.Type {
	if argc <= 0 {
		return nil
	}
	out := make([]ast.Type, argc)
	for i := 0; i < argc; i++ {
		if i < len(params) {
			out[i] = params[i]
		} else {
			out[i] = ast.NumberType{}
		}
	}
	return out
}

func (b *builder) call(n *ast.Call) error {
	// Captured-closure callee: closureconv rewrote a captured
	// function-typed name (param / outer var) inside this body
	// to a CaptureRef. Treat it as a function-typed value coming
	// from the env block — push the closure pair pointer, push
	// args, dispatch indirectly. The captured Type is the source
	// of truth for the call's signature (the closureconv pass
	// stamps it from the checker's resolved outer-scope type).
	if cr, ok := n.Callee.(*ast.CaptureRef); ok {
		ft, isFn := cr.Type.(*ast.FuncType)
		if !isFn {
			return fmt.Errorf("ir: captured callee %q is not function-typed", cr.Name)
		}
		for _, a := range n.Args {
			if err := b.expr(a); err != nil {
				return err
			}
		}
		if err := b.expr(cr); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: ft})
		return nil
	}
	// `(b.f)(args...)` where `b.f` is a struct field of FuncType:
	// the field load produces a closure pair pointer; OpCallIndirect
	// dispatches through the pair the same way function-typed
	// locals do. Without this dispatch the IR's call() guard
	// rejected the FieldAccess callee with `indirect call from
	// non-identifier expression`. The field's type comes from the
	// owning struct's declaration, looked up through fieldOwner.
	if fa, ok := n.Callee.(*ast.FieldAccess); ok {
		owner := b.fieldOwner(fa.Target)
		sd, sdOk := b.info.Structs[owner]
		var ft *ast.FuncType
		if sdOk {
			for _, f := range sd.Fields {
				if f.Name == fa.Field {
					if fnT, isFn := f.Type.(*ast.FuncType); isFn {
						ft = fnT
					}
					break
				}
			}
		}
		if ft != nil {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			if err := b.expr(fa); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: ft})
			return nil
		}
	}
	if _, ok := n.Callee.(*ast.Ident); !ok {
		return fmt.Errorf("ir: indirect call from non-identifier expression")
	}
	return b.callBody(n)
}

// callBody is the original b.call body — kept as a helper so
// future per-call wrapping (formerly state-rooted persistent-
// mode toggles) can be re-introduced without duplicating every
// call-lowering case.
func (b *builder) callBody(n *ast.Call) error {
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
	// `arr.push(v)` was rewritten by the checker to
	// `__method_Array_push(arr, v)` with the receiver's element
	// type stamped on `n.TypeArgs[0]`. Lower inline here — emit
	// alloc + memcpy + a width-correct tail store — instead of
	// dispatching through one of N per-stride lang-prelude
	// functions. The IR already knows the stride from
	// `ast.ElemSizeBytes(elemType)` and the right store op from
	// `payloadStoreOp(elemType)`; the previous shape compounded
	// boilerplate (5 prelude bodies + 5 mangled FuncSigs +
	// 5 codegen aliases + 5 treeshake aliases) per array
	// method. Inline lowering scales to one block of code per
	// method.
	if id.Name == "__method_Array_push" && len(n.Args) == 2 && len(n.TypeArgs) == 1 {
		return b.emitArrayPush(n)
	}
	// f32_bits / f32_from_bits: bit-level reinterpret between
	// i32 and f32. On native backends the bits stay put on the
	// operand stack so OpReinterpret* compiles to zero
	// instructions; wasm's typed stack needs a real
	// `i32.reinterpret_f32` / `f32.reinterpret_i32` op so the
	// stack-type changes. Either way the call disappears.
	if (id.Name == "f32_bits" || id.Name == "f32_from_bits") && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			if id.Name == "f32_bits" {
				b.emit(Op{Kind: OpReinterpretI32F32})
			} else {
				b.emit(Op{Kind: OpReinterpretF32I32})
			}
			return nil
		}
	}
	// `m.values()` on `Map[K, V]` where V is wide (i64 / u64 /
	// f64). Narrow V falls through to the normal
	// `__method_Map_values` call (codegen-aliased to the
	// `__map_values_impl` lang prelude function). Wide V needs
	// to follow each entry's cell pointer and copy the 8
	// payload bytes into a wide-stride result — emitted inline
	// here for the same reason as emitArrayPush: a single
	// codepath instead of a per-stride lang-prelude clone.
	if id.Name == "__method_Map_values" && len(n.Args) == 1 {
		recvType := b.exprType(n.Args[0])
		if st, ok := recvType.(ast.StructType); ok && len(st.Args) >= 2 {
			vType := st.Args[1]
			if isWideMapValueTypeIR(vType) {
				return b.emitWideMapValues(n, vType)
			}
		}
	}
	// `m.keys()` on `Map[K, V]` where K is wide (i64 / u64 /
	// f64). The prelude's `__map_keys_impl` uses a 4-byte
	// destStride which works for i32-K but truncates wide-K
	// values into the low 32 bits. We mirror emitWideMapValues
	// here, walking entries and producing a real wide-stride
	// `K[]`. On wasm32 (keyKind=2) the K slot stores a cell
	// pointer the IR follows via `__load_i64`; on natives
	// (keyKind=0, ptrW=8) the K slot stores the 8-byte value
	// raw and we load it directly.
	if id.Name == "__method_Map_keys" && len(n.Args) == 1 {
		recvType := b.exprType(n.Args[0])
		if st, ok := recvType.(ast.StructType); ok && len(st.Args) >= 1 {
			kType := st.Args[0]
			if isWideMapValueTypeIR(kType) {
				return b.emitWideMapKeys(n, kType)
			}
		}
	}
	// `len(x)` on a string, array, or slice is inlined. String and
	// array layouts carry a 4-byte little-endian length prefix at
	// `ptr - 4`; slice values carry the length at `slice + 4` after
	// the data pointer. Strings now route through OpStrLen so a
	// future small-string-optimisation pass can change the encoding
	// in one place instead of patching every backend's open-coded
	// `[ptr - 4]` load. Arrays keep the inline sub-4 / load shape
	// because their layout may diverge from strings later.
	//
	// The checker doesn't declare `len` as a function signature, so
	// the call falls here ahead of the FuncSigs / locals path.
	if id.Name == "len" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if _, isDeclared := b.info.FuncSigs[id.Name]; !isDeclared {
				// Compile-time fold: when the arg is a literal whose
				// length is statically known, collapse the whole
				// runtime-load sequence to a single const. Saves the
				// runtime alloc + prefix-load that the unfolded shape
				// would force, and lets the const propagate into
				// surrounding arithmetic.
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
				argT := b.exprType(n.Args[0])
				if _, isSlice := argT.(ast.SliceType); isSlice {
					b.emit(Op{Kind: OpConstI32, I32: 4})
					b.emit(Op{Kind: OpAdd})
					b.emit(Op{Kind: OpLoad})
				} else if _, isStr := argT.(ast.StringType); isStr {
					b.emit(Op{Kind: OpStrLen})
				} else {
					// Arrays (and the unknown / generic case) keep
					// the open-coded `[ptr - 4]` load — their layout
					// is decoupled from string SSO work.
					b.emit(Op{Kind: OpConstI32, I32: 4})
					b.emit(Op{Kind: OpSub})
					b.emit(Op{Kind: OpLoad})
				}
				return nil
			}
		}
	}
	// Map call-site boxing. Two axes:
	//
	//   - V needs boxing if it's a wide scalar (i64 / u64 / f64)
	//     on every target, or string V on wasm32 (`ptrW==4`).
	//     The wat helper sees all V as a single i32, so wide and
	//     string V values get alloc-and-stored into an 8-byte
	//     cell whose pointer is passed in the v slot. Reads
	//     follow the cell pointer back to the real value.
	//   - K needs boxing if K is string on wasm32 — same i32-
	//     slot constraint applies to the helper's k arg.
	//
	// Methods that return `Option[V]` or `V[]` need extra work to
	// translate the helper's i32-cell result into a real V; the
	// boxing-aware emitWideMap* helpers below do this. Methods
	// whose return type passes through unchanged (`set` void,
	// `has` / `delete` boolean, `get` when V is i32-scalar,
	// `get_or` when V is i32-scalar) flow through
	// emitStringKMapCall when only K needs boxing.
	needBoxK := len(n.TypeArgs) >= 1 && (isStringForBoxing(n.TypeArgs[0], b.ptrW) || mapKeyKindTag(n.TypeArgs[0], b.ptrW) == 2)
	needBoxV := len(n.TypeArgs) >= 2 && (isWideScalar(n.TypeArgs[1]) || isStringForBoxing(n.TypeArgs[1], b.ptrW))
	if needBoxK || needBoxV {
		switch id.Name {
		case "__method_Map_set":
			return b.emitWideMapSet(n, n.TypeArgs[0], n.TypeArgs[1])
		case "__method_Map_get":
			if needBoxV {
				return b.emitWideMapGet(n, n.TypeArgs[0], n.TypeArgs[1])
			}
			return b.emitStringKMapCall(n, n.TypeArgs[0], id.Name, 2)
		case "__method_Map_get_or":
			if needBoxV {
				return b.emitWideMapGetOr(n, n.TypeArgs[0], n.TypeArgs[1])
			}
			return b.emitStringKMapCall(n, n.TypeArgs[0], id.Name, 3)
		case "__method_Map_has", "__method_Map_delete":
			if needBoxK {
				return b.emitStringKMapCall(n, n.TypeArgs[0], id.Name, 2)
			}
			// Wide-V doesn't affect has/delete — fall through.
		case "__method_MapIter_value":
			if needBoxV {
				// MapIter.value() returns V — when boxed, the
				// wat helper hands back the cell pointer; unbox
				// via payloadLoadOpFor (wide scalar load on
				// natives, two-word string load on wasm32).
				for _, a := range n.Args {
					if err := b.expr(a); err != nil {
						return err
					}
				}
				b.emit(Op{Kind: OpCallDirect, Str: id.Name, I32: 1})
				b.emit(payloadLoadOpFor(n.TypeArgs[1], b.ptrW))
				return nil
			}
		case "__method_MapIter_key":
			if needBoxK {
				// String K on wasm32: the entries array stores
				// cell pointers; unbox to (data, len).
				for _, a := range n.Args {
					if err := b.expr(a); err != nil {
						return err
					}
				}
				b.emit(Op{Kind: OpCallDirect, Str: id.Name, I32: 1})
				b.emit(payloadLoadOpFor(n.TypeArgs[0], b.ptrW))
				return nil
			}
		}
	}
	for _, a := range n.Args {
		if err := b.expr(a); err != nil {
			return err
		}
	}
	argCount := int32(len(n.Args))
	// `map_new(cap)` is a generic builtin: the runtime helper
	// takes two extra runtime-tag args so the prelude can branch
	// without per-K/V monomorphisation. `keyKind` (i32-scalar vs
	// string) picks i32.eq vs strcmp on lookup; `valKind`
	// (i32-scalar vs pointer-shaped) sizes the .values()
	// snapshot's element stride correctly on arm64. The
	// Var-case destination inference stamped Call.TypeArgs[0]=K
	// and Call.TypeArgs[1]=V; translate both to their tags.
	if id.Name == "map_new" {
		var keyKind, valKind int32
		if len(n.TypeArgs) >= 1 {
			keyKind = mapKeyKindTag(n.TypeArgs[0], b.ptrW)
		}
		if len(n.TypeArgs) >= 2 {
			valKind = mapValKindTag(n.TypeArgs[1])
		}
		b.emit(Op{Kind: OpConstI32, I32: keyKind})
		b.emit(Op{Kind: OpConstI32, I32: valKind})
		argCount += 2
	}
	// Direct call if the name is a top-level / builtin function and not
	// shadowed by a local of the same name. Pair-form callees go
	// through OpCallDirectPair so the IR result is two i32s
	// (tag, payload) — each backend interprets that target-
	// appropriately:
	//   - wasm: function emits multi-value `(result i32 i32)`;
	//     the pair lands on the operand stack from the call.
	//   - natives: function emits the heap-box (current step-2
	//     fallback); OpCallDirectPair unpacks `[ptr+0]` (tag)
	//     and `[ptr+4]` (payload) onto the operand stack after
	//     the call, so the IR-level "two values post-call"
	//     contract holds across both backends.
	if sig, isFunc := b.info.FuncSigs[id.Name]; isFunc {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			kind := OpCallDirect
			width := 0
			if b.pairForm[id.Name] {
				kind = OpCallDirectPair
				// Carry the payload width on the call so the
				// consumer (OpCallDirectPair codegen) loads
				// from the right offset (+4 for i32 payload,
				// +8 for pointer-shape payload — matching the
				// maker's heap-box layout).
				width = b.payloadWidthForCalleeReturn(sig.Result)
			}
			// Stamp the callee's parameter types so the backend
			// can compute operand-stack slot counts under the
			// two-word string ABI without re-deriving them from
			// `Str`. `map_new` injected two trailing keyKind /
			// valKind i32s above; pad ArgTypes to match argCount
			// so the consumer can iterate freely. `sig.Params`
			// from FuncSigs is the source of truth for every
			// user-facing builtin (print / write_file / tcp_send
			// / __method_Writer_write / ...).
			argTypes := callArgTypesFromSig(sig.Params, int(argCount))
			b.emit(Op{Kind: kind, Str: id.Name, I32: argCount, Width: width, ArgTypes: argTypes})
			if kind == OpCallDirectPair && !b.suppressPairRebox {
				// Re-pack the (tag, payload) pair into a heap
				// box so existing callers (var assignment,
				// struct fields, etc.) keep seeing the heap-
				// pointer shape. The match-style scrutinee
				// path in IfLet / Match / LetElse sets
				// `suppressPairRebox` so the pair flows
				// straight from the call into the dispatch
				// without an alloc.
				if err := b.emitRepackPairAsHeapBox(width); err != nil {
					return err
				}
			}
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
		// the right per-stride helper, then a width-aware store.
		// Doesn't leave a value on the stack — exprLeavesValue
		// special-cases this shape so no drop is emitted by the
		// surrounding ExprStmt. Element-width choice mirrors the
		// read path: stride 1 / 2 / 4 / 8 picks helper +
		// `i32.store8` / `i32.store16` / `i32.store` /
		// `f32.store`. Float arrays use OpFStore.
		stride := int32(4)
		storeOp := OpStore
		storeWidth := 0
		if t.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(t.ElemType, b.ptrW))
			if nt, ok := t.ElemType.(ast.NumberType); ok {
				switch nt.NormalWidth() {
				case 8:
					storeOp = OpStoreI8
				case 16:
					storeOp = OpStoreI16
				case 64:
					storeWidth = 64
				}
			}
			if ast.IsPointerType(t.ElemType) {
				storeWidth = WidthPtr
			}
			if ft, ok := t.ElemType.(ast.FloatType); ok {
				storeOp = OpFStore
				if ft.NormalWidth() == 64 {
					storeWidth = 64
				}
			}
			// String elements: fan store out to two i32.store
			// calls on wasm via WidthString. Natives stay on
			// WidthPtr (single ptr-slot store).
			if _, isString := t.ElemType.(ast.StringType); isString && b.twoWordStrings() {
				storeWidth = WidthString
			}
		}
		var helper string
		if t.IsSlice {
			// Writing through a slice — bounds-check + offset
			// against the parent's storage. Per-stride
			// __slice_idx_N variants mirror the read path.
			switch stride {
			case 1:
				helper = "__slice_idx_1"
			case 2:
				helper = "__slice_idx_2"
			case 8:
				helper = "__slice_idx_8"
			case 16:
				helper = "__slice_idx_16"
			default:
				helper = "__slice_idx"
			}
		} else {
			switch stride {
			case 1:
				helper = "__arr_idx_1"
			case 2:
				helper = "__arr_idx_2"
			case 8:
				helper = "__arr_idx_8"
			case 16:
				helper = "__arr_idx_16"
			default:
				helper = "__arr_idx"
			}
		}
		if err := b.expr(t.Array); err != nil {
			return err
		}
		if err := b.expr(t.Idx); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
		if err := b.expr(n.Value); err != nil {
			return err
		}
		b.emit(Op{Kind: storeOp, Width: storeWidth})
		return nil
	case *ast.FieldAccess:
		// `p.field = v` lowers to base + offset; value; store.
		// Same no-result discipline as index assignment. Field
		// offsets + store widths come from the ptrW-aware
		// struct layout so pointer-typed fields land in
		// pointer-width slots on arm64.
		st := b.fieldOwner(t.Target)
		sd, ok := b.info.Structs[st]
		if !ok {
			return fmt.Errorf("ir: field assignment on unresolved struct %q", st)
		}
		offs, _ := structFieldLayout(sd.Fields, b.ptrW)
		off := int32(-1)
		var ft ast.Type
		for _, f := range sd.Fields {
			if f.Name == t.Field {
				off = offs[f.Name]
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
			b.emit(Op{Kind: OpConstI32, I32: off})
			b.emit(Op{Kind: OpAdd})
		}
		if err := b.expr(n.Value); err != nil {
			return err
		}
		b.emit(payloadStoreOpFor(ft, b.ptrW))
		return nil
	}
	if cr, ok := n.Target.(*ast.CaptureRef); ok {
		// `cap = v` inside a closure body, where `cap` is a
		// captured outer-scope variable. closureconv rewrote the
		// target ident to a CaptureRef during the body walk. The
		// env block is heap-allocated and shared by all calls to
		// this closure — mutation persists across re-invocations,
		// matching the user-expected "captures live in the closure's
		// environment" semantics. Outer-scope reads of the same
		// name AFTER closure construction see the original
		// (pre-capture) value — captures are by value at make-
		// time. No tee: assignment is statement-shaped; the
		// exprLeavesValue dispatch's default (false for non-Ident
		// targets) already suppresses the surrounding ExprStmt's
		// drop.
		envIdx, ok := b.locals["__env"]
		if !ok {
			return fmt.Errorf("ir: capture assignment %q in function without __env param (compiler bug)", cr.Name)
		}
		b.emit(Op{Kind: OpLoadLocal, I32: envIdx})
		b.emit(Op{Kind: OpConstI32, I32: int32(cr.Offset)})
		b.emit(Op{Kind: OpAdd})
		if err := b.expr(n.Value); err != nil {
			return err
		}
		b.emit(payloadStoreOpFor(cr.Type, b.ptrW))
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

// payloadSlotSize returns how many bytes a variant payload of
// the given type consumes inside an enum's heap object. Wide
// scalars (i64 / u64 / f64) take 8 bytes. Pointer-shaped
// payloads (string / array / struct / enum / slice / tuple /
// closure) take `ptrW` bytes — 4 on wasm32, 8 on arm64 (so
// arm64-darwin's >= 4 GiB heap addresses survive). Other
// scalars (i32 / u32 / sub-i32 / f32 / bool) take 4 bytes.
func payloadSlotSize(t ast.Type, ptrW int) int32 {
	if t == nil {
		return 4
	}
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return 8
	}
	if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
		return 8
	}
	if _, isString := t.(ast.StringType); isString {
		return stringSlotSize(ptrW)
	}
	if ast.IsPointerType(t) {
		return int32(ptrW)
	}
	return 4
}

// stringSlotSize returns the per-string storage size in bytes
// for a struct field / variant payload / function-arg slot.
// Centralises the slot-size decision so the SSO two-word flip
// (see docs/SSO-TWOWORD-EXEC.md) can be deferred per target.
//
// Wasm32 (`ptrW == 4`): returns 8. Two-word ABI is live; a
// string occupies two i32 slots `(data, len)`.
//
// Natives (`ptrW == 8`): returns 8. Native backends still use
// the single-i8-pointer-slot LSB-tagged inline encoding; one
// 8-byte slot is enough. When the natives flip to the two-word
// ABI (see `docs/SSO-NATIVE-FLIP-STATUS.md`), this branch
// returns `2 * ptrW = 16`.
//
// Both targets end up at 8 bytes per slot today — wasm via the
// two-word fan-out, natives via the existing pointer width.
// Centralising here means each backend can flip independently
// without disturbing the other.
func stringSlotSize(ptrW int) int32 {
	if useTwoWordStrings(ptrW) {
		return int32(2 * ptrW)
	}
	return int32(ptrW)
}

// useTwoWordStrings is the standalone-helper companion to
// `(b *builder) twoWordStrings()`. Routes through the
// canonical `ast.UseTwoWordStrings` (lives in the `ast`
// package so it's reachable from both `internal/ir` and
// `ast.ElemSizeBytesFor`'s string branch). Naming the seam
// means the eventual flip for arm64 is a one-line change in
// `ast.UseTwoWordStrings` instead of a grep across helpers.
func useTwoWordStrings(ptrW int) bool {
	return ast.UseTwoWordStrings(ptrW)
}

// isWideScalar reports whether `t` is a 64-bit numeric or
// float — the trigger for the wide-payload + boxed-Map-V code
// paths.
func isWideScalar(t ast.Type) bool {
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return true
	}
	if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
		return true
	}
	return false
}

// payloadLayout computes the per-slot byte offsets and the
// total enum heap size for a variant whose payloads have the
// given types. The tag occupies the first 4 bytes (offset 0);
// payload slots follow in order. Wide (8-byte) slots —
// including pointer-typed payloads on arm64 (ptrW=8) — are
// aligned to a multiple of 8 from the object base, which
// matches the natural alignment of `str x` / `i64.store`.
// Returned offsets are addresses relative to the variant
// pointer (i.e. payload[0] starts at offset 4 for a 4-byte
// payload, or 8 if the first payload is wide).
// structFieldLayout packs `fields` in declaration order, using
// `payloadSlotSize` (which is ptrW-aware) per field. Wide
// fields (i64 / f64 / pointer on arm64) are aligned to a
// multiple of 8 so str x / ldr x land on aligned addresses.
// Returned map is field-name → offset; second return is the
// total struct size.
func structFieldLayout(fields []ast.Param, ptrW int) (map[string]int32, int32) {
	offs := make(map[string]int32, len(fields))
	pos := int32(0)
	for _, f := range fields {
		size := payloadSlotSize(f.Type, ptrW)
		if size == 8 && pos%8 != 0 {
			pos += 4
		}
		offs[f.Name] = pos
		pos += size
	}
	return offs, pos
}

// tupleElemLayout is the tuple counterpart of structFieldLayout.
// Anonymous positional layout — element i lives at the returned
// `offs[i]`. Same packing + alignment rules as structs.
func tupleElemLayout(elems []ast.Type, ptrW int) ([]int32, int32) {
	offs := make([]int32, len(elems))
	pos := int32(0)
	for i, t := range elems {
		size := payloadSlotSize(t, ptrW)
		if size == 8 && pos%8 != 0 {
			pos += 4
		}
		offs[i] = pos
		pos += size
	}
	return offs, pos
}

func payloadLayout(types []ast.Type, count int, ptrW int) ([]int32, int32) {
	offsets := make([]int32, count)
	pos := int32(4)
	for i := 0; i < count; i++ {
		var t ast.Type
		if i < len(types) {
			t = types[i]
		}
		size := payloadSlotSize(t, ptrW)
		// Align multi-byte values to 8. Covers i64 / f64
		// (size=8) and string under two-word (size=16 on
		// arm64). Smaller sizes (1/2/4) stay where they
		// are.
		if size >= 8 && pos%8 != 0 {
			pos += 4
		}
		offsets[i] = pos
		pos += size
	}
	return offsets, pos
}

// pairPayloadWidth returns the `Op.Width` to set on
// OpMakeSomeI32 / OpMakeOkI32 / OpMakeErrI32 for a payload of
// type t. `WidthPtr` for pointer-shaped values (the native
// maker handler reads this to alloc 16 bytes and emit an
// 8-byte payload store at offset 8 — matching
// `payloadLayout(Option[T])` for pointer T); `0` (i32-default)
// otherwise. Wasm ignores the field — pointer = i32 on
// wasm32, so 4-byte stores work uniformly.
func pairPayloadWidth(t ast.Type) int {
	if t == nil {
		return 0
	}
	if n, ok := t.(ast.NumberType); ok && n.IsPointerWidth() {
		return WidthPtr
	}
	if ast.IsPointerType(t) {
		return WidthPtr
	}
	return 0
}

// payloadStoreOp returns the IR Op that stores a value of the
// given payload type to a heap slot — paired with payloadLoadOp
// for the symmetric load. Pointer-shaped payloads emit
// `Width: WidthPtr` so the backend picks 4-byte (wasm32) or
// 8-byte (arm64) stores per its native heap-pointer width.
func payloadStoreOp(t ast.Type) Op {
	return payloadStoreOpFor(t, 4)
}

// payloadStoreOpFor is the ptrW-aware variant. Returns
// `Width: WidthString` for string types on wasm32 (ptrW=4) so
// the wasm OpStore handler fans out to two i32.store calls; on
// natives (ptrW=8) the native backends still use the single-
// pointer-slot LSB-tagged form, so it returns `Width: WidthPtr`.
func payloadStoreOpFor(t ast.Type, ptrW int) Op {
	if isFloat(t) {
		w := 0
		if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
			w = 64
		}
		return Op{Kind: OpFStore, Width: w}
	}
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return Op{Kind: OpStore, Width: 64}
	}
	if _, isString := t.(ast.StringType); isString {
		if useTwoWordStrings(ptrW) {
			return Op{Kind: OpStore, Width: WidthString}
		}
		return Op{Kind: OpStore, Width: WidthPtr}
	}
	if ast.IsPointerType(t) {
		return Op{Kind: OpStore, Width: WidthPtr}
	}
	return Op{Kind: OpStore}
}

// arrayElemStoreOp picks the right store op for an array
// element of type `t`, using element stride to dispatch:
//   - 1-byte (u8 / i8 / bool)            → i32.store8
//   - 2-byte (u16 / i16)                 → i32.store16
//   - 4-byte (i32 / u32 / f32)           → i32.store / f32.store
//   - 8-byte (i64 / u64 / f64)           → i64.store / f64.store
//   - pointer (string / array / struct /
//     enum / slice / tuple / closure)    → OpStore Width:WidthPtr
//     (4-byte on wasm32, 8-byte on arm64)
// Pairs with the symmetric read in array-indexing lowering.
func arrayElemStoreOp(t ast.Type) Op {
	return arrayElemStoreOpFor(t, 4)
}

// arrayElemStoreOpFor is the ptrW-aware variant. Strings on
// wasm32 (ptrW=4) return `Width: WidthString` for two-word
// element stores; on natives they fall back to WidthPtr.
func arrayElemStoreOpFor(t ast.Type, ptrW int) Op {
	if isFloat(t) {
		w := 0
		if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
			w = 64
		}
		return Op{Kind: OpFStore, Width: w}
	}
	if _, isString := t.(ast.StringType); isString {
		if useTwoWordStrings(ptrW) {
			return Op{Kind: OpStore, Width: WidthString}
		}
		return Op{Kind: OpStore, Width: WidthPtr}
	}
	if ast.IsPointerType(t) {
		return Op{Kind: OpStore, Width: WidthPtr}
	}
	// Scalar-only switch (ptrW-independent; sub-i32 + wide).
	switch ast.ElemSizeBytes(t) {
	case 1:
		return Op{Kind: OpStoreI8}
	case 2:
		return Op{Kind: OpStoreI16}
	case 8:
		return Op{Kind: OpStore, Width: 64}
	}
	return Op{Kind: OpStore}
}

func payloadLoadOp(t ast.Type) Op {
	return payloadLoadOpFor(t, 4)
}

// payloadLoadOpFor is the ptrW-aware variant of payloadLoadOp.
// String types on wasm32 return `Width: WidthString` (two-load
// fan-out at offsets +0/+4); on natives they return WidthPtr.
func payloadLoadOpFor(t ast.Type, ptrW int) Op {
	if isFloat(t) {
		w := 0
		if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
			w = 64
		}
		return Op{Kind: OpFLoad, Width: w}
	}
	if _, isString := t.(ast.StringType); isString {
		if useTwoWordStrings(ptrW) {
			return Op{Kind: OpLoad, Width: WidthString}
		}
		return Op{Kind: OpLoad, Width: WidthPtr}
	}
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return Op{Kind: OpLoad, Width: 64}
	}
	if ast.IsPointerType(t) {
		return Op{Kind: OpLoad, Width: WidthPtr}
	}
	return Op{Kind: OpLoad}
}


// isWideMapValueTypeIR matches the checker's
// isWideMapValueType — V types that the Map runtime stores via
// the cell-pointer boxing path (i64 / u64 / f64). Reproduced
// here to avoid an IR → checker import cycle.
func isWideMapValueTypeIR(t ast.Type) bool {
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return true
	}
	if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
		return true
	}
	return false
}

// emitWideMapValues lowers `m.values()` when V is wide (i64 /
// u64 / f64). Each entry's V slot in the map's entries table
// holds a 4-byte pointer to a heap-allocated 8-byte cell — the
// boxing path the get / get_or / set helpers use to keep the
// wat-side runtime i32-shaped. To produce a real wide-stride
// V[] we walk the entries, follow each cell pointer, and
// `__memcpy` the 8 payload bytes into the result.
//
// Type-erased: i64 and f64 share an 8-byte memcpy, so one
// codepath handles both — the result type at the lang layer is
// `V[]` (substituted at the call site).
func (b *builder) emitWideMapValues(n *ast.Call, vType ast.Type) error {
	_ = vType // bit-identical 8-byte copy regardless of i64 vs f64

	// Stash the map handle: m holds an i32 pointing to the
	// Map's outer struct (one indirection above buf).
	mSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_m_%d", mSlot)] = mSlot
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: mSlot})

	// buf = i32.load(m); cap = i32.load(buf); len = i32.load(buf + 4)
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpLoadLocal, I32: mSlot})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})

	capSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_cap_%d", capSlot)] = capSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: capSlot})

	lenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_len_%d", lenSlot)] = lenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: lenSlot})

	// entriesBase = buf + 16 + cap * 4
	entriesSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_entries_%d", entriesSlot)] = entriesSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: 16})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: capSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: entriesSlot})

	// Per-entry stride + V-slot offset come from __ptr_width()
	// so the same IR works on wasm32 (4-byte ptr → stride 8,
	// V-offset 4) and arm64 (8-byte ptr → stride 16, V-offset
	// 8). Matches the prelude Map runtime's layout exactly.
	ptrWSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_ptrw_%d", ptrWSlot)] = ptrWSlot
	b.emit(Op{Kind: OpCallDirect, Str: "__ptr_width", I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: ptrWSlot})

	strideSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_stride_%d", strideSlot)] = strideSlot
	b.emit(Op{Kind: OpLoadLocal, I32: ptrWSlot})
	b.emit(Op{Kind: OpConstI32, I32: 2})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpStoreLocal, I32: strideSlot})

	// arr = __alloc(4 + len * 8); *arr = len; data = arr + 4
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpAlloc})
	hdrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_hdr_%d", hdrSlot)] = hdrSlot
	b.emit(Op{Kind: OpStoreLocal, I32: hdrSlot})

	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})

	dataSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_data_%d", dataSlot)] = dataSlot
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})

	// for (i = 0; i < len; i++) {
	//   cell = i32.load(entriesBase + i * 8 + 4)
	//   __memcpy(data + i * 8, cell, 8)
	// }
	iSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_i_%d", iSlot)] = iSlot
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: iSlot})

	b.openBlock(BlockTypeVoid)
	loopExitD := b.depth
	b.openLoop(BlockTypeVoid)
	loopBodyD := b.depth

	// if (i >= len) br loopExit
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpGeS})
	b.brTo(loopExitD, true)

	// cell = __load_ptr(entriesBase + i * stride + ptrW)
	// — V slot is pointer-width on arm64 (stores the cell ptr
	// without truncating); on wasm32 stride=8, ptrW=4 so this
	// matches the original i32.load.
	b.emit(Op{Kind: OpLoadLocal, I32: entriesSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: strideSlot})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: ptrWSlot})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpCallDirect, Str: "__load_ptr", I32: 1})
	cellSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_cell_%d", cellSlot)] = cellSlot
	b.emit(Op{Kind: OpStoreLocal, I32: cellSlot})

	// __memcpy(data + i * 8, cell, 8)
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpCallDirect, Str: "__memcpy", I32: 3})

	// i++
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: iSlot})

	b.brTo(loopBodyD, false)
	b.closeScope() // loop
	b.closeScope() // outer block

	// Result: data pointer.
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	return nil
}

// emitWideMapKeys lowers `m.keys()` when K is wide (i64 / u64 /
// f64). Mirror of emitWideMapValues, but pulls from each
// entry's K slot (offset 0) instead of V (offset ptrW). The
// storage layout differs by target:
//
//	wasm32 (keyKind=2, ptrW=4): K slot holds a 4-byte cell
//	  pointer. We load the cell ptr, then `__load_i64` the
//	  underlying 8-byte value out of the cell.
//	natives (keyKind=0, ptrW=8): K slot holds the raw
//	  8-byte value. We load it directly via `__load_i64`
//	  (which is a single ldr/mov on the matching asm stubs).
//
// Either way we end up with the 8-byte key value, which we
// store into a wide-stride `K[]`.
func (b *builder) emitWideMapKeys(n *ast.Call, kType ast.Type) error {
	_ = kType // 8-byte bit-identical copy regardless of i64 / u64 / f64

	mSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_m_%d", mSlot)] = mSlot
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: mSlot})

	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpLoadLocal, I32: mSlot})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})

	capSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_cap_%d", capSlot)] = capSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: capSlot})

	lenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_len_%d", lenSlot)] = lenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: lenSlot})

	// entriesBase = buf + 16 + cap * 4
	entriesSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_entries_%d", entriesSlot)] = entriesSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: 16})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: capSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: entriesSlot})

	// stride = 2 * __ptr_width()  — runtime, target-agnostic.
	ptrWSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_ptrw_%d", ptrWSlot)] = ptrWSlot
	b.emit(Op{Kind: OpCallDirect, Str: "__ptr_width", I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: ptrWSlot})

	strideSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_stride_%d", strideSlot)] = strideSlot
	b.emit(Op{Kind: OpLoadLocal, I32: ptrWSlot})
	b.emit(Op{Kind: OpConstI32, I32: 2})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpStoreLocal, I32: strideSlot})

	// `i64[]` layout has headerBytes = max(4, stride) = 8 on
	// natives; wasm32 ignores stride alignment so 4 also
	// works there. Use the canonical `max(4, 8) = 8` for both
	// so the array layout matches what ArrayLit would emit
	// (data pointer 8-byte stride-aligned). Length prefix
	// lives at `data - 4`.
	hdrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_hdr_%d", hdrSlot)] = hdrSlot
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: hdrSlot})

	// *(hdr + 4) = len  — length prefix at data - 4.
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})

	dataSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_data_%d", dataSlot)] = dataSlot
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})

	iSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_i_%d", iSlot)] = iSlot
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: iSlot})

	b.openBlock(BlockTypeVoid)
	loopExitD := b.depth
	b.openLoop(BlockTypeVoid)
	loopBodyD := b.depth

	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpGeS})
	b.brTo(loopExitD, true)

	// On wasm32 K is boxed (the K slot holds a 4-byte cell
	// pointer): __memcpy(data + i*8, cellPtr, 8). On natives
	// K is stored raw in an 8-byte slot at the entries base:
	// __memcpy(data + i*8, entriesBase + i*stride, 8).
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})

	// Push the source address. The K slot is at offset 0 of
	// the entry. For keyKind=2, follow the cell pointer; for
	// keyKind=0 raw natives, use the entry slot address itself.
	b.emit(Op{Kind: OpLoadLocal, I32: entriesSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: strideSlot})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	if mapKeyKindTag(kType, b.ptrW) == 2 {
		b.emit(Op{Kind: OpCallDirect, Str: "__load_ptr", I32: 1})
	}
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpCallDirect, Str: "__memcpy", I32: 3})

	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: iSlot})

	b.brTo(loopBodyD, false)
	b.closeScope() // loop
	b.closeScope() // outer block

	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	return nil
}

// emitArrayPush lowers `arr.push(v)` inline. Shape mirrors the
// pre-refactor lang-prelude `__array_append_*` family —
//
//	hdr  = __alloc(4 + (oldLen+1) * stride)
//	*hdr = oldLen + 1                     // length prefix
//	data = hdr + 4
//	memcpy(data, arr, oldLen * stride)    // copy existing
//	*(data + oldLen * stride) = v         // append the new tail
//	return data
//
// — but emits the IR ops directly so a single block of code
// covers every stride class (1 / 2 / 4 / 8 bytes), removing
// the per-stride prelude duplication. The element type is
// `n.TypeArgs[0]`, stamped by the checker's array.push
// dispatch.
func (b *builder) emitArrayPush(n *ast.Call) error {
	elemType := n.TypeArgs[0]
	stride := int32(ast.ElemSizeBytesFor(elemType, b.ptrW))
	if stride == 0 {
		stride = 4
	}
	// Same alignment rule as the array literal lowering:
	// length prefix is always at `data - 4`, but the FIRST
	// element must be stride-aligned when stride > 4 so Apple
	// Silicon's strict 8-byte LDR/STR alignment is satisfied.
	headerBytes := int32(4)
	if stride > 4 {
		headerBytes = stride
	}

	// Stash v in a typed scratch so the tail store can pick the
	// right load width (i64 / f64 vs i32). Without this the
	// `local.get` would have to know whether to push an i64 or
	// i32 — typed scratches make that automatic.
	vSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_v_%d", vSlot)] = vSlot
	b.scratchType[vSlot] = elemType
	if err := b.expr(n.Args[1]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: vSlot})

	// Stash arr (i32 heap pointer). Used twice — for the
	// length prefix lookup (arr - 4) and as memcpy source.
	arrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_arr_%d", arrSlot)] = arrSlot
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: arrSlot})

	// oldLen = i32.load(arr - 4)
	oldLenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_oldLen_%d", oldLenSlot)] = oldLenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: oldLenSlot})

	// allocSize = headerBytes + (oldLen + 1) * stride
	b.emit(Op{Kind: OpConstI32, I32: headerBytes})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpAlloc})
	hdrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_hdr_%d", hdrSlot)] = hdrSlot
	b.emit(Op{Kind: OpStoreLocal, I32: hdrSlot})

	// *(hdr + headerBytes - 4) = oldLen + 1 — length prefix
	// always lives at `data - 4` regardless of padding.
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	if headerBytes != 4 {
		b.emit(Op{Kind: OpConstI32, I32: headerBytes - 4})
		b.emit(Op{Kind: OpAdd})
	}
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStore})

	// data = hdr + headerBytes
	dataSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_data_%d", dataSlot)] = dataSlot
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: headerBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})

	// memcpy(data, arr, oldLen * stride)
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpCallDirect, Str: "__memcpy", I32: 3})

	// *(data + oldLen * stride) = v   (width-correct store)
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: vSlot})
	b.emit(arrayElemStoreOpFor(elemType, b.ptrW))

	// Result: data pointer (the array value).
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	return nil
}

// isStringForBoxing reports whether `t` is a string type that
// needs cell-pointer boxing across the Map runtime boundary on
// this target. Triggered only on wasm32 (`ptrW == 4`), where the
// two-word `(data, len)` string ABI doesn't fit the helper's
// i32-shaped K / V slots; boxing alloc-and-stores the pair into
// an 8-byte cell so the helper sees a single i32 cell pointer.
// Natives keep the single-pointer string layout, so no boxing is
// needed there.
func isStringForBoxing(t ast.Type, ptrW int) bool {
	if !useTwoWordStrings(ptrW) {
		return false
	}
	_, ok := t.(ast.StringType)
	return ok
}

// boxIntoCell allocates an 8-byte cell, evaluates `arg`, stores
// it into the cell via the type-correct payloadStoreOp, and
// leaves the cell pointer on the operand stack. Used by the
// Map-method boxing helpers to widen 2-word strings (and wide
// scalars) into a single i32 the helper can carry through its
// entries array.
func (b *builder) boxIntoCell(arg ast.Expr, t ast.Type, slotLabel string) error {
	cellSlot := b.allocSlot()
	b.locals[fmt.Sprintf("%s_%d", slotLabel, cellSlot)] = cellSlot
	// Cell size matches the value's slot size: 8 bytes for
	// wide scalars (i64 / u64 / f64) on every target; 8 bytes
	// for strings on wasm32 (two i32 slots); 16 bytes for
	// strings on arm64 under two-word (two i64 slots).
	b.emit(Op{Kind: OpConstI32, I32: payloadSlotSize(t, b.ptrW)})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: cellSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	if err := b.expr(arg); err != nil {
		return err
	}
	b.emit(payloadStoreOpFor(t, b.ptrW))
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	return nil
}

// pushMapMethodArg evaluates one argument to a Map method,
// boxing it if its declared type needs cell-pointer indirection
// across the helper boundary. `shouldBox` is the caller's
// per-arg decision (string K on wasm32, wide / string V).
func (b *builder) pushMapMethodArg(arg ast.Expr, t ast.Type, shouldBox bool, slotLabel string) error {
	if shouldBox {
		return b.boxIntoCell(arg, t, slotLabel)
	}
	return b.expr(arg)
}

// emitWideMapSet lowers `m.set(k, v)` when K or V needs cell-
// pointer boxing across the Map helper boundary — wide V
// (i64 / u64 / f64) on every target, and string K / V on wasm32
// (the two-word ABI doesn't fit the helper's i32 K/V slots).
// Each boxed arg is alloc-and-stored into an 8-byte cell whose
// pointer is passed through; the entries array ends up holding
// the cell pointers, transparent to the helper. Pairs with
// emitWideMapGet on the read side.
func (b *builder) emitWideMapSet(n *ast.Call, kType, vType ast.Type) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	boxK := isStringForBoxing(kType, b.ptrW) || mapKeyKindTag(kType, b.ptrW) == 2
	if err := b.pushMapMethodArg(n.Args[1], kType, boxK, "__map_set_kbox"); err != nil {
		return err
	}
	boxV := isWideScalar(vType) || isStringForBoxing(vType, b.ptrW)
	if err := b.pushMapMethodArg(n.Args[2], vType, boxV, "__map_set_vbox"); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_set", I32: 3})
	return nil
}

// emitWideMapGet lowers `m.get(k)` when V needs boxing — wide
// scalar (i64 / u64 / f64) on every target, or string V on
// wasm32. The wat helper returns an `Option<i32>` (4-byte
// payload = the boxed cell pointer). We translate that to a
// fresh `Option<V>` heap-box with the user-expected payload
// shape so the surrounding match / let-binding sees the
// substituted V type. Variant indices: Some = 0, None = 1
// (the auto-injected order in checker.builtinEnumDecls). K is
// also boxed when it's a string on wasm32.
func (b *builder) emitWideMapGet(n *ast.Call, kType, vType ast.Type) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	boxK := isStringForBoxing(kType, b.ptrW) || mapKeyKindTag(kType, b.ptrW) == 2
	if err := b.pushMapMethodArg(n.Args[1], kType, boxK, "__map_get_kbox"); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get", I32: 2})
	optPtrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__map_get_optptr_%d", optPtrSlot)] = optPtrSlot
	b.emit(Op{Kind: OpStoreLocal, I32: optPtrSlot})
	resultSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__map_get_res_%d", resultSlot)] = resultSlot
	// `if` runs the then-arm when the i32 cond is non-zero.
	// Some has tag 0, so we'd want eq-zero before the if to
	// route Some → then-arm; doing the equivalent by routing
	// Some → else-arm (no extra eqz op) keeps the IR shorter.
	b.emit(Op{Kind: OpLoadLocal, I32: optPtrSlot})
	b.emit(Op{Kind: OpLoad}) // tag at +0
	b.emit(Op{Kind: OpIf, I32: int32(BlockTypeVoid)})
	// --- tag != 0 (None on this side): 4-byte tag-only Option.
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1}) // tag = None
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpElse})
	// --- tag == 0 (Some): build a wide-payload Option<V>.
	offsets, size := payloadLayout([]ast.Type{vType}, 1, b.ptrW)
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: 0}) // tag = Some
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: offsets[0]})
	b.emit(Op{Kind: OpAdd})
	// load cell pointer from helper's Option<i32> payload, then
	// load wide V out of the cell.
	b.emit(Op{Kind: OpLoadLocal, I32: optPtrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoad}) // cell pointer
	b.emit(payloadLoadOpFor(vType, b.ptrW))
	b.emit(payloadStoreOpFor(vType, b.ptrW))
	b.emit(Op{Kind: OpEnd})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	return nil
}

// emitStringKMapCall boxes a string K argument and emits a
// regular call to the Map helper. Used for methods whose return
// shape passes through unchanged — `set` (void), `has` /
// `delete` (boolean), `get` when V is i32-scalar (Option[i32]),
// `get_or` when V is i32-scalar (i32). Args after the boxed K
// flow through normally — they're scalar in every case this
// helper handles.
//
// `argCount` is the IR-visible argument count (m + k + …).
func (b *builder) emitStringKMapCall(n *ast.Call, kType ast.Type, methodName string, argCount int32) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	if err := b.boxIntoCell(n.Args[1], kType, "__map_kbox"); err != nil {
		return err
	}
	for i := 2; i < len(n.Args); i++ {
		if err := b.expr(n.Args[i]); err != nil {
			return err
		}
	}
	b.emit(Op{Kind: OpCallDirect, Str: methodName, I32: argCount})
	return nil
}

// emitWideMapGetOr lowers `m.get_or(k, fallback)` when K and/or
// V needs cell-pointer boxing. The fallback is boxed inline (so
// the entries array sees a cell ptr the helper can carry), and
// the helper's i32 result is unboxed back into the user-shaped
// value — that result is the cell pointer the entries array was
// holding (or our just-allocated fallback cell on a miss).
func (b *builder) emitWideMapGetOr(n *ast.Call, kType, vType ast.Type) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	boxK := isStringForBoxing(kType, b.ptrW) || mapKeyKindTag(kType, b.ptrW) == 2
	if err := b.pushMapMethodArg(n.Args[1], kType, boxK, "__map_or_kbox"); err != nil {
		return err
	}
	if err := b.boxIntoCell(n.Args[2], vType, "__map_or_box"); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get_or", I32: 3})
	b.emit(payloadLoadOpFor(vType, b.ptrW))
	return nil
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
