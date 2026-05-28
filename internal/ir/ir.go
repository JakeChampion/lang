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
	OpReinterpretI64F64 // (f64) → i64 (bits, IEEE-754 layout)
	OpReinterpretF64I64 // (i64) → f64 (bits, IEEE-754 layout)

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
	case OpReinterpretI64F64:
		return "i64.reinterpret_f64"
	case OpReinterpretF64I64:
		return "f64.reinterpret_i64"
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
	// Captures lists the variables a hoisted closure target
	// captures, in declaration order. Empty for non-closure
	// functions. Used by codegen to size the env block + decide
	// per-capture stride (i32 / i64 / 2-word string ABI).
	Captures []ast.Param
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
	// Per-closure capture lists (closureconv stamps Captures on each
	// hoisted FuncDecl). Threaded into lowering so emitDec can route a
	// closure local's drop to its per-closure __closure_drop_<name>
	// thunk, and so the post-pass below knows which thunks to emit.
	closureCaps := map[string][]ast.Param{}
	for _, fn := range prog.Funcs {
		if len(fn.Captures) > 0 {
			closureCaps[fn.Name] = fn.Captures
		}
	}
	out := &Program{PairForm: pairForm, PtrW: ptrW}
	// Registry of generic-enum-instantiation drops discovered while
	// routing nested fields/payloads/captures (see builder.genEnumDrops).
	// Shared across lowering and the post-pass drop worklist below.
	genEnumDrops := map[string]*ast.EnumDecl{}
	for _, fn := range prog.Funcs {
		f, err := lowerFunc(fn, info, ptrW, pairForm, closureCaps, genEnumDrops)
		if err != nil {
			return nil, err
		}
		out.Funcs = append(out.Funcs, f)
	}
	// Closure reclamation Stage 3: emit a per-closure
	// __closure_drop_<name> thunk for every closure with rc-tracked
	// captures (arrays/structs/enums/closures — the ones inc'd at
	// MakeEnv). emitDec dispatches owned closure drops to these.
	if ast.RcFreeEnabled {
		for name, caps := range closureCaps {
			thunk := genClosureDropThunk(name, caps, ptrW, info, genEnumDrops)
			if thunk == nil {
				continue
			}
			// Codegen pairs prog.Funcs[i] (AST) with ip.Funcs[i] (IR)
			// by index and emits from the IR ops, so append a stub AST
			// decl in lockstep (same index) for the synthetic thunk —
			// it carries the name/params the prologue needs; the body
			// is unused (emission reads the IR Func's ops).
			stub := &ast.FuncDecl{
				Name:       thunk.Name,
				Params:     thunk.Params,
				ReturnType: thunk.ReturnType,
				Body:       &ast.Block{},
			}
			prog.Funcs = append(prog.Funcs, stub)
			out.Funcs = append(out.Funcs, thunk)
		}
	}
	// Transitive reclamation (Stages A + B): generate the recursive drop
	// functions that the lowered bodies CALL.
	//
	//   - __drop_struct_<N> (Stage A): a nested concrete-struct field
	//     recurses through this instead of a flat one-level rc_dec, so
	//     nested struct boxes reclaim on the owning value's last
	//     reference (dropStructField / appendChildDrop emit the call).
	//   - __drop_arr_struct_<Elem> (Stage B): an eligible array-of-struct
	//     drop recurses through this loop (calling __drop_struct_<Elem>
	//     per element, then __fern_arr_dec for the buffer) instead of the
	//     flat-element __fern_drop_arr_ptr (decValueOnStack emits it).
	//
	// Generation is driven by the calls actually emitted — collect every
	// __drop_struct_ / __drop_arr_struct_ callee name from the lowered
	// ops, then generate each (a generated body may call further drop
	// fns, so it's a worklist). Deriving the set from emitted calls keeps
	// generation and routing in exact agreement on names (notably
	// module-mangled ones like `parser__MatchArm`), which a separate
	// type-scan got wrong. Each fn is_unique-gates internally, so calling
	// it on a shared child/element is safe; deep recursion is only ever
	// emitted in the free-eligible drop branch.
	if ast.RcFreeEnabled {
		generated := map[string]bool{}
		queued := map[string]bool{}
		var work []string
		enqueueCalls := func(ops []Op) {
			for _, op := range ops {
				if op.Kind != OpCallDirect {
					continue
				}
				if (strings.HasPrefix(op.Str, "__drop_struct_") ||
					strings.HasPrefix(op.Str, "__drop_arr_struct_") ||
					strings.HasPrefix(op.Str, "__drop_map_via_") ||
					op.Str == "__drop_map_str_values" ||
					op.Str == "__drop_map_str_keys" ||
					strings.HasPrefix(op.Str, "__drop_enum_")) && !queued[op.Str] {
					queued[op.Str] = true
					work = append(work, op.Str)
				}
			}
		}
		for _, fn := range out.Funcs {
			enqueueCalls(fn.Ops)
		}
		for i := 0; i < len(work); i++ {
			name := work[i]
			if generated[name] {
				continue
			}
			var fn *Func
			if elem := strings.TrimPrefix(name, "__drop_arr_struct_"); elem != name {
				fn = genArrStructDropFn(elem, ptrW)
			} else if name == "__drop_map_str_values" {
				fn = genMapStrValDropFn(ptrW)
			} else if name == "__drop_map_str_keys" {
				fn = genMapStrKeyDropFn(ptrW)
			} else if perVal := strings.TrimPrefix(name, "__drop_map_via_"); perVal != name {
				// Map value-column drop loop; its body calls the embedded
				// per-value drop (__drop_struct_<V> / __drop_enum_<V>), which
				// this worklist then generates from enqueueCalls below.
				// Routing only names a perVal it verified is generatable
				// (mapValHasDrop).
				fn = genMapValDropFn(perVal, ptrW)
			} else if en := strings.TrimPrefix(name, "__drop_enum_"); en != name {
				ed, ok := info.Enums[en]
				if !ok {
					// Not a concrete enum — a generic instantiation
					// (Option[Item]) whose substituted decl dropFnNameFor
					// stashed under the mangled name. info.Enums only holds
					// the un-substituted generic, keyed by base name.
					ed, ok = genEnumDrops[en]
				}
				if !ok {
					continue
				}
				fn = genEnumDropFn(en, ed, info, ptrW, genEnumDrops)
				if fn == nil {
					continue // plan failed — routing shouldn't have named it
				}
			} else {
				sn := strings.TrimPrefix(name, "__drop_struct_")
				sd, ok := info.Structs[sn]
				if !ok {
					continue // routing only names structs it verified exist
				}
				fn = genStructDropFn(sn, sd, info, ptrW, genEnumDrops)
			}
			generated[name] = true
			enqueueCalls(fn.Ops) // a generated body may call further drop fns
			// Codegen pairs prog.Funcs[i] (AST) with out.Funcs[i] (IR) by
			// index; append a stub AST decl in lockstep — emission reads
			// the IR Func's ops, the stub carries the name/params the
			// prologue needs.
			prog.Funcs = append(prog.Funcs, &ast.FuncDecl{
				Name:       fn.Name,
				Params:     fn.Params,
				ReturnType: fn.ReturnType,
				Body:       &ast.Block{},
			})
			out.Funcs = append(out.Funcs, fn)
		}
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
//     (a) a `Some(EXPR)` / `None` literal (for Option) or
//     `Ok(EXPR)` / `Err(EXPR)` literal (for Result)
//     directly,
//     (b) a direct call `helper()` where `helper` is itself
//     in the pair-form set — the call's heap-box result
//     flows out unchanged, and `helper`'s callers got
//     the pair-form treatment too, or
//     (c) a ternary `cond ? Then : Else` whose Then and Else
//     are each themselves an (a) / (b) / (c) shape
//     (recursive). Each arm constructs a heap-box pair
//     independently; consumers still apply
//     `OpCallDirectPair` to the join.
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

// emitPairFormPushValue lowers e into a (tag, payload) pair on
// the operand stack — the shape OpReturnPair (and the `(result
// i32 i32)` wasm signature) consumes. Accepts every expression
// shape `isPairFormReturnExpr` reports as eligible:
//
//   - Variant literal: emits the matching OpMake* op with the
//     payload (or no payload for nullary variants).
//   - Pair-form tail call: emits the call under
//     `suppressPairRebox` so the callee's (tag, payload) flows
//     straight through instead of getting heap-boxed.
//   - IfExpr with both arms eligible: opens an if/else block
//     with the BlockTypeStringPair multi-value `(result i32
//     i32)` shape — semantically a string pair but the wasm
//     bytes are identical to a pair-form tag+payload — and
//     recurses into each arm.
//
// Caller emits OpReturnPair after this returns successfully.
func (b *builder) emitPairFormPushValue(e ast.Expr) error {
	if isVariantLiteralExpr(e, b.pairVariants) {
		variantName, payload := pairFormVariantOf(e)
		payloadType := b.pairFormPayloadType(variantName)
		payloadW := pairPayloadWidth(payloadType)
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
			// (OpMakeSomeI32), nullary → tag 1 (OpMakeNoneI32).
			// The canonical-order check in `pairFormVariantsFor`
			// keeps this tag mapping aligned with the variant's
			// declared order.
			if payload != nil {
				if err := b.expr(payload); err != nil {
					return err
				}
				b.emit(Op{Kind: OpMakeSomeI32, Width: payloadW})
			} else {
				b.emit(Op{Kind: OpMakeNoneI32})
			}
		}
		return nil
	}
	if isPairFormTailCall(e, b.pairForm) {
		save := b.suppressPairRebox
		b.suppressPairRebox = true
		err := b.expr(e)
		b.suppressPairRebox = save
		return err
	}
	if ie, ok := e.(*ast.IfExpr); ok {
		if err := b.expr(ie.Cond); err != nil {
			return err
		}
		b.openIf(BlockTypeStringPair)
		if err := b.emitPairFormPushValue(ie.Then); err != nil {
			return err
		}
		b.elseBranch()
		if err := b.emitPairFormPushValue(ie.Else); err != nil {
			return err
		}
		b.closeScope()
		return nil
	}
	return fmt.Errorf("ir: emitPairFormPushValue: unrecognised shape %T (eligibility check / emitter out of sync)", e)
}

// builder is the per-function lowering state.
type builder struct {
	info *checker.Info
	fn   *ast.FuncDecl
	out  *Func
	// genEnumDrops is the shared (LowerWith-owned) registry mapping a
	// generic-enum-instantiation drop name (the mangled `en` part of
	// `__drop_enum_<en>`, e.g. `Option_LB_Item_RB_`) to the SUBSTITUTED
	// enum decl its drop body is generated from. dropFnNameFor records
	// here when it routes a nested generic-enum field/payload/capture
	// (info.Enums only holds the un-substituted generic decl, keyed by
	// the base name), and the LowerWith drop worklist reads it to
	// generate the body. Map header is shared by value, so writes from
	// any lowering / post-pass site reach the worklist.
	genEnumDrops map[string]*ast.EnumDecl
	// freeEligible[name] is true for array-typed locals the
	// borrow-aware analysis proved are OWNED — safe for the array
	// dec sites to return to the freelist at rc==0. Borrowed /
	// borrowed-derived locals are absent (false) and use a plain
	// dec instead. Computed once by computeFreeEligible. See
	// docs/RC-PERCEUS-PLAN.md (the borrow⇄free resolution).
	freeEligible map[string]bool
	// closureTarget[localName] = the hoisted closure FuncName a
	// FuncType local was assigned via `var f = MakeClosure{...}`.
	// Lets emitDec dispatch a closure's drop to the per-closure
	// __closure_drop_<name> thunk (which frees the captured pointer
	// targets), falling back to the generic __fern_closure_drop for
	// locals with no single known closure source.
	closureTarget map[string]string
	// closureCaps[closureName] = that closure's capture list, used to
	// decide whether a closure has a __closure_drop_<name> thunk
	// (rc-tracked captures present) worth dispatching to.
	closureCaps map[string][]ast.Param
	// movedLocals[name] is true for an owned rc local whose LAST
	// occurrence is a top-level alias that always executes (Phase 4
	// move-on-alias): the alias skips its transfer inc and the exit
	// sweep skips the local's dec (a net-zero pair). Computed by
	// computeMovedLocals.
	movedLocals map[string]bool
	// moveSites[stmt] is true for the specific *ast.Var / *ast.Assign
	// alias statement that is a move (skips its transfer inc). Keyed
	// per-site so only the local's LAST alias moves — earlier aliases
	// of the same local keep their inc.
	moveSites map[ast.Node]bool
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

// emitRcDecLocalsAtExit emits __fern_rc_dec for every
// array-typed parameter and local in the current function.
// Phase 1d-v balances the inc emissions from Phase 1d-i
// through 1d-iv: each alias-bind / call-arg / reassignment
// bumped the rc on the underlying buffer, and the function
// exit returns those references to the caller.
//
// Phase 1d-v doesn't special-case the returned value yet:
// if the function returns a local of array type, that
// local's rc drops to 0 at the exit, but no free happens
// (the bump allocator in Phase 1 doesn't reclaim). The
// caller-side rc is tracked independently via the call-arg
// inc emitted in Phase 1d-iv, so the caller's reference
// stays consistent.
//
// Phase 2's freelist + mutate-or-copy rc check will need an
// "owned return" pass to keep the returned value's rc at 1
// instead of 0 — that lands together with `arr.push`'s
// mutate-in-place fast path. For now, the rc just goes
// briefly to zero on the returned ptr, harmless under the
// no-free regime.
// computeFreeEligible runs the borrow-aware free analysis: it
// returns the set of array-typed locals that are OWNED — every value
// ever written to them is freshly owned (an array literal, or a call
// whose arguments are all owned) — so the array dec sites may safely
// return their buffer to the freelist at rc==0. Borrowed values flow
// in without a caller-side inc (the Phase 2d borrow model), so the rc
// undercounts them; freeing one would use-after-free a buffer a live
// borrow still holds (the self-host VM's compile_stmt/compile_block
// `ops` threading). The analysis taints such values and excludes
// them; only the owner frees (Perceus's rule).
//
// Taint sources: parameters; for-in / match / if-let / let-else /
// destructure bindings; locals that ESCAPE into a container (stored
// as a map/array element, struct/tuple/enum payload — retained
// without an inc, so the owner must not free out from under them).
// Taint propagates through assignment: a local becomes tainted if
// it's ever assigned a tainted Ident, a field / index / slice access
// (which alias their container), or a call that receives a tainted
// argument or receiver (the result may alias it). It also flows
// backward across bare-Ident aliasing (`tmp = arr`) so freeing the
// source can't strand a tainted alias.
// Array literals and calls with only owned arguments produce owned
// values. The default for an unrecognised RHS is tainted (sound:
// over-tainting only costs reclamation, never safety). Fixpoint to a
// stable set since taint can flow backward through `x = f(y)`.
func (b *builder) computeFreeEligible() map[string]bool {
	tainted := map[string]bool{}
	for _, p := range b.fn.Params {
		tainted[p.Name] = true
	}
	// assigns[name] = list of RHS expressions ever written to it.
	assigns := map[string][]ast.Expr{}
	markBindings := func(names []string) {
		for _, n := range names {
			tainted[n] = true
		}
	}
	// escape taints a local that flows into a retain sink: a value
	// stored into a container (map/array element, struct/tuple/enum
	// payload) is RETAINED without a caller-side inc (the Phase 2d
	// borrow model — only the owner counts). Freeing the local at
	// scope exit would then use-after-free the alias the container
	// still holds (e.g. `var arr = [val]; m.set(k, arr)` in
	// std/url's __query_pair). Only direct Idents are tainted here;
	// nested sinks (`m.set(k, JArray(inner))`) are caught when the
	// walk visits the inner EnumLit / StructLit / TupleLit node.
	escape := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			tainted[id.Name] = true
		}
	}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.Var:
			if s.Init != nil {
				assigns[s.Name] = append(assigns[s.Name], s.Init)
			}
		case *ast.Assign:
			if id, ok := s.Target.(*ast.Ident); ok {
				assigns[id.Name] = append(assigns[id.Name], s.Value)
			} else {
				// Storing into an existing element / field / capture
				// (`grid[i] = row`, `p.items = arr`, `cap = arr`)
				// retains the value without an inc, so the source
				// local escapes into the container.
				switch s.Target.(type) {
				case *ast.Index, *ast.FieldAccess, *ast.CaptureRef:
					escape(s.Value)
				}
			}
		case *ast.IfExpr:
			// A bare local read in an if-expr's value position
			// (`var v = if (c) { v0 } else { v1 }`) aliases that local
			// WITHOUT a reliable inc — the alias-inc only fires for a
			// direct Ident RHS, not one wrapped in a conditional, and a
			// per-arm inc can't be emitted unconditionally (some arms
			// yield fresh rc=1 values). Freeing the source at scope exit
			// would then strand the alias the conditional handed out, so
			// taint any local that flows out of an arm.
			escape(s.Then)
			escape(s.Else)
		case *ast.Destructure:
			// Destructure bindings are NOT tainted: the lowering dups
			// (rc_inc) each extracted pointer-shaped element, so the
			// binding becomes a counted OWNER and reclaims through its own
			// type's machinery at scope exit (array → arr_dec, struct →
			// __drop_struct_, …). The matching dec is the tuple's
			// deep-drop. Match / IfLet / LetElse bindings stay tainted —
			// they alias enum payloads with no projection dup.
		case *ast.Match:
			for _, arm := range s.Arms {
				markBindings(arm.Bindings)
			}
		case *ast.MatchExpr:
			for _, arm := range s.Arms {
				markBindings(arm.Bindings)
				// A local yielded from a match-expression arm
				// (`var v = match (x) { … => v0 }`) is conditionally
				// aliased without a reliable inc, so it must not be
				// freed at scope exit. Mirrors the IfExpr case.
				escape(arm.Body)
			}
		case *ast.IfLet:
			markBindings(s.Bindings)
		case *ast.LetElse:
			markBindings(s.Bindings)
		case *ast.Call:
			// Retain sinks the checker lowers to Calls. None of
			// these inc the stored rc value (unlike StructLit /
			// TupleLit construction, which do — see
			// needsRcIncOnAlias at the alias sites), so a local
			// that flows in is retained uncounted and must not be
			// freed at scope exit.
			if id, ok := s.Callee.(*ast.Ident); ok {
				switch {
				case id.Name == "__method_Map_set":
					// Args[0] is the map (mutated in place), not a
					// retained value — skip it; taint key + value.
					for _, a := range s.Args[1:] {
						escape(a)
					}
				case id.Name == "__method_Array_push":
					// Args[0] is the receiver array (threaded /
					// reassigned), not retained — taint the element.
					if len(s.Args) == 2 {
						escape(s.Args[1])
					}
				default:
					// Variant constructor (`Arr(xs)`): emitEnumNew
					// stores the payload without an inc, so an array
					// local passed as a payload escapes into the box.
					if _, isLocal := b.locals[id.Name]; !isLocal {
						if _, _, _, isVariant := b.lookupVariant(id.Name); isVariant {
							for _, a := range s.Args {
								escape(a)
							}
						}
					}
				}
			}
		case *ast.StructLit:
			for _, f := range s.Fields {
				escape(f.Value)
			}
		case *ast.TupleLit:
			for _, e := range s.Elems {
				escape(e)
			}
		case *ast.MapLit:
			for _, ent := range s.Entries {
				escape(ent.Key)
				escape(ent.Value)
			}
		case *ast.EnumLit:
			for _, a := range s.Args {
				escape(a)
			}
		}
		return true
	})
	for {
		changed := false
		for name, rhss := range assigns {
			for _, rhs := range rhss {
				if !tainted[name] && b.rhsTainted(rhs, tainted) {
					tainted[name] = true
					changed = true
				}
				// Backward alias propagation: a tainted local
				// assigned a bare Ident shares that source's
				// buffer, so the source must not be freed either
				// (`tmp = arr; m.set(k, tmp)` taints arr too).
				if tainted[name] {
					if src, ok := rhs.(*ast.Ident); ok && !tainted[src.Name] {
						tainted[src.Name] = true
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	elig := map[string]bool{}
	for _, v := range b.info.Locals[b.fn] {
		if tainted[v.Name] {
			continue
		}
		switch t := v.Type.(type) {
		case ast.ArrayType:
			elig[v.Name] = true
		case ast.StructType:
			// A Map local (runtime handle "Map") frees its buf + handle;
			// a user struct (has a StructDecl) frees its box. Both at
			// the last reference, when owned (untainted). Other runtime
			// handles (Reader/Writer/MapIter) have no StructDecl and no
			// drop handler — not eligible.
			if t.Name == "Map" {
				elig[v.Name] = true
			} else if _, isUser := b.info.Structs[t.Name]; isUser {
				elig[v.Name] = true
			}
		case ast.EnumType:
			// An owned enum frees its box when its layout is uniform
			// (emitDec gates on uniformEnumDropLoads + uniformEnumBoxSize;
			// non-uniform / generic enums keep the plain box dec). The
			// eligibility just grants permission — the same borrow-aware
			// taint as arrays/structs.
			elig[v.Name] = true
		case *ast.FuncType:
			// An owned closure frees its env / pair rc1 block at the
			// last reference (emitDec → __fern_closure_drop). Same
			// borrow-aware taint: a closure that escapes (returned via
			// alias, stored into a container, passed as a retained arg)
			// is tainted and falls back to the plain dec.
			elig[v.Name] = true
		case ast.TupleType:
			// An owned tuple returns its box to the freelist at the
			// last reference (emitDec → __fern_box_free). Same
			// borrow-aware taint as the others; box reclamation only
			// (elements keep their own rc, freed where they're owned).
			elig[v.Name] = true
		case ast.StringType:
			// A fresh owned heap string (concat / slice result —
			// rhsTainted whitelists exactly those, since both COPY into a
			// new headered buffer) frees at its last reference via
			// __fern_str_dec. wasm32 only (ptrW==4 — __fern_str_dec is
			// wasm-only); aliases / views / literals are tainted above and
			// skipped.
			if b.ptrW == 4 {
				elig[v.Name] = true
			}
		}
	}
	return elig
}

// rhsTainted reports whether the value produced by `e` may alias a
// borrowed (tainted) value, given the current taint set. See
// computeFreeEligible. Conservative: unknown shapes are tainted.
func (b *builder) rhsTainted(e ast.Expr, tainted map[string]bool) bool {
	switch x := e.(type) {
	case *ast.ArrayLit:
		return false
	case *ast.StructLit:
		return false
	case *ast.TupleLit:
		// A freshly-built tuple (rc=1) owns its box, like an array /
		// struct literal — not an alias of a borrowed value, so the
		// tuple local is eligible to free its box at the last reference.
		// Escapes are still caught: a returned tuple takes move-on-return
		// and one stored into a container is escape-tainted at the sink.
		return false
	case *ast.MakeClosure:
		// A freshly-built closure (rc=1), like an array literal — it
		// owns its env, not an alias of a borrowed value. Owned, so the
		// FuncType local is eligible to free its env/captures at the
		// last reference (closure reclamation Stages 2-3). Escapes are
		// still caught: a returned closure takes move-on-return, and one
		// stored into a container is escape-tainted at the sink.
		return false
	case *ast.Ident:
		return tainted[x.Name]
	case *ast.FieldAccess, *ast.Index:
		return true
	case *ast.SliceExpr:
		// A STRING slice copies its bytes into a fresh owned heap buffer
		// (the wasm runtime always allocates), so it's reclaimable — not
		// a view. Array / other slices share the source buffer → tainted.
		if _, ok := b.exprType(x).(ast.StringType); ok {
			return false
		}
		return true
	case *ast.Binary:
		// String concat (`a + b`) copies both operands into a fresh owned
		// heap buffer regardless of operand provenance, so the result is
		// always reclaimable. Non-concat binaries are scalar (never an
		// rc-tracked local's RHS), so their value here is moot.
		return !x.IsStringConcat
	case *ast.Call:
		// Map builtins return the MAP HANDLE, which aliases only the
		// receiver (cow) — never the stored key/value args. The generic
		// any-arg-tainted rule below would taint every map handle (the
		// cap/key/value args are routinely tainted — params, literals via
		// the default case), leaving map locals permanently ineligible and
		// their buf/handle + values unreclaimed. The rc inc-on-set /
		// dec-on-drop balance makes freeing an owned map's storage safe.
		if id, ok := x.Callee.(*ast.Ident); ok {
			switch id.Name {
			case "map_new":
				return false // fresh owned handle
			case "__method_Map_set", "__method_Map_clear":
				// Aliases the receiver (Args[0]) only.
				return len(x.Args) > 0 && b.rhsTainted(x.Args[0], tainted)
			}
		}
		if fa, ok := x.Callee.(*ast.FieldAccess); ok && b.rhsTainted(fa.Target, tainted) {
			return true // method receiver is tainted
		}
		for _, a := range x.Args {
			if b.rhsTainted(a, tainted) {
				return true
			}
		}
		return false
	case *ast.IfExpr:
		return b.rhsTainted(x.Then, tainted) || b.rhsTainted(x.Else, tainted)
	default:
		return true
	}
}

func (b *builder) emitRcDecLocalsAtExit() {
	b.emitRcDecLocalsAtExitExcept("")
}

// isOwnedRcLocal reports whether `name` is a declared rc-tracked local
// (array / struct incl. Map / enum / closure) that the exit sweep would
// dec. Params are borrowed (not in info.Locals, never swept) so they're
// excluded. FuncType (closure) locals now free their env/pair block at
// the last reference (__fern_closure_drop), so they participate in
// move-on-return / move-on-alias like the other owned types — the
// transfer and the sweep-dec genuinely cancel.
func (b *builder) isOwnedRcLocal(name string) bool {
	for _, v := range b.info.Locals[b.fn] {
		if v.Name != name {
			continue
		}
		switch v.Type.(type) {
		case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType:
			return true
		case ast.StringType:
			// wasm two-word strings now alias-inc (__fern_str_inc), so
			// they participate in move-on-return / move-on-alias like the
			// other rc types: a returned string local cancels its
			// transfer-inc against the exit-sweep dec (no free under the
			// caller). Gated ptrW==4 (wasm-only reclamation).
			return b.ptrW == 4
		}
		return false
	}
	return false
}

// computeMovedLocals finds Phase 4 move-on-alias sites: a top-level
// `var y = x` / `y = x` statement whose source x is an owned rc local
// AND whose read of x is x's LAST occurrence in the function. Such an
// x is dead after the alias, so the alias's transfer inc and x's
// exit-sweep dec cancel — emitting neither moves the reference to y
// with no rc traffic. Removing a balanced inc+dec pair can't change
// the net rc (safe regardless of x's borrow-ness); the last-occurrence
// guard is what proves no live read is stranded.
//
// Two guards keep the global sweep-exclusion leak-free (the alias must
// run on EVERY path to an exit, so x is always moved, never
// double-counted nor stranded): the alias must be a TOP-LEVEL
// statement (not nested in a branch/loop that could skip it), and no
// `return` may precede it at the top level (which would let a path
// exit before the move). Aliases inside control flow keep their inc.
//
// "Last occurrence" is the max pre-order Ident index for the name: a
// `var x` definition isn't an Ident node, so the count covers reads
// plus assign-target writes — the alias being the last occurrence
// therefore also rules out any later read OR reassignment of x.
func (b *builder) computeMovedLocals() map[string]bool {
	moved := map[string]bool{}
	if b.fn.Body == nil {
		return moved
	}
	idx := 0
	identIdx := map[*ast.Ident]int{}
	maxIdx := map[string]int{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			idx++
			identIdx[id] = idx
			if idx > maxIdx[id.Name] {
				maxIdx[id.Name] = idx
			}
		}
		return true
	})
	sawReturn := false
	for _, st := range b.fn.Body.Stmts {
		if !sawReturn {
			// The lowering checks b.moveSites on the Var node or the
			// inner Assign node (assignments are ExprStmt-wrapped), so
			// key the site on whichever the lowering will see.
			var rhs *ast.Ident
			var site ast.Node
			switch s := st.(type) {
			case *ast.Var:
				rhs, _ = s.Init.(*ast.Ident)
				site = s
			case *ast.ExprStmt:
				if a, ok := s.Expr.(*ast.Assign); ok {
					if _, tok := a.Target.(*ast.Ident); tok {
						rhs, _ = a.Value.(*ast.Ident)
						site = a
					}
				}
			}
			if rhs != nil && b.isOwnedRcLocal(rhs.Name) && identIdx[rhs] == maxIdx[rhs.Name] {
				moved[rhs.Name] = true
				b.moveSites[site] = true
			}
		}
		if stmtContainsReturn(st) {
			sawReturn = true
		}
	}
	return moved
}

// stmtContainsReturn reports whether a statement (or anything nested
// in it) can `return` — used to stop move-on-alias once a path could
// exit before a later top-level alias.
func stmtContainsReturn(st ast.Stmt) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if _, ok := n.(*ast.Return); ok {
			found = true
		}
		return !found
	})
	return found
}

// emitRcDecLocalsAtExitExcept is emitRcDecLocalsAtExit but skips the
// dec for one named local. The Return lowering uses this for the
// move-on-return optimization: when a function returns a bare
// rc-tracked local, the return-transfer inc and that local's
// exit-sweep dec cancel (the inc exists only to survive the sweep), so
// emitting neither leaves the returned value at the same rc — fewer rc
// ops, identical result. `exclude == ""` decs every owned local.
func (b *builder) emitRcDecLocalsAtExitExcept(exclude string) {
	// decValueOnStack consumes a pointer value already on the
	// operand stack and dec's it per its static type. An array of
	// pointer-shaped rc-tracked elements routes through
	// __fern_drop_arr_ptr (which recurses one level into the
	// elements on the last reference); every other pointer-shaped
	// value gets a flat __fern_rc_dec (nested fields/elements of
	// those leak for now — safe under no-free, no over-release).
	// Both helpers carry the null / low-address / sentinel guards.
	decValueOnStack := func(t ast.Type, mayFree bool) {
		// Two-word string value (wasm): the caller loaded (data, len) via
		// payloadLoadOpFor, so reclaim via the two-word __fern_str_dec.
		// Reached from the enum payload drop (struct / tuple string
		// fields are handled inline before reaching here).
		if _, isStr := t.(ast.StringType); isStr && b.ptrW == 4 {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// `mayFree` is the borrow-aware permission to return this
		// value's buffer to the freelist. It's true only for OWNED
		// top-level array locals (computeFreeEligible); struct fields
		// and enum payloads always pass false (their borrow-ness
		// isn't tracked, so they never free — conservative, safe).
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) && mayFree {
			// Transitive reclamation Stage B: an array of CONCRETE
			// structs drops each element box deeply (via the generated
			// __drop_arr_struct_<Elem> loop → __drop_struct_<Elem> per
			// element) before freeing the buffer, instead of the flat
			// per-element rc_dec that __fern_drop_arr_ptr does (which
			// leaks the element boxes). Only the eligible (mayFree) path
			// reaches here. Gated on RcFreeEnabled to match the genfn
			// post-pass — with free off the genfn isn't emitted, so we
			// must fall back to the always-present __fern_drop_arr_ptr
			// (whose own buffer-free is internally RcFreeEnabled-gated).
			// Array-of-array / enum / closure keep __fern_drop_arr_ptr.
			if name, ok := arrElemStructDropName(at.Elem, b.info); ok && ast.RcFreeEnabled {
				b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
				b.emit(Op{Kind: OpDrop})
				return
			}
			b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_ptr", I32: 2})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// __fern_rc_dec is a void-returning runtime helper but
		// OpCallDirect's codegen always pushes the call's
		// return-value register (x0/rax) onto the operand stack;
		// drop the bogus push to keep the stack balanced.
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	// dropStructField drops one struct field whose pointer is already on
	// the operand stack. Transitive reclamation Stage A: a CONCRETE
	// struct field (statically exact type) recurses through its
	// generated __drop_struct_ fn, so its box + nested struct children
	// reclaim on the field's last reference; the generated fn
	// is_unique-gates internally, so this is safe whether the child is
	// shared or not. Every other field type (arrays, enums/unions,
	// closures, Map) keeps the flat one-level dec.
	dropStructField := func(t ast.Type) {
		// Two-word string value (wasm): caller loaded (data, len), reclaim
		// via __fern_str_dec. Reached from the enum variant-plan payload
		// drop (struct / tuple string fields are handled inline).
		if _, isStr := t.(ast.StringType); isStr && b.ptrW == 4 {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		if isMapType(t) {
			// A Map-typed field (e.g. struct Request { headers:
			// Map[..] }) reclaims the whole map structure on the owning
			// value's last reference: free the value column then the
			// buf + handle. Both helpers self-guard on the map's own
			// rc==1, so a shared map only dec's. They return the map
			// ptr, so the stack value chains through without a reload.
			b.emit(Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		if name, ok := dropFnNameFor(t, b.info, b.genEnumDrops); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		if at, ok := t.(ast.ArrayType); ok {
			// Any array field frees its BUFFER on the owning value's
			// last reference (the owner is eligible/unique here, so the
			// field is owned; each helper still is_unique-gates the
			// array, so a shared one only dec's). Array-of-struct also
			// deep-drops each element box (Stage B loop); array-of-rc
			// (e.g. i32[][]) frees the outer buffer + flat-dec's inner
			// (inner buffers are array-of-array, a later slice); plain
			// arrays (i32[]) free the buffer via arr_dec. Previously all
			// of these flat-dec'd, leaking the buffer.
			if name, ok := arrElemStructDropName(at.Elem, b.info); ok {
				b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
				b.emit(Op{Kind: OpDrop})
				return
			}
			helper := "__fern_arr_dec"
			if arrElemIsRcTracked(at.Elem) {
				helper = "__fern_drop_arr_ptr"
			} else if _, isStr := at.Elem.(ast.StringType); isStr && b.ptrW == 4 {
				// string[]: walk + __fern_str_dec each two-word element,
				// then free the buffer.
				helper = "__fern_drop_arr_str"
			}
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
			b.emit(Op{Kind: OpDrop})
			return
		}
		decValueOnStack(t, false)
	}
	emitDec := func(slot int32, t ast.Type, eligible bool, name string) {
		// `eligible` is the borrow-aware verdict for THIS local: true
		// only when it's a proven-OWNED array (computeFreeEligible).
		// Arrays of pointer-shaped rc-tracked elements route through
		// __fern_drop_arr_ptr (which walks + dec's the elements and,
		// flag-on, frees the buffer) ONLY when eligible; an ineligible
		// (borrowed-derived) array uses a plain dec — never freeing a
		// buffer a live borrow still holds. The helper carries the
		// null / low-address / sentinel guards.
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			decValueOnStack(t, eligible)
			return
		}
		// Phase 3 step-4: a plain array (primitive elements — i32[],
		// u8[], …) frees its buffer at the last reference when the
		// freelist is on AND it's an owned local. __fern_arr_dec
		// carries the same guards as __fern_rc_dec. Ineligible /
		// flag-off arrays fall through to the plain box dec.
		// string[] (wasm two-word elements): reclaim each element via the
		// two-word walk in __fern_drop_arr_str, then free the buffer.
		// Gated eligible — a borrowed string[] never frees its elements.
		if at, ok := t.(ast.ArrayType); ok && ast.RcFreeEnabled && eligible {
			if _, isStr := at.Elem.(ast.StringType); isStr && b.ptrW == 4 {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_str", I32: 2})
				b.emit(Op{Kind: OpDrop})
				return
			}
		}
		if at, ok := t.(ast.ArrayType); ok && ast.RcFreeEnabled && eligible {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Two-word string reclamation (wasm). A string local occupies one
		// IR logical slot that OpLoadLocal fans into (data, len); the
		// catch-all rc_dec below would consume only one of the two values
		// (corrupting the stack) and dec a two-word value as a single
		// pointer, so strings MUST return here, never fall through. An
		// ELIGIBLE string (a fresh owned concat/slice result — rhsTainted
		// whitelists exactly those, both of which COPY into a new headered
		// buffer) frees via __fern_str_dec (inline no-op / rc==1 box_free /
		// else dec). Ineligible strings (aliases / views / literals,
		// tainted) are SKIPPED entirely — never touched, so a view string
		// can never be misread/freed.
		if _, isStr := t.(ast.StringType); isStr {
			// Gated on wasm32 (ptrW==4) — __fern_str_dec is wasm-only;
			// the arm64 two-word override has no such helper.
			if ast.RcFreeEnabled && eligible && b.ptrW == 4 {
				b.emit(Op{Kind: OpLoadLocal, I32: slot}) // pushes (data, len)
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop}) // drop the returned data ptr
			}
			return
		}
		// Tuple reclamation: an OWNED tuple local drops its pointer-shaped
		// elements then returns its box to the freelist on the last
		// reference (rc==1), mirroring the struct box path. The box was
		// alloc'd as `tupleElemLayout size + 8` rc header, so
		// __fern_box_free frees base = data-8, size+8. Each element drop
		// is_unique-gates internally (dropStructField), so a shared
		// element only dec's; the per-element dec balances the dup the
		// projection sites (destructure / field read / return) emit when
		// they hand the element out. Ineligible (borrowed / escaped)
		// tuples and flag-off builds fall through to the plain box dec.
		if tt, ok := t.(ast.TupleType); ok && ast.RcFreeEnabled && eligible {
			offs, size := tupleElemLayout(tt.Elems, b.ptrW)
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			for i, et := range tt.Elems {
				if _, isStr := et.(ast.StringType); isStr && b.ptrW == 4 {
					// Two-word string element: load (data, len) and reclaim
					// via __fern_str_dec. Unique here (rc==1 guard), so the
					// element is uniquely owned; inline / literal strings
					// no-op. Balances the projection dup (__fern_str_inc) and
					// the construction retain.
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if offs[i] != 0 {
						b.emit(Op{Kind: OpConstI32, I32: offs[i]})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthString})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
					continue
				}
				if !arrElemIsRcTracked(et) {
					continue
				}
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				if offs[i] != 0 {
					b.emit(Op{Kind: OpConstI32, I32: offs[i]})
					b.emit(Op{Kind: OpAdd})
				}
				b.emit(Op{Kind: OpLoad, Width: WidthPtr})
				dropStructField(et)
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpConstI32, I32: size})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpElse})
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpEnd})
			return
		}
		// Phase 3 map reclamation: an OWNED Map local returns its buf
		// + handle to the freelist at the last reference (rc==1) via
		// __fern_map_drop. First free the value column via
		// __map_drop_values (which self-guards on rc==1): it reads the
		// buf's packed valKind+stride (2 = plain-elem array → arr_dec,
		// 3 = rc-elem array → drop_arr_ptr) and frees each live value.
		// The retain-on-store (inc-on-set) + retain-on-read (inc-on-
		// get) balance keeps this release-balanced. Non-array V and
		// entry KEYS still leak — a later slice. Ineligible (borrowed-
		// derived) maps and flag-off builds fall through to the plain
		// box dec.
		if st, ok := t.(ast.StructType); ok && st.Name == "Map" && ast.RcFreeEnabled && eligible {
			// Struct/enum-valued maps deep-drop each value via the generated
			// __drop_map_via_<perValueDrop> loop; array-valued maps use the
			// generic __map_drop_values (kind 2/3). Both free the buf +
			// handle via the trailing __fern_map_drop.
			dropValues := "__map_drop_values"
			if name, ok := mapValDropName(st, b.info, b.genEnumDrops); ok {
				dropValues = name
			} else if len(st.Args) >= 2 {
				// Map[K, string]: reclaim each value's string buffer
				// before freeing the buf + handle.
				//   wasm (ptrW=4)              : boxed (data, len) cell; str_dec + cell_free.
				//   native single-word (x86_64): direct data pointer; rc_dec.
				//   arm64 (ptrW=8 + TwoWord)   : boxed like wasm, but the wasm
				//                                str_dec / cell_free helpers don't
				//                                exist on arm64 yet — stay on the
				//                                pre-slice leaking-but-safe behaviour
				//                                until a native equivalent lands.
				// The generated __drop_map_str_values branches on ptrW for the
				// boxed vs direct shape.
				if _, isStr := st.Args[1].(ast.StringType); isStr {
					if b.ptrW == 4 || !ast.UseTwoWordStrings(b.ptrW) {
						dropValues = "__drop_map_str_values"
					}
				}
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: dropValues, I32: 1})
			b.emit(Op{Kind: OpDrop})
			// Map[string, V]: reclaim each key's string buffer.
			//   wasm (ptrW=4)              : boxed (data, len) cell; str_dec + cell_free.
			//   native single-word (x86_64): direct data pointer; rc_dec.
			//   arm64 (ptrW=8 + TwoWord)   : boxed like wasm, but no native
			//                                str_dec / cell_free helpers — stay
			//                                on pre-slice leaking-but-safe behaviour
			//                                until the boxed-string runtime is
			//                                ported. Same gating as __drop_map_str_values.
			// Generated __drop_map_str_keys branches on ptrW for the boxed vs
			// direct shape. Independent of the value walk above (both
			// self-guard on rc==1); runs before the buf + handle free.
			if len(st.Args) >= 1 {
				if _, isStr := st.Args[0].(ast.StringType); isStr {
					if b.ptrW == 4 || !ast.UseTwoWordStrings(b.ptrW) {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						b.emit(Op{Kind: OpCallDirect, Str: "__drop_map_str_keys", I32: 1})
						b.emit(Op{Kind: OpDrop})
					}
				}
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Phase 3 step 3: a user struct with pointer-shaped
		// rc-tracked fields drops those fields on its LAST
		// reference before dec'ing the box — balancing the
		// per-field inc from Phase 1e-struct-ii. Gated on
		// __fern_rc_is_unique (rc == 1, guarded) so an aliased
		// struct (rc > 1) or a non-pointer slot only dec's the
		// box. Runtime handle types (Map / Reader / Writer /
		// MapIter) have no StructDecl in info.Structs, so sdOk is
		// false and they fall through to the plain box dec — their
		// own drop handlers land in a follow-up. Fields are dropped
		// at one level (decValueOnStack); nested struct/enum/closure
		// fields are flat-dec'd (deep recursion is a later slice).
		if st, ok := t.(ast.StructType); ok {
			sd, sdOk := b.info.Structs[st.Name]
			// Phase 3 struct-box reclamation: an OWNED user struct
			// returns its box to the freelist on the last reference
			// (rc==1) after dropping its rc-tracked fields. Gated on
			// eligible (computeFreeEligible) + flag-on; otherwise the
			// box only dec's (leaks) as before. The box was alloc'd as
			// `structFieldLayout size + 8` rc header, so __fern_box_free
			// frees base = data-8, size+8.
			if sdOk && ast.RcFreeEnabled && eligible {
				offs, size := structFieldLayout(sd.Fields, b.ptrW)
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				// rc == 1: drop fields, then free the box. The struct is
				// uniquely owned here, so each field is too — a string
				// field's __fern_str_dec frees its buffer safely (inline /
				// literal-sentinel strings no-op). The field was retained
				// on construction (emitAliasInc) or moved in fresh, so the
				// dec balances. Direct string fields only; a string nested
				// in an array / tuple / enum field reclaims via that
				// container's own (future) string-aware drop.
				for _, f := range sd.Fields {
					if _, isStr := f.Type.(ast.StringType); isStr && b.ptrW == 4 {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthString})
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
						b.emit(Op{Kind: OpDrop})
						continue
					}
					if !arrElemIsRcTracked(f.Type) {
						continue
					}
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if off := offs[f.Name]; off != 0 {
						b.emit(Op{Kind: OpConstI32, I32: off})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthPtr})
					dropStructField(f.Type)
				}
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: size})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpElse})
				// rc > 1: just dec the box.
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpEnd})
				return
			}
			if sdOk {
				offs, _ := structFieldLayout(sd.Fields, b.ptrW)
				var ptrFields []ast.Param
				for _, f := range sd.Fields {
					if arrElemIsRcTracked(f.Type) {
						ptrFields = append(ptrFields, f)
					}
				}
				if len(ptrFields) > 0 {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					for _, f := range ptrFields {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthPtr})
						// Owned-but-NOT-free-eligible (escaped / tainted): the box
						// isn't freed here, and a nested struct field may still be
						// reachable through the escape, so it must NOT be deep-freed.
						// Flat one-level dec only; deep recursion fires solely in the
						// eligible branch above.
						decValueOnStack(f.Type, false)
					}
					b.emit(Op{Kind: OpEnd})
				}
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Phase 3 step 3: a heap-boxed enum with pointer-shaped
		// rc-tracked payloads drops those payloads on its LAST
		// reference before dec'ing the box. The box layout is
		// [rc@-8 | tag@0, payloads@payloadLayout offsets]. This
		// slice handles the UNIFORM case — every payload-carrying
		// variant shares an identical droppable-payload signature
		// (same offsets, same array-vs-flat kind) — so the payload
		// decs are emitted unconditionally inside the is_unique
		// guard, with no tag switch. That covers unions
		// (`type Value = VInt | VArr | ...`), whose variants each
		// carry a single struct pointer at the same offset.
		// Non-uniform enums (e.g. JsonValue, where JArray carries a
		// pointer but JBool doesn't) and generic enums (Option /
		// Result, whose ParamType payloads aren't statically
		// droppable) fall through to the plain box dec — their
		// payloads leak for now, which is safe under no-free and
		// reports 0 over-releases. Per-tag dispatch + type-arg
		// substitution are a later slice.
		if et, ok := t.(ast.EnumType); ok {
			ed, edOk := b.info.Enums[et.Name]
			// Phase 3 enum-box reclamation: an OWNED enum returns its
			// box to the freelist on the last reference (rc==1) after
			// dropping its payloads — but only when the box size is
			// statically known (uniformEnumBoxSize) AND the droppable
			// payloads are uniform (uniformEnumDropLoads), since the
			// drop emits no tag switch. The is_unique gate filters out
			// payloadless static sentinels (rc high-bit), so
			// __fern_box_free only ever sees a real rc==1 box.
			if edOk && ast.RcFreeEnabled && eligible {
				// Generic-enum reclamation: a heap-boxed instantiation
				// like Option[Item] / Result[Item, E] carries ParamType
				// payloads in its decl, so the drop plan can't see the
				// concrete type. Substitute the type args (et.Args) to
				// recover them — but ONLY adopt the substituted decl when
				// it exposes a concrete STRUCT payload. That guarantees a
				// pointer-shaped (heap-boxed, non-pair-form) instantiation,
				// so the variant-plan's box_free is valid; scalar
				// instantiations (Option[i32], pair-form, no box) keep the
				// generic decl and bail to the flat dec as before.
				if len(et.Args) > 0 {
					if sub := substituteEnumDecl(ed, et.Args); enumHasPointerPayload(sub) {
						ed = sub
					}
				}
				loads, loadsOk := uniformEnumDropLoads(ed, b.ptrW)
				size, sizeOk := uniformEnumBoxSize(ed, b.ptrW)
				// The branchless uniform path can only flat-dec its
				// payloads (it uses one static payload type, but a union's
				// variants differ at the shared offset). When a payload is
				// a CONCRETE struct that could be deep-dropped, skip it and
				// take the tag-dispatch (variant-plan) path below instead,
				// where each arm knows its exact type and recurses through
				// __drop_struct_<T> (Stage C). Uniform stays for array /
				// other payloads, which flat-dec under both paths anyway.
				if loadsOk && sizeOk && !enumHasPointerPayload(ed) {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					for _, ld := range loads {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if ld.off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: ld.off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(payloadLoadOpFor(ld.typ, b.ptrW))
						decValueOnStack(ld.typ, false)
					}
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpConstI32, I32: size})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
					b.emit(Op{Kind: OpDrop})
					b.emit(Op{Kind: OpElse})
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
					b.emit(Op{Kind: OpEnd})
					return
				}
				// Non-uniform enum (e.g. JsonValue): a tag switch over
				// the real box (rc==1) drops each variant's payloads and
				// frees with that variant's exact box size. The tag is
				// stashed in a scratch local so later arms read it from
				// the stack, never from the (possibly freed) box.
				if plan, ok := enumVariantDropPlan(ed, b.ptrW); ok {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					tagSlot := b.allocSlot()
					b.locals[fmt.Sprintf("__enum_drop_tag_%d", tagSlot)] = tagSlot
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpLoad}) // tag at [data+0]
					b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
					for _, vd := range plan {
						b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
						b.emit(Op{Kind: OpConstI32, I32: int32(vd.tag)})
						b.emit(Op{Kind: OpEq})
						b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
						for _, ld := range vd.loads {
							b.emit(Op{Kind: OpLoadLocal, I32: slot})
							if ld.off != 0 {
								b.emit(Op{Kind: OpConstI32, I32: ld.off})
								b.emit(Op{Kind: OpAdd})
							}
							b.emit(payloadLoadOpFor(ld.typ, b.ptrW))
							// Transitive reclamation Stage C: this arm is
							// tag-guarded (tag == vd.tag), so ld.typ is the
							// EXACT payload type of this variant — unlike the
							// uniform path, a type-specific recursive drop is
							// sound here. A concrete-struct payload recurses
							// through __drop_struct_<T> (freeing its box +
							// nested struct fields); other payloads keep the
							// flat one-level dec.
							dropStructField(ld.typ)
						}
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						b.emit(Op{Kind: OpConstI32, I32: vd.size})
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
						b.emit(Op{Kind: OpDrop})
						b.emit(Op{Kind: OpEnd})
					}
					b.emit(Op{Kind: OpElse})
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
					b.emit(Op{Kind: OpEnd})
					return
				}
			}
			if edOk {
				if loads, ok := uniformEnumDropLoads(ed, b.ptrW); ok {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					for _, ld := range loads {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if ld.off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: ld.off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(payloadLoadOpFor(ld.typ, b.ptrW))
						decValueOnStack(ld.typ, false)
					}
					b.emit(Op{Kind: OpEnd})
				}
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Closure reclamation: an OWNED FuncType local frees its env /
		// pair rc1 block at the last reference (rc==1). When the local
		// has a single known closure source with rc-tracked captures
		// (closureTarget), dispatch to that closure's
		// __closure_drop_<name> thunk, which ALSO frees the captured
		// pointer targets before freeing the env (Stage 3). Otherwise
		// the generic __fern_closure_drop frees just the env (Stage 2;
		// captures leak). Either way a single load+call keeps
		// ElideClosurePair's reader recognising the drop as benign.
		// Ineligible (borrowed / escaping) closures and flag-off
		// builds fall through to the plain dec.
		if _, isFunc := t.(*ast.FuncType); isFunc && ast.RcFreeEnabled && eligible {
			dropFn := "__fern_closure_drop"
			tgt := b.closureTarget[name]
			if tgt != "" && hasRcCapture(b.closureCaps[tgt], b.ptrW) {
				dropFn = "__closure_drop_" + tgt
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: dropFn, I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		b.emit(Op{Kind: OpLoadLocal, I32: slot})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	// Use b.locals[name] so we only dec slots that user code
	// actually writes to. Two scope-separated Var declarations
	// sharing a name (e.g. `var ns` declared 9 times across
	// different branches of vm.fern) all map to the SAME
	// physical slot via b.locals[name] — only the last entry
	// wins in the slot map, and every Var-decl Store reaches
	// that last slot. The "earlier" slot indices that
	// info.Locals[fn] tracks are never written by user code, so
	// dec'ing them at exit by index would read uninitialised
	// memory and trap.
	//
	// Dedup via a per-name set so we only dec each unique slot
	// once even if the same name appears multiple times in
	// info.Locals[fn].
	//
	// Phase 1e-struct-iii: dec sweep now also covers struct-
	// typed slots. The rc-tracked set matches the predicate
	// used by needsRcIncOnAlias / zeroRcTracked so inc and dec
	// agree on which slots get touched. The runtime guard
	// inside __fern_rc_dec (high-bit sentinel + low-address
	// short-circuit) keeps this safe for runtime-allocated
	// struct values (Reader/Writer/Map/MapIter) whose header
	// holds 0x80000000 instead of a real rc.
	rcTracked := func(t ast.Type) bool {
		if _, isArr := t.(ast.ArrayType); isArr {
			return true
		}
		if _, isStruct := t.(ast.StructType); isStruct {
			return true
		}
		// Phase 1e-enums-ii: enum values are always pointer-shaped
		// in a local / param slot — either a headered heap box
		// (emitEnumNew / pair rebox / runtime helper) or a static
		// sentinel that now carries a 0x80000000 rc header. The
		// transient pair-form (tag, payload) only lives on the
		// operand stack between a pair call and its match dispatch,
		// never in a slot, so the dec sweep never sees it.
		if _, isEnum := t.(ast.EnumType); isEnum {
			return true
		}
		// Phase 1e-closures-ii: a FuncType local holds a heap
		// closure pair / env block (rc=1 header via
		// __fern_alloc_rc1) or a static function-value cell
		// (immortal sentinel on natives, low-address short-circuit
		// on wasm). All pointer-shaped; rc_inc/dec are safe.
		if _, isFunc := t.(*ast.FuncType); isFunc {
			return true
		}
		// wasm32 strings: a heap string carries an rc header (the producer
		// prereq), and the emitDec string branch reclaims owned ones via
		// __fern_str_dec. Gated strictly on wasm (ptrW==4) — natives,
		// INCLUDING the arm64 two-word-string override, have no
		// __fern_str_dec runtime helper.
		if _, isStr := t.(ast.StringType); isStr {
			return b.ptrW == 4
		}
		// Tuple values are always pointer-shaped headered boxes
		// (TupleLit lowering); rc_inc/dec + box_free apply.
		if _, isTuple := t.(ast.TupleType); isTuple {
			return true
		}
		return false
	}
	// Phase 2d-borrow: parameters are BORROWED, not owned. The
	// caller no longer inc's a tracked argument when passing it
	// (the matching arg-inc at the OpCallDirect site is gone), so
	// the callee must NOT dec its parameters at exit — doing so
	// would underflow the rc. A borrowed value's lifetime is
	// owned by the caller; the callee only reads/mutates through
	// the borrow. This is what lets a Map passed to a function be
	// mutated in place (the handle stays rc==1, so the Phase 2d
	// copy-on-write check mutates rather than copies), while a
	// genuine local alias (`var m2 = m1`) still inc's and so gets
	// a copy on write. Only OWNED locals are dec'd below.
	seen := map[string]bool{}
	if exclude != "" {
		// Move-on-return: the returned local is handed to the caller
		// without an inc, so it must NOT be dec'd here.
		seen[exclude] = true
	}
	// Move-on-alias: locals consumed by a single-use alias were never
	// inc'd (the transfer moved the reference to the alias target), so
	// they must NOT be dec'd here either.
	for name := range b.movedLocals {
		seen[name] = true
	}
	for _, v := range b.info.Locals[b.fn] {
		if !rcTracked(v.Type) {
			continue
		}
		if seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		slot, ok := b.locals[v.Name]
		if !ok {
			continue
		}
		emitDec(slot, v.Type, b.freeEligible[v.Name], v.Name)
	}
}

// arrElemIsRcTracked reports whether an array element type is a
// pointer-shaped rc-tracked value — array / struct (incl. Map) /
// enum / closure. These are the elements __fern_drop_arr_ptr can
// safely dec on the array's last release (each was inc'd at
// array-literal construction). Strings are deliberately excluded:
// they are not rc-tracked yet (the SSO native flip is in flight),
// so they are never inc'd on insertion and must not be dec'd.
// Primitive elements (i32 etc.) are not pointers, so no drop.
func arrElemIsRcTracked(elem ast.Type) bool {
	switch elem.(type) {
	case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType:
		return true
	}
	return false
}

// irCaptureSlotSize mirrors closureconv.captureSlotSize: the env
// slot footprint of a capture (8 for wide scalars, ptrW for
// pointers, 2*ptrW for two-word strings, 4 otherwise). Kept in
// sync so the drop thunk reads captures at the same offsets the
// env was written with.
func irCaptureSlotSize(t ast.Type, ptrW int) int32 {
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return int32(2 * ptrW)
	}
	if ast.ElemSizeBytesFor(t, ptrW) == 8 {
		return 8
	}
	if ast.IsPointerType(t) {
		return int32(ptrW)
	}
	return 4
}

// hasRcCapture reports whether any capture is rc-tracked (i.e. was
// inc'd at MakeEnv and so needs dropping when the closure dies).
func hasRcCapture(caps []ast.Param, ptrW int) bool {
	for _, c := range caps {
		if arrElemIsRcTracked(c.Type) {
			return true
		}
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 4 {
			return true
		}
	}
	return false
}

// genClosureDropThunk builds the per-closure __closure_drop_<name>
// function: at the closure's last reference (rc==1) it drops each
// rc-tracked capture — arrays free their buffer (arr_dec /
// drop_arr_ptr), struct/enum/closure captures flat-dec one level
// (consistent with decValueOnStack) — then frees the env block via
// the generic __fern_closure_drop. Returns nil for closures with no
// rc-tracked captures (the generic helper already handles those).
// The thunk's env is a plain param (slot 0), not a closure-pair
// local, so re-loading it freely doesn't perturb ElideClosurePair.
func genClosureDropThunk(name string, caps []ast.Param, ptrW int, info *checker.Info, reg map[string]*ast.EnumDecl) *Func {
	if !hasRcCapture(caps, ptrW) {
		return nil
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	off := int32(0)
	for _, c := range caps {
		slot := irCaptureSlotSize(c.Type, ptrW)
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 4 {
			// Two-word string capture: load (data, len) from [env+off]
			// and reclaim via __fern_str_dec (balances the __fern_str_inc
			// at MakeEnv). Inside the env's is_unique branch, so the
			// capture is this closure's owned reference; inline / literal
			// strings no-op.
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthString},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			off += slot
			continue
		}
		if arrElemIsRcTracked(c.Type) {
			// Load the capture pointer from [env+off]. The thunk only
			// runs when every rc-tracked capture was inc'd at MakeEnv
			// (emitDec's closureTarget gate), and inside the env's
			// is_unique branch, so the captures are this closure's
			// exclusively-owned references — safe to deep-free. The
			// per-value drop fns is_unique-gate again, so a shared
			// capture only dec's.
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthPtr})
			if at, isArr := c.Type.(ast.ArrayType); isArr {
				if drop, ok := arrElemStructDropName(at.Elem, info); ok {
					// Array of concrete structs: deep-drop each element
					// box + the buffer (Stage B loop).
					ops = append(ops, Op{Kind: OpCallDirect, Str: drop, I32: 1})
				} else {
					helper := "__fern_arr_dec"
					if arrElemIsRcTracked(at.Elem) {
						helper = "__fern_drop_arr_ptr"
					}
					ops = append(ops,
						Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, ptrW))},
						Op{Kind: OpCallDirect, Str: helper, I32: 2})
				}
			} else if isMapType(c.Type) {
				// Map capture: reclaim the value column + buf + handle
				// (both helpers self-guard on the map's rc==1 and return
				// the map ptr, which the trailing OpDrop discards).
				ops = append(ops,
					Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1},
					Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
			} else if drop, ok := dropFnNameFor(c.Type, info, reg); ok {
				// Concrete-struct (or boxed generic-enum) capture: free its
				// box + nested children.
				ops = append(ops, Op{Kind: OpCallDirect, Str: drop, I32: 1})
			} else {
				// enum / closure capture: flat one-level dec (a union's
				// variant type isn't statically known; nested closures
				// keep the env-only drop).
				ops = append(ops, Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			}
			ops = append(ops, Op{Kind: OpDrop})
		}
		off += slot
	}
	ops = append(ops,
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpCallDirect, Str: "__fern_closure_drop", I32: 1},
		Op{Kind: OpReturn})
	return &Func{
		Name:       "__closure_drop_" + name,
		Params:     []ast.Param{{Name: "__cdenv", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// dropFnNameFor returns the generated recursive-drop function name for
// a NESTED value of type t, plus whether one exists. A CONCRETE user
// struct (rc-field-carrying OR childless) routes to __drop_struct_<Name>
// (genStructDropFn reads its exact fields). A CONCRETE (non-generic)
// enum with at least one statically-droppable payload routes to a
// tag-dispatched __drop_enum_<Name> — reading the runtime tag picks the
// exact per-variant payload type, so a union's differing variant types
// are handled correctly (no misread). Map / runtime handle types,
// arrays, closures, and generic enum instantiations (Args != nil; their
// box-vs-pair-form shape needs the type args, handled inline for locals)
// return ("", false) so the caller falls back to a flat one-level dec.
func dropFnNameFor(t ast.Type, info *checker.Info, reg map[string]*ast.EnumDecl) (string, bool) {
	switch v := t.(type) {
	case ast.StructType:
		if v.Name == "Map" {
			return "", false
		}
		if _, ok := info.Structs[v.Name]; !ok {
			return "", false
		}
		return "__drop_struct_" + v.Name, true
	case ast.EnumType:
		ed, ok := info.Enums[v.Name]
		if !ok {
			return "", false
		}
		if len(v.Args) > 0 {
			// Generic instantiation (Option[Item]). Substitute the type
			// args into the decl and route to a per-instantiation drop
			// IFF the substituted decl is heap-boxed (a pointer payload).
			// Scalar instantiations (Option[i32], pair-form, no box) read
			// false and fall through to the flat dec, exactly as before.
			// The substituted decl is stashed in reg under a mangled name
			// the worklist regenerates the body from. Without a registry
			// to record into (direct unit calls) we can't be regenerated,
			// so bail to the safe flat path.
			if reg == nil {
				return "", false
			}
			sub := substituteEnumDecl(ed, v.Args)
			if !enumHasPointerPayload(sub) {
				return "", false
			}
			mangled := mangleEnumInst(v)
			reg[mangled] = sub
			return "__drop_enum_" + mangled, true
		}
		if !enumNeedsDrop(ed) {
			return "", false
		}
		return "__drop_enum_" + v.Name, true
	}
	return "", false
}

// mangleEnumInst turns a generic enum instantiation type into a
// symbol-safe, injective name component for its `__drop_enum_<...>`
// drop function — `Option[Item]` → `Option_LB_Item_RB_`,
// `Result[Item, Err]` → `Result_LB_Item_C_Err_RB_`. Derived from the
// type's canonical String() with the non-identifier characters escaped
// to fixed tokens, so two distinct instantiations never collide and the
// same instantiation always mangles identically (routing ⇄ generation
// agreement, as the worklist requires). The escape tokens use only
// `[A-Za-z0-9_]`, keeping the name a valid wasm/asm symbol. The worklist
// resolves a `__drop_enum_<en>` name against info.Enums (concrete) before
// the generic registry, so a base name never shadows a real enum; the
// reverse — a hand-authored enum literally named to mimic an escaped
// instantiation (e.g. `Option_LB_Item_RB_`) — is a pathological clash we
// don't defend, as no realistic source produces it.
func mangleEnumInst(et ast.EnumType) string {
	return strings.NewReplacer(
		"[", "_LB_",
		"]", "_RB_",
		",", "_C_",
		" ", "",
	).Replace(et.String())
}

// enumNeedsDrop reports whether a concrete enum has a heap box worth
// reclaiming: at least one payload-carrying variant and no ParamType
// payload (generic). Mirrors enumVariantDropPlan's success condition
// without needing ptrW, so dropFnNameFor and the genEnumDropFn worklist
// agree on which enums get a __drop_enum_ fn.
func enumNeedsDrop(ed *ast.EnumDecl) bool {
	hasPayload := false
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if _, isParam := pt.(ast.ParamType); isParam {
				return false
			}
		}
		if len(v.Payloads) > 0 {
			hasPayload = true
		}
	}
	return hasPayload
}

// isMapType reports whether t is the runtime Map handle type. A
// Map-typed field / payload / capture reclaims its structure (value
// column + buf + handle) via __map_drop_values then __fern_map_drop,
// both of which self-guard on the map's own rc==1 and return the map
// ptr (so a stack value chains through).
func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}

// appendMapDrop appends the map-reclamation chain for a map pointer
// already on the operand stack.
func appendMapDrop(ops []Op) []Op {
	return append(ops,
		Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1},
		Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1},
		Op{Kind: OpDrop})
}

// enumHasPointerPayload reports whether any variant of ed carries a
// POINTER-shaped payload (array / struct / enum / closure / Map — all
// heap-boxed). This is the condition for "the eligible enum drop should
// take the tag-dispatch variant-plan path rather than the branchless
// uniform path": every such payload is deep-droppable in a tag-guarded
// arm (where its exact type is known), where the uniform path could
// only flat-dec it (and a union's variants differ at the shared
// offset). It's also the gate for adopting a generic instantiation's
// substituted decl — a pointer payload proves a heap-boxed (non-pair-
// form) instantiation, so the variant-plan's box_free is valid; scalar
// payloads (pair-form, no box) read false and stay on the flat path.
func enumHasPointerPayload(ed *ast.EnumDecl) bool {
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if arrElemIsRcTracked(pt) {
				return true
			}
		}
	}
	return false
}

// substituteEnumDecl returns a copy of ed with each variant payload's
// top-level ParamType bound to its concrete type arg (Option[Item] →
// Some(Item)). Reproduces exactly the payload types emitEnumNew sized
// the box from (b.info.VariantCallPayloads), so the resulting drop plan
// frees with the right box size. Returns ed unchanged when not a
// type-arg-bearing instantiation. Nested ParamType (e.g. `T[]`) is left
// alone — enumVariantDropPlan then finds no droppable load and bails,
// keeping the flat dec (safe).
func substituteEnumDecl(ed *ast.EnumDecl, args []ast.Type) *ast.EnumDecl {
	if len(ed.TypeParams) == 0 || len(args) != len(ed.TypeParams) {
		return ed
	}
	out := *ed
	out.Variants = make([]ast.EnumVariant, len(ed.Variants))
	for i, v := range ed.Variants {
		nv := v
		nv.Payloads = make([]ast.Type, len(v.Payloads))
		for j, pt := range v.Payloads {
			nv.Payloads[j] = resolveTypeParam(pt, ed.TypeParams, args)
		}
		out.Variants[i] = nv
	}
	return &out
}

// arrElemStructDropName returns the __drop_arr_struct_<Elem> function
// name for an array whose element type is a CONCRETE struct, plus
// whether one applies. Transitive reclamation Stage B routes an
// eligible array-of-struct drop to this generated loop (deep-dropping
// each element box, then freeing the buffer) instead of the flat-
// element __fern_drop_arr_ptr. The element type of an array is
// statically exact (no union ambiguity), so it's safe to dispatch a
// type-specific per-element drop. Array-of-array / array-of-enum /
// array-of-closure return ("", false) and keep __fern_drop_arr_ptr.
func arrElemStructDropName(elem ast.Type, info *checker.Info) (string, bool) {
	v, ok := elem.(ast.StructType)
	if !ok || v.Name == "Map" {
		return "", false
	}
	if _, ok := info.Structs[v.Name]; !ok {
		return "", false
	}
	return "__drop_arr_struct_" + v.Name, true
}

// genArrStructDropFn builds __drop_arr_struct_<Elem>(ptr): on the
// array's last reference (rc==1, real heap) it walks every element and
// drops it through __drop_struct_<Elem> (which is_unique-gates per
// element, so a shared element box only dec's), then hands the buffer
// to __fern_arr_dec for the rc-dec / freelist return. Element structs
// are pointer-shaped, so the stride is ptrW and the length lives at
// [ptr-4]. Slots: 0=ptr (param), 1=i, 2=len (scratch).
func genArrStructDropFn(elemName string, ptrW int) *Func {
	stride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// len = mem[ptr-4]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break out of the block (depth 1).
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __drop_struct_<Elem>(mem[ptr + i*stride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: "__drop_struct_" + elemName, I32: 1},
		{Kind: OpDrop},
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		// Dec / free the buffer itself (arr_dec re-checks rc==1).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_struct_" + elemName,
		Params:       []ast.Param{{Name: "__as", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// mapValDropName returns the column-walk drop function name for a Map
// whose VALUE type has a generated recursive drop (concrete struct or
// enum), plus whether one applies. The name embeds the per-value drop fn
// (__drop_map_via_<perValueDrop>), so the worklist regenerates the loop —
// and the per-value drop it calls — from the name alone, no type lookup.
// The map's drop routes here instead of the generic __map_drop_values
// (which only reclaims array values). Mirrors mapValHasDrop's domain.
func mapValDropName(st ast.StructType, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl) (string, bool) {
	if st.Name != "Map" || len(st.Args) < 2 {
		return "", false
	}
	perVal, ok := mapValHasDrop(st.Args[1], info, genEnumDrops)
	if !ok {
		return "", false
	}
	return "__drop_map_via_" + perVal, true
}

// genMapValDropFn builds __drop_map_via_<perValueDrop>(m): on the map's
// last reference (rc==1, guarded by __fern_rc_is_unique on the handle) it
// walks the value column and deep-drops each live value through
// perValueDrop (__drop_struct_<V> / __drop_enum_<V>, which is_unique-gate
// per value, so a value shared via an outstanding get/values borrow only
// dec's). The buf + handle are freed separately by the trailing
// __fern_map_drop the caller emits. Mirrors __map_drop_values' iteration:
// cap@buf+0, len@buf+4, entries at buf+16+cap*4, value at entry+ptrW with
// entryStride = 2*ptrW. Returns m so the caller's OpDrop pops a real
// value. Slots: 0=m (param), 1=buf, 2=len, 3=i, 4=entriesBase (scratch).
func genMapValDropFn(perValueDrop string, ptrW int) *Func {
	pw := int32(ptrW)
	entryStride := 2 * pw
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// buf = mem[m]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 1},
		// len = mem[buf+4]
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpAdd},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// entriesBase = buf + 16 + cap*4   (cap = mem[buf])
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 16},
		{Kind: OpAdd},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoad},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 4},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break (depth 1).
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __drop_struct_<V>(mem[entriesBase + i*entryStride + ptrW]); drop.
		{Kind: OpLoadLocal, I32: 4},
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: entryStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpConstI32, I32: pw},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: perValueDrop, I32: 1},
		{Kind: OpDrop},
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_map_via_" + perValueDrop,
		Params:       []ast.Param{{Name: "__dm", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genMapStrValDropFn / genMapStrKeyDropFn build the wasm string-column
// reclamation walks for a Map whose VALUE (resp. KEY) is a string: on the
// map's last reference (rc==1) the walk reclaims each string's heap buffer
// via __fern_str_dec. A string K/V is stored BOXED — the column holds an
// 8-byte cell pointer whose contents are the two-word (data, len) pair
// (boxIntoCell at set). So per entry we load the cell pointer at the
// column's byte offset (0 for keys, ptrW for values), and if non-null load
// (data, len) from it via the two-word WidthString load and __fern_str_dec
// the buffer (inline / literal strings no-op), then __fern_cell_free the
// now-dead 16-byte cell itself back to the freelist. The buf + handle are
// freed by the trailing __fern_map_drop the caller emits. Mirrors
// genMapValDropFn's iteration: cap@buf+0, len@buf+4, entries at
// buf+16+cap*4, entryStride = 2*ptrW.
// Slots: 0=m (param), 1=buf, 2=len, 3=i, 4=entriesBase, 5=cellPtr.
func genMapStrValDropFn(ptrW int) *Func {
	return genMapStrColDropFn("__drop_map_str_values", int32(ptrW), ptrW)
}

func genMapStrKeyDropFn(ptrW int) *Func {
	return genMapStrColDropFn("__drop_map_str_keys", 0, ptrW)
}

func genMapStrColDropFn(name string, colOff int32, ptrW int) *Func {
	pw := int32(ptrW)
	entryStride := 2 * pw
	// Inner block per entry differs by backend:
	//   wasm (ptrW=4): the kv slot stores a cell pointer; deref to load
	//     the (data, len) two-word string, __fern_str_dec it, then
	//     __fern_cell_free the now-dead 16-byte cell.
	//   natives (ptrW=8): the kv slot stores the string data pointer
	//     directly (no boxing — the slot is already pointer-wide). One
	//     __fern_rc_dec per entry is the whole reclamation; the L2
	//     header at data-8 + rc-sentinel literals from prereqs 1+2 make
	//     this safe across heap + literal sources.
	var inner []Op
	if ptrW == 4 {
		inner = []Op{
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpLoad, Width: WidthString},
			{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			{Kind: OpDrop},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpCallDirect, Str: "__fern_cell_free", I32: 1},
			{Kind: OpDrop},
			{Kind: OpEnd},
		}
	} else {
		inner = []Op{
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
			{Kind: OpDrop},
			{Kind: OpEnd},
		}
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// buf = mem[m]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 1},
		// len = mem[buf+4]
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpAdd},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// entriesBase = buf + 16 + cap*4   (cap = mem[buf])
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 16},
		{Kind: OpAdd},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoad},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 4},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break (depth 1).
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// cellOrDataPtr = mem[entriesBase + i*entryStride + colOff]
		{Kind: OpLoadLocal, I32: 4},
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: entryStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpConstI32, I32: colOff},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 5},
	}
	ops = append(ops, inner...)
	ops = append(ops,
		// i = i + 1; continue.
		Op{Kind: OpLoadLocal, I32: 3},
		Op{Kind: OpConstI32, I32: 1},
		Op{Kind: OpAdd},
		Op{Kind: OpStoreLocal, I32: 3},
		Op{Kind: OpBr, I32: 0},
		Op{Kind: OpEnd}, // loop
		Op{Kind: OpEnd}, // block
		Op{Kind: OpEnd}, // if rc==1
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn},
	)
	return &Func{
		Name:         name,
		Params:       []ast.Param{{Name: "__dm", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// pointer-shaped field (arrays, enums/unions, closures, Map, childless
// structs) takes a flat one-level __fern_rc_dec — matching the
// pre-transitive behaviour for those shapes (deep array-element,
// enum-payload, and map-key reclamation are later slices). Used by the
// generated __drop_struct_ bodies; the inline (builder) struct-field
// sweep delegates equivalently.
func appendChildDrop(ops []Op, t ast.Type, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl) []Op {
	// Two-word string value (wasm): the caller loaded (data, len) via a
	// string-aware load (payloadLoadOpFor), so reclaim via __fern_str_dec.
	// Reached from genEnumDropFn's payload drop (struct string fields are
	// handled inline in genStructDropFn before reaching here).
	if _, isStr := t.(ast.StringType); isStr && ptrW == 4 {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			Op{Kind: OpDrop})
	}
	if isMapType(t) {
		return appendMapDrop(ops)
	}
	if name, ok := dropFnNameFor(t, info, reg); ok {
		return append(ops,
			Op{Kind: OpCallDirect, Str: name, I32: 1},
			Op{Kind: OpDrop})
	}
	if at, ok := t.(ast.ArrayType); ok {
		// Any array field frees its buffer (see dropStructField for the
		// rationale): array-of-struct deep-drops elements + buffer,
		// array-of-rc frees the outer buffer, plain arrays arr_dec.
		if name, ok := arrElemStructDropName(at.Elem, info); ok {
			return append(ops,
				Op{Kind: OpCallDirect, Str: name, I32: 1},
				Op{Kind: OpDrop})
		}
		helper := "__fern_arr_dec"
		if arrElemIsRcTracked(at.Elem) {
			helper = "__fern_drop_arr_ptr"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ptrW == 4 {
			helper = "__fern_drop_arr_str"
		}
		return append(ops,
			Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, ptrW))},
			Op{Kind: OpCallDirect, Str: helper, I32: 2},
			Op{Kind: OpDrop})
	}
	return append(ops,
		Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop})
}

// genStructDropFn builds the recursive __drop_struct_<Name> function:
// at the value's last reference (rc==1) it drops each rc-tracked field
// — recursing into nested struct fields via their own drop fns — then
// returns the box to the freelist; otherwise it just dec's. The box was
// alloc'd as `structFieldLayout size + 8` rc header, so __fern_box_free
// frees base = data-8, size+8 (structFieldLayout's size already
// accounts for the header). Works for a childless struct too: the
// field loop is empty, so it just is_unique-gates and frees the box.
func genStructDropFn(name string, sd *ast.StructDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl) *Func {
	offs, size := structFieldLayout(sd.Fields, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	for _, f := range sd.Fields {
		_, isStr := f.Type.(ast.StringType)
		isStr = isStr && ptrW == 4
		if !arrElemIsRcTracked(f.Type) && !isStr {
			continue
		}
		ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
		if off := offs[f.Name]; off != 0 {
			ops = append(ops, Op{Kind: OpConstI32, I32: off}, Op{Kind: OpAdd})
		}
		if isStr {
			// Two-word string field: load (data, len) and reclaim via
			// __fern_str_dec at the struct's last reference. Inline and
			// static-literal strings are no-ops (flag / sentinel); a
			// headered heap buffer frees at its own rc==1. The field was
			// retained on construction (emitAliasInc → __fern_str_inc),
			// so this dec balances. Direct string fields only — a string
			// nested in an array / tuple / enum field reclaims via that
			// container's own (future) string-aware drop.
			ops = append(ops, Op{Kind: OpLoad, Width: WidthString})
			ops = append(ops,
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		ops = append(ops, Op{Kind: OpLoad, Width: WidthPtr})
		ops = appendChildDrop(ops, f.Type, info, ptrW, reg)
	}
	ops = append(ops,
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpConstI32, I32: size},
		Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2},
		Op{Kind: OpDrop},
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn})
	return &Func{
		Name:       "__drop_struct_" + name,
		Params:     []ast.Param{{Name: "__ds", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// genEnumDropFn builds the tag-dispatched __drop_enum_<Name> function
// for a concrete enum: at the value's last reference (rc==1) it reads
// the tag, and in each variant arm — where the payload type is
// statically exact — deep-drops the variant's payloads (recursing via
// appendChildDrop) then frees the box with THAT variant's size;
// otherwise it dec's. Payloadless / sentinel values fail the is_unique
// gate and take the dec path. Mirrors the inline non-uniform enum drop
// (emitDec), but as a standalone fn so a nested enum field / payload /
// capture can route to it. Slots: 0=ptr (param), 1=tag (scratch).
func genEnumDropFn(name string, ed *ast.EnumDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl) *Func {
	plan, ok := enumVariantDropPlan(ed, ptrW)
	if !ok {
		return nil
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// tag = mem[ptr+0] → slot 1 (stashed so arms read it after a
		// variant's box_free has freed the box).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 1},
	}
	for _, vd := range plan {
		ops = append(ops,
			Op{Kind: OpLoadLocal, I32: 1},
			Op{Kind: OpConstI32, I32: int32(vd.tag)},
			Op{Kind: OpEq},
			Op{Kind: OpIf, I32: BlockTypeVoid})
		for _, ld := range vd.loads {
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if ld.off != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: ld.off}, Op{Kind: OpAdd})
			}
			ops = append(ops, payloadLoadOpFor(ld.typ, ptrW))
			ops = appendChildDrop(ops, ld.typ, info, ptrW, reg)
		}
		ops = append(ops,
			Op{Kind: OpLoadLocal, I32: 0},
			Op{Kind: OpConstI32, I32: vd.size},
			Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2},
			Op{Kind: OpDrop},
			Op{Kind: OpEnd})
	}
	ops = append(ops,
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn})
	return &Func{
		Name:         "__drop_enum_" + name,
		Params:       []ast.Param{{Name: "__de", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// enumDropLoad is one pointer-shaped payload slot the enum drop
// must dec: the offset from the box's data pointer plus the
// payload's static type (which decValueOnStack uses to pick the
// recursive array drop vs a flat dec).
type enumDropLoad struct {
	off int32
	typ ast.Type
}

// uniformEnumDropLoads reports the per-box droppable payload loads
// for an enum IFF every payload-carrying variant shares an
// identical droppable signature — same offsets, and the same
// array-drop-vs-flat-dec kind at each. In that case the loads can
// be emitted unconditionally inside the is_unique guard with no
// runtime tag switch, because every heap box of this enum (whatever
// its tag) holds droppable pointers at exactly those offsets.
//
// This is the union shape (`type V = A | B | ...`): each variant
// carries a single struct pointer at offset 4. Payloadless
// variants (sentinels — never heap boxes) don't constrain the
// signature and are skipped. Returns (nil, false) when no variant
// has a droppable payload, or when payload-carrying variants
// disagree — those enums fall back to the plain box dec (their
// payloads leak, which is safe under no-free). Generic ParamType
// payloads are not statically droppable, so generic enums return
// (nil, false) too.
func uniformEnumDropLoads(ed *ast.EnumDecl, ptrW int) ([]enumDropLoad, bool) {
	dropKind := func(t ast.Type) (int, bool) {
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) {
			return 1, true // recursive array drop
		}
		if arrElemIsRcTracked(t) {
			return 2, true // flat dec (struct / enum / closure)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 4 {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		return 0, false
	}
	var want []enumDropLoad
	var wantKey string
	have := false
	for _, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no box
		}
		offsets, _ := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		var loads []enumDropLoad
		key := ""
		for i, pt := range v.Payloads {
			kind, ok := dropKind(pt)
			if !ok {
				continue
			}
			loads = append(loads, enumDropLoad{off: offsets[i], typ: pt})
			key += fmt.Sprintf("%d:%d;", offsets[i], kind)
		}
		if len(loads) == 0 {
			// A payload-carrying variant with NO droppable payload
			// (e.g. Some(i32), JBool(bool)) breaks uniformity: a box
			// of that variant has nothing to drop at the shared
			// offsets, so an unconditional dec would be wrong.
			return nil, false
		}
		if !have {
			want, wantKey, have = loads, key, true
			continue
		}
		if key != wantKey {
			return nil, false
		}
	}
	if !have {
		return nil, false
	}
	return want, true
}

// uniformEnumBoxSize reports the heap-box payload size shared by every
// payload-carrying variant of an enum, IFF they all agree. An enum box
// is alloc'd per-variant as `payloadLayout size + rcHeaderBytes`, so
// freeing it at drop needs a statically-known size — only possible
// when the variants don't disagree (e.g. a union of single-pointer
// variants all size to the same box). Returns (size, false) when
// variants disagree or none carry a payload; such enums keep leaking
// their box (safe under the rc==1 gate). Pairs with
// uniformEnumDropLoads: an enum frees its box only when BOTH agree.
func uniformEnumBoxSize(ed *ast.EnumDecl, ptrW int) (int32, bool) {
	var size int32
	have := false
	for _, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no heap box
		}
		_, sz := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		if !have {
			size, have = sz, true
		} else if sz != size {
			return 0, false
		}
	}
	if !have {
		return 0, false
	}
	return size, true
}

// variantDrop is the per-variant drop plan for the non-uniform enum
// box reclamation path: the runtime tag that selects this variant, the
// droppable payload loads, and the heap-box payload size to free.
type variantDrop struct {
	tag   int
	loads []enumDropLoad
	size  int32
}

// enumVariantDropPlan returns a per-variant drop plan for an enum whose
// payload-carrying variants DON'T share a uniform layout (so the
// uniform branchless path doesn't apply). emitDec emits a tag switch
// over these: each real box (rc==1) reads its tag, drops that variant's
// droppable payloads, and frees with that variant's exact box size.
// Payloadless variants are static sentinels (never rc==1 boxes), so
// they're skipped. Bails (false) if any variant carries a generic
// ParamType payload — its drop-kind / size isn't statically known, so
// the enum keeps leaking its box (safe). Mirrors uniformEnumDropLoads'
// dropKind classification.
func enumVariantDropPlan(ed *ast.EnumDecl, ptrW int) ([]variantDrop, bool) {
	dropKind := func(t ast.Type) (int, bool) {
		if _, isParam := t.(ast.ParamType); isParam {
			return 0, false
		}
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) {
			return 1, true // recursive array drop
		}
		if arrElemIsRcTracked(t) {
			return 2, true // flat dec (struct / enum / closure)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 4 {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		return 0, false // scalar — nothing to drop
	}
	var plan []variantDrop
	for i, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no heap box
		}
		offsets, size := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		var loads []enumDropLoad
		for j, pt := range v.Payloads {
			if _, isParam := pt.(ast.ParamType); isParam {
				return nil, false // generic payload — can't size/drop safely
			}
			if _, ok := dropKind(pt); !ok {
				continue
			}
			loads = append(loads, enumDropLoad{off: offsets[j], typ: pt})
		}
		plan = append(plan, variantDrop{tag: i, loads: loads, size: size})
	}
	if len(plan) == 0 {
		return nil, false
	}
	return plan, true
}

func lowerFunc(fn *ast.FuncDecl, info *checker.Info, ptrW int, pairForm map[string]bool, closureCaps map[string][]ast.Param, genEnumDrops map[string]*ast.EnumDecl) (*Func, error) {
	out := &Func{
		Name:       fn.Name,
		Params:     fn.Params,
		Locals:     info.Locals[fn],
		ReturnType: fn.ReturnType,
		Captures:   fn.Captures,
	}
	b := &builder{
		info:         info,
		fn:           fn,
		out:          out,
		locals:       map[string]int32{},
		scratchType:  map[int32]ast.Type{},
		ptrW:         ptrW,
		pairForm:     pairForm,
		closureCaps:  closureCaps,
		genEnumDrops: genEnumDrops,
		thisIsPair:   pairForm[fn.Name],
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
	// Borrow-aware free analysis: which array locals are OWNED and
	// thus safe to return to the freelist at rc==0. Borrowed /
	// borrowed-derived locals are excluded (only the owner frees).
	b.freeEligible = b.computeFreeEligible()
	b.moveSites = map[ast.Node]bool{}
	b.movedLocals = b.computeMovedLocals()
	b.closureTarget = map[string]string{}
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
	// Phase 1d-v safety net: zero-init every array-typed local
	// slot at function entry. The function-exit dec sweep (in
	// `emitRcDecLocalsAtExit`) visits every declared
	// array-typed local regardless of whether its Var statement
	// was reached at runtime — a conditional `if (false) { var
	// arr = [1, 2]; }` registers `arr` in info.Locals but never
	// runs its Var. Pre-zeroing makes the dec helper's `if ptr
	// == 0` short-circuit fire on never-initialised slots.
	//
	// Zero by slot — same `b.locals[name]` slot the dec sweep
	// resolves — so the two sides agree even when multiple Var
	// declarations across separate scopes share a name (the slot
	// map only keeps the last entry, all those declarations
	// store to the same physical slot, and only that slot needs
	// the safety zero).
	zeroSeen := map[string]bool{}
	zeroRcTracked := func(t ast.Type) bool {
		// Phase 1d covers arrays; Phase 1e-struct-iii widens to
		// user structs. Matches the rc-tracked set used by the
		// exit dec sweep so the safety zero and the dec agree on
		// which slots they touch.
		if _, isArr := t.(ast.ArrayType); isArr {
			return true
		}
		if _, isStruct := t.(ast.StructType); isStruct {
			return true
		}
		// Phase 1e-enums-ii: zero enum slots too so the exit dec
		// sweep's `ptr == 0` null guard fires on never-initialised
		// enum locals (conditional / match-arm declarations).
		if _, isEnum := t.(ast.EnumType); isEnum {
			return true
		}
		// Phase 1e-closures-ii: zero FuncType (closure) slots too.
		if _, isFunc := t.(*ast.FuncType); isFunc {
			return true
		}
		// Zero tuple slots too: the exit-dec null guard then fires on
		// a never-initialised tuple local (conditional declaration).
		if _, isTuple := t.(ast.TupleType); isTuple {
			return true
		}
		// Two-word string locals: zero both slots so a never-initialised
		// string local's exit dec sees (data=0, len=0) — __fern_str_dec
		// null-guards data=0. wasm32 only (ptrW==4), matching the dec
		// sweep's string gate.
		if _, isStr := t.(ast.StringType); isStr {
			return b.ptrW == 4
		}
		return false
	}
	for _, v := range info.Locals[fn] {
		if !zeroRcTracked(v.Type) {
			continue
		}
		if zeroSeen[v.Name] {
			continue
		}
		zeroSeen[v.Name] = true
		slot, ok := b.locals[v.Name]
		if !ok {
			continue
		}
		// A two-word string slot consumes two operand values (data, len);
		// push two zeros so OpStoreLocal balances.
		if _, isStr := v.Type.(ast.StringType); isStr {
			b.emit(Op{Kind: OpConstI32, I32: 0})
		}
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
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
			b.emitRcDecLocalsAtExit()
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
			b.emitRcDecLocalsAtExit()
			b.emit(Op{Kind: OpReturnPair})
		case isFloat(fn.ReturnType):
			b.emit(Op{Kind: OpConstF32, F32: 0})
			b.emitRcDecLocalsAtExit()
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
				b.emitRcDecLocalsAtExit()
				b.emit(Op{Kind: OpReturn})
			} else {
				b.emit(Op{Kind: OpConstI32, I32: 0})
				b.emitRcDecLocalsAtExit()
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
	return b.lookupVariantOn(name, "")
}

// lookupVariantOn resolves a variant by name, optionally restricted
// to a specific enum. The checker stamps `Ident.EnumName` when it
// resolves a qualified reference (`Color.Red`) or when an
// unqualified bare name is unambiguous; passing it back here makes
// the IR's resolution deterministic even when two enums declare the
// same variant. The `enumName == ""` fallback keeps every legacy
// caller (match-arm lookup, indirect-call dispatch) working: the
// checker has already rejected ambiguous unqualified references, so
// any name that reaches the IR with no qualifier is single-owner.
func (b *builder) lookupVariantOn(name, enumName string) (foundEnum string, varIdx int, payloadCount int, ok bool) {
	if enumName != "" {
		if ed, ok := b.info.Enums[enumName]; ok {
			for i, v := range ed.Variants {
				if v.Name == name {
					return enumName, i, len(v.Payloads), true
				}
			}
		}
		return "", 0, 0, false
	}
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
	// Phase 1e-enums-ii: the reboxed pair carries the same 8-byte
	// rc header as emitEnumNew (rc=1 at [base+0]; data = base+8;
	// tag / payload stores shift by rcHeaderBytes). Without it,
	// the dec sweep that enum-ii's predicate widening enables
	// would read heap-allocator metadata at [data-8] and corrupt
	// it; with it, the reboxed Option / Result value picks up real
	// rc tracking exactly like a directly-constructed box.
	const rcHeaderBytes = 8
	// Stack: [tag, payload] — top is payload. Stash payload in
	// a scratch local so we can alloc + store the box without
	// shuffling values around.
	payloadSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_payload_%d", payloadSlot)] = payloadSlot
	b.emit(Op{Kind: OpStoreLocal, I32: payloadSlot})
	tagSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_tag_%d", tagSlot)] = tagSlot
	b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
	b.emit(Op{Kind: OpConstI32, I32: boxSize + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	boxSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__pair_repack_box_%d", boxSlot)] = boxSlot
	b.emit(Op{Kind: OpStoreLocal, I32: boxSlot})
	// rc = 1 at [base + 0].
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// Store tag at data+0 (= base + rcHeaderBytes; always 4-byte i32).
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
	b.emit(Op{Kind: OpStore})
	// Store payload at data + payloadOff (4 for i32, 8 for
	// pointer-shape) and width.
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + payloadOff})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
	b.emit(storeOp)
	// Push the user-visible data pointer (= base + rc header).
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
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
	// Phase 1e-enums-i: enum variant boxes grow an 8-byte rc
	// header to match the array / struct layout. Alloc bumps by
	// `rcHeaderBytes`; rc=1 lives at `[base + 0]`; the returned
	// data pointer is `base + rcHeaderBytes`. Per-field offsets
	// shift by `rcHeaderBytes` so we keep using the same
	// baseSlot (storing `base`) — same accounting as the
	// StructLit migration in Phase 1e-struct-i.
	const rcHeaderBytes = 8
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__enum_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// rc = 1 at [base + 0].
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// Store tag at offset 0 of data (= base + rcHeaderBytes).
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
	b.emit(Op{Kind: OpStore})
	for i, a := range args {
		// Emit the arg expression first, into a scratch slot.
		// Pushing `base+offset` BEFORE evaluating the expression
		// is unsafe on wasm: if the expression contains an
		// if-block-with-result, the (addr, value) pair the store
		// expects would straddle the if-block's local stack scope
		// — the validator rejects loads / stores in a branch that
		// would consume values pushed outside it.
		//
		// Stash-then-restore lets the expression be a full
		// arbitrary control-flow shape; the (addr, value) pair
		// for OpStore lands on the operand stack only after the
		// expression's stack effects have settled.
		var pt ast.Type
		if i < len(payloadTypes) {
			pt = payloadTypes[i]
		}
		if err := b.expr(a); err != nil {
			return err
		}
		valSlot := b.allocSlot()
		if pt != nil {
			b.scratchType[valSlot] = pt
		}
		b.emit(Op{Kind: OpStoreLocal, I32: valSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offsets[i]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoadLocal, I32: valSlot})
		b.emit(payloadStoreOpFor(pt, b.ptrW))
	}
	// Push the user-visible data pointer (= base + rc header).
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
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

// isLiteralMatch reports whether every arm of a `match` is a
// literal pattern or the unguarded wildcard (i.e. no arm carries
// a VariantName). Used to dispatch between the enum-match
// lowering (default) and the literal-match if-else-chain
// lowering. The checker has already validated the arms — by the
// time we run here, an arm is literal/wildcard XOR variant.
func isLiteralMatch(arms []*ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.IsWildcard || arm.Literal != nil {
			continue
		}
		return false
	}
	return true
}

// isLiteralMatchExprArms is the MatchExpr-side counterpart.
func isLiteralMatchExprArms(arms []*ast.MatchExprArm) bool {
	for _, arm := range arms {
		if arm.IsWildcard || arm.Literal != nil {
			continue
		}
		return false
	}
	return true
}

// emitLiteralMatch lowers a non-enum `match` to an if-else-if
// chain. For each literal arm, evaluate the scrutinee + literal
// + eq-test; on true, run the arm body and branch out of the
// outer exit block. The wildcard arm (if any) runs as the final
// fall-through. Strings use OpStrEq via the regular Binary
// path; ints / bools use OpEq. Guards on literal arms make the
// arm conditional within its match — same exit-block topology.
func (b *builder) emitLiteralMatch(n *ast.Match) error {
	// Cache the scrutinee in a local so each arm's eq-test can
	// reload it without re-evaluating side effects.
	tagT := b.exprType(n.Tag)
	scrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__lit_match_scr_%d", scrSlot)] = scrSlot
	if tagT != nil {
		b.scratchType[scrSlot] = tagT
	}
	if err := b.expr(n.Tag); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: scrSlot})

	b.openBlock(BlockTypeVoid)
	exitDepth := b.depth
	for _, arm := range n.Arms {
		if arm.IsWildcard {
			// Wildcard guard: when false, the arm is a no-op and
			// the next arm runs (but for the unguarded shape this
			// is the fall-through default).
			if arm.Guard != nil {
				if err := b.expr(arm.Guard); err != nil {
					return err
				}
				b.openIf(BlockTypeVoid)
				if err := b.stmt(arm.Body); err != nil {
					return err
				}
				b.brTo(exitDepth, false)
				b.closeScope()
				continue
			}
			if err := b.stmt(arm.Body); err != nil {
				return err
			}
			b.brTo(exitDepth, false)
			continue
		}
		// Literal arm: build `scrutinee == literal` as a Binary
		// AST node so the existing OpStrEq / OpEq dispatch
		// (with IsStringCmp / IsFloat / Width settled by the
		// checker pass over Literal already) handles each
		// scrutinee type uniformly.
		cond := &ast.Binary{
			P:           arm.P,
			Op:          "==",
			Left:        &ast.Ident{P: arm.P, Name: literalMatchScrName(scrSlot)},
			Right:       arm.Literal,
			IsStringCmp: isStringType(tagT),
			IsFloat:     isFloatType(tagT),
		}
		// Stash the scrutinee under a synthetic local name so
		// Ident lookup hits scrSlot — saves us a manual
		// load/eval shape. The synthetic name's slot is already
		// in b.locals, set above.
		if err := b.expr(cond); err != nil {
			return err
		}
		if arm.Guard != nil {
			b.openIf(BlockTypeVoid)
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
			if err := b.stmt(arm.Body); err != nil {
				return err
			}
			b.brTo(exitDepth, false)
			b.closeScope()
			b.closeScope()
			continue
		}
		b.openIf(BlockTypeVoid)
		if err := b.stmt(arm.Body); err != nil {
			return err
		}
		b.brTo(exitDepth, false)
		b.closeScope()
	}
	b.closeScope() // outer exit block
	return nil
}

func literalMatchScrName(slot int32) string {
	return fmt.Sprintf("__lit_match_scr_%d", slot)
}

// emitLiteralMatchExpr is the expression-form counterpart of
// emitLiteralMatch. Same scrutinee-cache + per-arm eq-test
// shape, but each arm body is an Expr whose value is stored
// into a result slot before branching out. The post-block
// load yields the unified arm type as the match-expression's
// value.
func (b *builder) emitLiteralMatchExpr(n *ast.MatchExpr) error {
	tagT := b.exprType(n.Tag)
	// Cache scrutinee.
	scrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__lit_match_scr_%d", scrSlot)] = scrSlot
	if tagT != nil {
		b.scratchType[scrSlot] = tagT
	}
	if err := b.expr(n.Tag); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: scrSlot})

	// Result slot — type inferred from the first non-polymorphic
	// arm body, mirroring the enum-match path.
	resultType := ast.Type(ast.NumberType{})
	for _, arm := range n.Arms {
		if arm == nil || arm.Body == nil {
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
		if _, ok := t.(ast.StringType); ok {
			resultType = ast.StringType{}
			break
		}
		if _, ok := t.(ast.BoolType); ok {
			resultType = ast.BoolType{}
			break
		}
	}
	resultSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__matchexpr_r_%d", resultSlot)] = resultSlot
	b.scratchType[resultSlot] = resultType

	b.openBlock(BlockTypeVoid)
	exitDepth := b.depth
	for _, arm := range n.Arms {
		if arm.IsWildcard {
			if arm.Guard != nil {
				if err := b.expr(arm.Guard); err != nil {
					return err
				}
				b.openIf(BlockTypeVoid)
				if err := b.expr(arm.Body); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
				b.brTo(exitDepth, false)
				b.closeScope()
				continue
			}
			if err := b.expr(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(exitDepth, false)
			continue
		}
		cond := &ast.Binary{
			P:           arm.P,
			Op:          "==",
			Left:        &ast.Ident{P: arm.P, Name: literalMatchScrName(scrSlot)},
			Right:       arm.Literal,
			IsStringCmp: isStringType(tagT),
			IsFloat:     isFloatType(tagT),
		}
		if err := b.expr(cond); err != nil {
			return err
		}
		if arm.Guard != nil {
			b.openIf(BlockTypeVoid)
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
			if err := b.expr(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(exitDepth, false)
			b.closeScope()
			b.closeScope()
			continue
		}
		b.openIf(BlockTypeVoid)
		if err := b.expr(arm.Body); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
		b.brTo(exitDepth, false)
		b.closeScope()
	}
	b.closeScope()
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	return nil
}

func isStringType(t ast.Type) bool {
	_, ok := t.(ast.StringType)
	return ok
}

func isFloatType(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
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
			b.emitRcDecLocalsAtExit()
			b.emit(Op{Kind: OpReturnVoid})
			return nil
		}
		// Pair-form return: this function was marked eligible
		// for the (tag, payload) ABI by findPairFormFuncs.
		// `emitPairFormPushValue` handles every shape the
		// eligibility check accepts (variant literal, pair-form
		// tail call, IfExpr whose arms are themselves eligible)
		// by pushing (tag, payload) on the operand stack;
		// OpReturnPair then matches the (i32, i32) wasm return
		// signature. Defers fall back to the heap-box path —
		// pair-form is scoped to the no-defer subset for now.
		if b.thisIsPair && len(b.defers) == 0 && isPairFormReturnExpr(n.Value, b.pairVariants, b.pairForm) {
			if err := b.emitPairFormPushValue(n.Value); err != nil {
				return err
			}
			b.emitRcDecLocalsAtExit()
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
		// Return-transfer inc: when the returned value ALIASES an
		// rc-tracked local (or a field / element of one), the
		// caller becomes a new owner — but the value is still on
		// the operand stack while emitRcDecLocalsAtExit drops that
		// same local below. Without this inc the exit sweep would
		// dec the escaping value's rc (and, with the freelist on,
		// FREE the very buffer being returned — a use-after-free,
		// e.g. `lexer.tokenize` returning its `ts: Token[]` local).
		// Inc here so the sweep's dec nets the rc to the caller's
		// reference. __fern_rc_inc returns its argument, so the
		// value stays on the stack for OpReturn. Fresh return
		// values (call results / literals) aren't locals, so the
		// sweep never touches them — needsRcIncOnAlias filters to
		// exactly the alias case. (Under the old no-free arena this
		// drift was masked; it also fixes the symmetric latent
		// underflow where the caller later dec'd a value the callee
		// had already dec'd to 0.)
		// Move-on-return (Phase 4 pair-cancellation): when the value
		// is a bare rc-tracked LOCAL, the return-transfer inc and that
		// local's exit-sweep dec cancel. Emit neither — skip the inc
		// and exclude the local from the sweep — so the value is handed
		// to the caller at its current rc with no rc traffic. Only
		// applies with no defers (the defer path stashes the value in a
		// synthetic slot, so the returned value no longer aliases the
		// named local on the stack). Other returned aliases (field /
		// index loads) still take the inc; fresh values aren't locals.
		if id, ok := n.Value.(*ast.Ident); ok && len(b.defers) == 0 &&
			needsRcIncOnAlias(n.Value, b) && b.isOwnedRcLocal(id.Name) {
			b.emitRcDecLocalsAtExitExcept(id.Name)
			b.emit(Op{Kind: OpReturn})
			return nil
		}
		if needsRcIncOnAlias(n.Value, b) {
			// Transfer inc so the caller owns the returned alias and
			// the callee's exit-sweep dec is balanced. A returned
			// closure-via-Ident already took the move-on-return path
			// above (no inc, sweep-excluded); this covers the
			// remaining aliases (field / index loads), closures
			// included now that they free their env at last reference.
			b.emitAliasInc(n.Value)
		}
		b.emitRcDecLocalsAtExit()
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
		if mc, ok := n.Init.(*ast.MakeClosure); ok {
			// Single known closure source for this FuncType local —
			// emitDec can drop its captures via the per-closure thunk,
			// but ONLY if every rc-tracked capture here was actually
			// inc'd at MakeEnv (needsRcIncOnAlias). A capture that
			// wasn't (e.g. a nested closure's CaptureRef capture) would
			// be over-released by the thunk's unconditional drop, so
			// such closures fall back to the generic env-only drop.
			thunkSafe := true
			for _, capExpr := range mc.Captures {
				ct := b.exprType(capExpr)
				rcTracked := arrElemIsRcTracked(ct)
				if _, isStr := ct.(ast.StringType); isStr && b.ptrW == 4 {
					// A two-word string capture is dropped by the thunk
					// (__fern_str_dec), so it must have been inc'd at MakeEnv
					// or the thunk would over-release it.
					rcTracked = true
				}
				if rcTracked && !needsRcIncOnAlias(capExpr, b) {
					thunkSafe = false
					break
				}
			}
			if thunkSafe {
				b.closureTarget[n.Name] = mc.FuncName
			}
		}
		if err := b.expr(n.Init); err != nil {
			return err
		}
		idx, ok := b.locals[n.Name]
		if !ok {
			return fmt.Errorf("ir: var %q has no slot (compiler bug)", n.Name)
		}
		// Phase 1d: when the init expression aliases an existing
		// array (i.e. it's a bare ident load of an array-typed
		// variable), bump the refcount so the new binding owns
		// its own reference. Fresh allocations (array literals,
		// function returns, push results) come with rc = 1
		// already, so no inc needed there — only true aliases.
		// __fern_rc_inc returns the input pointer so it splices
		// into the expression chain without a temp local.
		// See docs/RC-PERCEUS-PLAN.md "Reference-creation sites".
		// Move-on-alias: skip the transfer inc at a move site
		// (computeMovedLocals) — the reference moves to this binding
		// and the source is excluded from the exit sweep, so the
		// inc/dec pair is elided.
		if needsRcIncOnAlias(n.Init, b) && !b.moveSites[n] {
			b.emitAliasInc(n.Init)
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
		// The temp is an rc-tracked tuple local (swept at scope exit).
		// When Init ALIASES an existing tuple (a bare ident / field /
		// index load — `var (a, b) = t`), the temp co-owns that box, so
		// bump the rc: otherwise the temp's exit box_free and the
		// source's would both free the same box (double free), or — for
		// a borrowed tuple PARAM — the temp would free the caller's box
		// (UAF). A fresh Init (TupleLit / call result) isn't alias-shaped
		// and reads false, so the temp owns the sole reference and frees
		// it normally. Mirrors the Var-binding alias inc.
		if needsRcIncOnAlias(n.Init, b) {
			b.emitAliasInc(n.Init)
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
			// Dup-on-projection: a pointer-shaped element is extracted by
			// reference (the load copies the box's stored pointer without
			// an inc). The binding now co-owns it alongside the tuple box,
			// so bump the rc — the binding (an owned, untainted rc local)
			// will dec/free it at scope exit, balanced by the tuple's
			// deep-drop dec of the same element. Without the dup the
			// binding and the tuple's drop would both release one
			// reference for a single count (double free / underflow).
			if _, isStr := tup.Elems[i].(ast.StringType); isStr && b.ptrW == 4 {
				// Two-word string element: dup via __fern_str_inc (consumes
				// + re-pushes the (data, len) pair) so the binding co-owns
				// the buffer alongside the tuple box. Without it the tuple's
				// deep-drop __fern_str_dec would free the buffer under the
				// still-live binding (UAF).
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
			} else if arrElemIsRcTracked(tup.Elems[i]) {
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
			}
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
		// Literal-pattern match: when the scrutinee isn't an enum
		// the arms are all literal-or-wildcard (the checker
		// dispatched to `checkLiteralMatch`). Lower as if-else-if
		// chain — eq-test against each literal, branch into the
		// arm body, fall through to wildcard.
		if isLiteralMatch(n.Arms) {
			if err := b.emitLiteralMatch(n); err != nil {
				return err
			}
			break
		}
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
			// Non-numeric type ascription (`None as Option[i32]`,
			// `[] as i32[]`, `Ok(1) as Result[i32, string]`). The
			// checker accepts these when the inner is assignable to
			// the target; the runtime representation is unchanged.
			// Inner has already evaluated to the right shape, so
			// this is a no-op at the IR level.
			_, srcIsNumOrFloat := n.InnerType.(ast.NumberType)
			if _, ok := n.InnerType.(ast.FloatType); ok {
				srcIsNumOrFloat = true
			}
			_, dstIsNumOrFloat := n.Target.(ast.NumberType)
			if _, ok := n.Target.(ast.FloatType); ok {
				dstIsNumOrFloat = true
			}
			if !srcIsNumOrFloat && !dstIsNumOrFloat {
				return nil
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
				// Width-tag the negate so the backend flips the
				// correct sign bit: f64 (bit 63) vs f32 (bit 31).
				// Without this, OpFNeg defaulted to the f32 form
				// and corrupted f64 values (`-5.0` came out
				// non-negative).
				w := 0
				if ft, ok := b.exprType(n.Operand).(ast.FloatType); ok && ft.NormalWidth() == 64 {
					w = 64
				}
				b.emit(Op{Kind: OpFNeg, Width: w})
				return nil
			}
			// No i32.neg on wasm; emit `0 - operand`. Width-tag
			// the zero + subtract so a wide (i64) operand uses the
			// 64-bit subtract rather than truncating to 32 bits.
			w := 0
			if nt, ok := b.exprType(n.Operand).(ast.NumberType); ok && nt.NormalWidth() == 64 {
				w = 64
			}
			if w == 64 {
				b.emit(Op{Kind: OpConstI64})
			} else {
				b.emit(Op{Kind: OpConstI32, I32: 0})
			}
			if err := b.expr(n.Operand); err != nil {
				return err
			}
			b.emit(Op{Kind: OpSub, Width: w})
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
		// Literal-pattern match-expr: arms are literal/wildcard,
		// scrutinee is number/string/bool. Lower as an if-chain
		// over the result slot — same shape as the stmt-form
		// emitLiteralMatch but each arm body is an Expr that gets
		// stored into the result slot before branching out.
		if isLiteralMatchExprArms(n.Arms) {
			if err := b.emitLiteralMatchExpr(n); err != nil {
				return err
			}
			return nil
		}
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
		b.emit(Op{Kind: OpLoad})             // tag at ptr+0
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
		// Allocate `headerBytes + len*stride` bytes (header + payload),
		// store the length at base+headerBytes-4, then each element
		// at base+headerBytes+i*stride and leave the content pointer
		// on the stack. Stride defaults to 4 (the historical i32 /
		// pointer layout) but drops to 1 / 2 for byte / halfword
		// arrays per ast.ElemSizeBytes.
		//
		// Header layout: `[pad:4, cap:4, rc:4, len:4 | pad-to-stride]`.
		//   data - 12: i32 capacity (new in Phase 2-prep) — the
		//              max number of elements the underlying
		//              buffer holds. Initial value matches len;
		//              future Phase 2 over-allocation will set
		//              cap > len so `arr.push` can mutate the
		//              tail in place when rc == 1.
		//   data - 8: i32 refcount
		//   data - 4: i32 length
		// `len(arr)` keeps reading from `[data - 4]`; rc / cap
		// readers know their fixed offsets. For stride > 16 we
		// still need the FIRST element to be stride-aligned
		// (Apple Silicon enforces 8-byte alignment for some
		// LDR/STR sequences and 16-byte alignment for SIMD); pad
		// the header up to `stride` so element 0 sits at a
		// stride-aligned offset from base. For stride ≤ 16 the
		// 16-byte header is already aligned.
		//
		// See `docs/RC-PERCEUS-PLAN.md` for the full phased rollout.
		nElems := int32(len(n.Elems))
		stride := int32(4)
		if n.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(n.ElemType, b.ptrW))
		}
		headerBytes := int32(16)
		if stride > 16 {
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
		// Capacity slot at base + headerBytes - 12 (so callers
		// can reach it via `data - 12`). Initialise to the
		// literal element count — Phase 2's mutate-in-place
		// fast path will need this on subsequent pushes.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		if headerBytes != 12 {
			b.emit(Op{Kind: OpConstI32, I32: headerBytes - 12})
			b.emit(Op{Kind: OpAdd})
		}
		b.emit(Op{Kind: OpConstI32, I32: nElems})
		b.emit(Op{Kind: OpStore})
		// Refcount slot at base + headerBytes - 8 (so callers
		// can reach it via `data - 8`). Initialise to 1 — this
		// array is uniquely owned by whoever's catching the
		// result.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		if headerBytes != 8 {
			b.emit(Op{Kind: OpConstI32, I32: headerBytes - 8})
			b.emit(Op{Kind: OpAdd})
		}
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpStore})
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
			// Phase 1d-viii: array-element initialisation is an
			// alias-creating site when the element type is also
			// array-shaped — e.g. `var matrix: u8[][] = [inner];`
			// stores `inner`'s pointer into the matrix's slot 0,
			// so the new matrix co-owns `inner`. Same gating as
			// the Var / Assign / call-arg / struct-field /
			// closure-capture sites.
			if needsRcIncOnAlias(el, b) {
				b.emitAliasInc(el)
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
		b.emit(Op{Kind: OpConstI32, I32: mapValTag(n.ValueType, b.ptrW, b.info, b.genEnumDrops)})
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
			// __method_Map_set now returns the map (Phase 2c
			// API audit). MapLit's per-entry call discards the
			// return — the map's address didn't change, we
			// re-use the stashed slot below.
			b.emit(Op{Kind: OpDrop})
		}
		b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	case *ast.TupleLit:
		// Same shape as StructLit — alloc enough heap for the
		// elements at their packed offsets, store each at
		// `offs[i]` with a width that matches the element's
		// type (so pointer-typed elements get pointer-width
		// slots on arm64).
		//
		// Tuple reclamation: like StructLit, the box carries an
		// 8-byte rc header before `data` (rc=1 at [base+0]); the
		// user-visible pointer is `base + rcHeaderBytes`, so
		// __fern_rc_inc/dec and __fern_box_free work uniformly.
		// Element offsets shift by the header. Field access /
		// destructure read `value + offs[i]` against this data
		// pointer unchanged (the header sits below it).
		elemTypes := make([]ast.Type, len(n.Elems))
		for i, elem := range n.Elems {
			elemTypes[i] = b.exprType(elem)
		}
		offs, size := tupleElemLayout(elemTypes, b.ptrW)
		const rcHeaderBytes = 8
		b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_tup_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// rc = 1 at [base + 0].
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpStore})
		for i, elem := range n.Elems {
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offs[i]})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(elem); err != nil {
				return err
			}
			// Phase 1d-viii: tuple element is a struct-lit-style
			// alias site. See the StructLit case below for the
			// gating rationale.
			if needsRcIncOnAlias(elem, b) {
				b.emitAliasInc(elem)
			}
			b.emit(payloadStoreOpFor(elemTypes[i], b.ptrW))
		}
		// Push the user-visible data pointer (= base + rc header).
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
		b.emit(Op{Kind: OpAdd})
	case *ast.StructLit:
		sd, ok := b.info.Structs[n.TypeName]
		if !ok {
			return fmt.Errorf("ir: unknown struct %q (compiler bug)", n.TypeName)
		}
		// Per-field layout — pointer-typed fields widen to
		// ptrW bytes on arm64 so heap addresses survive the
		// store/load round-trip. Wide / pointer fields are
		// 8-byte-aligned within the heap object.
		//
		// Phase 1e-struct-i: user-allocated struct values carry
		// an 8-byte rc header before `data`, mirroring the
		// array layout's rc-at-`data-8` convention so
		// `__fern_rc_inc/dec` work uniformly. The alloc bumps
		// by `rcHeaderBytes`; rc=1 lives at `[base + 0]`; the
		// returned user-visible pointer is `base + 8`. Field
		// offsets shift by `rcHeaderBytes` so we keep using
		// the same baseSlot (storing `base`) without changing
		// the SSA-lift's slot accounting.
		offs, size := structFieldLayout(sd.Fields, b.ptrW)
		const rcHeaderBytes = 8
		b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
		b.emit(Op{Kind: OpAlloc})
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// rc = 1 at [base + 0].
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpStore})
		for _, f := range n.Fields {
			off := offs[f.Name]
			b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
			b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + off})
			b.emit(Op{Kind: OpAdd})
			if err := b.expr(f.Value); err != nil {
				return err
			}
			// Phase 1d-viii: struct field initialisation is an
			// alias-creating site. `Holder { items: existing }`
			// stores `existing`'s pointer into the struct's
			// slot — the struct now co-owns the reference, so
			// the rc bumps. Same gating as the Var / Assign /
			// closure-capture sites: only fires when the
			// initialiser is alias-shaped (Ident, FieldAccess,
			// Index) and the field type is an array. Strings /
			// structs / enums / closures join in Phase 1e along
			// with their matching drop handlers.
			if needsRcIncOnAlias(f.Value, b) {
				b.emitAliasInc(f.Value)
			}
			// Reuse payloadStoreOp so the store is correctly
			// sized for the field's declared type: i32 / f32
			// / sub-i32 → 4 bytes, i64 / f64 → 8 bytes, and
			// pointer types (string / array / struct / enum
			// / slice / closure) → WidthPtr (4 on wasm32, 8
			// on arm64).
			b.emit(payloadStoreOpFor(fieldType(sd.Fields, f.Name), b.ptrW))
		}
		// Push the user-visible data pointer (= base + rc
		// header bytes). All callers receive this and use
		// offsets relative to it, unchanged from before.
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
		b.emit(Op{Kind: OpAdd})
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
		//
		// Phase 1d-vii: each captured array-typed alias gets an
		// inc — the closure's env now co-owns the reference
		// alongside the outer scope's local. Phase 1e's drop
		// handlers will dec captures when the closure itself is
		// reclaimed.
		for _, capExpr := range n.Captures {
			if err := b.expr(capExpr); err != nil {
				return err
			}
			if needsRcIncOnAlias(capExpr, b) {
				b.emitAliasInc(capExpr)
			}
		}
		b.emit(Op{Kind: OpMakeClosure, Str: n.FuncName, I32: int32(len(n.Captures))})
	case *ast.FieldAccess:
		// Qualified payload-less variant reference: `Color.Red`
		// in value position. The checker accepted it as an
		// EnumType; lower it as the same shared sentinel
		// `[tag=varIdx]` cell that `emitEnumNew` reuses for any
		// payload-less variant, so match / try sites just read
		// the tag with `[ptr+0]` like every other enum value.
		if tid, ok := n.Target.(*ast.Ident); ok {
			if _, isEnum := b.info.Enums[tid.Name]; isEnum {
				if _, varIdx, payloadCount, isVar := b.lookupVariantOn(n.Field, tid.Name); isVar && payloadCount == 0 {
					b.emit(Op{Kind: OpEnumSentinel, I32: int32(varIdx)})
					return nil
				}
			}
		}
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
		// Bare payload-less enum variant in value position
		// (`Green` in `(1, Green)`). Not a local / param, so the
		// lookups above miss; resolve it as a variant so an
		// enclosing TupleLit / StructLit sizes the slot at ptrW
		// (IsPointerType(EnumType) == true) instead of the
		// payloadSlotSize(nil) 4-byte default. Without this the
		// store side packs the variant pointer at offset 4 on
		// arm64 while the load side reads from offset 8 → garbage
		// pointer → segfault. Same family as the StructLit /
		// ArrayLit / TupleLit cases below.
		if ename, _, _, ok := b.lookupVariantOn(x.Name, x.EnumName); ok {
			return ast.EnumType{Name: ename}
		}
	case *ast.CaptureRef:
		// Captured variable references carry their resolved
		// outer-scope type on the AST node — needed when the
		// closure body asks "what struct/tuple is this?" for
		// field-access offset resolution.
		return x.Type
	case *ast.FString:
		// f-strings always produce a string. The arms of a
		// MatchExpr returning f-strings need to mark the
		// result slot as StringType so the codegen allocates
		// the two-word (data, len) slot pair on wasm32.
		_ = x
		return ast.StringType{}
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
		// inline-tagged returns from `__fern_strcat` go through
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
	case *ast.StructLit:
		// A struct literal is pointer-shaped; without this case
		// `payloadSlotSize(nil)` defaults to 4 and an enclosing
		// `(i32, Inner)` tuple stores the inner struct pointer at
		// offset 4 while the load side (which DOES know the static
		// type) reads from offset 8 on arm64. Garbage pointer →
		// segfault. Returns just enough type info for the
		// `IsPointerType` branch in `payloadSlotSize` —
		// Name + Args aren't consulted by slot sizing.
		return ast.StructType{Name: x.TypeName, Args: x.TypeArgs}
	case *ast.TupleLit:
		// Same shape as StructLit — a tuple-typed value is
		// pointer-shaped from the enclosing literal's POV. Recurse
		// on each element so the inner-tuple element types survive
		// for any downstream code that wants them; only
		// `IsPointerType(TupleType{...}) == true` is needed for
		// slot sizing.
		elems := make([]ast.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = b.exprType(e)
		}
		return ast.TupleType{Elems: elems}
	case *ast.ArrayLit:
		// Same family as StructLit / TupleLit — an array literal
		// stored as a tuple/struct element needs IsPointerType
		// → true so the slot gets ptrW bytes on arm64. ElemType
		// is set by the checker once elements are typed; nil-fall-
		// through still returns ArrayType (the IsPointerType
		// branch fires regardless).
		return ast.ArrayType{Elem: x.ElemType}
	case *ast.MapLit:
		// Map literal lowers to `Map` (the auto-injected struct);
		// pointer-shaped from the enclosing slot's POV.
		return ast.StructType{Name: "Map", Args: []ast.Type{x.KeyType, x.ValueType}}
	case *ast.FieldAccess:
		// Qualified payload-less variant in value position
		// (`Color.Red`). Same shape exprType expects for the
		// `Red`-as-Ident form — an EnumType naming the owning
		// enum, with no Args.
		if tid, ok := x.Target.(*ast.Ident); ok {
			if _, isEnum := b.info.Enums[tid.Name]; isEnum {
				if _, _, payloadCount, isVar := b.lookupVariantOn(x.Field, tid.Name); isVar && payloadCount == 0 {
					return ast.EnumType{Name: tid.Name}
				}
			}
		}
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
			// Variant constructor call (`Some(42)`, `Ok(x)`,
			// `Red(...)`). Not in FuncSigs, so resolve via the
			// variant table — an enclosing tuple/struct slot needs
			// EnumType (→ IsPointerType, ptrW bytes) rather than
			// the payloadSlotSize(nil) 4-byte default. Without this
			// `(1, Some(42))` packs the variant pointer at offset 4
			// on arm64 but the load reads offset 8 → segfault.
			if ename, _, _, ok := b.lookupVariantOn(id.Name, id.EnumName); ok {
				return ast.EnumType{Name: ename}
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
		// Struct field of tuple type — `r.pos.0` where `pos: (i32,
		// i32)` is a field of struct `r`. The tuple-of-tuple walk
		// above doesn't apply (the target is a struct, not a
		// tuple); resolve the field's declared type via exprType,
		// which handles the struct-field lookup. Without this the
		// FieldAccess lowering falls through to the struct path,
		// fieldOwner returns "", and codegen errors with "field
		// access on unresolved struct \"\"".
		if t, ok := b.exprType(x).(ast.TupleType); ok {
			return t, true
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
	//
	// Skipped for sub-i32 results (width 8 / 16): the fold paths
	// emit + return early, bypassing the wrap-narrowing the main
	// path applies below. e.g. `a * 16u8` strength-reduces to a
	// shift and would escape the `& 0xff` mask. Sub-i32 arithmetic
	// is rare, so forgoing the fold there costs nothing measurable
	// and keeps the wrap semantics correct.
	subI32 := n.IntWidth == 8 || n.IntWidth == 16
	if !n.IsStringConcat && !n.IsStringCmp && !n.IsFloat && !subI32 {
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
		// Skips the runtime `__fern_strcat` allocation + 2x
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
	// Sub-i32 arithmetic wraps to its declared width. `+`, `-`,
	// `*`, `<<` can push the result past 8 / 16 bits (e.g.
	// `255u8 + 1u8` → 256), but scalar locals + struct fields are
	// stored full-width (only array elements narrow via the
	// store8 / store16 op), so without an explicit narrow the
	// out-of-range value leaks — a `u16` var would hold 65536 and
	// a later widening cast / comparison (which assumes "every
	// store narrows", see the cast lowering) reads garbage. Mask
	// (unsigned) or sign-extend (signed) back to width. The other
	// ops (`/ % & | ^ >>`) can't exceed the operands' width.
	if w == 8 || w == 16 {
		switch op {
		case OpAdd, OpSub, OpMul, OpShl:
			if n.IsUnsigned {
				b.emit(Op{Kind: OpConstI32, I32: int32((1 << w) - 1)})
				b.emit(Op{Kind: OpAnd})
			} else if w == 8 {
				b.emit(Op{Kind: OpSignExtend8})
			} else {
				b.emit(Op{Kind: OpSignExtend16})
			}
		}
	}
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
//     `__fern_strcmp` runtime. For the common HTTP-routing
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

// mapValKindTag is mapKeyKindTag's V-side counterpart. Stored
// at buf+12 by map_new so `__map_values_impl` can size its
// snapshot array's element stride correctly on arm64 (4-byte
// for i32-V, 8-byte for pointer-V — surviving arm64-darwin's
// high heap). i64 / f64 V types still use the boxed-cell
// codepath (emitWideMapSet / emitWideMapGet) and never reach
// the value-column snapshot directly.
//
// Encoding (widened for Stage A of map-value reclamation):
//
//	0 = i32-sized scalar (no rc)
//	1 = non-array pointer (string / struct / enum / slice /
//	    tuple) — pointer-shaped but not yet reclaimed by
//	    map_drop
//	2 = array value with non-rc elements (plain arr_dec free)
//	3 = array value with rc-tracked elements (drop_arr_ptr)
//
// Kinds 2 / 3 are reclaimed by __map_drop_values + the
// overwrite-dec in __map_set_impl; readers mask the low byte
// (kind != 0 == pointer-shaped) since map_new stores the packed
// mapValTag (kind | stride<<8), not the bare kind.
func mapValKindTag(t ast.Type, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl) int32 {
	if at, ok := t.(ast.ArrayType); ok {
		if arrElemIsRcTracked(at.Elem) {
			return 3
		}
		return 2
	}
	// Pointer values with a generated deep-drop (concrete user struct or
	// concrete enum) tag as kind 4: deep-dropped via the generated
	// __drop_map_via_<drop> loop at the map's last reference, and retained
	// on set / get / values / iter through the same `kind >= 2` machinery
	// as arrays. The kind-4 set is exactly mapValHasDrop's domain, so
	// retain (here) and drop (routing) never disagree. Other pointers
	// (string / generic-enum / tuple / slice / runtime handles) fall
	// through to the non-reclaimed pointer kind (1).
	if _, ok := mapValHasDrop(t, info, genEnumDrops); ok {
		return 4
	}
	if ast.IsPointerType(t) {
		return 1
	}
	return 0
}

// mapValHasDrop reports the per-VALUE drop function for a map whose value
// type has a generated recursive drop — a concrete user struct
// (__drop_struct_<V>) or a concrete enum (__drop_enum_<V>) — plus whether
// one applies. Shared by mapValKindTag (which tags such values kind 4 so
// they're retained) and the map drop routing (which wraps the per-value
// drop in a __drop_map_via_<drop> column walk), keeping the retained set
// and the reclaimed set identical. Generic-enum instantiations (Args) and
// runtime handle structs are excluded — they stay non-reclaimed (kind 1).
func mapValHasDrop(v ast.Type, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl) (string, bool) {
	// Array-of-CONCRETE-struct value (Map[K, Item[]]): each value array
	// deep-drops its element boxes + buffer via the generated
	// __drop_arr_struct_<Elem> loop, rather than the shallow drop_arr_ptr
	// __map_drop_values uses for kind 3 (which frees the buffer but leaks
	// the element struct boxes). Only reached from routing — mapValKindTag
	// short-circuits arrays to kind 2/3 (whose `>= 2` retain still
	// applies), so this changes the DROP, not the retain. Other arrays
	// (plain / nested / enum-elem) keep __map_drop_values.
	if at, ok := v.(ast.ArrayType); ok {
		return arrElemStructDropName(at.Elem, info)
	}
	// Every other value with a generated recursive drop — concrete user
	// struct (__drop_struct_<V>), concrete enum (__drop_enum_<V>), or a
	// heap-boxed generic-enum instantiation (__drop_enum_<mangled>, recorded
	// in genEnumDrops) — routes through dropFnNameFor, the same dispatch
	// the struct/enum field drops use. Strings / tuples / slices / runtime
	// handles / pair-form generic enums read false and stay non-reclaimed.
	return dropFnNameFor(v, info, genEnumDrops)
}

// mapValTag is what map_new actually stores at buf+12: the low
// byte is the valKind (mapValKindTag) and, for array values
// (kind 2/3), the high bytes carry the value's element stride in
// bytes. Both __map_drop_values and __map_set_impl's overwrite-
// dec read the stride straight from the buf (vk = tag & 255,
// stride = tag >> 8) so the runtime can arr_dec / drop_arr_ptr a
// value without the IR threading the stride through every set /
// drop call. Non-array kinds (0/1) carry no stride.
func mapValTag(t ast.Type, ptrW int, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl) int32 {
	kind := mapValKindTag(t, info, genEnumDrops)
	if kind >= 2 {
		if at, ok := t.(ast.ArrayType); ok {
			return kind | (int32(ast.ElemSizeBytesFor(at.Elem, ptrW)) << 8)
		}
	}
	return kind
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
	// `(b.f)(args...)` / `(t.N)(args...)` where the field access
	// resolves to a FuncType: the field load produces a closure
	// pair pointer; OpCallIndirect dispatches through the pair the
	// same way function-typed locals do. Without this dispatch the
	// IR's call() guard rejected the FieldAccess callee with
	// `indirect call from non-identifier expression`. The field's
	// type comes from either the struct's declaration (via
	// fieldOwner) or from the tuple's static element types (via
	// targetTupleType for `t.N` form).
	if fa, ok := n.Callee.(*ast.FieldAccess); ok {
		var ft *ast.FuncType
		// Tuple field: `t.0`, `t.1`, ... — numeric selector with
		// a TupleType target.
		if tup, isTup := b.targetTupleType(fa.Target); isTup {
			if idx, err := strconv.Atoi(fa.Field); err == nil && idx >= 0 && idx < len(tup.Elems) {
				if fnT, isFn := tup.Elems[idx].(*ast.FuncType); isFn {
					ft = fnT
				}
			}
		}
		// Struct field fallback.
		if ft == nil {
			owner := b.fieldOwner(fa.Target)
			if sd, sdOk := b.info.Structs[owner]; sdOk {
				for _, f := range sd.Fields {
					if f.Name == fa.Field {
						if fnT, isFn := f.Type.(*ast.FuncType); isFn {
							ft = fnT
						}
						break
					}
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
	// `f()()` — the inner Call returns a closure, the outer call
	// dispatches through it. callReturnType resolves the inner
	// call's result type (including for closure-typed locals /
	// captures / pattern bindings via the same path
	// `exprType(*ast.Call)` uses). If that's a *FuncType, push
	// args + the inner call's result + OpCallIndirect.
	if innerCall, ok := n.Callee.(*ast.Call); ok {
		if rt := b.callReturnType(innerCall); rt != nil {
			if ft, isFn := rt.(*ast.FuncType); isFn {
				for _, a := range n.Args {
					if err := b.expr(a); err != nil {
						return err
					}
				}
				if err := b.expr(innerCall); err != nil {
					return err
				}
				b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: ft})
				return nil
			}
		}
	}
	// `arr[i](args)` where arr is `((T) => R)[]` — index expression
	// produces a closure pair pointer. Same indirect-call shape as
	// FieldAccess / CaptureRef / chained-Call callees: push args,
	// evaluate the indexed expression (yields the pair ptr), then
	// OpCallIndirect with the element's static FuncType.
	if idx, ok := n.Callee.(*ast.Index); ok {
		var ft *ast.FuncType
		if idx.ElemType != nil {
			ft, _ = idx.ElemType.(*ast.FuncType)
		}
		if ft == nil {
			// Fallback: peel through the source's static type
			// (arrays-of-T / slices-of-T) and recover the element.
			if at, ok := b.exprStaticType(idx.Array).(ast.ArrayType); ok {
				ft, _ = at.Elem.(*ast.FuncType)
			} else if st, ok := b.exprStaticType(idx.Array).(ast.SliceType); ok {
				ft, _ = st.Elem.(*ast.FuncType)
			}
		}
		if ft != nil {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			if err := b.expr(idx); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: ft})
			return nil
		}
	}
	// Immediate lambda call: `(function (x) { ... })(arg)`. The
	// Lambda lowers to a closure pair pointer (via closureconv's
	// MakeClosure rewrite); OpCallIndirect dispatches through it
	// like any other function-typed value. Same shape as the
	// chained-Call / CaptureRef / FieldAccess callee branches
	// — Lambda just happens to be a literal closure value
	// inlined right at the call site. closureconv has likely
	// already rewritten Lambda → MakeClosure before the IR
	// builder runs, so we handle both shapes.
	if lam, ok := n.Callee.(*ast.Lambda); ok {
		ft := &ast.FuncType{Result: lam.ReturnType}
		for _, p := range lam.Params {
			ft.Params = append(ft.Params, p.Type)
		}
		for _, a := range n.Args {
			if err := b.expr(a); err != nil {
				return err
			}
		}
		if err := b.expr(lam); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Sig: ft})
		return nil
	}
	if mc, ok := n.Callee.(*ast.MakeClosure); ok {
		// closureconv-emitted MakeClosure callee — same path as
		// the Lambda case above, but the function signature comes
		// from info.FuncSigs[mc.FuncName] (closureconv stamped
		// this for the hoisted target).
		var ft *ast.FuncType
		if sig, ok := b.info.FuncSigs[mc.FuncName]; ok && sig != nil {
			// The hoisted sig includes the trailing __env param;
			// drop it for the call-site signature, since the
			// OpCallIndirect emit uses the pair's env_ptr to
			// supply env automatically.
			userSig := &ast.FuncType{Result: sig.Result}
			if len(sig.Params) > 0 {
				userSig.Params = append([]ast.Type(nil), sig.Params[:len(sig.Params)-1]...)
			}
			ft = userSig
		}
		if ft != nil {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			if err := b.expr(mc); err != nil {
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
	// `arr.set(i, v)` — Phase 2b's value-returning sister. The
	// IR-level CoW desugar for `arr[i] = v` covers most
	// targets, but `arr.set` is the explicit API users can
	// call from anywhere (params, expression position, method
	// chains) and get value semantics. Internally calls
	// `__fern_arr_cow_inplace`, writes the element, returns
	// the (possibly new) buffer pointer.
	if id.Name == "__method_Array_set" && len(n.Args) == 3 && len(n.TypeArgs) == 1 {
		return b.emitArraySet(n)
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
	// __rc_inc / __rc_dec — refcount helpers exposed as Lang
	// builtins. Lower to OpCallDirect with the runtime-side
	// name so backends pick up the matching gate flag. Both
	// accept a u8[] today; Phase 1e will widen to strings /
	// structs / enums / closures. See docs/RC-PERCEUS-PLAN.md.
	if (id.Name == "__rc_inc" || id.Name == "__rc_dec") && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			target := "__fern_rc_inc"
			if id.Name == "__rc_dec" {
				target = "__fern_rc_dec"
			}
			b.emit(Op{Kind: OpCallDirect, Str: target, I32: 1})
			return nil
		}
	}
	// __rc_get(arr): i32 — read the rc word at [arr - 8].
	// Lowered inline rather than as a runtime call so the
	// load can const-fold / inline in the backends.
	if id.Name == "__rc_get" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpConstI32, I32: 8})
			b.emit(Op{Kind: OpSub})
			b.emit(Op{Kind: OpLoad})
			return nil
		}
	}
	// __rc_underflow_count(): i32 — Phase 3 detector probe. Reads
	// the rc-underflow counter that __fern_rc_dec bumps whenever it
	// over-releases a value (decrements an rc already <= 0). Lowered
	// to a runtime-helper call so each backend reads its own counter
	// store: wasm a fixed linear-memory slot, the natives a BSS
	// global. Lets the detector run on all three backends.
	if id.Name == "__rc_underflow_count" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_underflow_count", I32: 0})
			return nil
		}
	}
	// f64_bits / f64_from_bits: 64-bit cousin of the f32 pair.
	// Same zero-cost reinterpret on natives; wasm needs the
	// typed `i64.reinterpret_f64` / `f64.reinterpret_i64` op.
	if (id.Name == "f64_bits" || id.Name == "f64_from_bits") && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			if id.Name == "f64_bits" {
				b.emit(Op{Kind: OpReinterpretI64F64})
			} else {
				b.emit(Op{Kind: OpReinterpretF64I64})
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
	// `x.len()` on a string, array, or slice is inlined here off
	// the mangled method names the checker rewrites the dispatch
	// to. String and array layouts carry a 4-byte little-endian
	// length prefix at `ptr - 4`; slice values carry the length
	// at `slice + 4` after the data pointer. Strings route
	// through OpStrLen so a future small-string-optimisation
	// pass can change the encoding in one place instead of
	// patching every backend's open-coded `[ptr - 4]` load.
	// Arrays keep the inline sub-4 / load shape because their
	// layout may diverge from strings later.
	switch id.Name {
	case "__method_string_len", "__method_Array_len", "__method_slice_len":
		if len(n.Args) != 1 {
			break
		}
		// Compile-time fold: when the receiver is a literal whose
		// length is statically known, collapse the whole runtime-
		// load sequence to a single const. Saves the runtime alloc
		// + prefix-load that the unfolded shape would force, and
		// lets the const propagate into surrounding arithmetic.
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
		switch id.Name {
		case "__method_slice_len":
			b.emit(Op{Kind: OpConstI32, I32: 4})
			b.emit(Op{Kind: OpAdd})
			b.emit(Op{Kind: OpLoad})
		case "__method_string_len":
			b.emit(Op{Kind: OpStrLen})
		default: // __method_Array_len
			b.emit(Op{Kind: OpConstI32, I32: 4})
			b.emit(Op{Kind: OpSub})
			b.emit(Op{Kind: OpLoad})
		}
		return nil
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
	// Phase 2c: delete and clear are always value-returning now;
	// route them through dedicated emit helpers regardless of
	// boxing needs.
	switch id.Name {
	case "__method_Map_delete":
		kType := ast.Type(ast.NumberType{}) // i32 fallback
		if len(n.TypeArgs) >= 1 {
			kType = n.TypeArgs[0]
		}
		return b.emitMapDeleteReturningTuple(n, kType)
	case "__method_Map_clear":
		return b.emitMapClearReturningMap(n)
	}
	//
	// Methods that return `Option[V]` or `V[]` need extra work to
	// translate the helper's i32-cell result into a real V; the
	// boxing-aware emitWideMap* helpers below do this. Methods
	// whose return type passes through unchanged (`set` (Map[K,V]),
	// `has` boolean, `get` when V is i32-scalar,
	// `get_or` when V is i32-scalar) flow through
	// emitStringKMapCall when only K needs boxing.
	needBoxK := len(n.TypeArgs) >= 1 && (isStringForBoxing(n.TypeArgs[0], b.ptrW) || mapKeyKindTag(n.TypeArgs[0], b.ptrW) == 2)
	needBoxV := len(n.TypeArgs) >= 2 && (isWideScalar(n.TypeArgs[1]) || isStringForBoxing(n.TypeArgs[1], b.ptrW))
	// Map-value reclamation (write side): retain an aliased
	// array-typed value (valKind 2/3) before it's stored, so the
	// map co-owns it and map_drop's free balances the source
	// local's exit-sweep dec. Fresh values (literals / call
	// results) aren't aliases (needsRcIncOnAlias == false) and
	// transfer their rc=1 to the map with no inc — preventing an
	// over-count leak. Idempotent alias exprs (Ident / field /
	// index) are safe to re-evaluate for the inc; the set below
	// re-reads the same pointer. Runs before the wide/generic
	// dispatch so both set lowerings are covered uniformly.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 &&
		len(n.TypeArgs) >= 2 && mapValKindTag(n.TypeArgs[1], b.info, b.genEnumDrops) >= 2 &&
		needsRcIncOnAlias(n.Args[2], b) {
		if err := b.expr(n.Args[2]); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	// Map[K, string] (wasm) set retain: a string value's heap buffer is
	// co-owned by the map's boxed (data, len) cell, so retain an aliased
	// string before it's stored (__fern_str_inc), balancing the
	// __fern_str_dec at map drop / overwrite. Fresh strings (concat /
	// literal / call) aren't aliases (needsRcIncOnAlias == false) → moved
	// in with no inc. Strings stay valKind 1 at runtime (unchanged) — the
	// retain is driven by the static type, not the stored tag.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		b.ptrW == 4 && needsRcIncOnAlias(n.Args[2], b) {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			if err := b.expr(n.Args[2]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
			b.emit(Op{Kind: OpDrop, Width: WidthString})
		}
	}
	// Map[K, string] (native single-word) set retain: x86_64 stores
	// the string data pointer directly in the kv slot — __fern_rc_inc
	// bumps the buffer's L2 rc header at data-8. Literals short-circuit
	// on the 0x80000000 sentinel (prereq 2). Alias-shape check inlined
	// since needsRcIncOnAlias returns false for strings on ptrW=8.
	// Gated to non-two-word natives — arm64 stores strings boxed (the
	// IR runs with TwoWordOverride=true) and rc_inc on a cell pointer
	// would bump the cell's rc, not the string's. Excluded until the
	// arm64 boxed-string-reclaim path lands its own set-retain.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			switch n.Args[2].(type) {
			case *ast.Ident, *ast.FieldAccess, *ast.Index:
				if err := b.expr(n.Args[2]); err != nil {
					return err
				}
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		}
	}
	// Map[string, V] (wasm) set KEY retain: the key column co-owns an
	// aliased string key's buffer (boxed (data, len) cell), so __fern_str_inc
	// it, balancing the __fern_str_dec in the __drop_map_str_keys walk at map
	// drop. Fresh keys (concat / literal / call) are moved in with no inc. An
	// OVERWRITE discards the freshly-boxed key (the runtime keeps the
	// existing one), so an aliased overwrite key leaks its inc — safe (no
	// double free), bounded, and keys already leaked entirely pre-slice.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 1 &&
		b.ptrW == 4 && needsRcIncOnAlias(n.Args[1], b) {
		if _, isStr := n.TypeArgs[0].(ast.StringType); isStr {
			if err := b.expr(n.Args[1]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
			b.emit(Op{Kind: OpDrop, Width: WidthString})
		}
	}
	// Map[string, V] (native single-word) set KEY retain: x86_64 stores
	// the string data pointer directly in the key column slot —
	// __fern_rc_inc bumps the buffer's L2 rc header at data-8. Literals
	// short-circuit on the 0x80000000 sentinel (prereq 2). Alias-shape
	// check inlined since needsRcIncOnAlias returns false for strings on
	// ptrW=8. Gated to non-two-word natives — arm64 stores keys boxed
	// (the IR runs with TwoWordOverride=true) so rc_inc on the cell
	// pointer would bump the cell's rc, not the key string's. Excluded
	// until the arm64 boxed-string-reclaim path lands.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 1 &&
		b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
		if _, isStr := n.TypeArgs[0].(ast.StringType); isStr {
			switch n.Args[1].(type) {
			case *ast.Ident, *ast.FieldAccess, *ast.Index:
				if err := b.expr(n.Args[1]); err != nil {
					return err
				}
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		}
	}
	// Overwrite reclamation: `m.set(k, v)` REPLACES any existing value at
	// k. For a single-box pointer value (struct / enum / generic-enum,
	// kind 4) the type-erased runtime overwrite-dec (__map_dec_value) is a
	// no-op, so the replaced value would leak. Deep-drop the old value
	// here, just before the set: look it up (non-retaining) and, if
	// present, run its per-value drop. Scoped to non-boxed keys (the
	// common i32-key cache); m and k must be call-free since the set below
	// re-evaluates them (same idempotence requirement as the inc-on-set
	// above). The set's own overwrite-dec stays a no-op for kind 4, so
	// there's no double free; the freed box isn't dereferenced by the set
	// (it only probes keys, then overwrites the slot).
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		ast.RcFreeEnabled && !needBoxK &&
		mapValKindTag(n.TypeArgs[1], b.info, b.genEnumDrops) == 4 &&
		!exprContainsCall(n.Args[0]) && !exprContainsCall(n.Args[1]) {
		if perVal, ok := mapValHasDrop(n.TypeArgs[1], b.info, b.genEnumDrops); ok {
			if err := b.expr(n.Args[0]); err != nil { // m
				return err
			}
			if err := b.expr(n.Args[1]); err != nil { // k (non-boxed)
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__map_lookup_val", I32: 2})
			oldSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__map_overwrite_old_%d", oldSlot)] = oldSlot
			b.emit(Op{Kind: OpStoreLocal, I32: oldSlot})
			// if old != 0: deep-drop the replaced value.
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpCallDirect, Str: perVal, I32: 1})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpEnd})
		}
	}
	// Map[K, string] (wasm) overwrite pre-drop: m.set(k, v) replacing an
	// existing string value must reclaim the old buffer (the runtime's
	// type-erased overwrite-dec is a no-op for valKind 1). Look up the old
	// value cell (non-retaining) and, if present, __fern_str_dec the
	// (data, len) it holds. The old cell itself leaks (as on map drop).
	// Scoped to the non-boxed-key fast path; m / k must be call-free (the
	// set below re-evaluates them — same idempotence as the kind-4 path).
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		ast.RcFreeEnabled && b.ptrW == 4 && !needBoxK &&
		!exprContainsCall(n.Args[0]) && !exprContainsCall(n.Args[1]) {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			if err := b.expr(n.Args[0]); err != nil { // m
				return err
			}
			if err := b.expr(n.Args[1]); err != nil { // k (non-boxed)
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__map_lookup_val", I32: 2})
			oldSlot := b.allocSlot()
			b.locals[fmt.Sprintf("__map_overwrite_oldstr_%d", oldSlot)] = oldSlot
			b.emit(Op{Kind: OpStoreLocal, I32: oldSlot})
			// if oldCell != 0: __fern_str_dec the (data, len) in the cell.
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpLoad, Width: WidthString})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpEnd})
		}
	}
	// `m.get(k)` ALWAYS reboxes the helper's uniform `Option[usize]`
	// into a consumer-shaped `Option[V]`. The helper's payload sits
	// at the usize slot offset (8 on natives), which only lines up
	// with a direct `Option[V]` read when V's layout equals usize's
	// (pointer-shaped V on natives). For i32 V — the common case —
	// the consumer reads offset 4, so a passthrough would read the
	// wrong bytes. Reboxing makes every V correct on every target.
	if id.Name == "__method_Map_get" && len(n.TypeArgs) >= 2 {
		return b.emitMapGetRebox(n, n.TypeArgs[0], n.TypeArgs[1], needBoxV)
	}
	// Map[K, string].get_or on native single-word: the runtime returns the
	// string data pointer directly (no boxing — non-boxed V skips the wide
	// path) and __map_retain_val is a no-op for valKind 1, so the caller
	// would hold an un-retained alias the map's drop could later free
	// (UAF). Lower the call inline and __fern_rc_inc the returned pointer
	// so the caller co-owns the buffer. Inline strings short-circuit on
	// the low-bit guard; literals on the 0x80000000 sentinel.
	if id.Name == "__method_Map_get_or" && len(n.TypeArgs) >= 2 &&
		b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) && !needBoxK && !needBoxV {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get_or", I32: int32(len(n.Args))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
			return nil
		}
	}
	if needBoxK || needBoxV {
		switch id.Name {
		case "__method_Map_set":
			return b.emitWideMapSet(n, n.TypeArgs[0], n.TypeArgs[1])
		case "__method_Map_get_or":
			if needBoxV {
				return b.emitWideMapGetOr(n, n.TypeArgs[0], n.TypeArgs[1])
			}
			return b.emitStringKMapCall(n, n.TypeArgs[0], id.Name, 3)
		case "__method_Map_has":
			if needBoxK {
				return b.emitStringKMapCall(n, n.TypeArgs[0], id.Name, 2)
			}
			// Wide-V doesn't affect has — fall through.
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
		// Phase 2d-borrow: function parameters are borrowed, not
		// owned, so passing a tracked argument is NOT an
		// ownership-creating alias — no __fern_rc_inc here. The
		// callee reads/mutates through the borrow and does not dec
		// the parameter at exit (see emitRcDecLocalsAtExit). This
		// keeps a Map passed to a function at rc==1 so the callee
		// mutates it in place (visible to the caller), while
		// genuine ownership transfers (Var init, struct/array/
		// closure capture, assignment) still inc at their sites.
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
			valKind = mapValTag(n.TypeArgs[1], b.ptrW, b.info, b.genEnumDrops)
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
	// IR-introduced locals (match-arm bindings, if-let bindings,
	// let-else bindings) live in b.locals + b.scratchType — not in
	// info.Locals which only tracks checker-visible Var nodes.
	// Without this, calling a closure value pattern-bound by a
	// match arm (`match (o) { Some(f) => f(...) }`) errored with
	// `indirect call through unknown local "f"`.
	if slot, ok := b.locals[name]; ok {
		if t, ok := b.scratchType[slot]; ok {
			ft, isFn := t.(*ast.FuncType)
			if !isFn {
				return nil, fmt.Errorf("ir: indirect call through non-function-typed scratch %q", name)
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
		// Phase 1d: same alias-bump as the Var-binding path —
		// `y = x;` shares an existing array reference, so the
		// new binding needs its own rc. Move-on-alias skips the inc
		// at a move site (see the Var path).
		if needsRcIncOnAlias(n.Value, b) && !b.moveSites[n] {
			b.emitAliasInc(n.Value)
		}
		// Phase 1d-vi: dec the old value of `y` before
		// overwriting it. `y` previously held some array
		// reference (whose rc was bumped by the var-binding
		// site that filled it); the reassignment ends that
		// binding's ownership, so the dec balances the prior
		// inc. Without this, every `y = x;` orphans the
		// previous allocation — Phase 1's bump allocator
		// absorbs the leak, but Phase 2's mutate-or-copy
		// rc check needs accurate counts.
		//
		// Gating on `*ast.ArrayType` matches the inc side
		// (`needsRcIncOnAlias`). Phase 1e widens to strings
		// / structs / enums / closures together with their
		// matching inc sites.
		//
		// Phase 3 step 2: a self-mutating map reassignment
		// `m = m.set(...)` / `m = m.clear()` needs a COW-AWARE
		// dec. The map mutators copy-on-write through
		// __map_cow_inplace, which (unlike array push) does NOT
		// bump rc on the in-place path:
		//   - rc==1 (in-place): the call returns the SAME handle
		//     the slot holds, no reference was released, so an
		//     unconditional dec would drop a live rc to 0 (and a
		//     second self-assign to -1 — the over-release the
		//     detector flagged). Must NOT dec.
		//   - rc>1 (aliased copy): the call returns a fresh
		//     handle; the slot's old handle loses this binding's
		//     claim and MUST be dec'd, or its rc leaks.
		// Both cases are covered by dec'ing iff the new value
		// differs from the old (i.e. cow copied). Other rc-tracked
		// reassignments keep the unconditional dec.
		if isArrayTypeOfLocal(t.Name, b) {
			if isSelfMapMutation(n.Value, t.Name) {
				newTmp := b.allocSlot()
				b.locals[fmt.Sprintf("__selfmap_new_%d", newTmp)] = newTmp
				b.emit(Op{Kind: OpStoreLocal, I32: newTmp}) // stash new (RHS result)
				b.emit(Op{Kind: OpLoadLocal, I32: idx})     // old handle
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp})  // new handle
				b.emit(Op{Kind: OpNe})                      // cow copied?
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpEnd})
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp}) // restore new for the store
			} else if at, isArr := localArrayType(t.Name, b); isArr && ast.RcFreeEnabled && b.freeEligible[t.Name] {
				// Phase 3 step-4: free the OLD array buffer at rc==0.
				// On a push copy-grow the old buffer's pointer elements
				// were transferred to the new buffer (no inc), so freeing
				// the buffer — not walking elements — is correct; the
				// in-place push (rc bumped to 2) dec's to 1 and doesn't
				// free. This is the O(N²)→O(N) push-loop reclamation.
				//
				// Gated on freeEligible: only OWNED array locals free
				// here. Borrowed / borrowed-derived locals (params, and
				// anything aliased from them - e.g. the self-host VM's
				// `ops` threaded through compile_stmt/compile_block) keep
				// the plain dec: the owner upstream still references the
				// buffer (the borrow model gives no caller-side inc, so
				// the rc undercounts the borrow - freeing would be a
				// use-after-free). See computeFreeEligible.
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
				b.emit(Op{Kind: OpDrop})
			} else {
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
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
		// Phase 2b: `arr[i] = v` for a writable local-ident
		// array routes through `__fern_arr_cow_inplace(arr,
		// stride) → buf`. The helper returns the same arr on
		// rc==1 (mutate in place) and a memcpy'd copy on rc>1
		// (decrementing arr's rc as it takes over the
		// caller-side reference). Caller writes the element
		// into the returned buffer and stores it back into the
		// slot. Slices (which write through to the parent
		// storage) and complex Index targets (`obj.field[i] =
		// v`, `m[k][i] = v`, etc.) keep the legacy in-place
		// emit for now; follow-up PRs widen CoW to cover them.
		// See docs/RC-PERCEUS-PLAN.md "Phase 2".
		if !t.IsSlice {
			if arrIdent, ok := t.Array.(*ast.Ident); ok {
				if slot, isLocal := b.locals[arrIdent.Name]; isLocal && isArrayTypeOfLocal(arrIdent.Name, b) && !isParamName(arrIdent.Name, b) {
					return b.emitArrayIndexAssignCoW(arrIdent, slot, t, n, stride, storeOp, storeWidth, helper)
				}
			}
			if fa, ok := t.Array.(*ast.FieldAccess); ok {
				if rootName, found := rootIdentOfFieldChain(fa); found {
					if _, isLocal := b.locals[rootName]; isLocal && !isParamName(rootName, b) {
						st := b.fieldOwner(fa.Target)
						if sd, sdOk := b.info.Structs[st]; sdOk {
							offs, _ := structFieldLayout(sd.Fields, b.ptrW)
							off := int32(-1)
							var ft ast.Type
							for _, f := range sd.Fields {
								if f.Name == fa.Field {
									off = offs[f.Name]
									ft = f.Type
									break
								}
							}
							if _, isArr := ft.(ast.ArrayType); isArr && off >= 0 {
								return b.emitArrayIndexAssignCoWField(fa, off, ft, t, n, stride, storeOp, storeWidth, helper)
							}
						}
					}
				}
			}
			// Phase 2b extension: `mat[i][j] = v` where mat is a
			// writable local-ident array-of-arrays. The outer
			// store flips the inner-array pointer at mat[i] to a
			// fresh buffer (via CoW); the outer `mat` slot itself
			// is mutated in place, so callers that alias mat
			// still see the new mat[i] pointer (acceptable —
			// Phase 2c is what gates copy-on-write at the mat
			// level too).
			if outer, ok := t.Array.(*ast.Index); ok && !outer.IsSlice {
				if outerRoot, found := outerRootIdent(outer.Array); found {
					if _, isLocal := b.locals[outerRoot]; isLocal && !isParamName(outerRoot, b) {
						// outer.ElemType is the inner-array type
						// (e.g. i32[] for mat: i32[][]). Use it
						// to pick the right pointer-shaped
						// load/store width.
						if outerElemT := outer.ElemType; outerElemT != nil {
							if _, isInnerArr := outerElemT.(ast.ArrayType); isInnerArr {
								return b.emitArrayIndexAssignCoWNested(outer, t, n, stride, storeOp, storeWidth, helper)
							}
						}
					}
				}
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

// needsRcIncOnAlias returns true iff `e` is an alias expression
// (a load of an existing reference) whose result is an array
// type. Used by Phase 1d to decide when to splice in
// __fern_rc_inc: array literals, function-call returns, and
// push results all yield rc=1 ownership; only loads of existing
// references share an existing reference and need the bump.
// Other pointer-shaped types (string / struct / enum / closure)
// will join this in Phase 1e.
//
// Alias shapes:
//   - *ast.Ident       — variable load
//   - *ast.FieldAccess — struct field load (member is an array)
//   - *ast.Index       — element load (e.g. matrix[i] where matrix
//     is i32[][], so the element is i32[])
//
// Calls, literals, push results, slice / map operations etc.
// all yield fresh values with rc=1 already initialised by their
// allocator path, so they're explicitly excluded.
// isArrayTypeOfLocal reports whether the local named `name`
// in the current function is rc-tracked. Used by the Phase
// 1d-vi / 1e-struct-iv dec-on-overwrite emission in
// `b.assign`. Looks in the function's parameter list first
// (params share the slot map with declared locals), then the
// declared locals. Returns false on misses so callers stay
// conservative — a missing slot means no inc was emitted to
// balance, and a missing dec is preferable to a spurious one
// on a non-pointer slot.
//
// Phase 1e-struct-iv: rc-tracked set now includes user
// structs in addition to arrays. The matching inc widening
// (Phase 1e-struct-ii) ensures every aliasing event that
// bumped the rc gets a balancing dec when `y` is overwritten.
// isSelfMapMutation reports whether `value` is a value-returning
// map mutator called on the ident `targetName` — i.e. the RHS of
// a `m = m.set(...)` / `m = m.clear()` reassignment. The checker
// has already rewritten the source `m.set(k, v)` into a Call whose
// Callee is the `__method_Map_set` ident and whose Args[0] is the
// receiver. Used by `b.assign` to switch the dec-on-overwrite to a
// COW-AWARE form: the map mutators cow in place without bumping rc,
// so on a uniquely-held map the call returns the same handle the
// slot already holds (no reference released → no dec), while on an
// aliased map it returns a fresh copy (old handle released → dec).
// b.assign distinguishes the two at runtime by comparing the old
// and new handles. (delete is excluded — it returns a tuple and is
// bound via destructuring, not a bare `m = ...` reassignment.)
func isSelfMapMutation(value ast.Expr, targetName string) bool {
	call, ok := value.(*ast.Call)
	if !ok {
		return false
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	if callee.Name != "__method_Map_set" && callee.Name != "__method_Map_clear" {
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	recv, ok := call.Args[0].(*ast.Ident)
	return ok && recv.Name == targetName
}

func isArrayTypeOfLocal(name string, b *builder) bool {
	isRcTracked := func(t ast.Type) bool {
		if _, isArr := t.(ast.ArrayType); isArr {
			return true
		}
		if _, isStruct := t.(ast.StructType); isStruct {
			return true
		}
		if _, isEnum := t.(ast.EnumType); isEnum {
			return true
		}
		if _, isFunc := t.(*ast.FuncType); isFunc {
			return true
		}
		return false
	}
	for _, p := range b.fn.Params {
		if p.Name == name {
			return isRcTracked(p.Type)
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			return isRcTracked(v.Type)
		}
	}
	return false
}

// localArrayType returns the ArrayType of a param / local named
// `name` if it is array-typed. Used by the Phase 3 step-4
// dec-on-overwrite to route array targets through __fern_arr_dec
// (which frees the old buffer at rc==0) with the right element
// stride, while struct / enum / closure targets keep the plain dec.
func localArrayType(name string, b *builder) (ast.ArrayType, bool) {
	for _, p := range b.fn.Params {
		if p.Name == name {
			at, ok := p.Type.(ast.ArrayType)
			return at, ok
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			at, ok := v.Type.(ast.ArrayType)
			return at, ok
		}
	}
	return ast.ArrayType{}, false
}

// outerRootIdent resolves the root local-ident of an outer
// expression for the nested `mat[i][j] = v` CoW path. Handles
// `mat` (bare ident) and `obj.mat`, `a.b.mat`, ... (field
// chains). Anything else — call results, slices, deeper
// indexing — bottoms out unresolved and the caller falls
// through to the legacy in-place emit.
func outerRootIdent(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, true
	case *ast.FieldAccess:
		return rootIdentOfFieldChain(x)
	}
	return "", false
}

// rootIdentOfFieldChain walks a chain of nested FieldAccess
// back to the root Ident (if any). `a.b.c.d` resolves to "a";
// returns ("", false) when the chain bottoms out on a non-
// ident shape (a call result, an index, etc.) — those cases
// don't have a single writable "owner" slot the Phase 2b CoW
// path can update, so they fall through to the legacy in-
// place emit.
func rootIdentOfFieldChain(fa *ast.FieldAccess) (string, bool) {
	cur := fa.Target
	for {
		switch t := cur.(type) {
		case *ast.Ident:
			return t.Name, true
		case *ast.FieldAccess:
			cur = t.Target
		default:
			return "", false
		}
	}
}

// isParamName reports whether `name` resolves to a function
// parameter (as opposed to a declared local). Phase 2b's
// `arr[i] = v` copy-on-write desugar skips params for now —
// existing callers rely on the "mutate the caller's array
// through the parameter" idiom (e.g. `function update(arr,
// idx) { arr[idx] = ...; }`), which the CoW path would break
// once the param's rc bumps to ≥ 2 from the call-arg inc.
// Phase 2c will widen the desugar after migrating the in-tree
// callers off the shared-mutation pattern.
func isParamName(name string, b *builder) bool {
	for _, p := range b.fn.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// exprContainsCall reports whether e contains a function/method Call
// anywhere in its tree. Used to gate re-evaluation: an expression with no
// Call is side-effect-free, so evaluating it twice (e.g. once for the
// map-overwrite pre-drop lookup and again for the set itself) is safe.
func exprContainsCall(e ast.Expr) bool {
	found := false
	ast.Walk(e, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, ok := n.(*ast.Call); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// emitAliasInc emits the retain (inc) for an alias of expr `e` whose
// value is already on the operand stack. For a wasm two-word string it
// uses __fern_str_inc (consumes + returns the (data, len) pair, so the
// value survives for the following store); everything else uses the
// single-word __fern_rc_inc. The callers all pre-gate on
// needsRcIncOnAlias(e), so this only fires for rc-tracked aliases.
//
// String inc is now UNCONDITIONAL (matching arrays / structs / etc.):
// every string is one of inline (the flag makes __fern_str_inc/dec a
// no-op), static literal (the 0x80000000 data-segment sentinel header
// short-circuits inc/dec), or headered heap (real rc) — there is no
// view form anymore (args()/env() copy into owned strings; see the
// args/env view-fix PR). So a borrowed read of a string out of a
// container (`var s = foo.field` / `arr[i]`) co-owns the buffer, which
// is required once a container drop dec's its string fields/elements:
// without the inc, dropping the container would free the buffer out
// from under the still-live alias (UAF). The earlier eligibility gate
// (inc only fresh-owned bare idents) existed solely to avoid touching
// view strings and is no longer needed.
func (b *builder) emitAliasInc(e ast.Expr) {
	if _, isStr := b.exprType(e).(ast.StringType); isStr && b.ptrW == 4 {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
		return
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
}

func needsRcIncOnAlias(e ast.Expr, b *builder) bool {
	switch e.(type) {
	case *ast.Ident, *ast.FieldAccess, *ast.Index:
		// continue
	default:
		return false
	}
	t := b.exprType(e)
	if _, isArr := t.(ast.ArrayType); isArr {
		return true
	}
	// Phase 1e-struct-ii: user-declared struct values now carry
	// rc headers (either real rc=1 from StructLit lowering or
	// the static-sentinel 0x80000000 from runtime helpers like
	// __fern_make_handle / map_new_impl / __map_iter_impl).
	// Either way, calling __fern_rc_inc/dec on a struct pointer
	// is safe — the inc/dec helpers short-circuit on the high
	// bit so runtime-owned values stay shareable, and user-
	// allocated values pick up real rc tracking.
	if _, isStruct := t.(ast.StructType); isStruct {
		return true
	}
	// Phase 1e-enums-ii: aliasing an enum-typed ident / field /
	// index inc's the box. The value is always a heap pointer
	// (headered box) or a header-carrying static sentinel, so
	// __fern_rc_inc short-circuits on the sentinel and bumps a
	// real rc on a user box.
	if _, isEnum := t.(ast.EnumType); isEnum {
		return true
	}
	// Phase 1e-closures-ii: aliasing a FuncType (closure) value
	// inc's its rc=1 heap header; static cells short-circuit
	// (sentinel on natives, low-address guard on wasm).
	if _, isFunc := t.(*ast.FuncType); isFunc {
		return true
	}
	// Tuple reclamation: aliasing a tuple-typed ident / field / index
	// inc's its box (rc=1 header from TupleLit lowering). Balances the
	// box dec the exit sweep emits for tuple locals, and — critically —
	// keeps the box alive when a tuple flows into a container that
	// outlives the source local (no inc would let the source's box_free
	// strand the container's reference).
	if _, isTuple := t.(ast.TupleType); isTuple {
		return true
	}
	// wasm two-word strings: aliasing inc's the heap buffer's rc via
	// __fern_str_inc (emitAliasInc picks the two-word helper). Lets two
	// eligible string locals share a buffer safely (the dec's is_unique
	// gate frees once) and protects a string flowing into a container
	// that outlives the source. Gated ptrW==4 — __fern_str_inc is
	// wasm-only; on natives strings stay un-inc'd (and unreclaimed).
	if _, isStr := t.(ast.StringType); isStr {
		return b.ptrW == 4
	}
	return false
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
	// usize (WidthPtr) is pointer-width — 8 bytes on natives,
	// 4 on wasm32. Without this branch the `ast.IsPointerType`
	// check below misses it (usize is a NumberType, not in the
	// pointer-type list) and it falls through to the 4-byte
	// default — truncating the 8-byte payload of `Option[usize]`
	// (built by `__map_get_impl`) on natives. Mirrors
	// `pairPayloadWidth`, which already treats usize as WidthPtr.
	if n, ok := t.(ast.NumberType); ok && n.IsPointerWidth() {
		return int32(ptrW)
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
	// usize → pointer-width store (matches payloadSlotSize).
	if n, ok := t.(ast.NumberType); ok && n.IsPointerWidth() {
		return Op{Kind: OpStore, Width: WidthPtr}
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
	// usize → pointer-width load (matches payloadSlotSize).
	if n, ok := t.(ast.NumberType); ok && n.IsPointerWidth() {
		return Op{Kind: OpLoad, Width: WidthPtr}
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

	// arr = __alloc(16 + len*8): standard rc-array layout — a 16-byte
	// header carrying capacity (data-12), rc=1 (data-8) and length
	// (data-4); data = hdr + 16. Without the cap/rc slots the snapshot's
	// scope-exit drop reads heap metadata at data-8 as the rc and
	// underflows.
	b.emit(Op{Kind: OpConstI32, I32: 16})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpAlloc})
	hdrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_hdr_%d", hdrSlot)] = hdrSlot
	b.emit(Op{Kind: OpStoreLocal, I32: hdrSlot})

	// capacity at hdr+4 (= data-12)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})
	// rc = 1 at hdr+8 (= data-8)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// length at hdr+12 (= data-4)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 12})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})

	dataSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_data_%d", dataSlot)] = dataSlot
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 16})
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

	// Standard rc-array layout: a 16-byte header carrying capacity
	// (data-12), rc=1 (data-8) and length (data-4); data = hdr + 16,
	// 8-byte stride-aligned, matching what ArrayLit emits for `i64[]`.
	// Without the cap/rc slots the snapshot's scope-exit drop reads heap
	// metadata at data-8 as the rc and underflows.
	hdrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_hdr_%d", hdrSlot)] = hdrSlot
	b.emit(Op{Kind: OpConstI32, I32: 16})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: hdrSlot})

	// capacity at hdr+4 (= data-12)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})
	// rc = 1 at hdr+8 (= data-8)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 8})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// length at hdr+12 (= data-4)
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 12})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: lenSlot})
	b.emit(Op{Kind: OpStore})

	dataSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_data_%d", dataSlot)] = dataSlot
	b.emit(Op{Kind: OpLoadLocal, I32: hdrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 16})
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

// emitArrayPush lowers `arr.push(v)` inline. Phase 2: hand off
// the rc check + mutate-or-copy decision to a runtime helper so
// the per-call-site emit stays straight-line (avoids the SSA
// Lift dominance trap that bit the OpIf-in-IR attempt). The IR
// only does what's stride-aware: the width-correct tail store
// for the new element.
//
//	buf = __fern_arr_push_grow(arr, oldLen, stride)  ;; helper
//	*(buf + oldLen * stride) = v                     ;; tail store
//	return buf                                       ;; array value
//
// The helper itself (one per backend, in arm64.go / x86_64.go /
// wasmbin/runtime.go) checks `[arr-8] == 1 && [arr-12] > oldLen`
// to decide whether it can mutate `arr` in place. On the fast
// path it bumps rc to 2 and writes len, returning the same
// pointer; the surrounding Phase 1d-vi dec-on-overwrite then
// drops rc back to 1, leaving the caller's slot owning rc=1 of
// a freshly extended array. On the slow path the helper allocs
// a new buffer, copies the old payload, sets new rc=1, len, cap,
// and returns the new pointer.
//
// The element type is `n.TypeArgs[0]`, stamped by the checker's
// array.push dispatch — used by `arrayElemStoreOpFor` to pick
// the right store width.
// emitArrayIndexAssignCoW lowers `arr[i] = v` for a writable
// local-ident array. The helper `__fern_arr_cow_inplace`
// internalises all rc bookkeeping for this site:
//
//   - rc == 1 → return arr unchanged; caller writes into the
//     existing buffer.
//   - rc >  1 → allocate a fresh buffer with the same cap+len,
//     memcpy the payload, decrement arr's rc (skipping when
//     arr is a static sentinel), return the new ptr.
//
// The IR emit therefore does NOT need a separate dec-on-
// overwrite step — keeping that step would either double-dec,
// or skip-dec on raw wasm where heap addresses sit below
// 0x10000 (the `__fern_rc_dec` low-address guard short-
// circuits there). The helper is the sole rc-management point
// for this site.
func (b *builder) emitArrayIndexAssignCoW(arrIdent *ast.Ident, arrSlot int32, t *ast.Index, n *ast.Assign, stride int32, storeOp OpKind, storeWidth int, idxHelper string) error {
	// buf = __fern_arr_cow_inplace(arr, stride)
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__arr_set_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_cow_inplace", I32: 2})
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})
	// Element address via the per-stride bounds-check helper.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	if err := b.expr(t.Idx); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: idxHelper, I32: 2})
	// Element value.
	if err := b.expr(n.Value); err != nil {
		return err
	}
	b.emit(Op{Kind: storeOp, Width: storeWidth})
	// Write the (possibly new) buffer pointer back into the
	// ident's slot. The helper already dec'd the old buffer
	// when it had to copy; in the in-place case buf == arr and
	// the store is a no-op semantically.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpStoreLocal, I32: arrSlot})
	return nil
}

// emitArrayIndexAssignCoWField lowers `obj.field[i] = v` for a
// writable local-ident-target struct field whose type is an
// array. Same CoW shape as emitArrayIndexAssignCoW but the
// "slot" the new buffer flows back into is the struct field's
// memory location rather than a local-variable slot. The
// helper still internalises rc bookkeeping; the caller stashes
// the field's byte address up-front so both the field load
// (read OLD arr) and the field store (write NEW buf) hit the
// same address.
func (b *builder) emitArrayIndexAssignCoWField(fa *ast.FieldAccess, fieldOffset int32, fieldType ast.Type, t *ast.Index, n *ast.Assign, stride int32, storeOp OpKind, storeWidth int, idxHelper string) error {
	// fieldAddr = &obj.field
	fieldAddrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__arr_set_fld_%d", fieldAddrSlot)] = fieldAddrSlot
	if err := b.expr(fa.Target); err != nil {
		return err
	}
	if fieldOffset > 0 {
		b.emit(Op{Kind: OpConstI32, I32: fieldOffset})
		b.emit(Op{Kind: OpAdd})
	}
	b.emit(Op{Kind: OpStoreLocal, I32: fieldAddrSlot})
	// arr = *fieldAddr (load the array pointer from the field).
	b.emit(Op{Kind: OpLoadLocal, I32: fieldAddrSlot})
	b.emit(payloadLoadOpFor(fieldType, b.ptrW))
	// buf = __fern_arr_cow_inplace(arr, stride)
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_cow_inplace", I32: 2})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__arr_set_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})
	// Element address via the per-stride bounds-check helper.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	if err := b.expr(t.Idx); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: idxHelper, I32: 2})
	// Element value.
	if err := b.expr(n.Value); err != nil {
		return err
	}
	b.emit(Op{Kind: storeOp, Width: storeWidth})
	// Write buf back into obj.field. In the in-place case buf
	// == OLD arr (no change). In the copy case buf is the new
	// buffer and the field's pointer flips to it.
	b.emit(Op{Kind: OpLoadLocal, I32: fieldAddrSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(payloadStoreOpFor(fieldType, b.ptrW))
	return nil
}

// emitArrayIndexAssignCoWNested lowers `mat[i][j] = v` for a
// writable local-ident array-of-arrays. The outer slot — the
// address `&mat[i]` — flows through the per-stride bounds-
// check helper just like a regular `arr[i] = v` write. The
// inner-array pointer at that slot is fed through
// `__fern_arr_cow_inplace`, which mutates it in place on rc==1
// or returns a fresh copy on rc>1. The new buffer pointer is
// stored back into `&mat[i]`.
//
// Limitation: the outer `mat` slot is mutated in place. If
// some other local also aliases `mat`, the alias's view of
// `mat[i]` follows along (since they share the outer buffer).
// Phase 2c will gate the outer slot's write through
// `__fern_arr_cow_inplace` too, so aliases of mat see the
// pre-write inner-array pointer.
func (b *builder) emitArrayIndexAssignCoWNested(outer *ast.Index, t *ast.Index, n *ast.Assign, innerStride int32, storeOp OpKind, storeWidth int, idxHelper string) error {
	// Outer stride + outer __arr_idx_<N> helper for resolving
	// `&mat[i]`. Outer elements are pointer-shaped (each holds
	// the inner array's data pointer), so stride = ptrW on
	// natives or wasm.
	outerStride := int32(b.ptrW)
	outerHelper := outerArrIdxHelper(outerStride)
	// outerSlotAddr = &mat[i] via the bounds-check helper.
	outerSlotSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__arr_set_outer_%d", outerSlotSlot)] = outerSlotSlot
	if err := b.expr(outer.Array); err != nil {
		return err
	}
	if err := b.expr(outer.Idx); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: outerHelper, I32: 2})
	b.emit(Op{Kind: OpStoreLocal, I32: outerSlotSlot})
	// inner = *outerSlotAddr (pointer-width load).
	b.emit(Op{Kind: OpLoadLocal, I32: outerSlotSlot})
	b.emit(payloadLoadOpFor(outer.ElemType, b.ptrW))
	// buf = __fern_arr_cow_inplace(inner, innerStride)
	b.emit(Op{Kind: OpConstI32, I32: innerStride})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_cow_inplace", I32: 2})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__arr_set_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})
	// Element address: buf + j*innerStride via the inner per-
	// stride bounds-check helper.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	if err := b.expr(t.Idx); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: idxHelper, I32: 2})
	// Element value.
	if err := b.expr(n.Value); err != nil {
		return err
	}
	b.emit(Op{Kind: storeOp, Width: storeWidth})
	// Write buf back into *outerSlotAddr. In-place case is a
	// no-op semantically (buf == inner); copy case updates
	// mat[i] to point at the new buffer.
	b.emit(Op{Kind: OpLoadLocal, I32: outerSlotSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(payloadStoreOpFor(outer.ElemType, b.ptrW))
	return nil
}

// outerArrIdxHelper picks the per-stride bounds-check helper
// name for an outer-array indexing of pointer-width elements.
// Centralises the stride → helper-name mapping so the nested
// CoW path stays in sync with the regular `arr[i] = v` path.
func outerArrIdxHelper(stride int32) string {
	switch stride {
	case 1:
		return "__arr_idx_1"
	case 2:
		return "__arr_idx_2"
	case 8:
		return "__arr_idx_8"
	case 16:
		return "__arr_idx_16"
	default:
		return "__arr_idx"
	}
}

// emitArraySet lowers `arr.set(i, v)` inline — Phase 2b's
// explicit value-returning sister to `arr[i] = v`. Same shape
// as the IR-level CoW desugar but expression-position: leaves
// the (possibly new) buffer pointer on the operand stack as
// the call's result. Useful for callers that need value
// semantics in shapes the `arr[i] = v` desugar doesn't cover
// (param targets today, plus expression chaining like
// `arr.set(0, x).set(1, y)`).
func (b *builder) emitArraySet(n *ast.Call) error {
	elemType := n.TypeArgs[0]
	stride := int32(ast.ElemSizeBytesFor(elemType, b.ptrW))
	if stride == 0 {
		stride = 4
	}
	storeOp, storeWidth := arraySetStoreOp(elemType, b.ptrW)
	idxHelper := "__arr_idx"
	switch stride {
	case 1:
		idxHelper = "__arr_idx_1"
	case 2:
		idxHelper = "__arr_idx_2"
	case 8:
		idxHelper = "__arr_idx_8"
	case 16:
		idxHelper = "__arr_idx_16"
	}
	// Stash v in a typed scratch so the tail store picks the
	// right width on the wasm side.
	vSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__set_v_%d", vSlot)] = vSlot
	b.scratchType[vSlot] = elemType
	if err := b.expr(n.Args[2]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: vSlot})
	// Stash i in a scratch so we can use it after the helper
	// call (the helper consumes the operand stack).
	iSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__set_i_%d", iSlot)] = iSlot
	if err := b.expr(n.Args[1]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: iSlot})
	// buf = __fern_arr_cow_inplace(arr, stride)
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_cow_inplace", I32: 2})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__set_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})
	// Element address: buf + i*stride via the per-stride
	// bounds-check helper.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpCallDirect, Str: idxHelper, I32: 2})
	// Element value.
	b.emit(Op{Kind: OpLoadLocal, I32: vSlot})
	b.emit(Op{Kind: storeOp, Width: storeWidth})
	// Result: buf pointer (the array value).
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	return nil
}

// arraySetStoreOp picks the right store op + width for an
// element of type `t` on a target with pointer width `ptrW`.
// Mirrors the inline selection done in `b.assign`'s Index
// target — extracted here so emitArraySet can reuse it.
func arraySetStoreOp(t ast.Type, ptrW int) (OpKind, int) {
	storeOp := OpStore
	storeWidth := 0
	if t == nil {
		return storeOp, storeWidth
	}
	if nt, ok := t.(ast.NumberType); ok {
		switch nt.NormalWidth() {
		case 8:
			storeOp = OpStoreI8
		case 16:
			storeOp = OpStoreI16
		case 64:
			storeWidth = 64
		}
	}
	if ast.IsPointerType(t) {
		storeWidth = WidthPtr
	}
	if ft, ok := t.(ast.FloatType); ok {
		storeOp = OpFStore
		if ft.NormalWidth() == 64 {
			storeWidth = 64
		}
	}
	if _, isString := t.(ast.StringType); isString && ast.UseTwoWordStrings(ptrW) {
		storeWidth = WidthString
	}
	return storeOp, storeWidth
}

func (b *builder) emitArrayPush(n *ast.Call) error {
	elemType := n.TypeArgs[0]
	stride := int32(ast.ElemSizeBytesFor(elemType, b.ptrW))
	if stride == 0 {
		stride = 4
	}

	// Stash v in a typed scratch so the tail store picks the
	// right load width (i64 / f64 vs i32). Typed scratch makes
	// the `local.get` automatic on the wasm side.
	vSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_v_%d", vSlot)] = vSlot
	b.scratchType[vSlot] = elemType
	if err := b.expr(n.Args[1]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: vSlot})

	// Stash arr (heap pointer). The helper needs it; the tail
	// store also reads from the returned buffer pointer.
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

	// buf = __fern_arr_push_grow(arr, oldLen, stride)
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_push_grow", I32: 3})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})

	// *(buf + oldLen * stride) = v   (width-correct store)
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: vSlot})
	b.emit(arrayElemStoreOpFor(elemType, b.ptrW))

	// Result: buf pointer (the array value).
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
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
	_, err := b.boxIntoCellSlot(arg, t, slotLabel)
	return err
}

// boxIntoCellSlot is boxIntoCell that also returns the function-local
// slot holding the cell pointer, so a caller can reclaim the cell after
// the helper call that consumes it (see freeLookupKeyCell for the
// transient read-method lookup-key path).
func (b *builder) boxIntoCellSlot(arg ast.Expr, t ast.Type, slotLabel string) (int32, error) {
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
		return 0, err
	}
	b.emit(payloadStoreOpFor(t, b.ptrW))
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	return cellSlot, nil
}

// freeLookupKeyCell reclaims the transient boxed lookup-key cell after a
// READ-ONLY Map method (get / has / delete / get_or) has consumed it —
// the read helpers never retain the key cell (only set stores it). The
// helper's result sits underneath on the operand stack and is left
// untouched (the free ops are stack-balanced: load, call→returns cell,
// drop). When the key was a FRESH owned temporary (a concat / literal /
// call rather than an Ident / field / index alias the caller still
// owns), its string buffer is also reclaimed via __fern_str_dec first.
// wasm-only: cellSlot is only produced when the key was boxed (string K
// on wasm32). A cellSlot < 0 (unboxed native key) is a no-op.
func (b *builder) freeLookupKeyCell(cellSlot int32, keyArg ast.Expr, kType ast.Type) {
	if cellSlot < 0 || b.ptrW != 4 {
		return
	}
	if _, isStr := kType.(ast.StringType); isStr && !needsRcIncOnAlias(keyArg, b) {
		b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
		b.emit(Op{Kind: OpLoad, Width: WidthString})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_cell_free", I32: 1})
	b.emit(Op{Kind: OpDrop})
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

// emitMapGetRebox lowers `m.get(k)` by reboxing the helper's
// uniform `Option[usize]` return into a consumer-shaped
// `Option[V]`. The helper (`__map_get_impl`) always returns
// `Option[usize]` whose payload (at the usize slot offset — 8 on
// natives, 4 on wasm32) is EITHER the V value directly (when V
// isn't boxed) OR a cell pointer (when V is boxed: wide scalar
// i64/u64/f64, or string on wasm32). The rebox translates that to
// an `Option[V]` heap box laid out the way the surrounding match /
// let-binding expects.
//
// Reboxing is necessary even for the non-boxed case: the helper's
// `Option[usize]` payload sits at the usize offset (8 on natives),
// but a consumer reading `Option[i32]` expects its payload at
// offset 4. A direct passthrough only lines up when V's payload
// layout happens to equal usize's — true for pointer-shaped V on
// natives, false for i32 V. The rebox makes every V correct.
func (b *builder) emitMapGetRebox(n *ast.Call, kType, vType ast.Type, boxedV bool) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	boxK := isStringForBoxing(kType, b.ptrW) || mapKeyKindTag(kType, b.ptrW) == 2
	keyCell := int32(-1)
	if boxK {
		var err error
		keyCell, err = b.boxIntoCellSlot(n.Args[1], kType, "__map_get_kbox")
		if err != nil {
			return err
		}
	} else if err := b.expr(n.Args[1]); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get", I32: 2})
	optPtrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__map_get_optptr_%d", optPtrSlot)] = optPtrSlot
	b.emit(Op{Kind: OpStoreLocal, I32: optPtrSlot})
	resultSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__map_get_res_%d", resultSlot)] = resultSlot
	// The rebuilt Option box carries the same 8-byte rc header every
	// heap box gets (rc=1 at [base+0], data = base+8) — like emitEnumNew
	// / a Some(..) literal. Without it the scope-exit drop of an unused
	// `var o = m.get(k)` reads heap metadata at [data-8] as the rc and
	// underflows.
	const rcHeaderBytes = 8
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__map_get_base_%d", baseSlot)] = baseSlot
	// `if` runs the then-arm when the i32 cond is non-zero.
	// Some has tag 0, so we'd want eq-zero before the if to
	// route Some → then-arm; doing the equivalent by routing
	// Some → else-arm (no extra eqz op) keeps the IR shorter.
	b.emit(Op{Kind: OpLoadLocal, I32: optPtrSlot})
	b.emit(Op{Kind: OpLoad}) // tag at +0
	b.emit(Op{Kind: OpIf, I32: int32(BlockTypeVoid)})
	// --- tag != 0 (None on this side): 4-byte tag-only Option.
	b.emit(Op{Kind: OpConstI32, I32: 4 + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1}) // rc = 1
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1}) // tag = None
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpElse})
	// --- tag == 0 (Some): build the user-shaped Option<V>.
	offsets, size := payloadLayout([]ast.Type{vType}, 1, b.ptrW)
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1}) // rc = 1
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: 0}) // tag = Some
	b.emit(Op{Kind: OpStore})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpConstI32, I32: offsets[0]})
	b.emit(Op{Kind: OpAdd})
	// Read the helper's Option[usize] payload at the usize slot
	// offset with a pointer-width load so the full value (cell
	// pointer or pointer-shaped V) survives on natives.
	b.emit(Op{Kind: OpLoadLocal, I32: optPtrSlot})
	b.emit(Op{Kind: OpConstI32, I32: usizeOptionPayloadOffset(b.ptrW)})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoad, Width: WidthPtr})
	if boxedV {
		// Payload is a cell pointer — dereference to load the
		// real V (wide scalar / two-word string) out of the cell.
		b.emit(payloadLoadOpFor(vType, b.ptrW))
		// Map[K, string] get retain: the returned Option now co-owns the
		// string buffer alongside the map's cell, so __fern_str_inc (which
		// returns the (data, len) pair for the store below). Balanced by the
		// caller's dec of the gotten string and the map's drop dec.
		if _, isStr := vType.(ast.StringType); isStr && b.ptrW == 4 {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
		}
	} else if _, isStr := vType.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
		// Map[K, string] get retain (native single-word, non-boxed): the
		// payload IS the string data pointer; bump its L2 rc so the
		// returned Option co-owns the buffer alongside the map's column
		// slot. Balances the map's drop dec; literals short-circuit on
		// the sentinel. Gated to non-two-word natives — arm64 (boxed
		// strings under TwoWordOverride) is excluded since map values
		// are stored as cell pointers and rc_inc on a cell pointer
		// would bump the cell's rc, not the string's.
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
	}
	// Non-boxed: the payload IS the V value (an i32 in the low
	// bits, or a pointer-shaped V address). payloadStoreOpFor
	// narrows / widens to the V slot correctly.
	b.emit(payloadStoreOpFor(vType, b.ptrW))
	b.emit(Op{Kind: OpEnd})
	// get doesn't retain the key cell — reclaim the transient (the
	// operand stack is empty here, before the result is pushed).
	b.freeLookupKeyCell(keyCell, n.Args[1], kType)
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	return nil
}

// usizeOptionPayloadOffset returns the byte offset of the payload
// slot in an `Option[usize]` heap box — the shape `__map_get_impl`
// returns. The tag occupies offset 0; the pointer-width usize
// payload follows, 8-aligned on natives (→ offset 8) and at
// offset 4 on wasm32. Centralised so the wide-map get / get_or
// readers stay in sync with payloadLayout's usize sizing.
func usizeOptionPayloadOffset(ptrW int) int32 {
	offs, _ := payloadLayout([]ast.Type{ast.NumberType{Width: ast.WidthPtr}}, 1, ptrW)
	return offs[0]
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
	keyCell, err := b.boxIntoCellSlot(n.Args[1], kType, "__map_kbox")
	if err != nil {
		return err
	}
	for i := 2; i < len(n.Args); i++ {
		if err := b.expr(n.Args[i]); err != nil {
			return err
		}
	}
	b.emit(Op{Kind: OpCallDirect, Str: methodName, I32: argCount})
	// Read-only methods (has / get_or) don't retain the key cell — only
	// set does, and set never routes here. Reclaim the transient cell.
	if methodName != "__method_Map_set" {
		b.freeLookupKeyCell(keyCell, n.Args[1], kType)
	}
	return nil
}

// emitMapDeleteReturningTuple lowers `m.delete(k)` into a
// (Map[K,V], bool) tuple. The underlying `__map_delete_impl`
// still returns a boolean; this wrapper saves the map receiver
// before the call, then heap-allocates a 2-element tuple and
// stores (mapPtr, found) at the correct pointer-width offsets
// so the tuple matches the layout `tupleElemLayout([(Map[K,V],
// bool)], ptrW)` sees on the call side. The key arg is boxed via
// emitStringKMapCall-style boxing if needed.
func (b *builder) emitMapDeleteReturningTuple(n *ast.Call, kType ast.Type) error {
	// Save the map receiver to a local slot so we can store it
	// in the result tuple after the delete call.
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	mapSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__del_m_%d", mapSlot)] = mapSlot
	b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})

	// Phase 2d: copy-on-write. Route the receiver through
	// __map_cow_inplace before delete mutates it, then store the
	// (possibly new) handle back into mapSlot so it becomes BOTH
	// the mutation target and the map placed in the result tuple.
	// An aliased map (rc>1) is deep-copied here, so the source
	// alias keeps the deleted key; a uniquely-held map is mutated
	// in place.
	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__map_cow_inplace", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})

	// Push map and key for the delete call, boxing key when needed.
	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	needBoxK := isStringForBoxing(kType, b.ptrW) || mapKeyKindTag(kType, b.ptrW) == 2
	keyCell := int32(-1)
	if needBoxK {
		var err error
		keyCell, err = b.boxIntoCellSlot(n.Args[1], kType, "__del_kbox")
		if err != nil {
			return err
		}
	} else {
		if err := b.expr(n.Args[1]); err != nil {
			return err
		}
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_delete", I32: 2})

	// Save bool result.
	boolSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__del_ok_%d", boolSlot)] = boolSlot
	b.emit(Op{Kind: OpStoreLocal, I32: boolSlot})
	// delete doesn't retain the key cell — reclaim the transient.
	b.freeLookupKeyCell(keyCell, n.Args[1], kType)

	// Allocate (Map[K,V], bool) tuple: [mapPtr:ptrW | bool:4].
	// Layout matches tupleElemLayout([(Map[K,V], bool)], ptrW):
	// elem 0 = map at offset 0, elem 1 = bool at offset ptrW.
	// The box carries an 8-byte rc header before the data (rc=1 at
	// [base+0], data = base+8) exactly like emitEnumNew / a tuple
	// literal — without it the scope-exit tuple drop reads heap
	// metadata at [data-8] as the rc and underflows / corrupts.
	const rcHeaderBytes = 8
	size := int32(b.ptrW) + 4
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__del_base_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	tupSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__del_tup_%d", tupSlot)] = tupSlot
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: tupSlot})

	// Store map pointer at offset 0 (pointer-width).
	b.emit(Op{Kind: OpLoadLocal, I32: tupSlot})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	b.emit(Op{Kind: OpStore, Width: WidthPtr})

	// Store bool at offset ptrW (4-byte).
	b.emit(Op{Kind: OpLoadLocal, I32: tupSlot})
	b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: boolSlot})
	b.emit(Op{Kind: OpStore})

	b.emit(Op{Kind: OpLoadLocal, I32: tupSlot})
	return nil
}

// emitMapClearReturningMap lowers `m.clear()` into a Map[K,V]
// return. The underlying `__map_clear_impl` is void; this wrapper
// calls it for the side effect and then pushes the original map
// receiver back so the call site sees a Map[K,V] result.
func (b *builder) emitMapClearReturningMap(n *ast.Call) error {
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	mapSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__clr_m_%d", mapSlot)] = mapSlot
	b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})

	// Phase 2d: copy-on-write — see emitMapDeleteReturningTuple.
	// cow the receiver before clear empties it, then thread the
	// (possibly new) handle back as both the clear target and the
	// returned map so an aliased map keeps its entries.
	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__map_cow_inplace", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})

	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_clear", I32: 1})

	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
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
	keyCell := int32(-1)
	if boxK {
		var err error
		keyCell, err = b.boxIntoCellSlot(n.Args[1], kType, "__map_or_kbox")
		if err != nil {
			return err
		}
	} else if err := b.expr(n.Args[1]); err != nil {
		return err
	}
	if err := b.boxIntoCell(n.Args[2], vType, "__map_or_box"); err != nil {
		return err
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get_or", I32: 3})
	b.emit(payloadLoadOpFor(vType, b.ptrW))
	// Map[K, string] get_or retain (wasm boxed V): the returned (data, len)
	// pair is the string the column was holding (or our just-allocated
	// fallback cell). The caller will co-own the buffer alongside the
	// map's cell, so __fern_str_inc it. Balances the caller's later dec
	// and the map drop's column-walk dec. Mirrors emitMapGetRebox's
	// boxed-V retain — same correctness rationale, same gating.
	if _, isStr := vType.(ast.StringType); isStr && b.ptrW == 4 {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
	}
	// get_or doesn't retain the key cell — reclaim the transient (the
	// loaded result value sits underneath; the free ops are balanced).
	// NB the fallback value cell (__map_or_box) is a separate temporary
	// that still leaks — same fresh-arg-temporary class as elsewhere.
	b.freeLookupKeyCell(keyCell, n.Args[1], kType)
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
