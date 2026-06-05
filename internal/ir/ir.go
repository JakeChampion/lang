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

// typeHasDynTrait reports whether a type is, or nests, a `dyn Trait`.
func typeHasDynTrait(t ast.Type) bool {
	switch x := t.(type) {
	case ast.DynTraitType:
		return true
	case ast.ArrayType:
		return typeHasDynTrait(x.Elem)
	case ast.SliceType:
		return typeHasDynTrait(x.Elem)
	case ast.TupleType:
		for _, e := range x.Elems {
			if typeHasDynTrait(e) {
				return true
			}
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			if typeHasDynTrait(p) {
				return true
			}
		}
		return typeHasDynTrait(x.Result)
	case ast.StructType:
		for _, a := range x.Args {
			if typeHasDynTrait(a) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if typeHasDynTrait(a) {
				return true
			}
		}
	}
	return false
}

// rejectDynTrait scans a program's function signatures, local variable
// annotations, and dynamic method-call markers for `dyn Trait` usage,
// returning a clear unsupported-feature error if any is found. `dyn` is
// interpreter-only until the compiled-backend vtable slices land. See
// docs/DYN-TRAITS.md.
func rejectDynTrait(prog *ast.Program) error {
	const msg = "dyn Trait is not yet supported on compiled backends; run it on the interpreter (fern -interp) or model the closed case as an enum + match"
	var found bool
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			for _, p := range x.Params {
				found = found || typeHasDynTrait(p.Type)
			}
			found = found || typeHasDynTrait(x.ReturnType)
		case *ast.Var:
			found = found || typeHasDynTrait(x.Type)
		case *ast.Call:
			found = found || x.DynTrait != ""
		}
		return true
	})
	if found {
		return fmt.Errorf("ir: %s", msg)
	}
	return nil
}

// LowerWith is the pointer-width-aware variant. `ptrW` is 4 on
// wasm32 and 8 on arm64; it sizes pointer-typed enum payloads,
// struct fields, array elements, and closure captures so heap
// addresses survive arm64-darwin's >= 4 GiB heap.
func LowerWith(prog *ast.Program, info *checker.Info, ptrW int) (*Program, error) {
	// `dyn Trait` (runtime trait objects) is interpreter-only in its
	// first slice — the compiled backends need a fat-pointer + vtable
	// representation that isn't built yet (docs/DYN-TRAITS.md §4.2). The
	// IR layer is the single choke point for every compiled backend, so
	// reject `dyn` here with a clear message rather than letting it fall
	// through to a cryptic "indirect call from non-identifier" later.
	if err := rejectDynTrait(prog); err != nil {
		return nil, err
	}
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
	// Tuple-shape registry, sibling of genEnumDrops. Records the
	// canonical TupleType for every shape dropFnNameFor routed
	// through `__drop_tuple_<mangled>`, so the post-pass worklist can
	// recover the element list when generating each drop body.
	genTupleDrops := map[string]ast.TupleType{}
	// Per-function "result never aliases a parameter" property — lets the
	// stage-(b) arg-temp reclaim safely free an owned temp passed to a
	// pointer-returning callee (findReturnsNoParamEscape).
	returnsNoParamEscape := findReturnsNoParamEscape(prog, info)
	for _, fn := range prog.Funcs {
		f, err := lowerFunc(fn, info, ptrW, pairForm, closureCaps, genEnumDrops, genTupleDrops, returnsNoParamEscape)
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
		// Closures that form a real heap PAIR (OpMakeClosure — i.e. not
		// elided to a bare env via ElideClosurePair) carry a drop-fn
		// pointer in their pair; that pointer needs a __closure_drop_<name>
		// thunk even when the captures are all scalar (the thunk frees the
		// env block). Collect those targets so the loop below generates a
		// thunk for them too, not only for the rc-capture closures whose
		// elided drop sites reference one directly.
		makeClosureTargets := map[string]bool{}
		for _, f := range out.Funcs {
			for _, op := range f.Ops {
				if op.Kind == OpMakeClosure && op.Str != "" {
					makeClosureTargets[op.Str] = true
				}
			}
		}
		for name, caps := range closureCaps {
			if !hasRcCapture(caps, ptrW) && !makeClosureTargets[name] {
				continue
			}
			thunk := genClosureDropThunk(name, caps, ptrW, info, genEnumDrops, genTupleDrops)
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
					op.Str == "__drop_arr_closure" ||
					strings.HasPrefix(op.Str, "__drop_arr_struct_") ||
					strings.HasPrefix(op.Str, "__drop_arr_enum_") ||
					strings.HasPrefix(op.Str, "__drop_arr_tuple_") ||
					strings.HasPrefix(op.Str, "__drop_arr_arr_") ||
					strings.HasPrefix(op.Str, "__drop_arr_of_") ||
					strings.HasPrefix(op.Str, "__drop_map_via_") ||
					op.Str == "__drop_map_str_values" ||
					op.Str == "__drop_map_str_keys" ||
					strings.HasPrefix(op.Str, "__drop_enum_") ||
					strings.HasPrefix(op.Str, "__drop_tuple_")) && !queued[op.Str] {
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
			if name == "__drop_arr_closure" {
				// Array-of-closure outer drop: per element free the closure
				// env (generically, through the embedded drop-fn pointer) +
				// the pair block, then free the outer buffer.
				fn = genArrClosureDropFn(ptrW)
			} else if perElem := strings.TrimPrefix(name, "__drop_arr_of_"); perElem != name {
				// Array-of-(rc-inner-array) outer drop: per element call the
				// inner array's own deep-drop `perElem` (regenerated by the
				// worklist from this body), then free the outer buffer.
				fn = genArrOfArrDropFn(perElem, ptrW)
			} else if elem := strings.TrimPrefix(name, "__drop_arr_struct_"); elem != name {
				fn = genArrStructDropFn(elem, ptrW)
			} else if en := strings.TrimPrefix(name, "__drop_arr_enum_"); en != name {
				// Array-of-enum outer drop: per element deep-drop the enum box
				// via __drop_enum_<Name> (regenerated by the worklist from this
				// body), then free the outer buffer.
				fn = genArrEnumDropFn(en, ptrW)
			} else if name == "__drop_arr_arr_str" {
				// Array-of-string[] outer drop: per element reclaim the inner
				// string[] via __fern_drop_arr_str (walk + str_dec + free), then
				// free the outer buffer.
				fn = genArrArrStrDropFn(ptrW)
			} else if strideStr := strings.TrimPrefix(name, "__drop_arr_arr_"); strideStr != name {
				// Array-of-(primitive-array) outer drop. The name encodes the
				// inner element stride; the loop frees each inner buffer +
				// the outer (genArrArrDropFn). Stride-keyed, so i32[][] and
				// f32[][] (both inner stride 4) share one generated fn.
				stride, err := strconv.Atoi(strideStr)
				if err != nil {
					continue
				}
				fn = genArrArrDropFn(int32(stride), ptrW)
			} else if mangled := strings.TrimPrefix(name, "__drop_arr_tuple_"); mangled != name {
				// Routing recorded the tuple shape in genTupleDrops at
				// the call site (arrElemStructDropName), so the
				// per-element __drop_tuple_<mangled> the loop body
				// invokes is generatable; enqueueCalls picks it up
				// from this body for the worklist's next pass.
				if _, ok := genTupleDrops[mangled]; !ok {
					continue
				}
				fn = genArrTupleDropFn(mangled, ptrW)
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
				fn = genEnumDropFn(en, ed, info, ptrW, genEnumDrops, genTupleDrops)
				if fn == nil {
					continue // plan failed — routing shouldn't have named it
				}
			} else if mangled := strings.TrimPrefix(name, "__drop_tuple_"); mangled != name {
				// Tuple shape has no source name — dropFnNameFor stashed
				// the canonical TupleType in genTupleDrops under the
				// mangled key. The worklist regenerates the body from
				// the recovered shape.
				tt, ok := genTupleDrops[mangled]
				if !ok {
					continue
				}
				fn = genTupleDropFn(mangled, tt, info, ptrW, genEnumDrops, genTupleDrops)
				if fn == nil {
					continue
				}
			} else {
				sn := strings.TrimPrefix(name, "__drop_struct_")
				sd, ok := info.Structs[sn]
				if !ok {
					continue // routing only names structs it verified exist
				}
				fn = genStructDropFn(sn, sd, info, ptrW, genEnumDrops, genTupleDrops)
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

// findReturnsNoParamEscape computes, per user function, whether it provably
// never lets a parameter's heap value escape into its return value — i.e. its
// result can never alias (or transitively contain) any argument the caller
// passed. When true, an owned temporary passed as a borrowed argument to that
// function is DEAD the instant the call returns, so the stage-(b) post-call dec
// can reclaim it even though the function returns a pointer — lifting the
// conservative concrete-scalar-result gate that otherwise leaks the temp in a
// nested call like `outer(inner(mk()))` (`sum(dup(build(n)))` leaked the
// `build(n)` temp every iteration).
//
// Soundness: a function qualifies only when EVERY return expression is built
// purely from definitely-scalar values, FRESH constructions (variant / struct /
// tuple / array literals) whose pointer-typed slots are themselves qualifying,
// and calls to OTHER qualifying functions. A bare pointer ident / field / index
// (the identity- or projection-return that would alias a param) disqualifies
// it. Generic / unknown payload types are treated as pointers (never assumed
// scalar), so `wrap[T](x) { return Some(x); }` does NOT qualify. The fixpoint
// starts optimistic (all true) and removes any function whose body contradicts
// the property — the greatest fixpoint, so a self-recursive fresh-returner like
// `dup` (returns `Cons(h, t.dup())`) correctly stays true.
func findReturnsNoParamEscape(prog *ast.Program, info *checker.Info) map[string]bool {
	// Variant-constructor name -> payload types, for the construction recursion.
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	q := map[string]bool{}
	for _, fn := range prog.Funcs {
		q[fn.Name] = true
	}
	for {
		changed := false
		for _, fn := range prog.Funcs {
			if !q[fn.Name] || fn.Body == nil {
				continue
			}
			freshLocals := computeFreshLocals(fn, info, variantPayloads, q)
			ok := true
			ast.Walk(fn.Body, func(n ast.Node) bool {
				if r, isRet := n.(*ast.Return); isRet && r.Value != nil {
					if !exprNoParamEscape(r.Value, fn.ReturnType, info, variantPayloads, q, freshLocals) {
						ok = false
					}
				}
				return true
			})
			if !ok {
				q[fn.Name] = false
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return q
}

// inferParamEscapes computes, per function and per POINTER parameter, whether
// that parameter's heap value can ESCAPE the function — flow out through the
// return value, or be stored into a caller-visible container (a retain sink such
// as `m.set` / `arr.push`, or an `own` argument the callee itself lets escape).
// A NON-escaping parameter is reclaim-safe: under an owned-by-default model the
// callee may free it at the end without transferring ownership out, and if it is
// additionally only read it may be borrowed. This is the foundation analysis for
// ownership / borrow inference — Slice 0 of docs/OWNERSHIP-INFERENCE-PLAN.md. It
// does NOT change codegen yet.
//
// Greatest fixpoint over the call graph (optimistic: nothing escapes; a
// parameter flips to "escapes" once and never back, so it terminates). A value
// passed to a borrowing callee position does not escape through that call;
// passed to a position the callee escapes, to a retain sink, or returned as part
// of the result, it does. Unknown / builtin callees are treated conservatively
// (assume they escape a tainted argument) so the result is a sound
// under-approximation of "borrowable".
func inferParamEscapes(prog *ast.Program, info *checker.Info) map[string][]bool {
	variantPayloads := map[string][]ast.Type{}
	for _, en := range info.Enums {
		for _, v := range en.Variants {
			variantPayloads[v.Name] = v.Payloads
		}
	}
	escapes := map[string][]bool{}
	for _, fn := range prog.Funcs {
		escapes[fn.Name] = make([]bool, len(fn.Params))
	}
	for {
		changed := false
		for _, fn := range prog.Funcs {
			if fn.Body == nil {
				continue
			}
			for i, p := range fn.Params {
				if escapes[fn.Name][i] || !ast.IsPointerType(p.Type) {
					continue
				}
				if paramEscapesInFn(fn, p.Name, info, variantPayloads, escapes) {
					escapes[fn.Name][i] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return escapes
}

// exprRefsTainted reports whether any tainted name appears anywhere in e.
func exprRefsTainted(e ast.Expr, tainted map[string]bool) bool {
	found := false
	ast.Walk(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && tainted[id.Name] {
			found = true
		}
		return true
	})
	return found
}

// taintedReachesSlot reports whether evaluating e to fill a slot of static type
// `slot` can place a TAINTED (parameter-derived) heap value into that slot — the
// inverse of exprNoParamEscape, specialized to a concrete taint set and aware of
// per-callee escape facts.
func taintedReachesSlot(e ast.Expr, slot ast.Type, tainted map[string]bool, info *checker.Info, variantPayloads map[string][]ast.Type, escapes map[string][]bool) bool {
	if isDefinitelyScalar(slot) {
		return false // a scalar slot cannot carry a heap pointer
	}
	switch x := e.(type) {
	case *ast.Ident:
		return tainted[x.Name]
	case *ast.FieldAccess:
		return exprRefsTainted(x.Target, tainted) // projecting a tainted value's sub-heap out
	case *ast.Index:
		return exprRefsTainted(x.Array, tainted)
	case *ast.IfExpr:
		return taintedReachesSlot(x.Then, slot, tainted, info, variantPayloads, escapes) ||
			taintedReachesSlot(x.Else, slot, tainted, info, variantPayloads, escapes)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if taintedReachesSlot(arm.Body, slot, tainted, info, variantPayloads, escapes) {
				return true
			}
		}
		return false
	case *ast.TupleLit:
		tt, ok := slot.(ast.TupleType)
		for i, el := range x.Elems {
			var es ast.Type
			if ok && i < len(tt.Elems) {
				es = tt.Elems[i]
			}
			if taintedReachesSlot(el, es, tainted, info, variantPayloads, escapes) {
				return true
			}
		}
		return false
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			if taintedReachesSlot(el, x.ElemType, tainted, info, variantPayloads, escapes) {
				return true
			}
		}
		return false
	case *ast.StructLit:
		if x.Base != nil && exprRefsTainted(x.Base, tainted) {
			return true
		}
		sd, ok := info.Structs[x.TypeName]
		if !ok {
			return exprRefsTainted(e, tainted)
		}
		ft := map[string]ast.Type{}
		for _, f := range sd.Fields {
			ft[f.Name] = f.Type
		}
		for _, fi := range x.Fields {
			if taintedReachesSlot(fi.Value, ft[fi.Name], tainted, info, variantPayloads, escapes) {
				return true
			}
		}
		return false
	case *ast.Call:
		id, ok := x.Callee.(*ast.Ident)
		if !ok {
			return exprRefsTainted(e, tainted)
		}
		// Variant constructor: the result embeds its payloads.
		if pls, isVariant := variantPayloads[id.Name]; isVariant {
			for i, a := range x.Args {
				var pt ast.Type
				if i < len(pls) {
					pt = pls[i]
				}
				if taintedReachesSlot(a, pt, tainted, info, variantPayloads, escapes) {
					return true
				}
			}
			return false
		}
		// User function / method: a tainted argument reaches the result only if
		// the callee itself escapes that argument position. Unknown callees
		// (absent from `escapes`) are conservative: a tainted arg reaches out.
		ce, known := escapes[id.Name]
		for i, a := range x.Args {
			if !exprRefsTainted(a, tainted) {
				continue
			}
			if !known || (i < len(ce) && ce[i]) {
				return true
			}
		}
		return false
	}
	return exprRefsTainted(e, tainted)
}

// paramEscapesInFn reports whether parameter `pname` of `fn` escapes, given the
// current per-callee escape facts. It first taints every local / binding that
// carries pname's heap (match bindings of a tainted scrutinee, and any var whose
// init lets a tainted value reach its slot), then checks the escape sinks:
// returns, and tainted whole-value arguments passed to a retain sink or an
// escaping `own` position.
func paramEscapesInFn(fn *ast.FuncDecl, pname string, info *checker.Info, variantPayloads map[string][]ast.Type, escapes map[string][]bool) bool {
	tainted := map[string]bool{pname: true}
	for {
		grew := false
		taintBindings := func(scrut ast.Expr, names []string) {
			if !exprRefsTainted(scrut, tainted) {
				return
			}
			for _, nm := range names {
				if nm != "" && !tainted[nm] {
					tainted[nm] = true
					grew = true
				}
			}
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Var:
				if x.Init != nil && !tainted[x.Name] &&
					taintedReachesSlot(x.Init, x.Type, tainted, info, variantPayloads, escapes) {
					tainted[x.Name] = true
					grew = true
				}
			case *ast.Match:
				for _, arm := range x.Arms {
					taintBindings(x.Tag, arm.Bindings)
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					taintBindings(x.Tag, arm.Bindings)
				}
			}
			return true
		})
		if !grew {
			break
		}
	}
	escaped := false
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Return:
			if x.Value != nil && taintedReachesSlot(x.Value, fn.ReturnType, tainted, info, variantPayloads, escapes) {
				escaped = true
			}
		case *ast.Call:
			id, ok := x.Callee.(*ast.Ident)
			if !ok {
				return true
			}
			retains := calleeRetainsAnyArg(id.Name)
			ownFlags := info.OwnFuncs[id.Name]
			ce, known := escapes[id.Name]
			for i, a := range x.Args {
				aid, isIdent := a.(*ast.Ident)
				if !isIdent || !tainted[aid.Name] {
					continue
				}
				// Stored into a container (retain sink), or handed to an `own`
				// parameter the callee itself lets escape — either way the value
				// outlives this call frame.
				if retains {
					escaped = true
				}
				if i < len(ownFlags) && ownFlags[i] && (!known || (i < len(ce) && ce[i])) {
					escaped = true
				}
			}
		}
		return true
	})
	return escaped
}

// computeFreshLocals returns the locals of `fn` that provably hold a param-free
// value at every return — letting `return r` (where r was built locally)
// qualify, not just `return Ctor(..)`. A local is fresh when:
//   - it has exactly one `var` declaration (no shadowing / redeclaration),
//   - that declaration's init is itself param-free (exprNoParamEscape, with the
//     fresh set available so one fresh local may seed another), and
//   - the name is used ONLY inside return-value expressions.
//
// The last condition is the soundness crux: any MUTATION of the local (a
// reassignment, or passing it as a call argument / method receiver that could
// splice a param in) necessarily mentions the name OUTSIDE a return, so it
// disqualifies the local. What remains is born fresh and only ever read on the
// way out — its value can never come to alias a parameter. Fields being
// immutable after construction (E048) means no `r.f = param` backdoor either.
func computeFreshLocals(fn *ast.FuncDecl, info *checker.Info, variantPayloads map[string][]ast.Type, q map[string]bool) map[string]bool {
	if fn.Body == nil {
		return nil
	}
	decls := map[string]*ast.Var{}
	multi := map[string]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if v, ok := n.(*ast.Var); ok {
			if _, seen := decls[v.Name]; seen {
				multi[v.Name] = true
			}
			decls[v.Name] = v
		}
		return true
	})
	// Idents that occur inside a return value — the only use a fresh local is
	// allowed. (The declared name itself is a Var.Name string, not an Ident, so
	// it never counts as a use.)
	inReturn := map[*ast.Ident]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if r, ok := n.(*ast.Return); ok && r.Value != nil {
			ast.Walk(r.Value, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					inReturn[id] = true
				}
				return true
			})
		}
		return true
	})
	tainted := map[string]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && !inReturn[id] {
			tainted[id.Name] = true
		}
		return true
	})
	fresh := map[string]bool{}
	for name, v := range decls {
		if !multi[name] && !tainted[name] && v.Init != nil {
			fresh[name] = true
		}
	}
	// Greatest fixpoint: drop any candidate whose init isn't param-free given the
	// current set (a fresh local may legitimately appear in another's init).
	for {
		changed := false
		for name := range fresh {
			v := decls[name]
			if !exprNoParamEscape(v.Init, v.Type, info, variantPayloads, q, fresh) {
				delete(fresh, name)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return fresh
}

func isDefinitelyScalar(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true
	}
	return false
}

// exprNoParamEscape reports whether evaluating `e` to fill a slot of static type
// `slot` can never place a parameter's heap value into that slot. `q` is the
// in-progress greatest-fixpoint set from findReturnsNoParamEscape. Any shape it
// can't prove safe returns false (conservative — preserves the prior safe-leak).
func exprNoParamEscape(e ast.Expr, slot ast.Type, info *checker.Info, variantPayloads map[string][]ast.Type, q map[string]bool, freshLocals map[string]bool) bool {
	// A definitely-scalar destination cannot hold a heap pointer at all, so
	// whatever fills it carries no param heap. (Generic / unknown slot types are
	// NOT assumed scalar — they fall through and must be proven structurally.)
	if isDefinitelyScalar(slot) {
		return true
	}
	switch x := e.(type) {
	case *ast.Ident:
		// A nullary variant constructor (`Nil`, `None`) parses as a bare ident
		// and is a fresh constant — no parameter can escape through it. A
		// fresh-proven local (computeFreshLocals: a single-assignment local with
		// a fresh init, used only in return positions, so never mutated to embed
		// a param) is likewise param-free. Any other bare pointer-typed ident (a
		// parameter or a projection binding) is conservatively an escape.
		if pls, isVariant := variantPayloads[x.Name]; isVariant && len(pls) == 0 {
			return true
		}
		return freshLocals[x.Name]
	case *ast.IfExpr:
		return exprNoParamEscape(x.Then, slot, info, variantPayloads, q, freshLocals) &&
			exprNoParamEscape(x.Else, slot, info, variantPayloads, q, freshLocals)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if !exprNoParamEscape(arm.Body, slot, info, variantPayloads, q, freshLocals) {
				return false
			}
		}
		return true
	case *ast.TupleLit:
		tt, ok := slot.(ast.TupleType)
		if !ok || len(tt.Elems) != len(x.Elems) {
			return false
		}
		for i, el := range x.Elems {
			if !exprNoParamEscape(el, tt.Elems[i], info, variantPayloads, q, freshLocals) {
				return false
			}
		}
		return true
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			if !exprNoParamEscape(el, x.ElemType, info, variantPayloads, q, freshLocals) {
				return false
			}
		}
		return true
	case *ast.StructLit:
		if x.Base != nil {
			return false // struct-update spreads the base's (possibly param) fields
		}
		sd, ok := info.Structs[x.TypeName]
		if !ok {
			return false
		}
		ftype := map[string]ast.Type{}
		for _, f := range sd.Fields {
			ftype[f.Name] = f.Type
		}
		for _, fi := range x.Fields {
			ft, ok := ftype[fi.Name]
			if !ok || !exprNoParamEscape(fi.Value, ft, info, variantPayloads, q, freshLocals) {
				return false
			}
		}
		return true
	case *ast.Call:
		id, ok := x.Callee.(*ast.Ident)
		if !ok {
			return false
		}
		// Variant constructor: the result EMBEDS its payload args, so each
		// pointer-typed payload slot must itself be filled escape-free.
		if pls, isVariant := variantPayloads[id.Name]; isVariant {
			for i, a := range x.Args {
				var pt ast.Type
				if i < len(pls) {
					pt = pls[i]
				}
				if !exprNoParamEscape(a, pt, info, variantPayloads, q, freshLocals) {
					return false
				}
			}
			return true
		}
		// User function / method call: its result can't contain OUR args iff the
		// callee itself never lets a param escape. Builtins / locals / unknowns
		// (absent from q) are rejected.
		return q[id.Name]
	}
	return false
}

// isPairFormEligible returns true if fn can be lowered with the register-based
// pair-form return ABI. See findPairFormFuncs for the eligibility rules;
// `pairForm` is the previous fixpoint pass's known-pair-form set, used to
// authorise tail-call returns into it.
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
	// A function that registers a defer can't use the pair-form
	// (tag, payload) return ABI. The Return lowering's pair-form
	// fast path is gated on `len(b.defers) == 0`; with defers
	// present it falls back to the heap-box return path, which
	// emits a single-pointer OpReturn — a mismatch against the
	// two-i32 pair signature this function would otherwise be
	// given (the wasm validator rejects it: "expected i32 but
	// nothing on stack"; natives read a garbage payload register).
	// Keep such functions heap-form so the signature and every
	// return agree.
	var defers []*ast.Defer
	collectDefers(fn.Body, &defers)
	if len(defers) > 0 {
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
	// genTupleDrops is genEnumDrops' tuple sibling: the mangled tuple
	// shape (key) → the canonical TupleType (value). Tuples have no
	// declared name in source, so this registry IS the only way the
	// worklist can recover the element list for a tuple a nested
	// drop-fn called by mangled name. dropFnNameFor records each
	// distinct shape it routes through `__drop_tuple_<...>` here.
	genTupleDrops map[string]ast.TupleType
	// returnsNoParamEscape[name] is true for a user function the borrow
	// analysis proved never returns (a value aliasing) any parameter — see
	// findReturnsNoParamEscape. The stage-(b) arg-temp reclaim reads it to
	// safely dec an owned temp passed to a POINTER-returning such callee.
	returnsNoParamEscape map[string]bool
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
	// reuseSources pairs a construction site C (the *ast.StructLit or
	// *ast.TupleLit node) with the name of a dead, owned struct/tuple local D
	// whose box C reuses in place — the general FBIP win (Perceus reuse
	// token threaded D's drop → C's alloc, across DIFFERENT locals, beyond
	// the self-overwrite tryStructReuseOverwrite). reuseConsumed[D] marks
	// such a D so computePreciseDrops doesn't ALSO drop it (the reuse
	// already consumes D's box / dec's it on the shared path). See
	// computeReuseSources.
	reuseSources  map[ast.Expr]string
	reuseConsumed map[string]bool
	// consumingMatchReuse marks a construction C (an arm's variant constructor)
	// that reuses a CONSUMING match's scrutinee box in place (C2 — true
	// zero-alloc FBIP): instead of freeing the consumed `own` box and allocating
	// a fresh one for the arm's `return Ctor(..)`, the box shell is handed
	// straight to C via the reuse token. The scrutinee's old payloads were MOVED
	// into the arm bindings (reclaimed downstream), so unlike a general reuse C
	// must NOT drop the box's old fields — this flag tells emitEnumNew to skip
	// emitReuseOldFieldDrops. Rides on RcReuseEnabled.
	consumingMatchReuse map[*ast.Call]bool
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
		// `own` (consuming) params are OWNED by the callee — they're
		// freeEligible (reclaimed / reused here) rather than borrowed, the
		// reverse of the default. The caller transferred ownership (move-on-call
		// + the E051 guard), so the callee is the sole owner; the rest of the
		// escape analysis still re-taints an own param that escapes (stored /
		// returned-as-alias), so an owned-but-escaping param leaks safely
		// instead of double-freeing. Move-on-consume (passing it onward to
		// another `own` param) skips its drop via computeMovedLocals.
		if !p.Own {
			tainted[p.Name] = true
		}
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
	// std/url's __query_pair).
	//
	// A pointer-shaped value read OUT of a container and retained into
	// a sink — `def_body.push(body[k])`, `m.set(k, row[i])`,
	// `Arr(grid[j])` — copies the pointer without an inc too, so the
	// SOURCE container (`body` / `row` / `grid`) must not free it out
	// from under the sink either. escape unwraps such projection chains
	// (index / field / array-slice) to the root local and taints that.
	// The unwrap is gated on the projected value being pointer-shaped:
	// a scalar element (`i32[]`) can't alias, so its source stays
	// reclaimable. A string slice copies into a fresh owned buffer
	// (not a view), so it isn't unwrapped.
	var escape func(e ast.Expr)
	escape = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.Ident:
			tainted[x.Name] = true
		case *ast.Index:
			if ast.IsPointerType(b.exprType(x)) {
				escape(x.Array)
			}
		case *ast.FieldAccess:
			if ast.IsPointerType(b.exprType(x)) {
				escape(x.Target)
			}
		case *ast.SliceExpr:
			if !x.IsString {
				escape(x.Source)
			}
		}
	}
	// escapeOwned is the variant for the INC-ing sinks (StructLit /
	// TupleLit construction dups every stored pointer value), so only a
	// direct-Ident source can strand an uncounted alias — a projection
	// (`Holder { items: p.items }`) is inc'd into the box, so its
	// container stays reclaimable, and tainting it would needlessly
	// defeat constructor reuse (TestStructReuseFiresForPointerField).
	escapeOwned := func(e ast.Expr) {
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
					// Variant constructor (`Arr(xs)`): under the move model
					// emitEnumNew stores the payload without an inc, so a local
					// passed as a payload escapes into the box (full escape). Under
					// EnumRcPayloads it inc's like StructLit, so only a direct-Ident
					// source can strand an uncounted alias — escapeOwned (a
					// projection is inc'd, so its container stays reclaimable).
					if _, isLocal := b.locals[id.Name]; !isLocal {
						if en, _, _, isVariant := b.lookupVariant(id.Name); isVariant {
							rc := b.enumRcPayloadsEligible(en)
							for _, a := range s.Args {
								if rc {
									escapeOwned(a)
								} else {
									escape(a)
								}
							}
						}
					}
				}
			}
		case *ast.StructLit:
			for _, f := range s.Fields {
				escapeOwned(f.Value)
			}
		case *ast.TupleLit:
			for _, e := range s.Elems {
				escapeOwned(e)
			}
		case *ast.MapLit:
			for _, ent := range s.Entries {
				escape(ent.Key)
				escape(ent.Value)
			}
		case *ast.EnumLit:
			rc := b.enumRcPayloadsEligible(s.EnumName)
			for _, a := range s.Args {
				if rc {
					escapeOwned(a)
				} else {
					escape(a)
				}
			}
		case *ast.CastExpr:
			// Casting a pointer-shaped local to a raw integer (`buf as usize`)
			// hands out an address the rc analysis can't follow: code then
			// reads / writes through that raw pointer (random_bytes fills
			// `buf as usize`; int_to_string indexes an `as usize` scratch), so
			// the source buffer must stay live — freeing it at scope exit would
			// reclaim memory the raw pointer still uses. Taint the cast source
			// (escape unwraps any projection to the root local). This is the
			// load-bearing guard that lets the scalar-arg untaint below stay
			// safe: without it, untainting a literal / scalar-binary size arg
			// would make an `__alloc_u8(...) as usize` buffer eligible and
			// over-release it. Pointer→pointer casts keep rc tracking and are
			// left alone.
			it := s.InnerType
			if it == nil {
				it = b.exprType(s.Inner)
			}
			if ast.IsPointerType(it) && !ast.IsPointerType(s.Target) {
				escape(s.Inner)
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
			// __fern_str_dec. Two-word ABI only (wasm + arm64-
			// TwoWordOverride — both back __fern_str_dec with the
			// uniform L2 rc header); native single-word strings go
			// through their own emitDec branch via __fern_rc_dec which
			// doesn't need this eligibility flag. Aliases / views /
			// literals are tainted above and skipped.
			if ast.UseTwoWordStrings(b.ptrW) {
				elig[v.Name] = true
			}
		}
	}
	// Owned (`own`) params get the SAME borrow-aware eligibility as an owned
	// local — the callee reclaims them — but params aren't in info.Locals, so
	// the loop above never reaches them. Add each untainted own param of a
	// reclaimable type (the un-taint at the top kept them out of `tainted`; an
	// own param that escapes was re-tainted and is skipped here).
	for _, p := range b.fn.Params {
		if !p.Own || tainted[p.Name] {
			continue
		}
		switch t := p.Type.(type) {
		case ast.ArrayType, ast.EnumType, *ast.FuncType, ast.TupleType:
			elig[p.Name] = true
		case ast.StructType:
			if t.Name == "Map" {
				elig[p.Name] = true
			} else if _, isUser := b.info.Structs[t.Name]; isUser {
				elig[p.Name] = true
			}
		case ast.StringType:
			if ast.UseTwoWordStrings(b.ptrW) {
				elig[p.Name] = true
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
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit:
		// A scalar literal aliases nothing, so a fresh owned result whose only
		// "borrowed" input is a literal arg is reclaimable — e.g.
		// int_to_string's `__alloc_u8(16)` buffer, or split's literal-length
		// allocations. Without this the literal fell through to the tainted
		// default and stranded those buffers (unbounded leak in a loop). The
		// raw-pointer liveness such code threads via `buf as usize` is held by
		// the CastExpr escape taint in computeFreeEligible, not here.
		return false
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
		// always reclaimable. Non-concat binaries stay tainted (the original
		// conservative default): untainting a scalar-binary SIZE arg
		// (`__alloc_u8(out_len)`, out_len from `k + 1`) made buffers eligible
		// that the escape/move analysis can't prove safe to reclaim — it
		// over-released int_to_string_radix's result buffer (to_rgb_hex
		// returned the wrong hex). The win is in the NumberLit case above
		// (literal-sized temps); the scalar-binary case is marginal — most
		// such buffers are `as usize`-threaded or moved into the return — and
		// not worth the over-release risk.
		return !x.IsStringConcat
	case *ast.Call:
		// Slice 1b: under EnumRcPayloads a variant constructor is a FRESH
		// rc=1 box that inc's its pointer payloads (like StructLit), so the
		// constructed value is reclaimable regardless of payload taint — return
		// false, mirroring the StructLit/TupleLit cases. Without this the generic
		// any-arg-tainted recursion below propagates a tainted nullary-variant
		// arg (`Nil`) up to the enum local, leaving it permanently ineligible.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isLocal := b.locals[id.Name]; !isLocal {
				if en, _, _, isVar := b.lookupVariant(id.Name); isVar && b.enumRcPayloadsEligible(en) {
					return false
				}
			}
		}
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
			case "random_bytes":
				// random_bytes returns a string the two-word backends
				// (arm64 / wasm) allocate as RAW n bytes with NO rc header
				// (__fern_alloc, not __fern_alloc_rc1), so it is not a
				// reclaimable owned string — str_dec'ing it at scope exit
				// reads garbage for the rc and corrupts / over-releases the
				// buffer (TestArm64RandomBytes: 83 bytes, want 16). It must
				// stay ineligible. This was protected only by accident before
				// the scalar-arg untaint below — the literal size arg used to
				// taint the result; now it's marked explicitly. (The runtime
				// fills the buffer through a raw pointer too, which the
				// CastExpr escape taint can't see — it lives in asm, not Fern
				// source.)
				return true
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
	case *ast.MatchExpr:
		// A match-expression is owned iff every arm body is — the exact
		// mirror of IfExpr. Without this case it fell through to the tainted
		// default, leaving `var s = match (k) { 0 => a + b, _ => b + a }` (all
		// arms fresh concats) permanently ineligible and unreclaimed (leaked
		// 240000 → 2400000 in a loop). A bare-local arm is still caught: the
		// escape(arm.Body) in computeFreeEligible taints that local, so
		// rhsTainted reads it back as tainted here and the match stays
		// protected — same belt-and-suspenders as IfExpr.
		for _, arm := range x.Arms {
			if b.rhsTainted(arm.Body, tainted) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// freshOwnedRcTempType classifies an expression that, when evaluated,
// materialises a FRESH owned rc-tracked temporary — a brand-new box / heap
// string that aliases no borrowed value. It returns the value's static type
// and true for exactly the unambiguous fresh-ALLOCATING shapes that
// rhsTainted / computeFreeEligible treat as untainted-owned: array / struct
// / tuple literals, string concat (Binary.IsStringConcat), and string slice
// (SliceExpr whose result is a string — the runtime copies into a new owned
// buffer). These are exactly the RHS shapes that make a bound `var t = …`
// freeEligible, so DEC'ing such a temp is as safe as the already-shipped
// exit-sweep dec of that bound var.
//
// Two consuming sites use it to reclaim the unbounded statement-temporary
// leak (docs/RC-PERCEUS-PLAN.md):
//   - stage (a): a discarded bare-ExprStmt `a + b;` / `[x, y];` decs in place
//     (emitOwnedTempStackDrop) instead of OpDrop'ing the allocation.
//   - stage (b): an owned-temp passed as a borrowed arg to a non-retain-sink
//     call (`foo(a + b)`) is stashed and dec'd after the call.
//
// Ident / field / index reads are borrowed VIEWS (the owner upstream or the
// exit sweep accounts for them) and are never matched — dec'ing one would
// over-release. Calls are excluded — a method call can alias its receiver
// (`arr.push(x)` returns the receiver buffer), so dec'ing its result would
// double-free. MakeClosure is excluded for now (a bare closure temp is
// effectively nonexistent, and the per-closure capture-drop thunk is keyed
// by local name) — those keep their prior plain handling.
func (b *builder) freshOwnedRcTempType(e ast.Expr) (ast.Type, bool) {
	if !ast.RcFreeEnabled {
		return nil, false
	}
	switch x := e.(type) {
	case *ast.ArrayLit, *ast.StructLit, *ast.TupleLit:
		if t := b.exprType(e); ast.IsPointerType(t) {
			return t, true
		}
	case *ast.Binary:
		if x.IsStringConcat {
			if t, ok := b.exprType(e).(ast.StringType); ok {
				return t, true
			}
		}
	case *ast.SliceExpr:
		if t, ok := b.exprType(e).(ast.StringType); ok {
			return t, true
		}
	}
	return nil, false
}

// ownedCallResultType classifies an expression that is a direct call to a
// USER function returning a pointer-shaped (rc-tracked) value — a fresh
// struct / array / string / enum the callee owns. Two consuming sites reclaim
// it (otherwise it's dropped on the floor and leaks every iteration):
//   - a discarded ExprStmt `mk(i);` (leaked 800 → 80000 in a loop) — dec'd
//     in place via the is_unique-gated emitOwnedTempStackDrop;
//   - a call ARG `take(mk(i))` / `outer(inner(i))` (leaked 800 → 80000 /
//     1600 → 160000) — stashed and dec'd after the enclosing scalar-
//     returning call via emitOwnedSlotDrop, alongside the literal-shape
//     temps freshOwnedRcTempType already handles.
//
// It returns the result's static type + true.
//
// Safety rests on the is_unique gate inside every emitOwnedTempStackDrop
// branch: the dec only FREES a uniquely-owned (rc==1) result; an aliased
// return (a function handing back a param / field) carries the return-
// transfer inc, so its rc is >= 2 and the gate merely decs it — never frees
// a value the caller's source still owns. This is exactly the shipped
// `var t = call(); /* t unused */` exit-sweep dec (computeFreeEligible marks
// such a t eligible), so it inherits that proven safety.
//
// Excluded — the callees that hand back an UNCOUNTED rc==1 alias the
// is_unique gate cannot distinguish from a fresh value:
//   - `__`-prefixed builtins / method lowerings: `arr.push(x)` /
//     `m.set(k, v)` return the receiver's buffer in place at rc==1 (no
//     inc), so dec'ing would free a live container.
//   - variant constructors (`Some(p)`): not in FuncSigs, and they store a
//     borrowed payload uncounted.
//   - pair-form callees: return a (tag, payload) pair, a different stack
//     shape.
//   - indirect (function-typed local) callees: unknown body / borrow shape.
func (b *builder) ownedCallResultType(e ast.Expr) (ast.Type, bool) {
	if !ast.RcFreeEnabled {
		return nil, false
	}
	call, ok := e.(*ast.Call)
	if !ok {
		return nil, false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil, false
	}
	if _, isLocal := b.locals[id.Name]; isLocal {
		return nil, false
	}
	if _, ok := b.info.FuncSigs[id.Name]; !ok {
		return nil, false // not a known function (excludes variant constructors)
	}
	if strings.HasPrefix(id.Name, "__") {
		// `__`-prefixed callees are method lowerings. The builtin ones
		// (`arr.push` / `m.set` / string / Reader / …) return the receiver's
		// buffer in place at rc==1 (an uncounted alias), so reclaiming would free
		// a live container. A USER method PROVEN to return a fresh value
		// (returnsNoParamEscape — e.g. a recursive `map`/`dup` over an enum) is
		// as safe to reclaim as a fresh free-function result; without this, a
		// method-call result used as an arg (`sum(xs.dup())`) leaks every call.
		if !b.returnsNoParamEscape[id.Name] {
			return nil, false
		}
	}
	if b.pairForm[id.Name] {
		return nil, false
	}
	t := b.exprType(e)
	if t == nil || !ast.IsPointerType(t) {
		return nil, false
	}
	return t, true
}

// reclaimableMatchScrutinee reports whether a match's scrutinee `tag` is a
// FRESH owned enum box that can be freed once the match completes, and returns
// its enum type. A match consumes its scrutinee — the arms read payload fields
// out of the box and then it's dead — but the box is never dec'd, so a
// `match (mk(i)) { … }` over a per-iteration-fresh `mk(i)` leaks one box every
// iteration (the value-consuming-position sibling of the shipped index-of-fresh
// / `.len()`-of-fresh reclamation; docs/RC-PERCEUS-PLAN.md).
//
// Eligibility mirrors that family's gate exactly:
//   - the scrutinee is a fresh owned call result (ownedCallResultType — a user
//     function returning a heap-boxed enum; pair-form / builtin / variant-
//     constructor callees are excluded there, so the value is always a real
//     box this lowering stores in ptrSlot and dispatches on via OpMatchTag);
//   - every arm BINDING is non-pointer, so no pointer payload is extracted into
//     a binding that would outlive (and alias) the freed box;
//   - for the expression form, the RESULT is non-pointer too (`resultType`;
//     pass nil for the statement form, which yields no value).
//
// emitEnumSlotDrop then frees the box under an is_unique gate, so an aliased
// scrutinee (rc>1 via a return-transfer inc) is only dec'd, never freed.
func (b *builder) reclaimableMatchScrutinee(tag ast.Expr, bindingTypes [][]ast.Type, resultType ast.Type) (ast.EnumType, bool) {
	if !ast.RcFreeEnabled {
		return ast.EnumType{}, false
	}
	t, ok := b.ownedCallResultType(tag)
	if !ok {
		return ast.EnumType{}, false
	}
	et, ok := t.(ast.EnumType)
	if !ok {
		return ast.EnumType{}, false
	}
	if resultType != nil && ast.IsPointerType(resultType) {
		return ast.EnumType{}, false
	}
	for _, bts := range bindingTypes {
		for _, bt := range bts {
			if bt != nil && ast.IsPointerType(bt) {
				return ast.EnumType{}, false
			}
		}
	}
	return et, true
}

// emitOwnedTempStackDrop releases a FRESH owned rc temporary whose value is
// currently on top of the operand stack — the stage-(a) replacement for the
// plain OpDrop a discarded ExprStmt would otherwise emit (see
// freshOwnedRcTempType). The value aliases nothing borrowed and escapes
// nowhere, so a single dec is exactly balanced (rc==1 → free). It mirrors
// the per-type drop bodies the exit sweep / emitVarReinitDropOld use, but
// consumes the value in place rather than from a named slot — so the only
// shape needing a scratch slot is the plain-element tuple, whose inline
// is_unique + box_free reads the box pointer twice.
func (b *builder) emitOwnedTempStackDrop(t ast.Type) {
	switch ty := t.(type) {
	case ast.StringType:
		// Mirrors the exit-sweep / reinit string branch exactly (slice 5g is
		// done): two-word ABIs (wasm + arm64) free via __fern_str_dec, which
		// consumes the (data, len) pair on the stack and returns the data ptr
		// (dropped after); native single-word x86_64 via __fern_rc_dec. The
		// guards (inline-SSO / literal sentinel / rc>1) keep every source safe,
		// and this drop only fires for a provably-fresh owned temp.
		if ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else if b.ptrW == 8 {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.ArrayType:
		// An array-of-(struct / tuple / primitive-array) routes to the deep
		// per-element drop fn (1 arg); a plain / pointer-element array frees
		// its buffer via __fern_arr_dec(ptr, elemSize). Same dispatch the
		// exit sweep / reinit use (arrElemStructDropName), so element
		// reclamation matches the bound-var case.
		if dropName, ok := arrElemStructDropName(ty.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
			b.emit(Op{Kind: OpCallDirect, Str: dropName, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(ty.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.StructType, ast.EnumType:
		// A droppable struct / enum recurses through its generated __drop_*
		// fn (1 arg); types dropFnNameFor declines (Map handles can't be a
		// literal temp, non-uniform generics) fall back to the flat one-level
		// rc_dec — leak-but-never-UAF, exactly as the slot-drop sibling.
		if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.TupleType:
		// A needs-drop tuple has a generated __drop_tuple_<mangled> fn (1
		// arg). A plain-element tuple's inline is_unique + box_free reads the
		// box pointer twice, so stash it in a scratch slot and route through
		// the shared emitTupleSlotDrop (single-word box pointer → a normal
		// i32 scratch slot is exact).
		if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			slot := b.allocSlot()
			b.locals[fmt.Sprintf("__tmpdrop_%d", slot)] = slot
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
			b.emitTupleSlotDrop(slot, ty)
		}
	default:
		// Not an rc-tracked shape we reclaim here — keep the plain drop.
		b.emit(Op{Kind: OpDrop})
	}
}

func (b *builder) emitRcDecLocalsAtExit() {
	b.emitRcDecLocalsAtExitExcept("")
}

// computePreciseDrops implements the Perceus "garbage-free" precise-drop
// placement for the STRAIGHT-LINE subset: an owned, free-eligible rc local
// (array / struct / Map / enum / tuple) whose every reference is a top-level
// statement (none inside a nested if / while / for / match block) and that
// isn't moved or reassigned is dropped right AFTER its last top-level use,
// instead of waiting for the function-exit sweep. Freeing the value at its
// last use rather than at scope end lowers peak memory — a later same-shaped
// allocation reuses the freed block instead of bumping a new one (measured:
// two sequentially-dead 2 KiB arrays go 4128 → ~2064 B high-water on wasm,
// four go 8256 → ~2064).
//
// Soundness: the drop is the SAME deep-drop the exit sweep emits, followed by
// ZEROING the slot. Zeroing makes it control-flow-robust and fail-loud:
//   - the function-exit sweep (and any earlier `return`'s sweep) loads the
//     zeroed slot and null-guards to a no-op, so there's no double-drop on
//     any path — a `return` BEFORE the precise point still drops the live
//     value via that sweep, a `return` after sees the zeroed slot.
//   - correctness never depends on the drop being the TRUE last use: it's a
//     dec, freeing only at rc 0, so a value aliased (inc'd) into a container
//     survives; and a mis-analysis surfaces as a null-slot read (trap / wrong
//     value caught by the differential corpus), not a silent UAF.
//
// Conservative gates (single declaration, no reassignment, no nested-block
// use) keep slice 1 obviously sound; control-flow-aware placement inside
// branches is a later slice. Returns stmtIndex → names to drop after lowering
// that top-level statement.
func (b *builder) computePreciseDrops() map[int][]string {
	if !ast.RcFreeEnabled || b.fn.Body == nil {
		return nil
	}
	stmts := b.fn.Body.Stmts
	declIdx := map[string]int{}
	reassigned := map[string]bool{}
	for i, st := range stmts {
		switch s := st.(type) {
		case *ast.Var:
			if _, dup := declIdx[s.Name]; dup {
				reassigned[s.Name] = true // shadowed redeclaration — bail
			} else {
				declIdx[s.Name] = i
			}
		case *ast.ExprStmt:
			if a, ok := s.Expr.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					reassigned[id.Name] = true
				}
			}
		}
	}
	// A precise drop now allows the last use to sit inside a nested block
	// (control-flow-aware placement — slice 5), so reassignment must be
	// detected at ANY depth: a `name = ...` inside an `if`/`while` rebinds the
	// slot, and precise-dropping the post-loop value at its last use is only
	// sound if the slot wasn't re-overwritten on some path in a way the
	// straight-line `last` index can't see. Conservatively bail on any
	// assignment to the local anywhere in the body.
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})
	references := func(st ast.Node, name string) bool {
		found := false
		ast.Walk(st, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		return found
	}
	out := map[int][]string{}
	for name, di := range declIdx {
		if reassigned[name] || b.movedLocals[name] || !b.freeEligible[name] || !b.localNameUnique(name) {
			continue
		}
		// A local whose box is handed off to a general-FBIP reuse site
		// (computeReuseSources) is already consumed there (its box taken, or
		// dec'd on the shared path, and its slot zeroed) — dropping it again
		// here would double-release. The reuse site subsumes its drop.
		if b.reuseConsumed[name] {
			continue
		}
		if !b.preciseDroppableType(name) {
			continue
		}
		// The local's INIT may itself produce an uncounted alias of a still-
		// live value — a slice (view), a pointer-typed if/match expr, or a
		// call whose result could BE a pointer argument (`var v3 = id(v2)`).
		// Precise-dropping such a local would free a buffer the source still
		// holds. A scalar-arg call (`fill(100)`) returns a fresh value and
		// stays eligible (the common builder-call win). The OTHER end — the
		// source flowing INTO the call — is handled by flowsIntoUncountedAlias
		// below.
		if v, ok := stmts[di].(*ast.Var); ok {
			if b.initMayAliasLive(v.Init) {
				continue
			}
			// Slice 1 targets dead OWNED values (fresh literals / scalar-arg
			// builder calls) — the clear peak-memory win. A local whose init
			// is a counted ALIAS (`var y = x` / `x.field` / `x[i]` —
			// needsRcIncOnAlias) is excluded: precise-dropping it only cancels
			// the alias inc (sound, but a marginal win that needlessly churns
			// the rc-count golden tests). Dead-alias cancellation is a later
			// slice.
			if needsRcIncOnAlias(v.Init, b) {
				continue
			}
		}
		unsafe := false
		last := -1
		for i := di + 1; i < len(stmts); i++ {
			if !references(stmts[i], name) {
				continue
			}
			// Control-flow-aware placement (slice 5): the last use may now sit
			// INSIDE a nested if / while / for / match. We still drop the local
			// right after the whole top-level statement that contains its last
			// use — by then the local is dead on EVERY path through that
			// statement, so a single top-level drop + zero-slot is sound, and
			// any early `return` on a path keeps the value live to its own exit
			// sweep (the zeroed slot makes the post-statement drop a no-op on
			// the paths that already returned). Slightly less precise than a
			// per-branch drop, but it reclaims before the (often long) tail
			// after an `if`, which is where the win is.
			//
			// A reference inside a pointer-producing call / slice / if-expr /
			// match-expr can create an UNCOUNTED alias of `name` that outlives
			// the drop point (e.g. `var v3 = id(v2)` — a generic identity
			// returns its borrowed arg with no inc). The inc'd-alias sites
			// (`var y = x` / `x.field` / `x[i]`, container literals) are SAFE —
			// the precise drop only decs there. Bail on the uncounted-alias
			// shapes (flowsIntoUncountedAlias walks the whole nested statement).
			if b.flowsIntoUncountedAlias(stmts[i], name) {
				unsafe = true
				break
			}
			last = i
		}
		if unsafe {
			continue
		}
		if last < 0 {
			// Declared but never used after — drop right after the decl
			// (a dead owned alloc reclaims immediately).
			last = di
		}
		// Control-flow placement guard: when the last use sits INSIDE a
		// nested control-flow statement (if / while / for / match / block),
		// the precise drop fires after that whole top-level statement —
		// the slice-5 extension over the straight-line slices 1-3, which
		// only placed drops after a simple top-level use. That extension is
		// only enabled for PRIMITIVE-element arrays (i32[] / f64[] / …): a
		// dead `int[]` freed early is the clean peak-memory win (the
		// headline two-KiB-array case) with no per-element rc to balance.
		//
		// A pointer-element array (string[] / struct[] / T[][] / tuple[])
		// is EXCLUDED from this nested placement: its deep drop dec's each
		// element, and an element aliased out across the drop point (e.g.
		// the self-host driver's `entry_path = av[1]` / `root = av[2]` from
		// `var av: string[] = args()`, last-used at `av[2]` inside an `if`)
		// relies on the per-element retain/release balancing exactly on
		// EVERY backend. On arm64 two-word heap strings that balance rides
		// the native heap-string reclamation path the plan still defers
		// (slice 5g, "arm64 native heap-string rc — verify on hardware"),
		// so an early drop there corrupts under allocation-reuse pressure
		// (the args buffer reclaimed and reused while a still-live element
		// alias points into it). Falling back to the exit sweep for these
		// keeps the nested-use win for primitive arrays without crossing
		// that unverified arm64 boundary. Straight-line (simple top-level
		// last use) placement keeps the full slice 1-3 element scope.
		if b.isControlFlowStmt(stmts[last]) && !b.safeForControlFlowDrop(name) {
			continue
		}
		// A `return` whose value is this local is handled by the Return
		// lowering's own move-on-return / sweep; dropping after it is dead
		// code. Skip — the value reclaims at the return instead.
		if _, isRet := stmts[last].(*ast.Return); isRet {
			continue
		}
		out[last] = append(out[last], name)
	}
	return out
}

// isControlFlowStmt reports whether `st` is a control-flow statement whose
// body holds a nested block — an if / while / for / match / bare block. A
// reference to a local inside one of these is a "nested" use, so a precise
// drop placed after the whole statement is the slice-5 control-flow
// extension (vs a simple top-level Var / ExprStmt / Return use).
func (b *builder) isControlFlowStmt(st ast.Node) bool {
	switch st.(type) {
	case *ast.If, *ast.While, *ast.For, *ast.Match, *ast.LetElse, *ast.Block:
		return true
	}
	return false
}

// safeForControlFlowDrop reports whether `name`'s declared type may take the
// slice-5 control-flow precise-drop placement (a drop after the whole top-level
// if/while/for/match that holds its last use, vs the function-exit sweep).
// Allowed for:
//   - PRIMITIVE-element arrays (i32[] / f64[] / …) — the original slice-5 scope;
//     no per-element rc to balance across the early drop.
//   - enum / struct / tuple values whose deep-drop touches NO string or array
//     buffer (typeIsStringArrayFree) — the FBIP list/tree-of-scalars case. Their
//     generated `__drop_*` helper is is_unique-gated and verified on every
//     backend, and being string/array-free keeps the deferred arm64 two-word
//     heap-string reclamation path (slice 5g) out of the early-drop window —
//     exactly the hazard that excludes pointer-element arrays and strings.
//
// Everything else (pointer-element arrays, strings, anything transitively
// containing them, Map, generics) falls back to the exit sweep. Unknown ⇒ false.
func (b *builder) safeForControlFlowDrop(name string) bool {
	t, ok := b.localDeclType(name)
	if !ok {
		return false
	}
	switch ty := t.(type) {
	case ast.ArrayType:
		return !ast.IsPointerType(ty.Elem)
	case ast.EnumType, ast.StructType, ast.TupleType:
		return b.typeIsStringArrayFree(t, map[string]bool{})
	}
	return false
}

// typeIsStringArrayFree reports whether `t`'s deep-drop reclaims no string or
// array buffer — i.e. t is built transitively from scalars, enums, structs, and
// tuples only (no string / array / slice / Map, no unresolved generic). `seen`
// breaks recursive-type cycles (a self-recursive enum like List is fine: the
// back-edge is assumed free, and any string/array on a real payload is caught on
// its own first visit before the back-edge is taken).
func (b *builder) typeIsStringArrayFree(t ast.Type, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true
	case ast.StringType, ast.ArrayType, ast.SliceType:
		return false
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !b.typeIsStringArrayFree(e, seen) {
				return false
			}
		}
		return true
	case ast.StructType:
		if ty.Name == "Map" {
			return false
		}
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		sd, ok := b.info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if !b.typeIsStringArrayFree(f.Type, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		ed, ok := b.info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !b.typeIsStringArrayFree(pl, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// flowsIntoUncountedAlias reports whether `name` appears inside an
// expression that produces an UNCOUNTED pointer alias of it within `st`: a
// pointer-returning call (the result may BE the arg, e.g. `id(x)` / a
// borrowed-param-returning function, with no inc at the binding), a slice
// (always a view into its source), or a pointer-typed if/match expression
// (whose value position aliases a branch operand without an inc). References
// via needsRcIncOnAlias shapes (bare ident / field / index) and container
// literals are NOT flagged — those inc the value, so a precise drop only
// decs and the alias survives. Used to gate precise-drop placement.
func (b *builder) flowsIntoUncountedAlias(st ast.Node, name string) bool {
	hasName := func(n ast.Node) bool {
		found := false
		ast.Walk(n, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		return found
	}
	bad := false
	ast.Walk(st, func(n ast.Node) bool {
		if bad {
			return false
		}
		switch e := n.(type) {
		case *ast.SliceExpr:
			if hasName(e) {
				bad = true
			}
		case *ast.Call:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		case *ast.IfExpr:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		case *ast.MatchExpr:
			if b.mayAliasResult(e) && hasName(e) {
				bad = true
			}
		}
		return !bad
	})
	return bad
}

// mayAliasResult reports whether expression `e`'s result may be a heap pointer
// that aliases one of its operands — conservatively treating an UNRESOLVED
// generic result (a `ParamType`, e.g. `id[T]`'s `T`) or an unknown type as
// pointer-shaped, since `b.exprType` doesn't instantiate generic call results.
// Concrete scalar results (i32 / bool / float) are not aliasing.
func (b *builder) mayAliasResult(e ast.Expr) bool {
	t := b.exprType(e)
	if t == nil {
		return true
	}
	if _, isParam := t.(ast.ParamType); isParam {
		return true
	}
	return ast.IsPointerType(t)
}

// initMayAliasLive reports whether a Var initialiser may bind an UNCOUNTED
// pointer alias of a value that outlives it: a slice (a view into its
// source), a pointer-typed if / match expression (aliases a branch operand),
// or a pointer-returning call with at least one pointer-typed argument /
// receiver (the result may be that argument — `id(v2)` returns its arg). A
// call with only scalar args (`fill(100)` / `make(n)`) returns a fresh value
// that can't alias a live pointer local, so it stays precise-droppable.
func (b *builder) initMayAliasLive(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SliceExpr:
		return true
	case *ast.IfExpr:
		return ast.IsPointerType(b.exprType(x))
	case *ast.MatchExpr:
		return ast.IsPointerType(b.exprType(x))
	case *ast.Call:
		// The local is droppable (pointer-shaped), so the call's result is
		// pointer regardless of what b.exprType reports for a generic. The
		// alias risk is a pointer-shaped ARGUMENT / receiver the result could
		// be (`id(v2)` returns its arg); a scalar-only-arg call (`fill(100)`)
		// returns a fresh value.
		for _, a := range x.Args {
			if b.mayAliasResult(a) {
				return true
			}
		}
		if fa, ok := x.Callee.(*ast.FieldAccess); ok && b.mayAliasResult(fa.Target) {
			return true
		}
		return false
	}
	return false
}

// preciseDroppableType reports whether `name`'s declared type is in the
// precise-drop scope: any owned ARRAY. emitOwnedSlotDrop reclaims every
// element kind fully — primitive via `__fern_arr_dec` (pure buffer free,
// slice 1); rc-tracked (`struct[]` / `enum[]` / `T[][]` / `tuple[]`) via the
// deep `__drop_arr_*` loop (slice 2); and `string[]` via `__fern_drop_arr_str`
// / `__fern_drop_arr_ptr` (slice 3 — str_dec each element, then the buffer).
// Each per-element drop is_unique-gates, so a counted alias of an element only
// DECs. Non-array box types (structs / enums / tuples — small boxes whose deep
// drops dec shared fields and churn the `__rc_get` golden tests) are deferred.
func (b *builder) preciseDroppableType(name string) bool {
	t, ok := b.localDeclType(name)
	if !ok {
		return false
	}
	if et, isEnum := t.(ast.EnumType); isEnum {
		// ENUMs are precise-droppable only under Slice 1b (rc-eligible enums):
		// once enum construction rc-counts its pointer payloads (like StructLit)
		// the deep drop is rc-protected exactly like a struct, and the
		// escape-taint that kept enum locals ineligible is lifted in tandem.
		// Under the default move model (or for Map-containing enums) they stay
		// excluded (payloads carry no counted box reference).
		return b.enumRcPayloadsEligible(et.Name)
	}
	switch t.(type) {
	case ast.ArrayType, ast.StructType, ast.TupleType:
		// Arrays (every element kind — slices 1–3) and STRUCT / Map / tuple
		// boxes (slice 4). emitOwnedSlotDrop reclaims each fully and
		// is_unique-gates; freeEligible (the taint set) excludes any value
		// whose nested fields/payloads alias a live local; and the init/use
		// alias gates exclude boxes bound from / flowing into an uncounted
		// alias. Struct & tuple construction INC their pointer fields/elements
		// (StructLit / TupleLit), so a precise drop is rc-protected — the same
		// reason slice-2 rc-element arrays are sound. Non-droppable runtime handles (Reader /
		// Writer / MapIter) aren't freeEligible, so they never reach here.
		return true
	}
	return false
}

// emitPreciseDrop deep-drops the owned local `name` at its last use and
// zeroes the slot (see computePreciseDrops). Net-zero on the operand stack.
func (b *builder) emitPreciseDrop(name string) {
	slot, ok := b.locals[name]
	if !ok {
		return
	}
	t, ok := b.localDeclType(name)
	if !ok {
		return
	}
	b.emitOwnedSlotDrop(slot, t)
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: slot})
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
			// Two-word string ABIs (wasm + arm64-TwoWordOverride) and
			// native single-word (x86_64) all participate in move-on-
			// return / move-on-alias now that the rc-tracked predicate is
			// uniform for strings: a returned string local cancels its
			// transfer-inc against the exit-sweep dec (no free under the
			// caller). The arm64 unblock landed __fern_str_inc / dec, so
			// the boxed-string case applies too.
			return true
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
			var val ast.Expr
			switch s := st.(type) {
			case *ast.Var:
				rhs, _ = s.Init.(*ast.Ident)
				site = s
				val = s.Init
			case *ast.ExprStmt:
				if a, ok := s.Expr.(*ast.Assign); ok {
					if _, tok := a.Target.(*ast.Ident); tok {
						rhs, _ = a.Value.(*ast.Ident)
						site = a
						val = a.Value
					}
				}
			case *ast.Return:
				val = s.Value
			case *ast.Destructure:
				// `var (a, b) = t` aliases the source tuple into the
				// destructure temp (inc at the alias site below). When t
				// is an owned rc local at its last use, that inc and t's
				// exit-sweep dec cancel — move t into the temp, which
				// frees the box once. Keyed on the Destructure node (the
				// lowering checks b.moveSites[n] there).
				rhs, _ = s.Init.(*ast.Ident)
				site = s
			}
			if rhs != nil && b.isOwnedRcLocal(rhs.Name) && identIdx[rhs] == maxIdx[rhs.Name] {
				moved[rhs.Name] = true
				b.moveSites[site] = true
			}
			// Move-on-construction: a struct literal built at this
			// top-level statement that consumes an owned rc local at the
			// local's last use moves it into the field (see
			// markConstructionMoves).
			if val != nil {
				b.markConstructionMoves(val, identIdx, maxIdx, moved)
			}
		}
		if stmtContainsReturn(st) {
			sawReturn = true
		}
	}

	// Move-on-call: an `own` argument (one of THIS function's owned params)
	// passed at its last use to an `own` PARAMETER of a callee is consumed —
	// the callee now owns and drops it — so skip the caller's drop. There's no
	// inc to elide (a call arg is passed without one), so only the exit-sweep /
	// precise drop is suppressed via `moved`. Gated on the E051 guard, which is
	// what guarantees an `own`-position arg is an owned, transferable value.
	if len(b.info.OwnFuncs) > 0 {
		ownParam := map[string]bool{}
		for _, p := range b.fn.Params {
			if p.Own {
				ownParam[p.Name] = true
			}
		}
		if len(ownParam) > 0 {
			// A consuming match (`match (own_param) { … }`) consumes the
			// scrutinee — its box is shallow-freed at the match — so the exit
			// sweep must not ALSO deep-drop it. Mark the own-param scrutinee
			// moved (its last use is the match).
			markScrutinee := func(tag ast.Expr) {
				if id, ok := tag.(*ast.Ident); ok && ownParam[id.Name] &&
					identIdx[id] == maxIdx[id.Name] {
					moved[id.Name] = true
				}
			}
			ast.Walk(b.fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Match:
					markScrutinee(x.Tag)
				case *ast.MatchExpr:
					markScrutinee(x.Tag)
				case *ast.Call:
					id, ok := x.Callee.(*ast.Ident)
					if !ok {
						return true
					}
					flags, isOwn := b.info.OwnFuncs[id.Name]
					if !isOwn {
						return true
					}
					for i := 0; i < len(x.Args) && i < len(flags); i++ {
						if !flags[i] {
							continue
						}
						if arg, ok := x.Args[i].(*ast.Ident); ok &&
							ownParam[arg.Name] && identIdx[arg] == maxIdx[arg.Name] {
							moved[arg.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	return moved
}

// markConstructionMoves implements the move-on-construction slice of
// Phase 4 pair-cancellation: when a struct literal built at a
// dominating top-level statement consumes an OWNED rc local in a
// non-string rc-tracked field at the local's LAST use
// (`var s = Wrap{ inner: x }`, `x` dead after), the field-init inc and
// x's exit-sweep dec cancel — x's single reference is moved into the
// struct's field. Skipping the inc (gated on b.moveSites[fieldIdent] at
// the StructLit lowering) and x's dec (moved[x] excludes it from the
// exit sweep) leaves the struct owning x; the struct's own field-drop
// (emitDec) releases it exactly once, so the net rc is unchanged.
//
// The eligibility mirrors the inc/drop sides exactly: the field must be
// `arrElemIsRcTracked` (array / struct / enum / closure / tuple — the
// fields the StructLit inc's AND emitDec dec's; strings are excluded,
// their two-word retain/release diverges per backend), and the value
// must be an owned rc local (isOwnedRcLocal) whose occurrence here is
// its max pre-order index. The caller has already established the
// dominance guards (top-level statement, no preceding return), so —
// exactly as move-on-alias — x is moved on every path to an exit.
func (b *builder) markConstructionMoves(val ast.Expr, identIdx map[*ast.Ident]int, maxIdx map[string]int, moved map[string]bool) {
	// mark moves the Ident when it's an owned rc local at its last use.
	// The caller has established the dominance guards; the per-container
	// drop (struct field-drop / array drop_arr_ptr) releases the moved
	// value exactly once, balancing the skipped construction inc.
	mark := func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok || !b.isOwnedRcLocal(id.Name) || identIdx[id] != maxIdx[id.Name] {
			return
		}
		moved[id.Name] = true
		b.moveSites[id] = true
	}
	switch lit := val.(type) {
	case *ast.StructLit:
		sd, ok := b.info.Structs[lit.TypeName]
		if !ok {
			return
		}
		for _, f := range lit.Fields {
			// Only fields the StructLit inc's AND emitDec dec's on drop
			// (arrElemIsRcTracked; strings excluded).
			if arrElemIsRcTracked(fieldType(sd.Fields, f.Name)) {
				mark(f.Value)
			}
		}
	case *ast.ArrayLit:
		// An array of rc-tracked elements: each element is inc'd on
		// construction and dec'd by __fern_drop_arr_ptr at the array's
		// drop, so a moved element balances. Plain-scalar arrays never
		// reach the element inc — mark is a no-op there (isOwnedRcLocal
		// is false for scalars).
		for _, el := range lit.Elems {
			mark(el)
		}
	case *ast.TupleLit:
		// A tuple with rc-tracked elements: each is inc'd on
		// construction and dec'd by __drop_tuple_<...> at the tuple's
		// drop (tupleNeedsDrop / dropFnNameFor), so a moved element
		// balances — same shape as the struct/array cases. Only mark
		// owned rc locals; mark self-filters non-pointer elements via
		// isOwnedRcLocal.
		for _, el := range lit.Elems {
			mark(el)
		}
	case *ast.MakeClosure:
		// A closure capturing rc-tracked locals: each is inc'd at
		// MakeEnv (Phase 1d-vii) and dec'd by the closure's drop
		// (__closure_drop_<name> / __fern_closure_drop at its last
		// reference), so a moved capture balances — same shape as the
		// other containers. Eligibility matches hasRcCapture
		// (arrElemIsRcTracked; strings are reclaimed by the thunk too
		// but excluded here for the same single-word-temp reason as the
		// struct/array cases). mark self-filters via isOwnedRcLocal.
		// Eliding an inc only REMOVES ops, which the Defunctionalise /
		// ElideClosurePair passes tolerate (they already treat the inc
		// as a value-preserving pass-through when chasing alias chains);
		// the defunc/elide unit tests + self-host VM gate this.
		for _, cap := range lit.Captures {
			if arrElemIsRcTracked(b.exprType(cap)) {
				mark(cap)
			}
		}
	case *ast.Call:
		// Slice 1b: an enum variant constructor — emitEnumNew now inc's an
		// aliased pointer payload and the enum's deep drop dec's it, so a moved
		// last-use OWNED-LOCAL payload balances (mark self-filters via
		// isOwnedRcLocal — own params aren't locals, so they're inc'd and
		// balanced by the exit-sweep dec, exactly like a struct field). Only
		// variant-constructor calls.
		if id, ok := lit.Callee.(*ast.Ident); ok {
			if en, _, _, isVar := b.lookupVariant(id.Name); isVar && b.enumRcPayloadsEligible(en) {
				for _, a := range lit.Args {
					mark(a)
				}
			}
		}
	}
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
	// Local aliases so existing call sites stay unchanged; the bodies were
	// promoted to *builder methods (decValueOnStack / dropStructField) so the
	// shared emitEnumSlotDrop can reuse them.
	decValueOnStack := b.decValueOnStack
	dropStructField := b.dropStructField
	_ = decValueOnStack
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
		// string[] on any two-word ABI (wasm + arm64-TwoWordOverride):
		// reclaim each element via the two-word walk in
		// __fern_drop_arr_str, then free the buffer. Gated eligible —
		// a borrowed string[] never frees its elements.
		if at, ok := t.(ast.ArrayType); ok && ast.RcFreeEnabled && eligible {
			if _, isStr := at.Elem.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_str", I32: 2})
				b.emit(Op{Kind: OpDrop})
				return
			}
			// Native single-word string[] (x86_64, !TwoWordOverride): each
			// element is a single pointer; __fern_drop_arr_ptr walks +
			// __fern_rc_dec's each one. arm64 / wasm two-word ABIs take
			// the __fern_drop_arr_str branch above.
			if _, isStr := at.Elem.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_ptr", I32: 2})
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
			// Two-word string ABIs (wasm + arm64-TwoWordOverride): __fern_str_dec
			// consumes (data, len), returns data; drop the returned ptr.
			// Native single-word (x86_64, !TwoWordOverride): __fern_rc_dec
			// consumes ptr, returns ptr (SSO inline-tag low-bit guard +
			// literal sentinel + low-address guard all safe).
			if ast.RcFreeEnabled && eligible && ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot}) // pushes (data, len)
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop}) // drop the returned data ptr
			} else if ast.RcFreeEnabled && eligible && b.ptrW == 8 {
				b.emit(Op{Kind: OpLoadLocal, I32: slot}) // pushes single data ptr
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop}) // drop the returned ptr
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
				if _, isStr := et.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
					// Two-word string element (wasm + arm64-TwoWordOverride):
					// load (data, len) and reclaim via __fern_str_dec. Unique
					// here (rc==1 guard), so the element is uniquely owned;
					// inline / literal strings no-op. Balances the projection
					// dup (__fern_str_inc) and the construction retain.
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
				// Native single-word string tuple element (x86_64,
				// !TwoWordOverride): load WidthPtr + __fern_rc_dec. SSO
				// inline-tag guard + literal sentinel keep all sources
				// safe. arm64 / wasm two-word ABIs take the WidthString
				// + __fern_str_dec branch above.
				if _, isStr := et.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if offs[i] != 0 {
						b.emit(Op{Kind: OpConstI32, I32: offs[i]})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthPtr})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
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
			b.emitMapSlotDrop(slot, st)
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
					if _, isStr := f.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
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
					// Native single-word string field (x86_64, !TwoWordOverride):
					// the field is a single pointer at the field offset; load
					// it as WidthPtr and __fern_rc_dec (SSO inline-tag low-bit
					// guard + literal sentinel keep all sources safe). arm64
					// boxed strings excluded — same gating as the rest of the
					// native string-reclaim path.
					if _, isStr := f.Type.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthPtr})
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
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
		// Phase 3 enum-box reclamation: an OWNED enum frees its box to
		// the freelist on the last reference (rc==1) after dropping its
		// payloads. The full tiered logic — uniform branchless path,
		// non-uniform / scalar variant-plan tag switch, and the generic
		// fallthrough flat-dec — lives in emitEnumSlotDrop so the loop-var
		// reinit / reassign drop (emitStructEnumSlotDrop) and the
		// fresh-match-scrutinee reclamation can free a scalar-payload box
		// the same way instead of leaking it.
		if et, ok := t.(ast.EnumType); ok {
			b.emitEnumSlotDrop(slot, et, eligible)
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
		// Heap strings carry an rc header (prereq 1), and the emitDec
		// string branch reclaims owned ones via __fern_str_dec on wasm
		// or __fern_rc_dec on native single-word (x86_64). arm64
		// (TwoWordOverride boxed) excluded — no native str_dec runtime
		// helper, same gating as the rest of the native string-reclaim
		// path. The SSO inline-tag low-bit guard in __fern_rc_dec
		// (Slice 8) keeps short inline strings safe.
		if _, isStr := t.(ast.StringType); isStr {
			// arm64 now has __fern_str_inc / __fern_str_dec / __fern_cell_free
			// runtime helpers, so the wasm two-word path applies there too.
			// All non-zero ptrW with strings is rc-tracked.
			return true
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
	// Owned (`own`) params are reclaimed by the callee at exit, like an owned
	// local — the borrow model sweeps only `var` locals, so they need an extra
	// pass. A moved own param (passed onward to another `own` param) is already
	// in `seen` (b.movedLocals) and skipped, so the value is freed exactly once
	// at the end of the transfer chain; an own param that escaped is not
	// freeEligible (re-tainted) and is likewise skipped.
	for _, p := range b.fn.Params {
		if !p.Own || !rcTracked(p.Type) || seen[p.Name] || !b.freeEligible[p.Name] {
			continue
		}
		seen[p.Name] = true
		slot, ok := b.locals[p.Name]
		if !ok {
			continue
		}
		emitDec(slot, p.Type, true, p.Name)
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
		if _, isStr := c.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return true
		}
		// Native single-word string capture (x86_64, !TwoWordOverride):
		// the env slot holds a single ptr that needs __fern_rc_dec'ing
		// on the closure's last reference.
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
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
func genClosureDropThunk(name string, caps []ast.Param, ptrW int, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType) *Func {
	// A no-rc-capture closure (scalar/i64/f64 captures only) still gets a
	// thunk when it's MakeClosure'd: the pair's drop-fn pointer needs a
	// callable target that frees the env block. Its body is just the
	// is_unique-gated (empty) capture sweep + the __fern_closure_drop(env)
	// tail, which frees the env block (a no-op for env==0). The thunk loop
	// gates generation on hasRcCapture || MakeClosure-target, so an elided
	// scalar closure that never forms a pair generates nothing.
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	off := int32(0)
	for _, c := range caps {
		// Same 8-byte alignment the env layout uses (closureconv +
		// the backend store loops) so the drop reads each capture at
		// the offset it was written to.
		off = ast.CaptureAlign(off, c.Type, ptrW)
		slot := irCaptureSlotSize(c.Type, ptrW)
		if _, isStr := c.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			// Two-word string capture (wasm + arm64-TwoWordOverride):
			// load (data, len) from [env+off] and reclaim via
			// __fern_str_dec (balances the __fern_str_inc at MakeEnv).
			// Inside the env's is_unique branch, so the capture is
			// this closure's owned reference; inline / literal
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
		// Native single-word string capture (x86_64, !TwoWordOverride):
		// load the single ptr from [env+off] and __fern_rc_dec it
		// (balances the __fern_rc_inc at MakeEnv via emitAliasInc).
		// Inside the env's is_unique branch, so the capture is uniquely
		// owned; SSO inline-tag low-bit guard + literal sentinel keep
		// all sources safe. arm64 / wasm two-word ABIs take the
		// WidthString + __fern_str_dec branch above.
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthPtr},
				Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
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
				if drop, ok := arrElemStructDropName(at.Elem, info, reg, tupleReg, ptrW); ok {
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
			} else if drop, ok := dropFnNameFor(c.Type, info, reg, tupleReg, ptrW); ok {
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
// are handled correctly (no misread). A TUPLE with at least one rc-
// tracked element routes to __drop_tuple_<mangled> (genTupleDropFn
// generates a uniform deep-drop from the captured tuple shape) — the
// caller MUST supply a non-nil `tupleReg` so the worklist can recover
// the shape; absent a registry we fall back to the safe flat dec, the
// same way generic enums do. Map / runtime handle types, arrays,
// closures, and generic enum instantiations (Args != nil; their
// box-vs-pair-form shape needs the type args, handled inline for locals)
// return ("", false) so the caller falls back to a flat one-level dec.
func dropFnNameFor(t ast.Type, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, ptrW int) (string, bool) {
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
	case ast.TupleType:
		if tupleReg == nil {
			return "", false
		}
		if !tupleNeedsDrop(v, ptrW) {
			return "", false
		}
		mangled := mangleTupleInst(v)
		tupleReg[mangled] = v
		return "__drop_tuple_" + mangled, true
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
	return tupleEnumMangler.Replace(et.String())
}

// mangleTupleInst is mangleEnumInst's tuple sibling — same escape
// vocabulary, applied to a tuple's canonical String() so a tuple shape
// gets a stable, symbol-safe name component for its
// `__drop_tuple_<...>` recursive-drop function. `(string, i32)` →
// `__drop_tuple__LP_string_C_i32_RP_`. The mangled token uniquely
// determines the shape, so two structurally-equal tuples share one
// generated drop and two distinct shapes never collide.
func mangleTupleInst(tt ast.TupleType) string {
	return tupleEnumMangler.Replace(tt.String())
}

// tupleEnumMangler is the shared escape table for tuple + enum
// instantiation mangling. `[`/`]` carry enum type args, `(`/`)` carry
// tuple-element lists, and `,` separates either; all four collapse to
// `[A-Za-z0-9_]` tokens so the result is a valid wasm/asm symbol and
// no two distinct types compress to the same mangled name.
var tupleEnumMangler = strings.NewReplacer(
	"[", "_LB_",
	"]", "_RB_",
	"(", "_LP_",
	")", "_RP_",
	",", "_C_",
	" ", "",
)

// tupleNeedsDrop reports whether tt has at least one element worth
// recursing through — its drop fn dec's only rc-tracked / string
// elements, so a tuple of plain i32s (or any other non-rc shape) has
// nothing to do beyond the surrounding box dec the caller already
// emits. Mirrors enumNeedsDrop in role: dropFnNameFor uses it to
// decide whether to register and route through `__drop_tuple_<...>`
// at all.
func tupleNeedsDrop(tt ast.TupleType, ptrW int) bool {
	for _, et := range tt.Elems {
		if arrElemIsRcTracked(et) {
			return true
		}
		if _, isStr := et.(ast.StringType); isStr {
			// Two-word string element (wasm + arm64-TwoWordOverride) or
			// native single-word: both reach __fern_str_dec / __fern_rc_dec
			// from the per-tuple drop.
			if ast.UseTwoWordStrings(ptrW) || (ptrW == 8 && !ast.UseTwoWordStrings(ptrW)) {
				return true
			}
		}
	}
	return false
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
func arrElemStructDropName(elem ast.Type, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, ptrW int) (string, bool) {
	if v, ok := elem.(ast.StructType); ok {
		if v.Name == "Map" {
			return "", false
		}
		if _, ok := info.Structs[v.Name]; !ok {
			return "", false
		}
		return "__drop_arr_struct_" + v.Name, true
	}
	// Closure-element array (`(() => R)[]`): each element is a pointer to a
	// closure PAIR `{fn_ptr, env_ptr, drop_fn, env_ptr}`. A flat
	// __fern_drop_arr_ptr rc_dec'd each element but never freed the pair
	// block OR its env block (the captures) — an array element typed
	// `(() => R)` can't name WHICH closure it holds, so it has no static
	// __closure_drop_<name> thunk to call. The representation change
	// (the pair carries a drop-fn POINTER at offset 2*ptrW) lets a single
	// generic __drop_arr_closure loop free each element's env generically:
	// it derefs the embedded drop-fn through the duplicated {drop_fn,
	// env_ptr} sub-pair (OpCallIndirect on pair+2*ptrW) on the element's
	// last reference, then frees the pair block. Static function-value
	// cells (OpConstFunc, rc sentinel) are skipped by the is_unique gate.
	if _, ok := elem.(*ast.FuncType); ok {
		return "__drop_arr_closure", true
	}
	// Enum-element sibling of the struct case: a `E[]` whose variants carry
	// rc-tracked payloads (e.g. `Value[]` — pervasive in the self-host
	// compiler) flat-rc_dec'd each element under __fern_drop_arr_ptr,
	// freeing the enum box but leaking its payloads. Route a CONCRETE
	// droppable enum to a __drop_arr_enum_<Name> loop whose per-element
	// call is __drop_enum_<Name> (tag-dispatched deep drop). Generic enum
	// instantiations (Option[…][]) need the genEnumDrops registry the
	// worklist threads but arrElemStructDropName doesn't carry — they keep
	// the flat path for now.
	if v, ok := elem.(ast.EnumType); ok && len(v.Args) == 0 {
		if ed, ok := info.Enums[v.Name]; ok && enumNeedsDrop(ed) {
			return "__drop_arr_enum_" + v.Name, true
		}
	}
	// Tuple-element sibling of the struct case: the per-element loop
	// recurses through __drop_tuple_<mangled>, which dec's the tuple's
	// rc-tracked / string elements (e.g. the string inside
	// `(string, i32)`) before returning the tuple box to the freelist.
	// Without this branch the array drop fell through to the flat
	// __fern_drop_arr_ptr (rc_dec per element only) — freed each tuple
	// box but never traversed it, leaking the strings inside. Caller
	// supplies the tuple registry so the per-shape drop can be
	// regenerated by the post-pass worklist; absent a registry (direct
	// unit calls) we bail to the safe flat path.
	if tt, ok := elem.(ast.TupleType); ok && tupleReg != nil && tupleNeedsDrop(tt, ptrW) {
		mangled := mangleTupleInst(tt)
		tupleReg[mangled] = tt
		return "__drop_arr_tuple_" + mangled, true
	}
	// Enum-element array (`E[]`): each element is a pointer to an enum box
	// whose rc-tracked payloads (string / array / struct / nested enum) must
	// reclaim, not just be flat-rc_dec'd by __fern_drop_arr_ptr (which frees
	// the box but leaks its payloads). Route through the generic
	// __drop_arr_of_<__drop_enum_E> loop: genArrOfArrDropFn calls the enum's
	// own deep drop (__drop_enum_<E>, is_unique-gated) per element, then frees
	// the outer buffer. dropFnNameFor declines scalar-only / non-heap enums
	// (Option[i32], payload-less) — no payload to reclaim, so those keep the
	// flat path. A generic instantiation registers its substituted decl into
	// `reg` so the worklist regenerates the __drop_enum_<mangled> body.
	if _, ok := elem.(ast.EnumType); ok {
		if perElem, ok := dropFnNameFor(elem, info, reg, tupleReg, ptrW); ok {
			return "__drop_arr_of_" + perElem, true
		}
		return "", false
	}
	// Array-of-array element (`i32[][]`'s outer drop): each element is
	// itself an array whose BUFFER must be freed, not just flat-rc_dec'd
	// (the __fern_drop_arr_ptr fallback frees the outer buffer but leaks
	// the inner ones). The per-element drop depends on the inner array's
	// element type:
	//   - PRIMITIVE inner (`i32[][]`): a plain __fern_arr_dec frees the
	//     inner buffer → stride-keyed __drop_arr_arr_<innerStride>.
	//   - STRING inner (`string[][]`): each inner buffer's string elements
	//     must reclaim too → __drop_arr_arr_str, whose loop calls
	//     __fern_drop_arr_str per element (walk + str_dec each (data,len) +
	//     free the inner buffer). Two-word ABIs (wasm + arm64-TwoWord) and
	//     native single-word both back __fern_drop_arr_str.
	// Deeper inner shapes route through a generated __drop_arr_of_<perElem>
	// loop whose per-element call is the INNER array's own deep drop —
	// arrElemStructDropName(inner.Elem), recursively. So a `P[][]` (inner
	// = P[], concrete struct) drops each inner P[] via __drop_arr_struct_P,
	// and a `i32[][][]` (inner = i32[][]) drops each inner i32[][] via
	// __drop_arr_arr_4 — then frees the outer buffer. The worklist
	// regenerates the per-element drop transitively (enqueueCalls picks it
	// up from the generated body). Inner element types arrElemStructDropName
	// declines (enum[]/closure[]) keep the flat __fern_drop_arr_ptr (inner
	// buffers leak — safe, a later slice).
	if inner, ok := elem.(ast.ArrayType); ok {
		if _, isStr := inner.Elem.(ast.StringType); isStr {
			return "__drop_arr_arr_str", true
		}
		if !arrElemIsRcTracked(inner.Elem) {
			return fmt.Sprintf("__drop_arr_arr_%d", ast.ElemSizeBytesFor(inner.Elem, ptrW)), true
		}
		// rc-tracked inner element (struct / array / tuple / enum): recurse to
		// the inner array's drop (also registers any tuple / generic-enum
		// shape it discovers).
		if perElem, ok := arrElemStructDropName(inner.Elem, info, reg, tupleReg, ptrW); ok {
			return "__drop_arr_of_" + perElem, true
		}
	}
	return "", false
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

// genArrEnumDropFn is genArrStructDropFn's enum sibling: __drop_arr_enum_<Name>(ptr)
// walks each element (a pointer-shaped enum box, stride ptrW) and drops it
// through the tag-dispatched __drop_enum_<Name> (which reclaims the box +
// its rc-tracked payloads, is_unique-gated per element), then frees the
// buffer. The worklist regenerates __drop_enum_<Name> from this body.
// Slots: 0=ptr, 1=i, 2=len.
func genArrEnumDropFn(elemName string, ptrW int) *Func {
	stride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __drop_enum_<Name>(mem[ptr + i*stride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: "__drop_enum_" + elemName, I32: 1},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_enum_" + elemName,
		Params:       []ast.Param{{Name: "__ae", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrClosureDropFn builds __drop_arr_closure(ptr): the array-of-closure
// sibling of genArrStructDropFn. Each element is a pointer to a closure PAIR
// laid out as {fn_ptr, env_ptr, drop_fn, env_ptr} (slots of ptrW bytes; the
// env_ptr is duplicated at slot 3 so {drop_fn@2, env_ptr@3} forms a callable
// sub-pair). On the array's last reference (rc==1) it walks each element and,
// for elements that are themselves uniquely held (is_unique gate — skips
// shared closures AND static OpConstFunc cells, whose rc word is the immortal
// sentinel), frees the captures + env block by dispatching through the
// embedded drop-fn pointer: OpCallIndirect on (pair + 2*ptrW) calls
// drop_fn(env_ptr) — i.e. the per-closure __closure_drop_<name> thunk, which
// deep-drops rc-tracked captures and frees the env. The pair block itself is
// then freed via the generic __fern_closure_drop (rc==1 → box_free). Finally
// the buffer is returned to the freelist via __fern_arr_dec. Slots: 0=ptr
// (param), 1=i, 2=len, 3=p (current element pair pointer).
func genArrClosureDropFn(ptrW int) *Func {
	stride := int32(ptrW)
	// Empty signature + the closure-call ABI's appended env_ptr makes the
	// dispatched wasm type (i32)->i32, matching __closure_drop_<name>'s
	// actual (env)->env shape; natives read env from sub-pair+ptrW.
	dropSig := &ast.FuncType{Result: ast.NumberType{}}
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
		// p = mem[ptr + i*stride]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 3},
		// if is_unique(p): drop_fn(env) via OpCallIndirect on (p + 2*ptrW).
		// The is_unique gate skips shared closures (rc>1, another holder
		// keeps the env live) and static function-value cells (sentinel rc,
		// only 2 slots — never read slot 2 on them). Inside, the drop-fn
		// slot is 0 for zero-capture closures (env==0, nothing to free) —
		// guard it so OpCallIndirect never dispatches through a null slot.
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: 2 * int32(ptrW)},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpIf, I32: BlockTypeVoid}, // drop_fn != 0
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: 2 * int32(ptrW)},
		{Kind: OpAdd},
		{Kind: OpCallIndirect, I32: 0, Sig: dropSig},
		{Kind: OpDrop},
		{Kind: OpEnd}, // if drop_fn != 0
		{Kind: OpEnd}, // if is_unique(p)
		// Free / dec the pair block itself (rc==1 → box_free, else dec).
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpCallDirect, Str: "__fern_closure_drop", I32: 1},
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
		Name:         "__drop_arr_closure",
		Params:       []ast.Param{{Name: "__acl", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrTupleDropFn is genArrStructDropFn's tuple sibling — same
// loop shape, but the per-element call dispatches to a generated
// __drop_tuple_<mangled> helper (sized for THIS tuple's shape) so
// each element's rc-tracked / string members reclaim before the
// buffer is freed. Tuples have no source name; the mangled tuple
// shape carries the only key the worklist + per-element helper
// agree on. Slots: 0=ptr (param), 1=i, 2=len (scratch).
func genArrTupleDropFn(mangled string, ptrW int) *Func {
	stride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __drop_tuple_<mangled>(mem[ptr + i*stride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: "__drop_tuple_" + mangled, I32: 1},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_tuple_" + mangled,
		Params:       []ast.Param{{Name: "__at", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrArrDropFn builds __drop_arr_arr_<innerStride>(ptr) — the
// array-of-array sibling of genArrStructDropFn. On the OUTER array's last
// reference (rc==1) it walks each element (a pointer to an INNER array
// buffer, so the outer stride is ptrW) and frees that inner buffer via
// __fern_arr_dec(elem, innerStride) — which is_unique-gates the inner
// array, so a shared inner buffer only dec's — then frees the outer
// buffer. Generated only for inner arrays of PRIMITIVE elements
// (arrElemStructDropName's array-of-array branch gates on that), so the
// inner __fern_arr_dec is the complete reclamation; inner arrays of rc /
// string elements keep the flat __fern_drop_arr_ptr (a later slice).
// Slots: 0=ptr (param), 1=i, 2=len (scratch).
func genArrArrDropFn(innerStride int32, ptrW int) *Func {
	outerStride := int32(ptrW)
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
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __fern_arr_dec(mem[ptr + i*outerStride], innerStride); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpConstI32, I32: innerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
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
		// Dec / free the outer buffer itself (arr_dec re-checks rc==1).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         fmt.Sprintf("__drop_arr_arr_%d", innerStride),
		Params:       []ast.Param{{Name: "__aa", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrArrStrDropFn builds __drop_arr_arr_str(ptr) — the array-of-string[]
// outer drop. On the outer array's last reference (rc==1) it walks each
// element (a pointer to an inner string[] buffer, outer stride ptrW) and
// reclaims that inner array via __fern_drop_arr_str(elem, stringStride) —
// which walks the inner buffer's (data,len) string elements, str_dec's
// each, and frees the inner buffer — then frees the outer buffer. The
// string element stride is ElemSizeBytesFor(string) (2*ptrW two-word /
// ptrW single-word). Each helper is_unique-gates internally, so a shared
// inner array or string only dec's. Slots: 0=ptr, 1=i, 2=len.
func genArrArrStrDropFn(ptrW int) *Func {
	outerStride := int32(ptrW)
	strStride := int32(ast.ElemSizeBytesFor(ast.StringType{}, ptrW))
	// Inner string[] reclamation helper, matching the exit sweep's string[]
	// routing: two-word ABIs (wasm + arm64-TwoWord) walk (data,len) pairs
	// via __fern_drop_arr_str; native single-word (x86_64) elements are
	// single pointers, so __fern_drop_arr_ptr (rc_dec each, SSO-safe) is the
	// available helper (__fern_drop_arr_str isn't emitted there).
	innerDrop := "__fern_drop_arr_str"
	if !ast.UseTwoWordStrings(ptrW) {
		innerDrop = "__fern_drop_arr_ptr"
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __fern_drop_arr_str(mem[ptr + i*outerStride], strStride); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpConstI32, I32: strStride},
		{Kind: OpCallDirect, Str: innerDrop, I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_arr_str",
		Params:       []ast.Param{{Name: "__aas", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrOfArrDropFn builds __drop_arr_of_<perElem>(ptr) — the generic
// "array of pointer-shaped, deeply-droppable element" outer drop. On the
// outer array's last reference (rc==1) it walks each element (a pointer,
// outer stride ptrW) and drops it through the 1-arg `perElemDrop` (each
// frees the element's storage + its rc-tracked contents and is_unique-gates
// internally), then frees the outer buffer. perElemDrop is any generated
// 1-arg → i32 deep drop: an INNER ARRAY's own drop for array-of-array
// (__drop_arr_struct_<E> / __drop_arr_arr_<n> / __drop_arr_arr_str /
// __drop_arr_of_<…>), OR the ENUM's own __drop_enum_<E> for array-of-enum
// (arrElemStructDropName's EnumType branch). The worklist regenerates
// perElemDrop transitively from this body. Slots: 0=ptr, 1=i, 2=len.
func genArrOfArrDropFn(perElemDrop string, ptrW int) *Func {
	outerStride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// perElemDrop(mem[ptr + i*outerStride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: perElemDrop, I32: 1},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_of_" + perElemDrop,
		Params:       []ast.Param{{Name: "__ao", Type: ast.NumberType{}}},
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
func mapValDropName(st ast.StructType, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, ptrW int) (string, bool) {
	if st.Name != "Map" || len(st.Args) < 2 {
		return "", false
	}
	perVal, ok := mapValHasDrop(st.Args[1], info, genEnumDrops, genTupleDrops, ptrW)
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
	// Inner block per entry differs by backend layout:
	//   two-word ABI (wasm ptrW=4 + arm64-TwoWordOverride ptrW=8):
	//     the kv slot stores a cell pointer; deref to load the
	//     (data, len) two-word string, __fern_str_dec it, then
	//     __fern_cell_free the now-dead 16-byte cell.
	//   native single-word (x86_64 ptrW=8, !TwoWordOverride): the
	//     kv slot stores the string data pointer directly (no
	//     boxing — the slot is already pointer-wide). One
	//     __fern_rc_dec per entry is the whole reclamation; the L2
	//     header at data-8 + rc-sentinel literals from prereqs 1+2
	//     make this safe across heap + literal sources.
	var inner []Op
	if ast.UseTwoWordStrings(ptrW) {
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
func appendChildDrop(ops []Op, t ast.Type, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType) []Op {
	// Two-word string value (wasm + arm64-TwoWordOverride): the
	// caller loaded (data, len) via a string-aware load
	// (payloadLoadOpFor), so reclaim via __fern_str_dec. Reached from
	// genEnumDropFn's payload drop (struct string fields are handled
	// inline in genStructDropFn before reaching here).
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			Op{Kind: OpDrop})
	}
	// Single-word string value (native single-word, x86_64): the caller
	// loaded a ptr via payloadLoadOpFor; reclaim via __fern_rc_dec (SSO
	// inline-tag low-bit guard + literal sentinel keep all sources safe).
	// arm64 / wasm two-word ABIs take the WidthString + __fern_str_dec
	// branch above.
	if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
			Op{Kind: OpDrop})
	}
	if isMapType(t) {
		return appendMapDrop(ops)
	}
	if name, ok := dropFnNameFor(t, info, reg, tupleReg, ptrW); ok {
		return append(ops,
			Op{Kind: OpCallDirect, Str: name, I32: 1},
			Op{Kind: OpDrop})
	}
	if at, ok := t.(ast.ArrayType); ok {
		// Any array field frees its buffer (see dropStructField for the
		// rationale): array-of-struct deep-drops elements + buffer,
		// array-of-rc frees the outer buffer, plain arrays arr_dec.
		if name, ok := arrElemStructDropName(at.Elem, info, reg, tupleReg, ptrW); ok {
			return append(ops,
				Op{Kind: OpCallDirect, Str: name, I32: 1},
				Op{Kind: OpDrop})
		}
		helper := "__fern_arr_dec"
		if arrElemIsRcTracked(at.Elem) {
			helper = "__fern_drop_arr_ptr"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			helper = "__fern_drop_arr_str"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			// string[] on native single-word: __fern_drop_arr_ptr walks +
			// __fern_rc_dec's each pointer element. Same routing as the
			// local-side gate above and the dropStructField gate.
			helper = "__fern_drop_arr_ptr"
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

// genTupleDropFn builds the recursive __drop_tuple_<mangled> function
// — the struct-drop sibling for anonymous tuple shapes. At the box's
// last reference (rc==1) it dec's every rc-tracked / string element
// and returns the box to the freelist; otherwise it just dec's. The
// box was alloc'd as `tupleElemLayout size + 8` rc header, so
// __fern_box_free frees base = data-8 with that size. The body mirrors
// the inline tuple-LOCAL drop in emitDec (string elements split by
// wasm two-word vs native single-word ABI; rc-tracked elements recurse
// via appendChildDrop), so a nested tuple — `(string, i32)` as a
// struct field, an array element, an enum payload, or another tuple's
// element — reaches the same dec calls a top-level local does, fixing
// the leak the docs called out under "nested tuples … strings still
// leak."
//
// Tuples not worth dropping (no rc-tracked or string element) are
// filtered upstream by tupleNeedsDrop before the routing fires, so
// genTupleDropFn assumes at least one element drop is emitted; the
// box_free + dec arms are always emitted.
func genTupleDropFn(mangled string, tt ast.TupleType, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType) *Func {
	offs, size := tupleElemLayout(tt.Elems, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	for i, et := range tt.Elems {
		if _, isStr := et.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			// Two-word string element (wasm + arm64-TwoWordOverride):
			// load (data, len) and reclaim via __fern_str_dec. Mirrors
			// the inline tuple-local path's two-word branch.
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if offs[i] != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
			}
			ops = append(ops,
				Op{Kind: OpLoad, Width: WidthString},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		if _, isStr := et.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			// Native single-word string element (x86_64,
			// !TwoWordOverride): single ptr + __fern_rc_dec. SSO
			// inline-tag low-bit guard + literal sentinel keep all
			// sources safe. arm64 / wasm two-word ABIs take the
			// WidthString + __fern_str_dec branch above.
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if offs[i] != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
			}
			ops = append(ops,
				Op{Kind: OpLoad, Width: WidthPtr},
				Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		if !arrElemIsRcTracked(et) {
			continue
		}
		ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
		if offs[i] != 0 {
			ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
		}
		ops = append(ops, Op{Kind: OpLoad, Width: WidthPtr})
		ops = appendChildDrop(ops, et, info, ptrW, reg, tupleReg)
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
		Name:       "__drop_tuple_" + mangled,
		Params:     []ast.Param{{Name: "__dt", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// genStructDropFn builds the recursive __drop_struct_<Name> function:
// at the value's last reference (rc==1) it drops each rc-tracked field
// — recursing into nested struct fields via their own drop fns — then
// returns the box to the freelist; otherwise it just dec's. The box was
// alloc'd as `structFieldLayout size + 8` rc header, so __fern_box_free
// frees base = data-8, size+8 (structFieldLayout's size already
// accounts for the header). Works for a childless struct too: the
// field loop is empty, so it just is_unique-gates and frees the box.
func genStructDropFn(name string, sd *ast.StructDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType) *Func {
	offs, size := structFieldLayout(sd.Fields, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	for _, f := range sd.Fields {
		_, isStr := f.Type.(ast.StringType)
		isStr = isStr && ast.UseTwoWordStrings(ptrW)
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
		ops = appendChildDrop(ops, f.Type, info, ptrW, reg, tupleReg)
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
func genEnumDropFn(name string, ed *ast.EnumDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType) *Func {
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
			ops = appendChildDrop(ops, ld.typ, info, ptrW, reg, tupleReg)
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
		if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			return 4, true // single-word native string dec (__fern_rc_dec)
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
		if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			return 4, true // single-word native string dec (__fern_rc_dec)
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

func lowerFunc(fn *ast.FuncDecl, info *checker.Info, ptrW int, pairForm map[string]bool, closureCaps map[string][]ast.Param, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, returnsNoParamEscape map[string]bool) (*Func, error) {
	out := &Func{
		Name:       fn.Name,
		Params:     fn.Params,
		Locals:     info.Locals[fn],
		ReturnType: fn.ReturnType,
		Captures:   fn.Captures,
	}
	b := &builder{
		info:                 info,
		fn:                   fn,
		out:                  out,
		locals:               map[string]int32{},
		scratchType:          map[int32]ast.Type{},
		ptrW:                 ptrW,
		pairForm:             pairForm,
		closureCaps:          closureCaps,
		genEnumDrops:         genEnumDrops,
		genTupleDrops:        genTupleDrops,
		returnsNoParamEscape: returnsNoParamEscape,
		thisIsPair:           pairForm[fn.Name],
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
	b.reuseSources, b.reuseConsumed = b.computeReuseSources()
	b.consumingMatchReuse = map[*ast.Call]bool{}
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
		// String locals: zero so a never-initialised local's exit dec
		// sees a null pointer — __fern_str_dec / __fern_rc_dec null-
		// guard. wasm two-word zeroes both slots (data, len); native
		// single-word (x86_64, !TwoWordOverride) zeroes one slot. arm64
		// excluded for the same reason as the dec sweep.
		if _, isStr := t.(ast.StringType); isStr {
			// arm64 now has __fern_str_inc / __fern_str_dec / __fern_cell_free
			// runtime helpers, so the wasm two-word path applies there too.
			// All non-zero ptrW with strings is rc-tracked.
			return true
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
		// push two zeros so OpStoreLocal balances. Native single-word
		// string slots take only one zero (the single data pointer).
		// Wasm and arm64-two-word both take two-word strings.
		if _, isStr := v.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpConstI32, I32: 0})
		}
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
	}
	// Perceus precise drops (garbage-free, straight-line subset): drop an
	// owned rc local right after its last top-level use instead of at the
	// exit sweep, lowering peak memory. Iterate the function body's top-
	// level statements directly (the Block case is a bare loop) so we can
	// splice the per-statement precise drops in; nested blocks still lower
	// through b.stmt unchanged. precise[i] is empty when RcFreeEnabled is
	// off, so this is identical to b.stmt(fn.Body) on the no-free path.
	precise := b.computePreciseDrops()
	if b.tryEmitTrmc() {
		// TRMC took over the whole body (a single `match`); skip normal
		// statement lowering. Scratch-type recording below still runs.
	} else {
		for i, st := range fn.Body.Stmts {
			if err := b.stmt(st); err != nil {
				return nil, err
			}
			for _, name := range precise[i] {
				b.emitPreciseDrop(name)
			}
		}
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

// enumRcPayloadsEligible reports whether the Slice-1b EnumRcPayloads model
// applies to enum `enumName`: the flag is on AND the enum's deep drop is fully
// wired on every backend. Enums whose payloads transitively contain a Map are
// excluded — a Map-in-enum deep drop calls `__map_drop_values`, a runtime helper
// the wasm helper-inclusion pass doesn't pull in for a generated `__drop_enum_`
// body, and Map key/value reclamation is itself an open gap. Excluded enums keep
// the move model (flag-off behaviour) at every site, a documented safe leak.
func (b *builder) enumRcPayloadsEligible(enumName string) bool {
	return ast.EnumRcPayloads && !b.enumTransitivelyContainsMap(enumName, map[string]bool{})
}

// enumRcPayloadsEligibleForValue is the expression form: true when `e` is a
// variant constructor (`Cons(..)`) or enum literal of an rc-eligible enum.
func (b *builder) enumRcPayloadsEligibleForValue(e ast.Expr) bool {
	var name string
	switch x := e.(type) {
	case *ast.Call:
		id, ok := x.Callee.(*ast.Ident)
		if !ok {
			return false
		}
		en, _, _, isVar := b.lookupVariant(id.Name)
		if !isVar {
			return false
		}
		name = en
	case *ast.EnumLit:
		name = x.EnumName
	default:
		return false
	}
	return b.enumRcPayloadsEligible(name)
}

func (b *builder) enumTransitivelyContainsMap(enumName string, seen map[string]bool) bool {
	if seen["e:"+enumName] {
		return false
	}
	seen["e:"+enumName] = true
	ed, ok := b.info.Enums[enumName]
	if !ok {
		return true // unknown / generic-erased — conservative (exclude)
	}
	for _, v := range ed.Variants {
		for _, pl := range v.Payloads {
			if b.typeTransitivelyContainsMap(pl, seen) {
				return true
			}
		}
	}
	return false
}

func (b *builder) typeTransitivelyContainsMap(t ast.Type, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.StructType:
		if ty.Name == "Map" {
			return true
		}
		if seen["s:"+ty.Name] {
			return false
		}
		seen["s:"+ty.Name] = true
		sd, ok := b.info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if b.typeTransitivelyContainsMap(f.Type, seen) {
				return true
			}
		}
		return false
	case ast.EnumType:
		return b.enumTransitivelyContainsMap(ty.Name, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if b.typeTransitivelyContainsMap(e, seen) {
				return true
			}
		}
		return false
	case ast.ArrayType:
		return b.typeTransitivelyContainsMap(ty.Elem, seen)
	case ast.SliceType:
		return b.typeTransitivelyContainsMap(ty.Elem, seen)
	}
	return false
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
	// General FBIP reuse (computeReuseSources): a dead, owned enum local D of
	// the same type/box-class is reused in place for this variant construction.
	// Same machinery as the StructLit / TupleLit hooks — emitReuseToken leaves
	// the box BASE on the stack (or a fresh OpAlloc), and emitReuseOldFieldDrops
	// frees D's OLD payload (D's uniform drop loads, variant-independent offsets)
	// on the reuse branch before the tag + payload stores below overwrite them.
	reuseSrcUniqSlot := int32(-1)
	var reuseSrcOffs []int32
	var reuseSrcTypes []ast.Type
	if dName, paired := b.reuseSources[callNode]; paired && callNode != nil {
		var dSize int32
		reuseSrcOffs, reuseSrcTypes, dSize = b.reuseSourceLayout(dName)
		if b.consumingMatchReuse[callNode] {
			// Consuming-match reuse (C2): the scrutinee's pointer payloads were
			// MOVED into the arm bindings and are reclaimed downstream, so the
			// reused box's OLD fields must not be dropped — only its shell is
			// taken. Keep dSize (for the reuse token's class accounting) but drop
			// nothing.
			reuseSrcOffs, reuseSrcTypes = nil, nil
		}
		reuseSrcUniqSlot = b.emitReuseToken(dName, dSize+rcHeaderBytes, size+rcHeaderBytes)
	} else {
		b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
		b.emit(Op{Kind: OpAlloc})
	}
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__enum_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// rc = 1 at [base + 0].
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	if reuseSrcUniqSlot >= 0 {
		b.emitReuseOldFieldDrops(reuseSrcUniqSlot, baseSlot, reuseSrcOffs, reuseSrcTypes)
	}
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
		// Slice 1b (EnumRcPayloads): rc-count pointer payloads exactly like
		// StructLit fields — an aliased payload (`Cons(0, t)`, t live elsewhere)
		// is inc'd so the box co-owns its reference, a moved last-use owned-local
		// payload (b.moveSites, markConstructionMoves' enum case) skips the inc.
		// Inc-and-passthrough (leaves the value on the stack for the store).
		// Consuming-match reuse stores moved-out bindings back, so it's excluded.
		if b.enumRcPayloadsEligible(enumName) && !b.consumingMatchReuse[callNode] &&
			needsRcIncOnAlias(a, b) && !b.moveSites[a] {
			b.emitAliasInc(a)
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
				if _, isStr := ct.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
					// A two-word string capture (wasm + arm64-TwoWordOverride)
					// is dropped by the thunk (__fern_str_dec), so it must
					// have been inc'd at MakeEnv or the thunk would over-
					// release it.
					rcTracked = true
				}
				if _, isStr := ct.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
					// Native single-word string capture (x86_64): thunk dec's
					// via __fern_rc_dec; same inc-at-MakeEnv requirement.
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
		// Phase 5h: release the slot's previous value before this
		// (re-)init store. For a loop-body `var` this reclaims the prior
		// iteration's allocation instead of leaking it; for a once-run
		// `var` the zero-init makes it a NULL-guarded no-op. The new value
		// is on the stack underneath — emitVarReinitDropOld is net-zero —
		// so it survives for the store below.
		b.emitVarReinitDropOld(n.Name, idx)
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
		//
		// Phase 4 move-on-destructure: when Init is an owned rc tuple
		// local at its last use (b.moveSites[n] set in
		// computeMovedLocals), the alias inc and the source's exit-sweep
		// dec cancel — move the source into the temp instead.
		if needsRcIncOnAlias(n.Init, b) && !b.moveSites[n] {
			b.emitAliasInc(n.Init)
		}
		// Loop-body reclamation: a destructure inside a loop reuses the
		// synthetic temp slot across iterations, so the prior iteration's
		// tuple box must be released before this iteration overwrites it —
		// otherwise every iteration but the last leaks its box (the exit
		// sweep only reclaims the final one). emitVarReinitDropOld deep-
		// drops the old temp (via __drop_tuple_ / box_free, is_unique-gated)
		// and is net-zero on the operand stack, so the new box ptr (and any
		// alias-inc above it) is left in place for the store. Gated inside
		// on RcFreeEnabled + freeEligible + localNameUnique + !movedLocals;
		// the temp is an untainted owned TupleType local, so it qualifies.
		// First-iteration safe: the slot is zero-init'd at entry, so the
		// drop's is_unique / null guards no-op on the NULL.
		b.emitVarReinitDropOld(n.TempName, tempIdx)
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
			if _, isStr := tup.Elems[i].(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
				// Two-word string element (wasm + arm64-TwoWordOverride):
				// dup via __fern_str_inc (consumes + re-pushes the
				// (data, len) pair) so the binding co-owns the buffer
				// alongside the tuple box. Without it the tuple's deep-
				// drop __fern_str_dec would free the buffer under the
				// still-live binding (UAF).
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
			} else if _, isStr := tup.Elems[i].(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				// Native single-word string element: dup via __fern_rc_inc
				// so the binding co-owns the buffer alongside the tuple
				// box's later dec.
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
			} else if arrElemIsRcTracked(tup.Elems[i]) {
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
			}
			// Loop-body reclamation: like the temp above, a binding declared
			// by a destructure inside a loop reuses one slot per iteration.
			// The dup-on-projection inc made this binding an owned co-owner,
			// so releasing the prior iteration's value here (before the
			// overwrite) balances that inc and reclaims the per-element
			// storage — matching the exit-sweep dec that fires on the final
			// iteration. Net-zero on the stack, leaving the freshly-loaded
			// element value in place for the store; gated identically inside
			// (RcFreeEnabled + freeEligible + unique + !moved). b is zero-
			// init'd at entry, so the first-iteration drop no-ops on NULL.
			b.emitVarReinitDropOld(name, nameIdx)
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
		//
		// Stage (a) statement-temp reclamation: when the discarded value
		// is a FRESH owned rc temporary (a literal / concat / string slice
		// that aliases nothing and escapes nowhere — freshOwnedRcTempType),
		// DEC it instead of dropping an owned allocation on the floor.
		// Without this a bare `a + b;` / `[x, y];` leaks its box every time.
		// Gated inside freshOwnedRcTempType on RcFreeEnabled, so free-off
		// stays byte-identical to the plain-drop baseline below.
		if exprLeavesValue(n.Expr, b.info) {
			if t, ok := b.freshOwnedRcTempType(n.Expr); ok {
				b.emitOwnedTempStackDrop(t)
				break
			}
			// Discarded owned call result: a bare `mk(i);` whose user-function
			// result is a fresh struct / array / string / enum leaks it (the
			// floor-drop below). Dec it via the is_unique-gated drop instead —
			// safe because an aliased return is rc>=2 (return-transfer inc) so
			// the gate only decs it, never frees a still-owned value. Builtins
			// / mutators / variant ctors that hand back an uncounted alias are
			// excluded (ownedCallResultType).
			if t, ok := b.ownedCallResultType(n.Expr); ok {
				b.emitOwnedTempStackDrop(t)
				break
			}
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
		// Reclaim a FRESH owned enum scrutinee box once the match completes:
		// `mk(i)` in `match (mk(i)) { … }` is dead after dispatch but the box
		// is never dec'd, so a per-iteration-fresh scrutinee leaks one box per
		// iteration. Heap-form only (pair-form Option[i32] has no box). Gated
		// (reclaimableMatchScrutinee) on all arm bindings being non-pointer so
		// no payload escapes the freed box; the dec itself is is_unique-gated.
		var (
			reclaimScrut bool
			scrutEnum    ast.EnumType
		)
		// Consuming match: the scrutinee is an OWN (consuming) parameter. The
		// arms move its pointer payloads into bindings (reclaimed downstream),
		// so after the match the box is freed SHALLOW (buffer only, no payload
		// deep-drop) — the FBIP traversal. The scrutinee is marked moved
		// (computeMovedLocals) so the exit sweep doesn't ALSO deep-drop it.
		var consumeEnum ast.EnumType
		consumeScrut := false
		if !pairFormScrutinee {
			consumeEnum, consumeScrut = b.ownParamEnumScrutinee(n.Tag)
		}
		if !pairFormScrutinee && !consumeScrut {
			bts := make([][]ast.Type, 0, len(n.Arms))
			for _, arm := range n.Arms {
				bts = append(bts, arm.BindingTypes)
			}
			scrutEnum, reclaimScrut = b.reclaimableMatchScrutinee(n.Tag, bts, nil)
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
			// Consuming match: the bindings are now copied into their own slots,
			// so the scrutinee box is dead (its pointer payloads were MOVED into
			// the bindings, reclaimed downstream). Free the box SHALLOW here —
			// after extraction, before the arm body runs (which uses the binding
			// slots, not the box) and before any `return`. Doing it here (not
			// after the whole match) is what makes it reach: the arms of a
			// consuming traversal `return`, so post-match code is dead. The
			// scrutinee is marked moved (computeMovedLocals) so the exit sweep
			// doesn't deep-drop it too. Guarded arms free only on the matched
			// path; a guard-false fall-through leaves the box for the next arm.
			if consumeScrut && !pairFormScrutinee && arm.Guard == nil {
				// C2: when this arm's body is `return Ctor(..)` constructing a
				// (payloadful) variant of the SAME enum, the scrutinee box and the
				// constructed box share the enum's uniform box size — so instead of
				// freeing the consumed box and allocating a fresh one, hand the box
				// shell straight to that construction via the reuse token (true
				// zero-alloc FBIP). Rides on RcReuseEnabled; otherwise free (C1).
				reuseCtor := b.consumingReuseCtor(arm, consumeEnum)
				scrutIdent, scrutIsIdent := n.Tag.(*ast.Ident)
				_, already := b.reuseSources[reuseCtor]
				if ast.RcReuseEnabled && reuseCtor != nil && scrutIsIdent && !already {
					b.reuseSources[reuseCtor] = scrutIdent.Name
					b.consumingMatchReuse[reuseCtor] = true
				} else {
					b.emitConsumingMatchBoxFree(ptrSlot, consumeEnum)
				}
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
		if reclaimScrut {
			b.emitOwnedEnumDrop(ptrSlot, scrutEnum, true)
		}
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
			if n.FloatWidth == 32 {
				b.emit(Op{Kind: OpConstF32, F32: float32(n.Value)})
			} else {
				b.emit(Op{Kind: OpConstF64, F64: float64(n.Value)})
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
			case sw == dw && dw == 64:
				// i64 ↔ u64: bit-identical reinterpret.
			case sw <= 32 && dw <= 32:
				// Source and destination both ride in i32 storage.
				// Produce the destination's *canonical* form so any
				// later read is correct: a sub-i32 signed dest is
				// sign-extended from its width, an unsigned dest is
				// zero-extended (masked); i32 / u32 keep the full
				// 32-bit pattern. This must run even for a same-width
				// signed↔unsigned reinterpret — `(-128i8) as u8` has
				// to clear the sign bits so a following `as i64`
				// zero-extends to 128 rather than 4294967168. (A
				// widening read assumes clean upper bits for unsigned
				// and re-signs from the width for signed, so every
				// producer stores canonically.)
				switch {
				case dw == 8 && dstInt.IsSigned():
					b.emit(Op{Kind: OpSignExtend8})
				case dw == 8:
					b.emit(Op{Kind: OpConstI32, I32: 0xFF})
					b.emit(Op{Kind: OpAnd})
				case dw == 16 && dstInt.IsSigned():
					b.emit(Op{Kind: OpSignExtend16})
				case dw == 16:
					b.emit(Op{Kind: OpConstI32, I32: 0xFFFF})
					b.emit(Op{Kind: OpAnd})
					// dw == 32: the 32-bit pattern is the value.
				}
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
			// float → int (truncate-toward-zero). The trunc op only
			// targets i32 / i64, so a sub-i32 destination truncs to
			// i32; Unsigned is chosen per the destination's
			// signed-ness.
			realW := dstInt.NormalWidth()
			dw := realW
			if dw < 32 {
				dw = 32
			}
			if srcFloat.NormalWidth() == 64 {
				b.emit(Op{Kind: OpITruncF64, Width: dw, Unsigned: !dstInt.IsSigned()})
			} else {
				b.emit(Op{Kind: OpITruncF32, Width: dw, Unsigned: !dstInt.IsSigned()})
			}
			// The trunc produced a 32-bit result; canonicalise it to
			// a sub-i32 destination's representation (sign-extend a
			// signed dest, zero-extend/mask an unsigned one) so its
			// upper bits match how every other producer stores that
			// type. Without this, `(3e9 as u8) as i64` came out
			// 3000000000 instead of 64 (the trunc's high bits leaked
			// through the widening zero-extend).
			switch {
			case realW == 8 && !dstInt.IsSigned():
				b.emit(Op{Kind: OpConstI32, I32: 0xFF})
				b.emit(Op{Kind: OpAnd})
			case realW == 8:
				b.emit(Op{Kind: OpSignExtend8})
			case realW == 16 && !dstInt.IsSigned():
				b.emit(Op{Kind: OpConstI32, I32: 0xFFFF})
				b.emit(Op{Kind: OpAnd})
			case realW == 16:
				b.emit(Op{Kind: OpSignExtend16})
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
		// concrete float type is known. Width=0 means the literal
		// stayed unsettled (no expected-type pressure); it defaults
		// to f64 — the double-precision default and the language's
		// primary float. Only an explicit f32 context stamps 32.
		if n.Width == 32 {
			b.emit(Op{Kind: OpConstF32, F32: float32(n.Value)})
		} else {
			b.emit(Op{Kind: OpConstF64, F64: n.Value})
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
		// Reclaim a FRESH owned enum scrutinee box after the match-expr
		// completes (value-consuming position; see reclaimableMatchScrutinee).
		// Gated additionally on the RESULT being non-pointer (resultType): a
		// pointer result could alias an extracted payload, so it's left alone.
		bts := make([][]ast.Type, 0, len(n.Arms))
		for _, arm := range n.Arms {
			bts = append(bts, arm.BindingTypes)
		}
		scrutEnum, reclaimScrut := b.reclaimableMatchScrutinee(n.Tag, bts, resultType)
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
		// Free the fresh owned scrutinee box (scalar result already in
		// resultSlot; net-zero on the operand stack) before loading the result.
		if reclaimScrut {
			b.emitOwnedEnumDrop(ptrSlot, scrutEnum, true)
		}
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
		// Cleanup-before-return: this is an early return on the error
		// path, so — exactly like the `*ast.Return` lowering — it must
		// run the active defers and the owned-local dec sweep before
		// leaving the function. Without these, `expr?` on the failure
		// path silently skipped BOTH: registered `defer`s never ran (a
		// correctness bug beyond rc — e.g. a `defer f.close()` leaked
		// its handle on every error propagation), and live owned locals
		// were never dec'd (an rc leak / count inflation that defeats
		// the rc==1 mutate-in-place fast path).
		if err := b.emitDeferCleanup(); err != nil {
			return err
		}
		// Decide how the dec sweep treats the value THIS path returns,
		// mirroring the Return lowering's transfer rules so the sweep
		// never frees the value being handed to the caller:
		//   - Option returns a FRESH None (never a tracked local), so a
		//     full sweep is always safe.
		//   - Result FORWARDS the source error value (the whole box in
		//     heap form, its (tag, payload) in pair form):
		//       * bare owned local `r?`  → hand r over untouched:
		//         exclude it from the sweep (move-on-return).
		//       * field / index alias (`s.f?`, `a[i]?`) → the box is a
		//         child of a tracked container the sweep would deep-drop;
		//         heap form re-incs the forwarded box so the drop nets
		//         out, pair form conservatively skips the sweep (the
		//         extracted payload could otherwise dangle — preferring
		//         today's leak to a use-after-free).
		//       * fresh call result (`foo()?`) → not a tracked local,
		//         full sweep is safe.
		sweepExclude := ""
		forwardInc := false
		sweepFailurePath := true
		if n.Kind == ast.TryKindResult {
			if id, ok := n.Inner.(*ast.Ident); ok && b.isOwnedRcLocal(id.Name) {
				sweepExclude = id.Name
			} else if needsRcIncOnAlias(n.Inner, b) {
				if b.thisIsPair {
					sweepFailurePath = false
				} else {
					forwardInc = true
				}
			}
		}
		// Failure-path return shape has to match the enclosing
		// function's ABI: pair-form fns return (tag, payload)
		// via OpReturnPair, heap-form fns return a single
		// heap-box pointer via OpReturn.
		switch n.Kind {
		case ast.TryKindOption:
			if b.thisIsPair {
				b.emit(Op{Kind: OpMakeNoneI32})
				b.emitRcDecLocalsAtExit()
				b.emit(Op{Kind: OpReturnPair})
			} else {
				if err := b.emitEnumNew(nil, "Option", 1, 0, nil); err != nil {
					return err
				}
				b.emitRcDecLocalsAtExit()
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
				if sweepFailurePath {
					b.emitRcDecLocalsAtExitExcept(sweepExclude)
				}
				b.emit(Op{Kind: OpReturnPair})
			} else {
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				if forwardInc {
					// Field / index alias: the container drop below
					// would otherwise free the forwarded box out from
					// under the caller. Re-inc so the dec nets out.
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
				}
				b.emitRcDecLocalsAtExitExcept(sweepExclude)
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
		// Reclaim a FRESH owned array container consumed by a SCALAR index —
		// `mk(i)[1]` (mk returns a fresh i32[]) loaded element 1 and dropped
		// the buffer on the floor, leaking it every iteration (160000 ->
		// 1600000 in a loop). Stash the container, index off the reload, then
		// dec it after the load via the is_unique-gated emitOwnedSlotDrop.
		// Gated to a NON-POINTER element (resultCannotAliasArg-style): the
		// loaded scalar can't alias the buffer, so freeing it is safe; a
		// pointer element (`mk_strs()[1]`) would alias and is left alone. The
		// is_unique gate additionally protects an aliased container (a callee
		// returning its param, rc>=2 via the return-transfer inc — only
		// dec'd, never freed). Idents / fields aren't fresh temps and lower
		// in place. String / slice index paths are excluded (n.IsString /
		// n.IsSlice).
		idxContainerSlot := int32(-1)
		if !n.IsString && !n.IsSlice && n.ElemType != nil && !ast.IsPointerType(n.ElemType) {
			ct, ok := b.freshOwnedRcTempType(n.Array)
			if !ok {
				ct, ok = b.ownedCallResultType(n.Array)
			}
			if ok {
				if _, isArr := ct.(ast.ArrayType); isArr {
					idxContainerSlot = b.allocSlot()
					b.locals[fmt.Sprintf("__idxbase_%d", idxContainerSlot)] = idxContainerSlot
					b.scratchType[idxContainerSlot] = ct
					if err := b.expr(n.Array); err != nil {
						return err
					}
					b.emit(Op{Kind: OpStoreLocal, I32: idxContainerSlot})
					b.emit(Op{Kind: OpLoadLocal, I32: idxContainerSlot})
				}
			}
		}
		if idxContainerSlot < 0 {
			if err := b.expr(n.Array); err != nil {
				return err
			}
		}
		if err := b.expr(n.Idx); err != nil {
			return err
		}
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
		// Dec the stashed fresh array container now that the scalar element
		// is loaded (only set on the array-index path above). Net-zero on the
		// operand stack, leaving the loaded element on top.
		if idxContainerSlot >= 0 {
			b.emitOwnedSlotDrop(idxContainerSlot, b.scratchType[idxContainerSlot])
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
			//
			// Phase 4 move-on-construction: an owned rc local consumed as
			// an element at its last use is moved into the array —
			// __fern_drop_arr_ptr dec's the element at the array's drop,
			// balancing the skipped inc (markConstructionMoves sets
			// b.moveSites[el]).
			if needsRcIncOnAlias(el, b) && !b.moveSites[el] {
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
		b.emit(Op{Kind: OpConstI32, I32: mapValTag(n.ValueType, b.ptrW, b.info, b.genEnumDrops, b.genTupleDrops)})
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
		// General FBIP reuse (computeReuseSources): a dead, owned tuple local D
		// of the same box class is reused in place for this TupleLit. Identical
		// machinery to the StructLit hook — emitReuseToken leaves the box BASE
		// on the stack (or a fresh OpAlloc), and emitReuseOldFieldDrops releases
		// D's OLD pointer elements (D's own layout) on the reuse branch before
		// the element stores below overwrite them.
		reuseSrcUniqSlot := int32(-1)
		var reuseSrcOffs []int32
		var reuseSrcTypes []ast.Type
		if dName, paired := b.reuseSources[n]; paired {
			var dSize int32
			reuseSrcOffs, reuseSrcTypes, dSize = b.reuseSourceLayout(dName)
			reuseSrcUniqSlot = b.emitReuseToken(dName, dSize+rcHeaderBytes, size+rcHeaderBytes)
		} else {
			b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
			b.emit(Op{Kind: OpAlloc})
		}
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_tup_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// rc = 1 at [base + 0].
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpStore})
		if reuseSrcUniqSlot >= 0 {
			b.emitReuseOldFieldDrops(reuseSrcUniqSlot, baseSlot, reuseSrcOffs, reuseSrcTypes)
		}
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
			//
			// Phase 4 move-on-construction: an owned rc local consumed
			// as a tuple element at its last use is moved into the
			// tuple — __drop_tuple_<...> dec's the element at the
			// tuple's drop, balancing the skipped inc
			// (markConstructionMoves sets b.moveSites[elem]).
			if needsRcIncOnAlias(elem, b) && !b.moveSites[elem] {
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
		// Struct-update `Foo { ...base, field: v }`: evaluate the base
		// once into a scratch slot. The base is *borrowed* for the
		// copy — evaluating it (typically an Ident local) just loads
		// the pointer; the local keeps ownership and decs at its own
		// scope exit, so we must NOT drop the base here. Each un-
		// overridden pointer-shaped field copied out of the base is
		// rc-inc'd: the new struct co-owns it, balancing the base
		// local's eventual dec (same aliasing rule the field-init path
		// applies). See docs/IMMUTABILITY-MIGRATION-PLAN.md.
		updBaseSlot := int32(-1)
		if n.Base != nil {
			if err := b.expr(n.Base); err != nil {
				return err
			}
			updBaseSlot = b.allocSlot()
			b.emit(Op{Kind: OpStoreLocal, I32: updBaseSlot})
		}
		// General FBIP reuse (computeReuseSources): if this construction C is
		// paired with a dead, owned, same-shape struct local D, reuse D's box
		// in place instead of bump-allocating. token = is_unique(D) ? base(D)
		// : 0 (the shared / null branch dec's D so its alias keeps the box and
		// __alloc_reuse falls through to a fresh alloc); D's slot is then
		// zeroed so the exit sweep (and any path that didn't reach C) treats
		// it as already consumed. Leaves the box BASE pointer on the stack,
		// exactly where OpAlloc would. D may be a DIFFERENT type (even a
		// different KIND) than C: emitReuseToken passes D's own alloc size
		// (tokenSize) so a class mismatch frees D's block to ITS class, and the
		// old-field release walks D's own layout. reuseSrcUniqSlot carries the
		// is_unique result to the release block (the reused box still holds D's
		// OLD pointer fields, which must go before C's stores overwrite them;
		// D is dead at C, so every field is replaced, no carried field to keep).
		reuseSrcUniqSlot := int32(-1)
		var reuseSrcOffs []int32
		var reuseSrcTypes []ast.Type
		if dName, paired := b.reuseSources[n]; paired && updBaseSlot < 0 {
			var dSize int32
			reuseSrcOffs, reuseSrcTypes, dSize = b.reuseSourceLayout(dName)
			reuseSrcUniqSlot = b.emitReuseToken(dName, dSize+rcHeaderBytes, size+rcHeaderBytes)
		} else {
			b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
			b.emit(Op{Kind: OpAlloc})
		}
		baseSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_lit_%d", baseSlot)] = baseSlot
		b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
		// rc = 1 at [base + 0].
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: 1})
		b.emit(Op{Kind: OpStore})
		if reuseSrcUniqSlot >= 0 {
			b.emitReuseOldFieldDrops(reuseSrcUniqSlot, baseSlot, reuseSrcOffs, reuseSrcTypes)
		}
		overridden := map[string]bool{}
		for _, f := range n.Fields {
			overridden[f.Name] = true
		}
		// Copy un-overridden fields from the base into the new box.
		if updBaseSlot >= 0 {
			for _, sf := range sd.Fields {
				if overridden[sf.Name] {
					continue
				}
				off := offs[sf.Name]
				ft := sf.Type
				// dst address: newbox + rcHeader + off
				b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
				b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + off})
				b.emit(Op{Kind: OpAdd})
				// value: load from base + off. The base temp holds the
				// user-visible data pointer (already past the rc
				// header), so field reads use just `+off` — same as
				// FieldAccess lowering, NOT the `rcHeader+off` the
				// dst (raw alloc base) needs.
				b.emit(Op{Kind: OpLoadLocal, I32: updBaseSlot})
				if off != 0 {
					b.emit(Op{Kind: OpConstI32, I32: off})
					b.emit(Op{Kind: OpAdd})
				}
				b.emit(payloadLoadOpFor(ft, b.ptrW))
				// The new struct co-owns a copied pointer field — inc
				// it (mirrors emitAliasInc, keyed on the field type
				// since there's no source expr). Two-word strings go
				// through __fern_str_inc so the inline-bit tag check
				// applies; everything else pointer-shaped uses
				// __fern_rc_inc. Both inc-and-passthrough (leave the
				// value on the stack for the store).
				if ast.IsPointerType(ft) {
					if _, isStr := ft.(ast.StringType); isStr && b.twoWordStrings() {
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
					} else {
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
					}
				}
				b.emit(payloadStoreOpFor(ft, b.ptrW))
			}
		}
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
			//
			// Phase 4 move-on-construction: when this field consumes an
			// owned rc local at its last use (b.moveSites set by
			// markConstructionMoves), skip the inc — the local's
			// reference is moved into the field and its exit-sweep dec is
			// skipped to match.
			if needsRcIncOnAlias(f.Value, b) && !b.moveSites[f.Value] {
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
			// Phase 4 move-on-construction: an owned rc local captured at
			// its last use is moved into the closure env — the closure's
			// drop thunk dec's the capture, balancing the skipped inc
			// (markConstructionMoves sets b.moveSites[capExpr]).
			if needsRcIncOnAlias(capExpr, b) && !b.moveSites[capExpr] {
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
		// Reclaim a FRESH owned struct/tuple container consumed by a SCALAR
		// field access — `mk(i).x` (mk returns a fresh struct) loaded field x
		// and dropped the box on the floor, leaking it (240000 -> 2400000 in
		// a loop). Stash the container, load the field off the reload, then
		// deep-drop it after via the is_unique-gated emitOwnedSlotDrop (which
		// also reclaims the struct's OTHER rc fields — we only took a scalar).
		// Gated to a NON-POINTER field: the loaded scalar can't alias the box,
		// so freeing it is safe; a pointer field (`mk().data` -> array) WOULD
		// alias and is left alone. The is_unique gate protects an aliased
		// container (a callee returning its param, rc>=2 — only dec'd). Idents
		// / nested fields aren't fresh temps and lower in place.
		faContainerSlot := int32(-1)
		if ft != nil && !ast.IsPointerType(ft) {
			ct, ok := b.freshOwnedRcTempType(n.Target)
			if !ok {
				ct, ok = b.ownedCallResultType(n.Target)
			}
			if ok {
				if _, isStruct := ct.(ast.StructType); isStruct {
					faContainerSlot = b.allocSlot()
				} else if _, isTuple := ct.(ast.TupleType); isTuple {
					faContainerSlot = b.allocSlot()
				}
				if faContainerSlot >= 0 {
					b.locals[fmt.Sprintf("__fabase_%d", faContainerSlot)] = faContainerSlot
					b.scratchType[faContainerSlot] = ct
					if err := b.expr(n.Target); err != nil {
						return err
					}
					b.emit(Op{Kind: OpStoreLocal, I32: faContainerSlot})
					b.emit(Op{Kind: OpLoadLocal, I32: faContainerSlot})
				}
			}
		}
		if faContainerSlot < 0 {
			if err := b.expr(n.Target); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpConstI32, I32: off})
		b.emit(Op{Kind: OpAdd})
		b.emit(payloadLoadOpFor(ft, b.ptrW))
		if faContainerSlot >= 0 {
			b.emitOwnedSlotDrop(faContainerSlot, b.scratchType[faContainerSlot])
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
		// Unsettled float literal: defaults to f64 (8-byte slot),
		// matching the Width-0 → OpConstF64 lowering above.
		return ast.FloatType{Polymorphic: true}
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

// isOwnedStringTemp reports whether `e` produces a FRESH owned heap
// string — one that allocates a new rc=1 buffer the surrounding
// expression must reclaim if it doesn't bind / store / return it. True
// for a string concat (`a + b`, which always copies into a fresh buffer)
// and a string slice (which copies its bytes out). Idents / field / index
// reads (borrowed views) and literals (static .rodata) are NOT owned
// temps — freeing them would corrupt a live value, so they read false.
// Used by the concat lowering to dec a nested-concat intermediate.
func (b *builder) isOwnedStringTemp(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Binary:
		return x.IsStringConcat
	case *ast.SliceExpr:
		_, isStr := b.exprType(x).(ast.StringType)
		return isStr
	}
	return false
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
	if n.IsStringConcat {
		// Reclaim NESTED concat intermediates: in `a + b + c`
		// (= `(a + b) + c`) the inner `(a + b)` is a fresh owned heap
		// string consumed by the outer OpStrConcat, then orphaned —
		// OpStrConcat copies its bytes but never frees its buffer, so a
		// chained / parenthesised concat leaks one buffer per join. An
		// operand that is itself an owned string temp (a sub-concat or a
		// string slice — isOwnedStringTemp) is stashed in a scratch slot,
		// used for the concat, then __fern_str_dec'd. Recurses: each level
		// frees its own immediate intermediate, so a whole left-/right-
		// nested chain reclaims. Borrowed operands (idents / fields / index
		// / literals) are NOT stashed — decing them would free a live value.
		// Gated on RcFreeEnabled (free-off stays byte-identical).
		stash := func(e ast.Expr) (int32, error) {
			if err := b.expr(e); err != nil {
				return -1, err
			}
			if !ast.RcFreeEnabled || !b.isOwnedStringTemp(e) {
				return -1, nil
			}
			sl := b.allocSlot()
			b.locals[fmt.Sprintf("__cattmp_%d", sl)] = sl
			b.scratchType[sl] = ast.StringType{}
			b.emit(Op{Kind: OpStoreLocal, I32: sl}) // pop (data,len) → slot
			b.emit(Op{Kind: OpLoadLocal, I32: sl})  // re-push for the concat
			return sl, nil
		}
		slL, err := stash(n.Left)
		if err != nil {
			return err
		}
		slR, err := stash(n.Right)
		if err != nil {
			return err
		}
		b.emit(Op{Kind: OpStrConcat})
		// Reclaim each stashed owned-temp operand. ABI-correct dec, matching
		// the exit sweep: two-word ABIs (wasm + arm64-TwoWord) consume the
		// (data,len) pair via __fern_str_dec; native single-word (x86_64)
		// dec's the single pointer via __fern_rc_dec (its inline-tag / SSO
		// guards make it safe for short concats that never heap-allocated).
		decHelper := "__fern_rc_dec"
		if ast.UseTwoWordStrings(b.ptrW) {
			decHelper = "__fern_str_dec"
		}
		for _, sl := range []int32{slL, slR} {
			if sl < 0 {
				continue
			}
			b.emit(Op{Kind: OpLoadLocal, I32: sl})
			b.emit(Op{Kind: OpCallDirect, Str: decHelper, I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
		return nil
	}
	if err := b.expr(n.Left); err != nil {
		return err
	}
	if err := b.expr(n.Right); err != nil {
		return err
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
		// FloatWidth=0 means "unannotated" — an unsettled float
		// binary with no expected-type pressure (e.g. an inferred
		// `var y = 1.0 / 3.0`). Default to f64 so the op width
		// matches the f64 default its operand literals lower to;
		// a 32-bit default here left the operands as f64 consts
		// feeding an f32 op (wasm rejected the type mismatch, the
		// natives mis-read the result). Only an explicit f32
		// context stamps 32.
		w := n.FloatWidth
		if w == 0 {
			w = 64
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
func mapValKindTag(t ast.Type, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, ptrW int) int32 {
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
	if _, ok := mapValHasDrop(t, info, genEnumDrops, genTupleDrops, ptrW); ok {
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
func mapValHasDrop(v ast.Type, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, ptrW int) (string, bool) {
	// Array-of-CONCRETE-struct value (Map[K, Item[]]): each value array
	// deep-drops its element boxes + buffer via the generated
	// __drop_arr_struct_<Elem> loop, rather than the shallow drop_arr_ptr
	// __map_drop_values uses for kind 3 (which frees the buffer but leaks
	// the element struct boxes). Only reached from routing — mapValKindTag
	// short-circuits arrays to kind 2/3 (whose `>= 2` retain still
	// applies), so this changes the DROP, not the retain. Other arrays
	// (plain / nested / enum-elem) keep __map_drop_values.
	if at, ok := v.(ast.ArrayType); ok {
		return arrElemStructDropName(at.Elem, info, genEnumDrops, genTupleDrops, ptrW)
	}
	// Every other value with a generated recursive drop — concrete user
	// struct (__drop_struct_<V>), concrete enum (__drop_enum_<V>), or a
	// heap-boxed generic-enum instantiation (__drop_enum_<mangled>, recorded
	// in genEnumDrops) — routes through dropFnNameFor, the same dispatch
	// the struct/enum field drops use. Strings / tuples / slices / runtime
	// handles / pair-form generic enums read false and stay non-reclaimed.
	return dropFnNameFor(v, info, genEnumDrops, genTupleDrops, ptrW)
}

// mapValTag is what map_new actually stores at buf+12: the low
// byte is the valKind (mapValKindTag) and, for array values
// (kind 2/3), the high bytes carry the value's element stride in
// bytes. Both __map_drop_values and __map_set_impl's overwrite-
// dec read the stride straight from the buf (vk = tag & 255,
// stride = tag >> 8) so the runtime can arr_dec / drop_arr_ptr a
// value without the IR threading the stride through every set /
// drop call. Non-array kinds (0/1) carry no stride.
func mapValTag(t ast.Type, ptrW int, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType) int32 {
	kind := mapValKindTag(t, info, genEnumDrops, genTupleDrops, ptrW)
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
// calleeRetainsAnyArg reports whether a direct-call callee MOVES / retains a
// fresh rc arg into a container without an inc — making it unsafe for the
// stage-(b) post-call dec to free that arg (it would UAF the stored element).
// These are exactly the Call escapes computeFreeEligible taints (so a bound-
// equivalent temp there is INELIGIBLE): Map_set moves a fresh value / key into
// the map, Array_push moves a fresh element into the buffer. Variant
// constructors (the third escape) lower via emitEnumNew and never reach the
// generic direct-call arg loop, but are listed for completeness / safety.
func calleeRetainsAnyArg(name string) bool {
	switch name {
	case "__method_Map_set", "__method_Array_push":
		return true
	}
	return false
}

// resultCannotAliasArg reports whether a call result of type t provably
// cannot BE or CONTAIN any rc-tracked argument value — true only for a
// CONCRETE scalar (number incl. usize / bool / float / void). This is the
// stage-(b) safety gate: a post-call arg dec fires immediately, so it is
// only safe when the arg is dead at that point, which requires the result
// not to alias it. A pointer-shaped result could be / wrap the arg (a
// callee returning its own arg — `id`/`pick` — or a struct built from it),
// and an UNRESOLVED generic result (ast.ParamType `T`, or nil) hides
// exactly those identity-return shapes (id[T](x)->x, pick[T](c,a,b)->a|b)
// behind a non-pointer-looking type var — both observed to UAF when the
// result was bound and read later (diff-oracle seeds 1392/1596/1836). So
// only a concrete scalar result opts a call into arg reclamation; every
// other shape keeps its prior safe-leak.
func resultCannotAliasArg(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true
	}
	return false
}

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
	// __heap_bump_bytes(): i32 — Phase 6 measurement probe. Returns the
	// bump allocator's high-water mark in bytes (current cursor minus the
	// region base; 0 before the first allocation). The cursor only moves
	// forward on a fresh bump and never on a freelist reuse, so this is
	// exactly the "did the freelist reclaim?" metric: a loop that frees +
	// reuses each iteration keeps it flat, a leaking loop grows it. Lowered
	// to a runtime-helper call so each backend reads its own cursor store
	// (wasm a fixed linear-memory slot minus allocMinStart, the natives
	// __fern_heap_ptr minus __fern_heap_base).
	if id.Name == "__heap_bump_bytes" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_heap_bump_bytes", I32: 0})
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
		// Stage (c) statement-temp reclamation: when the receiver is a
		// FRESH owned rc temporary (`(a + b).len()` — a string concat /
		// slice), the value-consuming op below (OpStrLen / the array
		// length load) consumes its (data,len) / pointer and returns an
		// i32, dropping the buffer on the floor — nothing dec's it. That's
		// the measured `(a + b).len()` loop leak (1600 → 160000 → 1600000
		// on wasm, no plateau). The receiver is created solely for this
		// call and is DEAD after it (the i32 length cannot alias it), so
		// reclaiming it is exactly as safe as a discarded stage-(a) temp.
		// Stash it in a typed scratch slot, run the op off a reload, then
		// dec the slot — emitOwnedSlotDrop is net-zero, leaving the i32
		// result on top. Non-temp receivers (idents / fields) don't match
		// freshOwnedRcTempType and keep the plain consume. (Array / struct
		// literal receivers are const-folded above, so the shape reaching
		// here in practice is a string concat / slice.)
		lenTempSlot := int32(-1)
		if tt, ok := b.freshOwnedRcTempType(n.Args[0]); ok {
			lenTempSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__lentmp_%d", lenTempSlot)] = lenTempSlot
			b.scratchType[lenTempSlot] = tt
			b.emit(Op{Kind: OpStoreLocal, I32: lenTempSlot})
			b.emit(Op{Kind: OpLoadLocal, I32: lenTempSlot})
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
		if lenTempSlot >= 0 {
			b.emitOwnedSlotDrop(lenTempSlot, b.scratchType[lenTempSlot])
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
		len(n.TypeArgs) >= 2 && mapValKindTag(n.TypeArgs[1], b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW) >= 2 &&
		needsRcIncOnAlias(n.Args[2], b) {
		if err := b.expr(n.Args[2]); err != nil {
			return err
		}
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	// Map[K, string] (two-word ABI — wasm + arm64-TwoWordOverride)
	// set retain: a string value's heap buffer is co-owned by the
	// map's boxed (data, len) cell, so retain an aliased string
	// before it's stored (__fern_str_inc), balancing the
	// __fern_str_dec at map drop / overwrite. Fresh strings (concat
	// / literal / call) aren't aliases (needsRcIncOnAlias == false
	// before #1665's widening; afterwards every string is) → still
	// no-op via the str_inc sentinel guards on literals + inline
	// strings. Strings stay valKind 1 at runtime (unchanged) — the
	// retain is driven by the static type, not the stored tag.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		ast.UseTwoWordStrings(b.ptrW) && needsRcIncOnAlias(n.Args[2], b) {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			if err := b.expr(n.Args[2]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
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
	// would bump the cell's rc, not the string's. arm64 takes the
	// boxed __fern_str_inc branch above (Slice 7), which retains the
	// cell-pointed (data, len) instead of the cell itself.
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
	// Map[string, V] (two-word ABI — wasm + arm64-TwoWordOverride)
	// set KEY retain: the key column co-owns an aliased string
	// key's buffer (boxed (data, len) cell), so __fern_str_inc it,
	// balancing the __fern_str_dec in the __drop_map_str_keys walk
	// at map drop. Fresh keys (concat / literal / call) are moved
	// in with no inc. An OVERWRITE discards the freshly-boxed key
	// (the runtime keeps the existing one), so an aliased overwrite
	// key leaks its inc — safe (no double free), bounded, and keys
	// already leaked entirely pre-slice.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 1 &&
		ast.UseTwoWordStrings(b.ptrW) && needsRcIncOnAlias(n.Args[1], b) {
		if _, isStr := n.TypeArgs[0].(ast.StringType); isStr {
			if err := b.expr(n.Args[1]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
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
	// pointer would bump the cell's rc, not the key string's. arm64
	// takes the boxed __fern_str_inc branch above (Slice 8), which
	// retains the cell-pointed (data, len) instead of the cell itself.
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
		mapValKindTag(n.TypeArgs[1], b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW) == 4 &&
		!exprContainsCall(n.Args[0]) && !exprContainsCall(n.Args[1]) {
		if perVal, ok := mapValHasDrop(n.TypeArgs[1], b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
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
	// Map[K, string] (two-word ABI — wasm + arm64-TwoWordOverride)
	// overwrite pre-drop: m.set(k, v) replacing an existing string
	// value must reclaim the old buffer (the runtime's type-erased
	// overwrite-dec is a no-op for valKind 1). Look up the old
	// value cell (non-retaining) and, if present, __fern_str_dec the
	// (data, len) it holds. The old cell itself leaks (as on map
	// drop). Scoped to the non-boxed-key fast path; m / k must be
	// call-free (the set below re-evaluates them — same idempotence
	// as the kind-4 path).
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		ast.RcFreeEnabled && ast.UseTwoWordStrings(b.ptrW) && !needBoxK &&
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
	// Map[K, string] (native single-word) overwrite pre-drop: the native
	// counterpart of the wasm gate above. Natives store the string data
	// pointer directly in the value slot (no boxing), so __map_lookup_val
	// returns the data pointer instead of a cell pointer — no deref
	// needed; just __fern_rc_dec on the loaded pointer. SSO inline-tag
	// guard in __fern_rc_dec skips inline-packed shorts; literal sentinel
	// short-circuits at data-8. arm64 (ptrW=8 + TwoWordOverride) stays
	// excluded — same gating as the rest of the native string-reclaim
	// path, awaiting boxed-string runtime helpers.
	if id.Name == "__method_Map_set" && len(n.Args) == 3 && len(n.TypeArgs) >= 2 &&
		ast.RcFreeEnabled && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) && !needBoxK &&
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
			b.locals[fmt.Sprintf("__map_overwrite_oldstr_native_%d", oldSlot)] = oldSlot
			b.emit(Op{Kind: OpStoreLocal, I32: oldSlot})
			// if oldPtr != 0: __fern_rc_dec it (low-bit guard handles inline).
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			b.emit(Op{Kind: OpLoadLocal, I32: oldSlot})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
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
				// Map[K, string].iter().value() retain (two-word ABI
				// — wasm + arm64-TwoWordOverride): the returned
				// (data, len) co-owns the buffer alongside the map's
				// column cell, so __fern_str_inc balances the column-
				// walk dec at map drop. Mirrors emitMapGetRebox's
				// boxed-V retain.
				if _, isStr := n.TypeArgs[1].(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
				}
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
				// Map[string, V].iter().key() retain (two-word ABI —
				// wasm + arm64-TwoWordOverride): same rationale as the
				// value retain above.
				if _, isStr := n.TypeArgs[0].(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
				}
				return nil
			}
		}
	}
	// MapIter.value() / MapIter.key() on native single-word string K/V:
	// the runtime returns the string data pointer directly (the boxed
	// branch above doesn't fire because nothing needs boxing) and the
	// caller would hold an un-retained alias the map drop's column walk
	// could later free (UAF). Lower the call inline and __fern_rc_inc
	// the returned pointer so the caller co-owns the buffer. Same SSO
	// inline-tag / literal-sentinel safety as the get / get_or retains.
	// arm64 (ptrW=8 + TwoWordOverride boxed) takes the boxed branch
	// above — its map drop reclaims through the same column-walk path
	// (Slices 7 + 8 landed), so the boxed retain there balances; this
	// native-single-word inline retain is the !TwoWordOverride sibling.
	if b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) && len(n.TypeArgs) >= 2 {
		var retain bool
		switch id.Name {
		case "__method_MapIter_value":
			if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
				retain = true
			}
		case "__method_MapIter_key":
			if _, isStr := n.TypeArgs[0].(ast.StringType); isStr {
				retain = true
			}
		}
		if retain {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			b.emit(Op{Kind: OpCallDirect, Str: id.Name, I32: int32(len(n.Args))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
			return nil
		}
	}
	// Stage (b) statement-temp reclamation: a FRESH owned rc temporary
	// passed as a borrowed arg to a normal direct call (`foo(a + b)`) is
	// never dec'd by anyone — the callee borrows it (no callee-side dec
	// under the Phase-2d borrow model) and the caller drops the operand on
	// the floor. So stash each such arg's value in a scratch slot and dec
	// it right after the call.
	//
	// CRITICAL safety gate — CONCRETE-SCALAR RESULT ONLY
	// (resultCannotAliasArg). The dec fires immediately after the call, so
	// it is only safe when the arg is DEAD at that point. If the callee
	// RETURNS its arg (`pick[T](c,a,b)` → `if c { a } else { b }`;
	// `id[T](x)` → `x`) the result aliases the arg, and a caller that binds
	// / reads the result later (`var v = pick(false, x, Pair{...}); v.fst`)
	// would touch freed memory (observed: diff-oracle seeds 1392/1596/1836
	// segfault). Only a concrete scalar result (number / bool / float /
	// void) provably cannot BE or CONTAIN the arg. Note a pointer result is
	// excluded, AND so is an unresolved generic result — b.exprType(n)
	// returns the generic's bare type var `T` (ast.ParamType), which is the
	// exact shape an identity-return hides behind, so resultCannotAliasArg
	// rejects it too. Every excluded shape keeps its prior safe-leak.
	//
	// Further gated to the plain direct-call path (a FuncSig callee, not
	// shadowed by a local, not pair-form, not map_new's arg-injecting
	// builtin) and EXCLUDING retain sinks: __method_Map_set MOVES a fresh
	// value/key into the map with no inc (see the set-retain block above —
	// "transfer their rc=1 to the map"), so dec'ing it would free the stored
	// element (UAF); __method_Array_push is the array analogue.
	_, calleeIsLocal := b.locals[id.Name]
	_, calleeIsFunc := b.info.FuncSigs[id.Name]
	reclaimArgTemps := ast.RcFreeEnabled && calleeIsFunc && !calleeIsLocal &&
		(resultCannotAliasArg(b.exprType(n)) || b.returnsNoParamEscape[id.Name]) &&
		!b.pairForm[id.Name] && id.Name != "map_new" && !calleeRetainsAnyArg(id.Name)
	var argTempSlots []int32
	var argTempTypes []ast.Type
	// An `own` (consuming) parameter takes ownership of its argument, so the
	// callee — not the caller — reclaims a fresh temp passed there. Suppress the
	// stage-(b) post-call dec at those positions (else the temp is freed twice).
	ownArgFlags := b.info.OwnFuncs[id.Name]
	for ai, a := range n.Args {
		toOwnParam := ai < len(ownArgFlags) && ownArgFlags[ai]
		if reclaimArgTemps && !toOwnParam {
			// An owned-temp arg is either a fresh-allocating literal shape
			// (freshOwnedRcTempType) OR a fresh-returning user-function call
			// (ownedCallResultType — `take(mk(i))` leaked the mk result: the
			// callee borrows it and nothing dec'd it). Both reclaim the same
			// way (stash + post-call dec). The call-result case is sound for
			// the same reasons the discarded `mk(i);` case is — the is_unique
			// gate in emitOwnedSlotDrop only frees a uniquely-owned rc==1
			// result, and the enclosing call's concrete-scalar result
			// (resultCannotAliasArg) means it can't hand the arg back.
			tt, ok := b.freshOwnedRcTempType(a)
			if !ok {
				tt, ok = b.ownedCallResultType(a)
			}
			if ok {
				// Evaluate into a scratch slot (typed so two-word strings
				// store/load correctly), then reload for the call. Records
				// the slot for the post-call dec below.
				slot := b.allocSlot()
				b.locals[fmt.Sprintf("__argtmp_%d", slot)] = slot
				b.scratchType[slot] = tt
				if err := b.expr(a); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				argTempSlots = append(argTempSlots, slot)
				argTempTypes = append(argTempTypes, tt)
				continue
			}
		}
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
			valKind = mapValTag(n.TypeArgs[1], b.ptrW, b.info, b.genEnumDrops, b.genTupleDrops)
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
			// Stage (b): dec each stashed owned-temp arg now that the
			// call has consumed (borrowed) it. emitOwnedSlotDrop is
			// net-zero on the operand stack, so the call's result (if
			// any) sitting underneath is left untouched. reclaimArgTemps
			// required kind == OpCallDirect (not pair-form), so the
			// result is a single value / void — never the rebox'd pair.
			for i, slot := range argTempSlots {
				b.emitOwnedSlotDrop(slot, argTempTypes[i])
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

// structReuseEligible reports whether every field of the struct is a
// shape the Phase 5b/5c drop-reuse path handles: an i32-class integer
// scalar (≤ 32 bits) OR a single-word rc-tracked pointer
// (array / struct / Map / enum / closure / tuple — `arrElemIsRcTracked`).
// Excluded, falling back to the normal dec-on-overwrite + fresh alloc:
//   - strings (two-word on wasm / boxed on arm64 — the reuse temps are
//     single-word; their rc release also diverges per backend),
//   - wide / float scalars (i64 / f64 / f32 — would need width-correct
//     temp slots; the temps here are i32/pointer-width),
//   - bool and anything else not in the two categories above.
//
// fieldCarriedFrom reports whether the struct-literal field value `val` is
// exactly `<targetName>.<fieldName>` — the field is carried over unchanged
// from the struct being self-overwritten. Drives field-store elision in
// tryStructReuseOverwrite (the reuse branch keeps the box's existing value).
func (b *builder) fieldCarriedFrom(val ast.Expr, targetName, fieldName string) bool {
	fa, ok := val.(*ast.FieldAccess)
	if !ok {
		return false
	}
	id, ok := fa.Target.(*ast.Ident)
	return ok && id.Name == targetName && fa.Field == fieldName
}

func structReuseEligible(sd *ast.StructDecl) bool {
	for _, f := range sd.Fields {
		if nt, ok := f.Type.(ast.NumberType); ok && nt.NormalWidth() <= 32 {
			continue
		}
		if arrElemIsRcTracked(f.Type) {
			continue
		}
		return false
	}
	return true
}

// tupleReuseEligible is the tuple analogue of structReuseEligible: every
// element is an i32-class scalar OR a single-word rc-tracked pointer (array /
// struct / Map / enum / closure / tuple). Strings (two-word) and wide/float
// scalars are excluded, exactly as for structs — single-word temps only, and
// the old-element release rides emitFieldDropOnStack.
func tupleReuseEligible(elems []ast.Type) bool {
	if len(elems) == 0 {
		return false
	}
	for _, e := range elems {
		if nt, ok := e.(ast.NumberType); ok && nt.NormalWidth() <= 32 {
			continue
		}
		if arrElemIsRcTracked(e) {
			continue
		}
		return false
	}
	return true
}

// computeReuseSources is the general-FBIP pairing analysis: it matches a
// construction site C (a `var c = T{…}` / `c = (…)` whose RHS is a plain
// StructLit or a TupleLit) with a DEAD, OWNED struct/tuple local D whose box C
// can reuse in place — the Perceus reuse token threaded from D's drop to C's
// allocation, generalised beyond the self-overwrite tryStructReuseOverwrite
// (where D == C). Returns the C→D map (keyed by the StructLit / TupleLit node)
// and the set of consumed D names (so computePreciseDrops won't ALSO drop
// them). D and C must be the same KIND (struct↔struct or tuple↔tuple).
//
// First cut — deliberately narrow and obviously sound:
//   - D and C are the SAME `structReuseEligible` struct type, so the box sizes
//     match exactly. Fields may be i32-class scalars OR single-word rc-tracked
//     pointers (array / struct / Map / enum / closure / tuple — strings and
//     wide/float scalars are still excluded, same gate as the self-overwrite
//     5c path). D is DEAD at C, so C never carries a field from D: every one
//     of D's old pointer-field references is RELEASED (deep freeing drop) on
//     the reuse branch before C's stores overwrite them, and each of C's new
//     pointer fields is retained on eval as normal StructLit construction.
//   - D and C are in the SAME statement list (block): the function body OR any
//     nested block (loop body, if arm). Pairing within a loop body is the
//     high-value case — a per-iteration `var a = T{…}; …; var b = T{…}` reuses
//     a's box for b every iteration. A block-scoped D dies at block exit, so
//     "dead from C within the block" is sufficient.
//   - D is a `var`, declared before C in the list, never reassigned,
//     name-unique (no shadowing ambiguity), and `freeEligible` (OWNED — never
//     a borrowed param; the runtime is_unique check at the reuse site is the
//     second gate, so a shared D copies).
//   - D is DEAD from C onward within its block: referenced in no statement at
//     or after C's index (so C's fields don't read D, and nothing observes D's
//     box after C repurposes it). The reuse zeroes D's slot, so the exit sweep
//     — and any path that DOESN'T reach C — null-guards to a no-op / drops D
//     normally. A mispaired or shared D degrades to dec-then-fresh-alloc
//     (never unsound, never a leak — the same invariant as __alloc_reuse).
func (b *builder) computeReuseSources() (map[ast.Expr]string, map[string]bool) {
	sources := map[ast.Expr]string{}
	consumed := map[string]bool{}
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled || b.fn.Body == nil {
		return sources, consumed
	}

	// Reassigned-at-any-depth set (a reassigned D's box at C is still owned,
	// but excluding them keeps the first cut simple, matching precise-drop).
	reassigned := map[string]bool{}
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				reassigned[id.Name] = true
			}
		}
		return true
	})

	references := func(st ast.Node, name string) bool {
		found := false
		ast.Walk(st, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		return found
	}
	const rcHeaderBytes = 8
	// reuseClassOf returns a local's box "kind" (struct / tuple / enum), its
	// type name (empty for tuples), and its freelist class — (alloc+15)&-16 of
	// data+rc-header, within the exact-fit ≤ 2048 range — for any general-FBIP
	// reuse-eligible struct / tuple / enum local. ok=false for anything else
	// (non-box type, a string/wide-scalar field/element, a non-uniform enum, or
	// > 2048).
	reuseClassOf := func(name string) (kind string, typeName string, class int32, ok bool) {
		t, ok2 := b.localDeclType(name)
		if !ok2 {
			return "", "", 0, false
		}
		switch tt := t.(type) {
		case ast.StructType:
			sd, ok3 := b.info.Structs[tt.Name]
			if !ok3 || !structReuseEligible(sd) {
				return "", "", 0, false
			}
			_, size := structFieldLayout(sd.Fields, b.ptrW)
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "struct", tt.Name, (alloc + 15) &^ 15, true
		case ast.TupleType:
			if !tupleReuseEligible(tt.Elems) {
				return "", "", 0, false
			}
			_, size := tupleElemLayout(tt.Elems, b.ptrW)
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "tuple", "", (alloc + 15) &^ 15, true
		case ast.EnumType:
			ed, ok3 := b.info.Enums[tt.Name]
			if !ok3 {
				return "", "", 0, false
			}
			if len(tt.Args) > 0 {
				ed = substituteEnumDecl(ed, tt.Args)
			}
			_, size, eok := b.enumReuseLoads(ed)
			if !eok {
				return "", "", 0, false
			}
			alloc := size + rcHeaderBytes
			if alloc > 2048 {
				return "", "", 0, false
			}
			return "enum", tt.Name, (alloc + 15) &^ 15, true
		}
		return "", "", 0, false
	}
	// constructionAt extracts (targetName, constructionNode) from a
	// `var c = T{…}` / `c = (…)` / `c = Variant(…)` (or the assign forms) whose
	// RHS is a plain (non-update) StructLit, a TupleLit, or a payload-carrying
	// enum variant constructor call. The node keys reuseSources.
	constructionAt := func(st ast.Stmt) (string, ast.Expr) {
		rhsConstruction := func(e ast.Expr) ast.Expr {
			switch v := e.(type) {
			case *ast.StructLit:
				if v.Base == nil {
					return v
				}
			case *ast.TupleLit:
				return v
			case *ast.Call:
				// A payload-carrying enum variant constructor (`Wrap(x)`).
				// Payloadless variants lower to a shared sentinel (no box to
				// reuse), and a shadowing local rules out a constructor ref.
				if callee, ok := v.Callee.(*ast.Ident); ok {
					if _, isLocal := b.locals[callee.Name]; !isLocal {
						if _, _, pc, isVar := b.lookupVariant(callee.Name); isVar && pc > 0 {
							return v
						}
					}
				}
			}
			return nil
		}
		switch s := st.(type) {
		case *ast.Var:
			if c := rhsConstruction(s.Init); c != nil {
				return s.Name, c
			}
		case *ast.ExprStmt:
			if a, ok := s.Expr.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					if c := rhsConstruction(a.Value); c != nil {
						return id.Name, c
					}
				}
			}
		}
		return "", nil
	}

	// attemptPair tries to pair construction C (cName / cNode) with a dead,
	// owned source D drawn from `declIdx` (name → declaration index in some
	// statement list), where D must be declared before `k` and dead from `k`
	// onward per `deadFrom`. Used by BOTH the same-block pass (declIdx/k/deadFrom
	// scoped to C's own block) and the cross-block pass (scoped to the function
	// body, with k the top-level statement that ENCLOSES a nested C). Records the
	// pairing in `sources`/`consumed` and returns true on success.
	attemptPair := func(cName string, cNode ast.Expr, declIdx map[string]int, k int, deadFrom func(string, int) bool) bool {
		cKind, cTypeName, cClass, ok := reuseClassOf(cName)
		if !ok {
			return false
		}
		switch cn := cNode.(type) {
		case *ast.StructLit:
			if cKind != "struct" || cTypeName != cn.TypeName {
				return false
			}
		case *ast.TupleLit:
			if cKind != "tuple" {
				return false
			}
		case *ast.Call:
			if cKind != "enum" {
				return false
			}
		}
		// Choose deterministically (smallest decl index, tie-broken by name):
		// Go map iteration is per-process randomised, so picking the "first"
		// eligible D would make codegen non-reproducible — fatal for the
		// byte-equal self-host gate when two D's qualify for one C.
		bestD, bestDi := "", -1
		for dName, di := range declIdx {
			if di >= k || dName == cName || consumed[dName] || reassigned[dName] {
				continue
			}
			// A D whose box was MOVED into another live container (an array /
			// struct / tuple / closure element, per markConstructionMoves) is
			// freeEligible but no longer owns its box — that box is now reachable
			// through the container. Reusing it in place for C would alias C's
			// fresh value onto the element the container still points at
			// (observed: `var a=[d]; var c=T{…}` made a[0] read as c). The exit
			// sweep already excludes movedLocals (computePreciseDrops); the reuse
			// pass must too.
			if b.movedLocals[dName] {
				continue
			}
			if !b.freeEligible[dName] || !b.localNameUnique(dName) {
				continue
			}
			dKind, dTypeName, dClass, ok := reuseClassOf(dName)
			if !ok || dKind != cKind {
				continue
			}
			// Same NAMED struct/enum pairs at any size (D's box is reused as
			// itself). Otherwise (a different struct type, or a tuple) pair
			// only when D and C fall in the SAME freelist class — C's box
			// fits D's reused block and __alloc_reuse's runtime class check
			// matches. D's old fields are released and C's stored using each
			// one's OWN layout (see the hooks). Enums require the same type
			// (their old-payload free walks D's uniform drop loads; pairing a
			// different enum of equal class is left to a later cut).
			sameNamed := (cKind == "struct" || cKind == "enum") && dTypeName == cTypeName
			if cKind == "enum" && !sameNamed {
				continue
			}
			if !sameNamed && dClass != cClass {
				continue
			}
			if !deadFrom(dName, k) {
				continue
			}
			if bestD == "" || di < bestDi || (di == bestDi && dName < bestD) {
				bestD, bestDi = dName, di
			}
		}
		if bestD != "" {
			sources[cNode] = bestD
			consumed[bestD] = true
			return true
		}
		return false
	}

	// declIndices / deadFromIn build the per-statement-list machinery shared by
	// both passes: declIdx maps a top-level `var` name to its index, and
	// deadFrom reports whether a name is referenced in NO statement at index
	// >= k of that list.
	declIndices := func(stmts []ast.Stmt) map[string]int {
		declIdx := map[string]int{}
		for i, st := range stmts {
			if v, ok := st.(*ast.Var); ok {
				if _, dup := declIdx[v.Name]; !dup {
					declIdx[v.Name] = i
				}
			}
		}
		return declIdx
	}
	deadFromIn := func(stmts []ast.Stmt) func(string, int) bool {
		return func(name string, k int) bool {
			for i := k; i < len(stmts); i++ {
				if references(stmts[i], name) {
					return false
				}
			}
			return true
		}
	}

	// SAME-BLOCK pass: every block in the function — the body and each nested
	// loop / if arm — is its own statement list with block-scoped locals. A
	// construction C pairs with a D declared earlier in (and dead from C onward
	// within) the SAME list. This is the high-value case (loop-body churn).
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if blk, ok := n.(*ast.Block); ok {
			declIdx := declIndices(blk.Stmts)
			deadFrom := deadFromIn(blk.Stmts)
			for k, st := range blk.Stmts {
				if cName, cNode := constructionAt(st); cNode != nil {
					attemptPair(cName, cNode, declIdx, k, deadFrom)
				}
			}
		}
		return true
	})

	// CROSS-BLOCK pass: a block-top-level local D dominates and outlives a
	// construction C NESTED inside a LATER top-level statement of that same
	// block (an if / loop / nested block). D pairs with C when D is dead from
	// that enclosing top-level statement onward across the WHOLE block —
	// deadFrom over the block's stmts rejects any use of D after k on any path
	// (a sibling branch, the rest of C's block, or a post-merge use), so
	// reusing D's box on the C-path and zeroing its slot can never strand a
	// live read; the not-taken path leaves D's slot intact for the exit sweep /
	// the next-iteration reinit drop. The args-alias hazard is excluded
	// structurally by freeEligible[D] (a D whose field/element aliases a live
	// local is tainted out) — and arrays are never reuse sources.
	//
	// This generalises the function-body case to EVERY block, so the dominant
	// shape — a loop-body D (`var a = …`) reused by a construction nested in an
	// `if` inside the loop — fires every iteration (D is block-scoped, so it's
	// re-declared and the slot reinit-dropped each turn). Only C's not already
	// paired (same-block, or a CLOSER cross-block ancestor) are considered:
	// blocks are visited descendant-before-ancestor (reversed pre-order), so the
	// innermost eligible D — the most natural, per-iteration reuse — wins.
	var blocks []*ast.Block
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		if blk, ok := n.(*ast.Block); ok {
			blocks = append(blocks, blk)
		}
		return true
	})
	for i := len(blocks) - 1; i >= 0; i-- {
		blkStmts := blocks[i].Stmts
		declIdx := declIndices(blkStmts)
		deadFrom := deadFromIn(blkStmts)
		for k, st := range blkStmts {
			ast.Walk(st, func(n ast.Node) bool {
				inner, ok := n.(ast.Stmt)
				if !ok || ast.Node(inner) == ast.Node(st) {
					return true // skip the enclosing top-level statement itself
				}
				cName, cNode := constructionAt(inner)
				if cNode == nil {
					return true
				}
				if _, done := sources[cNode]; done {
					return true // already paired (same-block, or a closer ancestor)
				}
				attemptPair(cName, cNode, declIdx, k, deadFrom)
				return true
			})
		}
	}
	return sources, consumed
}

// reuseSourceLayout returns the parallel (offset, type) slices and data size
// of a general-FBIP reuse source local D — a struct OR tuple. Used by the
// StructLit / TupleLit reuse hooks to compute D's allocation size (tokenSize)
// and to walk D's OLD pointer fields/elements at D's own offsets when its box
// is handed off (D may be a different type — even a different KIND — than C).
func (b *builder) reuseSourceLayout(dName string) (offsets []int32, types []ast.Type, size int32) {
	t, _ := b.localDeclType(dName)
	switch dt := t.(type) {
	case ast.StructType:
		sd := b.info.Structs[dt.Name]
		offMap, sz := structFieldLayout(sd.Fields, b.ptrW)
		offsets = make([]int32, len(sd.Fields))
		types = make([]ast.Type, len(sd.Fields))
		for i, f := range sd.Fields {
			offsets[i] = offMap[f.Name]
			types[i] = f.Type
		}
		return offsets, types, sz
	case ast.TupleType:
		offs, sz := tupleElemLayout(dt.Elems, b.ptrW)
		return offs, dt.Elems, sz
	case ast.EnumType:
		ed := b.info.Enums[dt.Name]
		if len(dt.Args) > 0 {
			ed = substituteEnumDecl(ed, dt.Args)
		}
		loads, sz, _ := b.enumReuseLoads(ed)
		offsets = make([]int32, len(loads))
		types = make([]ast.Type, len(loads))
		for i, ld := range loads {
			offsets[i] = ld.off
			types[i] = ld.typ
		}
		return offsets, types, sz
	}
	return nil, nil, 0
}

// enumReuseLoads is the general-FBIP eligibility + old-payload-free layout for
// an enum reuse source D, mirroring tryEnumReuseOverwrite's gate exactly: the
// enum must have a uniform box size, and either be uniform-droppable with NO
// string payload (the rc-pointer loads to free on the reuse branch) or be
// scalar-only (nothing to free). ok=false otherwise — that enum declines reuse.
// The returned loads carry the variant-INDEPENDENT payload offsets (so the
// old-payload free needs no runtime tag guard), and freeEligible[D] guarantees
// those payloads alias nothing live (the same soundness basis the self-overwrite
// enum reuse relies on).
func (b *builder) enumReuseLoads(ed *ast.EnumDecl) (loads []enumDropLoad, size int32, ok bool) {
	if ed == nil {
		return nil, 0, false
	}
	sz, sizeOk := uniformEnumBoxSize(ed, b.ptrW)
	if !sizeOk {
		return nil, 0, false
	}
	if lds, uok := uniformEnumDropLoads(ed, b.ptrW); uok {
		for _, ld := range lds {
			if _, isStr := ld.typ.(ast.StringType); isStr {
				return nil, 0, false // string payload — needs two-word str_dec
			}
		}
		return lds, sz, true
	}
	// Not uniform-droppable: only scalar-only enums are safe (nothing to free).
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if _, isStr := pt.(ast.StringType); isStr {
				return nil, 0, false
			}
			if arrElemIsRcTracked(pt) {
				return nil, 0, false
			}
		}
	}
	return nil, sz, true
}

// emitReuseToken lowers the general-FBIP reuse allocation for a construction C
// paired with the dead, owned source local D. It emits the runtime is_unique
// gate, the token select (`reused ? base(D) : 0`; the shared/null branch dec's
// D so its alias keeps the box and __alloc_reuse falls through to a fresh
// alloc), zeroes D's slot (consumed — so the exit sweep / a non-C path never
// double-releases), and the __alloc_reuse call — leaving the box BASE pointer
// on the operand stack exactly where OpAlloc would. dAlloc / cAlloc are D's and
// C's allocation sizes (data + rc header); tokenSize is D's real size so a
// runtime class mismatch frees D's block to ITS class. Returns the slot holding
// the i32 is_unique result so the caller can gate the old-field release.
func (b *builder) emitReuseToken(dName string, dAlloc, cAlloc int32) int32 {
	const rcHeaderBytes = 8
	dSlot := b.locals[dName]
	reusedSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__reuse_src_uniq_%d", reusedSlot)] = reusedSlot
	b.emit(Op{Kind: OpLoadLocal, I32: dSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: reusedSlot})
	tokenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__reuse_src_tok_%d", tokenSlot)] = tokenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: dSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: dSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpEnd})
	// D consumed (box taken or dec'd) — zero its slot.
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: dSlot})
	// base = __alloc_reuse(token, D_alloc, C_alloc).
	b.emit(Op{Kind: OpLoadLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpConstI32, I32: dAlloc})
	b.emit(Op{Kind: OpConstI32, I32: cAlloc})
	b.emit(Op{Kind: OpCallDirect, Str: "__alloc_reuse", I32: 3})
	return reusedSlot
}

// emitReuseOldFieldDrops releases D's OLD pointer fields/elements from the
// reused box on the REUSE branch (gated reusedSlot), before C's stores
// overwrite them. D is dead at C, so every pointer slot is replaced — each old
// reference is deep-freeing-dropped (emitFieldDropOnStack). On the decline
// branch the box is fresh, so this is gated out. offsets/types are D's OWN
// layout (reuseSourceLayout). Scalar slots need no drop.
func (b *builder) emitReuseOldFieldDrops(reusedSlot, baseSlot int32, offsets []int32, types []ast.Type) {
	const rcHeaderBytes = 8
	hasPtr := false
	for _, t := range types {
		if arrElemIsRcTracked(t) {
			hasPtr = true
			break
		}
	}
	if !hasPtr {
		return
	}
	b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	for i, t := range types {
		if !arrElemIsRcTracked(t) {
			continue
		}
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offsets[i]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		b.emitFieldDropOnStack(t)
	}
	b.emit(Op{Kind: OpEnd})
}

// tryStructReuseOverwrite lowers a self-overwrite `p = T{ ... }` (where
// p is an owned, uniquely-droppable struct local of the same type T,
// every field of which is structReuseEligible) so the new value reuses
// p's old box in place when it's the sole owner — the Phase 5b/5c
// constructor-reuse (FBIP) win. Returns (true, err) when it took the
// reuse path (the caller returns immediately), (false, nil) when the
// shape isn't eligible and normal lowering should proceed.
//
// Soundness:
//   - Gated on b.freeEligible[p] (OWNED, not a borrowed param / alias):
//     a borrowed value can be rc==1 while the caller still holds it, so
//     static ownership — not just the runtime rc check — is required
//     before reusing storage in place.
//   - The runtime is_unique check is the second gate: reuse fires only
//     at rc==1. An aliased p (rc>1) is dec'd and a fresh box allocated,
//     so the alias keeps the old value intact.
//   - All field expressions are evaluated into temps BEFORE the box is
//     reused, so a field that reads p (`x: p.x + 1`) sees the old value
//     even though the box it lives in is about to be overwritten — no
//     read-after-overwrite hazard, including field swaps.
//   - Pointer fields: each new value is retained on eval (emitAliasInc
//     for an alias-shaped RHS, same as normal StructLit construction).
//     On the REUSE branch only, the box's OLD pointer-field values are
//     deep-dropped (emitFieldDropOnStack, a FREEING drop) before the new
//     ones overwrite them. For a carried-over field (`name: p.name`) the
//     eval-inc and this drop balance, so its rc is unchanged; for a
//     REPLACED field the old reference reaches rc 0 and its buffer/box is
//     freed (5f — no longer the leak-but-never-UAF flat dec). This is
//     SOUND, not the deferred-alias hazard the old note feared, precisely
//     because construction inc's the new field: any live alias of the old
//     buffer (including one read in the self-overwrite RHS,
//     `items: ident(p.items)`) holds a counted reference, so the freeing
//     drop only reclaims the genuine last one (the field's own is_unique
//     gate dec's a shared buffer instead of freeing it). The drop is gated
//     on the i32 is_unique result (not the raw token pointer) so the branch
//     condition is backend-safe truthiness. (Contrast tryEnumReuseOverwrite,
//     where construction does NOT count payloads, so its old payload still
//     flat-leaks — a separate open item.)
//   - tokenSize == size (same type T), so __alloc_reuse's class check
//     always matches on the reuse path and never frees.
func (b *builder) tryStructReuseOverwrite(n *ast.Assign, t *ast.Ident, idx int32) (bool, error) {
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled {
		return false, nil
	}
	sl, ok := n.Value.(*ast.StructLit)
	if !ok {
		return false, nil
	}
	st, ok := b.exprStaticType(t).(ast.StructType)
	if !ok || st.Name != sl.TypeName {
		return false, nil
	}
	sd, ok := b.info.Structs[st.Name]
	if !ok || !structReuseEligible(sd) {
		return false, nil
	}
	if !b.freeEligible[t.Name] {
		return false, nil
	}

	offs, size := structFieldLayout(sd.Fields, b.ptrW)
	const rcHeaderBytes = 8

	// 1. Evaluate every field value into a single-word temp. These reads
	//    of the OLD p (still live in slot idx) all complete before the
	//    box is reused below. Pointer fields are retained here exactly
	//    as normal StructLit construction does (Phase 1d-viii).
	type fieldTemp struct {
		name    string
		slot    int32
		ptr     bool
		carried bool // value is exactly `p.<name>` — unchanged, kept on reuse
	}
	temps := make([]fieldTemp, 0, len(sl.Fields))
	hasPtr := false
	hasCarried := false
	for _, f := range sl.Fields {
		isPtr := arrElemIsRcTracked(fieldType(sd.Fields, f.Name))
		// Field-store elision (Perceus reuse specialization): a field whose
		// value is literally `p.<sameName>` is carried over UNCHANGED. On the
		// reuse branch the same box already holds it, so its retain + store +
		// old-value release are all redundant and elided — the box keeps its
		// existing reference (rc unchanged). This is the dominant case of
		// Fern's record-update idiom: E048 forbids field assignment, so an
		// update is written `p = T{ changed: ..., rest: p.rest, ... }`. The
		// retain + store are still emitted on the FRESH-alloc path below
		// (a new box needs its own copy + reference).
		carried := b.fieldCarriedFrom(f.Value, t.Name, f.Name)
		if err := b.expr(f.Value); err != nil {
			return true, err
		}
		if isPtr && !carried && needsRcIncOnAlias(f.Value, b) && !b.moveSites[f.Value] {
			b.emitAliasInc(f.Value)
		}
		ts := b.allocSlot()
		b.locals[fmt.Sprintf("__reuse_fld_%d", ts)] = ts
		b.emit(Op{Kind: OpStoreLocal, I32: ts})
		temps = append(temps, fieldTemp{f.Name, ts, isPtr, carried})
		hasPtr = hasPtr || isPtr
		hasCarried = hasCarried || carried
	}

	// 2. reused = is_unique(old) (an i32 0/1, captured so both the token
	//    select and the old-field-dec branch can read it). token =
	//    reused ? base(old) : 0; the aliased / null / sentinel branch
	//    dec's the old box (the alias keeps it) and yields 0 so
	//    __alloc_reuse allocates a fresh box.
	reusedSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__reuse_uniq_%d", reusedSlot)] = reusedSlot
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: reusedSlot})

	tokenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__reuse_tok_%d", tokenSlot)] = tokenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpEnd})

	// 3. base = __alloc_reuse(token, size+hdr, size+hdr).
	boxSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__reuse_box_%d", boxSlot)] = boxSlot
	b.emit(Op{Kind: OpLoadLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpCallDirect, Str: "__alloc_reuse", I32: 3})
	b.emit(Op{Kind: OpStoreLocal, I32: boxSlot})

	// 4. REUSE branch only: release the box's OLD pointer-field values
	//    before the new ones overwrite them. On a fresh box (reused==0)
	//    the slots are uninitialised, so this is gated on the is_unique
	//    result. Per-field deep drop (emitFieldDropOnStack): the field's
	//    own is_unique gate means a carried-over field (its eval-inc above
	//    bumped it to rc>1) is only dec'd, while a REPLACED field's old
	//    reference reaches rc 0 and its buffer/box is freed — fixing the
	//    leak the prior flat __fern_rc_dec left (rc_dec has no free path,
	//    so a replaced array field's buffer was orphaned every iteration).
	//    The rc arithmetic is unchanged (still one dec per field); only the
	//    rc-0 free is added, so there's zero over-release surface.
	if hasPtr {
		b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
		b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
		for _, tp := range temps {
			if !tp.ptr || tp.carried {
				// A carried-over field keeps its old value (no replacement),
				// so it must NOT be released — and its step-1 retain was
				// elided to match.
				continue
			}
			b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
			b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offs[tp.name]})
			b.emit(Op{Kind: OpAdd})
			b.emit(Op{Kind: OpLoad, Width: WidthPtr})
			b.emitFieldDropOnStack(fieldType(sd.Fields, tp.name))
		}
		b.emit(Op{Kind: OpEnd})
	}

	// 5. rc = 1 at [base+0] (already 1 on the reuse path; set fresh
	//    otherwise).
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})

	// 6. Store the CHANGED field temps at [base + hdr + off]. Carried-over
	//    fields are skipped here — on the reuse branch the box already holds
	//    them; on the fresh-alloc branch they're stored in step 6b.
	for _, tp := range temps {
		if tp.carried {
			continue
		}
		b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offs[tp.name]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoadLocal, I32: tp.slot})
		b.emit(payloadStoreOpFor(fieldType(sd.Fields, tp.name), b.ptrW))
	}

	// 6b. FRESH-alloc branch only (reused==0): a fresh box has no field
	//     values, so the carried-over fields must be stored + (pointer
	//     fields) retained here — exactly what the reuse branch elides.
	if hasCarried {
		b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
		b.emit(Op{Kind: OpNot}) // reused == 0 → fresh
		b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
		for _, tp := range temps {
			if !tp.carried {
				continue
			}
			if tp.ptr {
				// Retain the carried pointer for the fresh box's own
				// reference (the reuse branch keeps the box's existing one).
				b.emit(Op{Kind: OpLoadLocal, I32: tp.slot})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_inc", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
			b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
			b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offs[tp.name]})
			b.emit(Op{Kind: OpAdd})
			b.emit(Op{Kind: OpLoadLocal, I32: tp.slot})
			b.emit(payloadStoreOpFor(fieldType(sd.Fields, tp.name), b.ptrW))
		}
		b.emit(Op{Kind: OpEnd})
	}

	// 7. p = data (= base + hdr); leave the tee for expression position.
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: idx})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	return true, nil
}

// tryEnumReuseOverwrite is the enum analogue of tryStructReuseOverwrite
// (Phase 5e). A self-overwrite of an owned enum local with a freshly
// constructed, payload-carrying variant of the SAME enum reuses the old
// box's storage in place when the box is uniquely owned at runtime — so
// a loop like `c = Step(c.n + 1)` allocates zero boxes per iteration.
//
// Crucially, this changes NO rc accounting versus the baseline
// (emitEnumNew + the flat __fern_rc_dec the overwrite-dec emits for a
// non-array enum target). Enum construction does NOT inc its payloads,
// and the baseline overwrite-dec does NOT release the old box's payloads
// (it flat-dec's the box only — old payloads leak). So reuse mirrors
// that exactly: no arg inc, no old-payload release. That makes it a pure
// alloc-elision with ZERO over-release surface — the opposite of the
// struct case, where StructLit construction incs fields and the reuse
// path must therefore release old fields to balance. On the unique path
// the old box (rc==1) is repurposed as the new value (rc stays 1); on
// the aliased / sentinel path it's flat-dec'd and a fresh box allocated,
// byte-for-byte the baseline. Reuse even leaks strictly less: it
// reclaims the box the baseline orphans at rc 0.
//
// Gated on uniformEnumBoxSize: every payload-carrying variant must share
// one box size, so the constructed variant always fits the old box no
// matter which variant it currently holds. freeEligible gates out
// borrowed locals whose runtime is_unique can spuriously read 1 (the
// borrow model gives no caller-side inc) — reusing those would corrupt
// the caller's value, the same UAF guard the array-free overwrite path
// uses.
func (b *builder) tryEnumReuseOverwrite(n *ast.Assign, t *ast.Ident, idx int32) (bool, error) {
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled {
		return false, nil
	}
	call, ok := n.Value.(*ast.Call)
	if !ok {
		return false, nil
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false, nil
	}
	if _, isLocal := b.locals[callee.Name]; isLocal {
		return false, nil // shadowed by a local — not a constructor ref
	}
	enumName, varIdx, payloadCount, isVariant := b.lookupVariant(callee.Name)
	if !isVariant || payloadCount == 0 {
		return false, nil // payloadless ⇒ static sentinel, no box to reuse
	}
	et, ok := b.exprStaticType(t).(ast.EnumType)
	if !ok || et.Name != enumName {
		return false, nil
	}
	if !b.freeEligible[t.Name] {
		return false, nil
	}
	ed, ok := b.info.Enums[enumName]
	if !ok {
		return false, nil
	}
	if len(et.Args) > 0 {
		ed = substituteEnumDecl(ed, et.Args)
	}
	size, sizeOk := uniformEnumBoxSize(ed, b.ptrW)
	if !sizeOk {
		return false, nil // variants disagree on box size — can't reuse
	}
	const rcHeaderBytes = 8

	// 5f-enum (replaced-payload free): the in-place reuse keeps the OLD box,
	// so the normal overwrite-dec that would free its payload never runs —
	// `c = Wrap([..])` in a loop leaked the prior payload every iteration.
	// Free the OLD payload on the reuse branch before it's overwritten.
	//
	// Sound for the same reason the normal enum overwrite-free is: the reuse
	// path already requires freeEligible[t], a whole-function property that
	// (via rhsTainted propagation through variant-constructor args) is false
	// whenever any value `c` holds has a payload aliasing a live local. So an
	// `c` that reaches here only ever holds payloads with no live alias —
	// freeing the old one reclaims the genuine last reference (each per-type
	// drop is_unique-gates again, so a shared buffer would only dec).
	//
	// We free via emitFieldDropOnStack (the freeing drop) at the uniform
	// droppable offsets. Enums that aren't uniform-droppable, or carry a
	// string payload (which needs the two-word str_dec rather than
	// emitFieldDropOnStack), DECLINE reuse and fall to the normal overwrite
	// path — which frees the old payload soundly (and bounded — verified) at
	// the cost of a fresh box alloc. Scalar-only enums have nothing to free.
	var reuseDropLoads []enumDropLoad
	if loads, uok := uniformEnumDropLoads(ed, b.ptrW); uok {
		for _, ld := range loads {
			if _, isStr := ld.typ.(ast.StringType); isStr {
				return false, nil // string payload — let the normal path free it
			}
		}
		reuseDropLoads = loads
	} else {
		// Not uniform-droppable: decline if any payload would leak (rc-tracked
		// or string), so the normal overwrite path frees it; scalar-only
		// enums fall through and reuse with nothing to free.
		for _, v := range ed.Variants {
			for _, pt := range v.Payloads {
				if _, isStr := pt.(ast.StringType); isStr {
					return false, nil
				}
				if arrElemIsRcTracked(pt) {
					return false, nil
				}
			}
		}
	}

	// Resolve the constructed variant's payload types (same precedence
	// as emitEnumNew: checker-substituted concrete types first, else the
	// declared payload list).
	var payloadTypes []ast.Type
	if pts, ok := b.info.VariantCallPayloads[call]; ok {
		payloadTypes = pts
	}
	if payloadTypes == nil && varIdx < len(ed.Variants) {
		payloadTypes = ed.Variants[varIdx].Payloads
	}
	offsets, _ := payloadLayout(payloadTypes, payloadCount, b.ptrW)

	// 1. Evaluate every payload arg into a scratch temp BEFORE the box
	//    is reused (reads of the old c, still live in slot idx, complete
	//    first). No inc — emitEnumNew doesn't inc payloads, so the
	//    drop-side accounting matches a freshly constructed box.
	type argTemp struct {
		slot int32
		typ  ast.Type
	}
	temps := make([]argTemp, 0, len(call.Args))
	for i, a := range call.Args {
		var pt ast.Type
		if i < len(payloadTypes) {
			pt = payloadTypes[i]
		}
		if err := b.expr(a); err != nil {
			return true, err
		}
		ts := b.allocSlot()
		b.locals[fmt.Sprintf("__ereuse_arg_%d", ts)] = ts
		if pt != nil {
			b.scratchType[ts] = pt
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ts})
		temps = append(temps, argTemp{ts, pt})
	}

	// 2. reused = is_unique(old); token = reused ? base(old) : 0. On the
	//    aliased / sentinel path, flat-dec the old box (exactly the
	//    baseline overwrite-dec) and yield 0 so __alloc_reuse allocates
	//    fresh. is_unique reads false for payloadless sentinels (rc high
	//    bit), so they take the fresh-alloc branch.
	reusedSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__ereuse_uniq_%d", reusedSlot)] = reusedSlot
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: reusedSlot})

	tokenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__ereuse_tok_%d", tokenSlot)] = tokenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpEnd})

	// 3. base = __alloc_reuse(token, size+hdr, size+hdr).
	boxSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__ereuse_box_%d", boxSlot)] = boxSlot
	b.emit(Op{Kind: OpLoadLocal, I32: tokenSlot})
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpCallDirect, Str: "__alloc_reuse", I32: 3})
	b.emit(Op{Kind: OpStoreLocal, I32: boxSlot})

	// 3b. REUSE branch only: free the box's OLD payload(s) before the new
	//     ones overwrite them. On a fresh box (reused==0) those slots are
	//     uninitialised, so this is gated on the is_unique result. The
	//     offsets are the uniform droppable layout (every payload-carrying
	//     variant shares it — the gate above declined otherwise), read
	//     relative to the data pointer (base + rcHeaderBytes). Each
	//     emitFieldDropOnStack is_unique-gates internally, so a shared
	//     buffer only dec's; freeEligible[t] (required above) means no live
	//     alias anyway. Mirrors tryStructReuseOverwrite step 4.
	if len(reuseDropLoads) > 0 {
		b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
		b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
		for _, ld := range reuseDropLoads {
			b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
			b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + ld.off})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(ld.typ, b.ptrW))
			b.emitFieldDropOnStack(ld.typ)
		}
		b.emit(Op{Kind: OpEnd})
	}

	// 4. rc = 1 at [base+0] (already 1 on the reuse path; set fresh
	//    otherwise).
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})

	// 5. Store the tag at [base+hdr+0].
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: int32(varIdx)})
	b.emit(Op{Kind: OpStore})

	// 6. Store each payload temp at [base+hdr+offset]. The old payloads
	//    were already freed on the reuse branch (step 3b) when droppable,
	//    so this overwrite doesn't strand them; a scalar-only enum had
	//    nothing to free and stays rc-neutral.
	for i, tp := range temps {
		b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offsets[i]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoadLocal, I32: tp.slot})
		b.emit(payloadStoreOpFor(tp.typ, b.ptrW))
	}

	// 7. c = data (= base + hdr); leave the tee for expression position.
	b.emit(Op{Kind: OpLoadLocal, I32: boxSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: idx})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	return true, nil
}

// localNameUnique reports whether `name` has exactly one declaration in
// the current function's locals — i.e. it is not shadowed by a same-name
// `var` in a sibling/nested scope. The Phase 1d-v zero-init safety net
// keys on name (`zeroSeen[v.Name]`), so a unique name's single slot is
// guaranteed zero-initialised at function entry; a shadowed name has
// multiple distinct slots sharing one name-keyed zero, only one of which
// is actually zeroed. emitVarReinitDropOld relies on the zero-init to
// NULL-guard its first dec, so it fires only for unique names.
func (b *builder) localNameUnique(name string) bool {
	n := 0
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			n++
			if n > 1 {
				return false
			}
		}
	}
	return n == 1
}

// emitVarReinitDropOld releases the value currently in a var's slot
// before its (re-)initialisation store — Phase 5h (loop-body local
// drops). A `var row = …` declared inside a loop reuses one slot across
// iterations; without this the prior iteration's value is overwritten
// with no dec, so N-1 allocations leak — and the rc undercount keeps the
// freelist from reclaiming them, so a hot build-and-discard loop grows
// unbounded. A loop-body `var` is a re-DECLARATION (not a reassignment),
// so the assign hook's dec-on-overwrite never ran for it.
//
// Mirrors the reassignment dec-on-overwrite (the assign Ident case) for
// the SAME rc-tracked set and dec choice — owned arrays free via
// `__fern_arr_dec`, other single-box rc types (struct / enum / closure)
// flat `__fern_rc_dec`, owned strings on x86_64/wasm (arm64 deferred per
// slice 5g) — MINUS the self-mutation / map-COW branches, which cannot
// arise for a var-init RHS: a fresh binding can never reference its own
// prior slot value. Net-zero on the operand stack (load → dec → drop), so
// the new value already sitting underneath is left in place for the store.
//
// Safety gates:
//   - ast.RcFreeEnabled: the free-off baseline emits nothing here, so it
//     stays byte-identical to before this slice — the differential gate's
//     free-on == free-off comparison is the meaningful one.
//   - localNameUnique: the var's single slot is zero-init'd at entry
//     (Phase 1d-v), so the first-iteration dec is a NULL-guarded no-op.
//     Shadowed names (multiple distinct slots, one name-keyed zero) are
//     skipped — dec-ing an un-zeroed slot would read garbage.
//   - !movedLocals: a var whose reference was MOVED out (move-on-alias /
//     -construction / -destructure / -return — all top-level, last-use)
//     is excluded from the exit sweep; dec-ing its slot would over-
//     release. Moves are top-level only, so a loop-body var is never
//     marked; this guards the rare top-level re-declaration case.
func (b *builder) emitVarReinitDropOld(name string, idx int32) {
	if !ast.RcFreeEnabled {
		return
	}
	// freeEligible is the borrow-aware verdict the EXIT sweep uses: true
	// only for an OWNED local that genuinely holds its own reference.
	// Ineligible locals — borrowed params, and crucially a var whose init
	// ALIASES another live local WITHOUT an inc (e.g. `var a1 = match (o)
	// { _ => a0 }`, an alias shape needsRcIncOnAlias doesn't catch) — must
	// be skipped entirely: they don't own a reference to release, so a dec
	// here would over-release the shared buffer. Mirroring the exit
	// sweep's gate keeps dec-on-reinit balanced against the binding's inc.
	if !b.freeEligible[name] {
		return
	}
	if !b.localNameUnique(name) || b.movedLocals[name] {
		return
	}
	t, ok := b.localDeclType(name)
	if !ok {
		return
	}
	b.emitOwnedSlotDrop(idx, t)
}

// emitOwnedSlotDrop releases the OWNED rc value in local slot `idx` per its
// static type `t` — the shared per-type drop body behind emitVarReinitDropOld
// (loop-body var reinit) and the stage-(b) post-call dec of stashed owned-temp
// call args. Each branch mirrors the exit sweep exactly (deep array / struct /
// enum / tuple drop, Map column reclaim, string str_dec/rc_dec) and is net-zero
// on the operand stack, so a value sitting underneath is left untouched.
// Callers are responsible for the borrow-aware gating (RcFreeEnabled +
// owned-ness): emitVarReinitDropOld checks freeEligible / localNameUnique /
// !movedLocals; the call-arg path only stashes provably-fresh owned temps
// (freshOwnedRcTempType) passed to non-retain-sink calls.
func (b *builder) emitOwnedSlotDrop(idx int32, t ast.Type) {
	switch ty := t.(type) {
	case ast.ArrayType:
		// Owned array: free the buffer at rc 0 — the O(N) loop reclamation.
		// An array-of-(struct / tuple / primitive-array) routes to the
		// deep per-element loop (__drop_arr_struct_ / __drop_arr_tuple_ /
		// __drop_arr_arr_) so the element boxes / inner buffers reclaim too,
		// mirroring the exit sweep (arrElemStructDropName). Without this a
		// `var g = [[..],[..]]` loop leaked the INNER buffers every iteration
		// (the profiling probe measured 3264 B → 320064 B). Other pointer-
		// element buffers (array-of-rc whose element isn't deep-droppable
		// here) still leak their elements via the plain arr_dec — safe under
		// no-double-free.
		if dropName, ok := arrElemStructDropName(ty.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
			b.emit(Op{Kind: OpLoadLocal, I32: idx})
			b.emit(Op{Kind: OpCallDirect, Str: dropName, I32: 1})
			b.emit(Op{Kind: OpDrop})
			break
		}
		// string[]: each element is a heap string whose buffer must be
		// reclaimed before the outer buffer — __fern_drop_arr_str walks +
		// __fern_str_dec's each (data, len) on the two-word ABIs (wasm +
		// arm64-TwoWord); native single-word x86_64 routes through
		// __fern_drop_arr_ptr (per-element __fern_rc_dec). Mirrors the exit
		// sweep so a precise / reinit drop reclaims the strings, not just the
		// outer buffer (the plain __fern_arr_dec below would leak them).
		if _, isStr := ty.Elem.(ast.StringType); isStr {
			helper := "__fern_drop_arr_str"
			if b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				helper = "__fern_drop_arr_ptr"
			}
			b.emit(Op{Kind: OpLoadLocal, I32: idx})
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(ty.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
			b.emit(Op{Kind: OpDrop})
			break
		}
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(ty.Elem, b.ptrW))})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
		b.emit(Op{Kind: OpDrop})
	case ast.StructType, ast.EnumType:
		// Map loop var: reclaim the whole map structure (value column +
		// string-key column + buf + handle) via emitMapSlotDrop, mirroring
		// the exit sweep. Routing it through emitStructEnumSlotDrop instead
		// would hit dropFnNameFor's Map decline → a flat box dec that leaks
		// the buf/handle/values — the `var m = map_new(8)` loop leak the
		// profiling probe measured (6400 B → 640000 B). Every map-drop helper
		// self-guards on rc==1, so a shared map only dec's.
		if st, isMap := ty.(ast.StructType); isMap && st.Name == "Map" {
			b.emitMapSlotDrop(idx, st)
			break
		}
		// Owned struct / enum loop var: deep-drop on reinit, mirroring the
		// exit sweep. A flat __fern_rc_dec here neither frees the box (rc_dec
		// has no free path) nor recurses into rc-tracked fields / payloads, so
		// a `var b = Box{ data: [...] }` (or `var e = Arr([...])`) re-declared
		// in a loop leaked its box AND its nested heap field every iteration
		// but the last. Route through the generated __drop_struct_<N> /
		// __drop_enum_<N> fn (via dropFnNameFor — the same one the exit
		// sweep's dropStructField uses for nested fields), which is_unique-
		// gates, deep-drops the fields/payloads, then __fern_box_free's the
		// box. dropFnNameFor registers any generic-enum instantiation into
		// b.genEnumDrops so the post-pass worklist emits the body.
		//
		// We only reach here for a freeEligible (owned, untainted) local, so
		// the deep recursion is safe — the premature-free that bit escaped
		// values can't arise (those are ineligible and skipped above). Types
		// dropFnNameFor declines — non-uniform / non-heap-boxed generic enums
		// — fall back to the flat box dec (leak-but-never-UAF, exactly as
		// before). Net-zero on the operand stack.
		b.emitStructEnumSlotDrop(idx, ty)
	case ast.StringType:
		// Mirrors the exit sweep's string-local reclaim exactly (slice 5g is
		// done: native heap-string rc reclaims on arm64 too). Two-word ABIs
		// (wasm + arm64) free via __fern_str_dec — OpLoadLocal fans the slot
		// into (data, len), the helper consumes the logical string and returns
		// the data ptr (dropped after); native single-word x86_64 via
		// __fern_rc_dec. The caller (emitVarReinitDropOld) already gated
		// freeEligible / localNameUnique / !movedLocals, so the value is owned
		// and alias-free, and __fern_str_dec is_unique-gates again (a shared
		// buffer only dec's; an inline-SSO / literal sentinel is a no-op).
		if ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpLoadLocal, I32: idx})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else if b.ptrW == 8 {
			b.emit(Op{Kind: OpLoadLocal, I32: idx})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.TupleType:
		// Tuple reclamation on loop-body re-declaration — mirrors the exit
		// sweep's TupleType branch (emitRcDecLocalsAtExitExcept). A tuple is
		// heap-boxed with an rc header, so a `var t = (a, b)` re-declared in
		// a loop reuses one slot across iterations; without a dec on reinit
		// every prior iteration's box (and its rc-tracked elements) leaks.
		//
		// A needs-drop tuple (rc-tracked / string elements) routes through
		// the generated __drop_tuple_<mangled> fn, which is_unique-gates,
		// deep-drops each pointer-shaped element (string str_dec, recursive
		// element drops), then box_frees — exactly the body the exit sweep
		// emits inline. dropFnNameFor registers the shape into
		// b.genTupleDrops so the post-pass worklist generates that body. A
		// plain-element tuple ((i32, i32) etc.) has no element to drop, so
		// emit the is_unique-gated box_free directly to reclaim its box, as
		// the exit sweep's inline branch does for every eligible tuple.
		//
		// Net-zero on the operand stack (load → call → drop, the OpIf
		// consuming is_unique's result), so the new RHS value already
		// sitting underneath is left in place for the store.
		b.emitTupleSlotDrop(idx, ty)
		// FuncType (closure) is deliberately skipped: a closure dec emitted
		// between OpMakeClosure and OpStoreLocal breaks the defunctionalise /
		// closure-pair-elide pattern match, and a flat closure dec leaks
		// captures anyway — it keeps its prior safe-leak behaviour.
	}
}

// emitTupleSlotDrop releases the tuple value currently in local slot
// `idx` — the shared body for the loop-body re-declaration drop
// (emitVarReinitDropOld) and the reassignment dec-on-overwrite. It
// mirrors the exit sweep's inline TupleType branch (emitDec in
// emitRcDecLocalsAtExitExcept): a needs-drop tuple routes through the
// generated __drop_tuple_<mangled> fn (is_unique gate → per-element
// deep drop → box_free), registering the shape into b.genTupleDrops so
// the post-pass worklist emits that body; a plain-element tuple
// box_frees directly under the same is_unique gate. Net-zero on the
// operand stack, so a value sitting underneath (a reinit/reassign RHS)
// is left untouched. Callers gate on RcFreeEnabled + freeEligible +
// localNameUnique + !movedLocals before invoking.
func (b *builder) emitTupleSlotDrop(idx int32, tt ast.TupleType) {
	if name, ok := dropFnNameFor(tt, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	_, size := tupleElemLayout(tt.Elems, b.ptrW)
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
}

// emitStructEnumSlotDrop releases the struct / enum value currently in
// local slot `idx` — the shared body for the loop-body re-declaration
// drop (emitVarReinitDropOld) and the reassignment dec-on-overwrite. A
// droppable type routes through the generated __drop_struct_<N> /
// __drop_enum_<N> fn (via dropFnNameFor — the same helper the exit
// sweep's dropStructField uses), which is_unique-gates, deep-drops the
// fields/payloads, then __fern_box_free's the box; generic-enum
// instantiations register into b.genEnumDrops for the post-pass worklist.
// Types dropFnNameFor declines — Map handles, non-uniform / non-heap-
// boxed generic enums — fall back to the flat box dec (leak-but-never-
// UAF). Net-zero on the operand stack, so a value sitting underneath
// (a reinit/reassign RHS) is left untouched. Callers gate on
// RcFreeEnabled + freeEligible (+ localNameUnique + !movedLocals for the
// reinit path) before invoking, so only an OWNED value is deep-dropped.
// decValueOnStack consumes a pointer value already on the operand stack and
// dec's it per its static type. An array of pointer-shaped rc-tracked elements
// routes through __fern_drop_arr_ptr (which recurses one level into the
// elements on the last reference); every other pointer-shaped value gets a flat
// __fern_rc_dec (nested fields/elements of those leak for now — safe under
// no-free, no over-release). Both helpers carry the null / low-address /
// sentinel guards. Promoted from a closure in emitRcDecLocalsAtExitExcept so
// emitEnumSlotDrop's uniform payload-drop path can share it.
func (b *builder) decValueOnStack(t ast.Type, mayFree bool) {
	// Two-word string value (wasm + arm64-TwoWordOverride): the caller loaded
	// (data, len) via payloadLoadOpFor, so reclaim via the two-word
	// __fern_str_dec. Reached from the enum payload drop (struct / tuple string
	// fields are handled inline before reaching here).
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// Single-word string value (native single-word, x86_64): caller loaded a
	// ptr; __fern_rc_dec it (inline-tag / sentinel guards keep all sources
	// safe). arm64 / wasm two-word ABIs take the two-word str_dec branch above.
	if _, isStr := t.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// `mayFree` is the borrow-aware permission to return this value's buffer to
	// the freelist. It's true only for OWNED top-level array locals
	// (computeFreeEligible); struct fields and enum payloads always pass false
	// (their borrow-ness isn't tracked, so they never free — conservative).
	if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) && mayFree {
		// Transitive reclamation Stage B: an array of CONCRETE structs drops
		// each element box deeply (via __drop_arr_struct_<Elem> →
		// __drop_struct_<Elem> per element) before freeing the buffer, instead
		// of the flat per-element rc_dec __fern_drop_arr_ptr does. Gated on
		// RcFreeEnabled to match the genfn post-pass.
		if name, ok := arrElemStructDropName(at.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok && ast.RcFreeEnabled {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_ptr", I32: 2})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// __fern_rc_dec is a void-returning runtime helper but OpCallDirect's
	// codegen always pushes the call's return-value register onto the operand
	// stack; drop the bogus push to keep the stack balanced.
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
}

// dropStructField drops one struct field whose pointer is already on the
// operand stack. Transitive reclamation Stage A: a CONCRETE struct field
// (statically exact type) recurses through its generated __drop_struct_ fn, so
// its box + nested struct children reclaim on the field's last reference; the
// generated fn is_unique-gates internally, so this is safe whether the child is
// shared or not. Every other field type (arrays, enums/unions, closures, Map)
// keeps the flat one-level dec. Promoted from a closure in
// emitRcDecLocalsAtExitExcept so emitEnumSlotDrop's variant-plan path shares it.
func (b *builder) dropStructField(t ast.Type) {
	// Two-word string value (wasm + arm64-TwoWordOverride): caller loaded
	// (data, len), reclaim via __fern_str_dec. Reached from the enum
	// variant-plan payload drop (struct / tuple string fields handled inline).
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// Single-word string value (native single-word, x86_64): caller loaded a
	// ptr via payloadLoadOpFor; __fern_rc_dec it. SSO inline-tag low-bit guard
	// + literal sentinel keep all sources safe.
	if _, isStr := t.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	if isMapType(t) {
		// A Map-typed field reclaims the whole map structure on the owning
		// value's last reference: free the value column then the buf + handle.
		// Both helpers self-guard on the map's own rc==1, so a shared map only
		// dec's. They return the map ptr, so the stack value chains through.
		b.emit(Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	if name, ok := dropFnNameFor(t, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	if at, ok := t.(ast.ArrayType); ok {
		// Any array field frees its BUFFER on the owning value's last reference
		// (the owner is eligible/unique here; each helper still is_unique-gates
		// the array, so a shared one only dec's). Array-of-struct also
		// deep-drops each element box (Stage B loop); array-of-rc frees the
		// outer buffer + flat-dec's inner; plain arrays free the buffer.
		if name, ok := arrElemStructDropName(at.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		helper := "__fern_arr_dec"
		if arrElemIsRcTracked(at.Elem) {
			helper = "__fern_drop_arr_ptr"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
			// string[] on any two-word ABI (wasm + arm64-TwoWordOverride):
			// walk + __fern_str_dec each (data, len) element, then free buffer.
			helper = "__fern_drop_arr_str"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
			// string[] on native single-word (x86_64, !TwoWordOverride):
			// elements are single pointers; __fern_drop_arr_ptr walks +
			// __fern_rc_dec's each one (SSO inline-tag low-bit guard is safe).
			helper = "__fern_drop_arr_ptr"
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
		b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
		b.emit(Op{Kind: OpDrop})
		return
	}
	b.decValueOnStack(t, false)
}

func (b *builder) emitStructEnumSlotDrop(idx int32, ty ast.Type) {
	if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// dropFnNameFor declines a SCALAR-payload enum (enumNeedsDrop false: no
	// pointer payload to recurse into), so a flat rc_dec here would leak its
	// box. Route through emitOwnedEnumDrop (generated __drop_enum_<Name> →
	// is_unique-gated variant-plan box_free) — reclaiming a loop-var reinit /
	// reassign of e.g. `Box{ Val(i32), Empty }` that previously leaked one box
	// per iteration, and reusing it on wasm (the gen-fn box_free reuses where
	// an inline one wouldn't). Callers gate on owned-ness, so pass eligible=true;
	// the is_unique gate is the final safety net.
	if et, ok := ty.(ast.EnumType); ok {
		b.emitOwnedEnumDrop(idx, et, true)
		return
	}
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
}

// emitEnumDropViaGenFn routes an OWNED CONCRETE enum box in slot `slot`
// through the generated `__drop_enum_<Name>` drop function (is_unique-gated
// variant-plan: per-variant payload drop + exact-size box_free), returning
// true if it handled the drop. It's the wasm-correct alternative to the inline
// box-free tiers in emitEnumSlotDrop: an inline box_free in a complex function
// body doesn't return the box to the freelist on wasm, but the identical
// box_free inside a generated FUNCTION does (verified). The post-pass worklist
// regenerates the body from info.Enums on encountering this call, so scalar
// enums (which dropFnNameFor declines — enumNeedsDrop is false) reclaim too.
//
// Concrete enums only: a generic instantiation's drop is named/handled via
// dropFnNameFor (mangled name + genEnumDrops registry) upstream, and a
// sentinel/un-planned enum (enumVariantDropPlan false) falls through to the
// inline flat dec. Net-zero on the operand stack.
func (b *builder) emitEnumDropViaGenFn(slot int32, et ast.EnumType) bool {
	if !ast.RcFreeEnabled || len(et.Args) > 0 {
		return false
	}
	ed, ok := b.info.Enums[et.Name]
	if !ok {
		return false
	}
	if _, ok := enumVariantDropPlan(ed, b.ptrW); !ok {
		return false
	}
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: "__drop_enum_" + et.Name, I32: 1})
	b.emit(Op{Kind: OpDrop})
	return true
}

// emitOwnedEnumDrop reclaims an OWNED enum box for a PER-ITERATION drop site
// (loop-var reinit, match-scrutinee): it prefers the generated
// __drop_enum_<Name> fn (emitEnumDropViaGenFn — whose box_free reuses the freed
// box on wasm, unlike an inline box_free in a loop / match body) and falls back
// to the inline emitEnumSlotDrop for shapes the gen-fn route declines (generic
// instantiations, sentinel-only / un-planned enums). The exit sweep
// deliberately does NOT use this — it calls emitEnumSlotDrop directly, since a
// once-per-call inline drop neither leaks unboundedly nor needs the gen-fn
// indirection (and keeps its golden-test codegen shape).
func (b *builder) emitOwnedEnumDrop(slot int32, et ast.EnumType, eligible bool) {
	if eligible && b.emitEnumDropViaGenFn(slot, et) {
		return
	}
	b.emitEnumSlotDrop(slot, et, eligible)
}

// consumingReuseCtor returns the arm's constructor call when the arm body is
// exactly `return Ctor(args)` and Ctor is a PAYLOADFUL variant of the consumed
// enum `et` — the case where the scrutinee box (uniform size for all variants
// of et) can be reused in place for the construction (C2). Returns nil
// otherwise (a payloadless `return Nil`, a non-constructor body, or a different
// enum), leaving the box to the normal shallow free.
func (b *builder) consumingReuseCtor(arm *ast.MatchArm, et ast.EnumType) *ast.Call {
	if arm.Body == nil || len(arm.Body.Stmts) != 1 {
		return nil
	}
	ret, ok := arm.Body.Stmts[0].(*ast.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	call, ok := ret.Value.(*ast.Call)
	if !ok {
		return nil
	}
	cid, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	cenum, _, payloadCount, ok := b.lookupVariant(cid.Name)
	if !ok || cenum != et.Name || payloadCount == 0 {
		return nil
	}
	// The enum must be uniform-box-sized (every variant the same box) for the
	// matched box to fit the constructed variant — the same gate the shallow
	// free uses.
	ed, ok := b.info.Enums[et.Name]
	if !ok {
		return nil
	}
	if len(et.Args) > 0 {
		ed = substituteEnumDecl(ed, et.Args)
	}
	if _, ok := uniformEnumBoxSize(ed, b.ptrW); !ok {
		return nil
	}
	return call
}

// emitConsumingMatchBoxFree frees an OWNED enum scrutinee's box after a
// CONSUMING match (`match (own_param) { Cons(h, t) => … }`) WITHOUT deep-dropping
// its payloads — the pointer payloads were moved into the arm bindings and are
// reclaimed downstream (the recursive `map(t)` owns + frees the tail), so
// dropping them here would double-free. is_unique-gated: a payloadless sentinel
// (Nil) or a shared box (shouldn't occur for a uniquely-owned `own` param) is
// only dec'd, never freed. Uniform-droppable enums only (a statically sizable
// box); others keep their box (a safe leak). This is the heart of Fern's
// consuming match — the Perceus FBIP traversal.
func (b *builder) emitConsumingMatchBoxFree(slot int32, et ast.EnumType) {
	ed, ok := b.info.Enums[et.Name]
	if !ok {
		return
	}
	if len(et.Args) > 0 {
		ed = substituteEnumDecl(ed, et.Args)
	}
	size, ok := uniformEnumBoxSize(ed, b.ptrW)
	if !ok {
		return
	}
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	// Unique: free the box BUFFER only — NO per-payload deep-drop (they were
	// moved into the bindings). __fern_box_free(data, size) internally frees
	// base = data-8 (size+8) and returns the data ptr (dropped); `size` is the
	// uniform data size, exactly as the normal enum box-free uses.
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpElse})
	// Shared / sentinel: just dec (an alias keeps it; a sentinel dec no-ops).
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
}

// ownParamEnumScrutinee reports the enum type when `tag` is a bare reference to
// an OWN (consuming) parameter of the current function whose static type is an
// enum — the scrutinee of a consuming match. Gated on the program using `own`
// (b.info.OwnFuncs), so non-`own` code never triggers the consuming path.
func (b *builder) ownParamEnumScrutinee(tag ast.Expr) (ast.EnumType, bool) {
	if !ast.RcFreeEnabled || len(b.info.OwnFuncs) == 0 {
		return ast.EnumType{}, false
	}
	id, ok := tag.(*ast.Ident)
	if !ok {
		return ast.EnumType{}, false
	}
	flags := b.info.OwnFuncs[b.fn.Name]
	isOwn := false
	for i, p := range b.fn.Params {
		if p.Name == id.Name && i < len(flags) && flags[i] {
			isOwn = true
			break
		}
	}
	if !isOwn {
		return ast.EnumType{}, false
	}
	et, ok := b.exprStaticType(tag).(ast.EnumType)
	if !ok {
		return ast.EnumType{}, false
	}
	return et, true
}

// emitEnumSlotDrop releases the OWNED enum box in local slot `slot` INLINE per
// its static type `et` — the shared body extracted verbatim from the exit-sweep
// enum branch (emitRcDecLocalsAtExitExcept), which still uses it directly. The
// PER-ITERATION callers (loop-var reinit, match-scrutinee) go through
// emitOwnedEnumDrop instead, which prefers the generated __drop_enum_<Name> fn
// (the box_free inside a generated FUNCTION reuses the freed box on wasm, where
// an inline box_free in a loop / match body does not — a per-iteration leak the
// once-per-call exit sweep doesn't suffer). `eligible` is the caller's
// borrow-aware owned-ness gate; when false (or rc-free off) it degrades to the
// prior flat dec.
//
// Three tiers, all is_unique-gated so a shared / payloadless-sentinel value is
// only dec'd, never freed:
//   - uniform non-pointer payloads → flat-dec payloads + __fern_box_free;
//   - non-uniform / scalar variants → tag switch, per-variant payload drop +
//     exact-size __fern_box_free (the tier that reclaims a `Box{ Val(i32),
//     Empty }` box the flat dec would leak);
//   - anything statically un-sizable (generic ParamType payloads) → the
//     uniform-payload flat-dec (if any) + flat rc_dec, exactly as before.
//
// Net-zero on the operand stack, so a value sitting underneath is untouched.
func (b *builder) emitEnumSlotDrop(slot int32, et ast.EnumType, eligible bool) {
	ed, edOk := b.info.Enums[et.Name]
	if edOk && ast.RcFreeEnabled && eligible {
		// Generic-enum reclamation: a heap-boxed instantiation like
		// Option[Item] / Result[Item, E] carries ParamType payloads in its
		// decl, so substitute the type args to recover the concrete types —
		// but ONLY adopt the substituted decl when it exposes a pointer
		// payload (guaranteeing a heap-boxed, non-pair-form instantiation, so
		// the variant-plan's box_free is valid). Scalar instantiations
		// (Option[i32], pair-form, no box) keep the generic decl and bail to
		// the flat dec as before.
		if len(et.Args) > 0 {
			if sub := substituteEnumDecl(ed, et.Args); enumHasPointerPayload(sub) {
				ed = sub
			}
		}
		loads, loadsOk := uniformEnumDropLoads(ed, b.ptrW)
		size, sizeOk := uniformEnumBoxSize(ed, b.ptrW)
		// Uniform branchless path: every payload-carrying variant shares an
		// identical droppable-payload signature, so the payload decs run
		// unconditionally inside the is_unique guard with no tag switch. A
		// CONCRETE-struct payload that could be deep-dropped is excluded here
		// (!enumHasPointerPayload) and taken by the variant-plan path below,
		// where each arm knows its exact type.
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
				b.decValueOnStack(ld.typ, false)
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
		// Non-uniform / scalar enum (JsonValue, or a non-pair-form scalar enum
		// reached via the generic fallthrough): a tag switch over the real box
		// (rc==1) drops each variant's payloads and frees with that variant's
		// exact box size. The tag is stashed in a scratch local so later arms
		// read it from the stack, never from the (possibly freed) box.
		// Concrete enums take the wasm-correct generated-fn route above before
		// reaching here; this inline path serves generic instantiations.
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
					// Tag-guarded, so ld.typ is this variant's EXACT payload
					// type — a concrete-struct payload recurses through
					// __drop_struct_<T>; other payloads keep the flat dec.
					b.dropStructField(ld.typ)
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
				b.decValueOnStack(ld.typ, false)
			}
			b.emit(Op{Kind: OpEnd})
		}
	}
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
}

// emitFieldDropOnStack consumes a pointer-shaped field value already on
// the operand stack and releases it per its static type — the
// struct-reuse-overwrite sibling of the exit sweep's dropStructField.
// An array field frees its BUFFER via __fern_arr_dec (pointer-element
// buffers leak their elements, exactly as the array exit-sweep / reinit
// paths — leak-but-never-UAF); a concrete struct / enum / tuple field
// recurses through its generated __drop_* fn; everything else (Map
// handles, closures, non-droppable generics) keeps the flat one-level
// __fern_rc_dec. Each helper is_unique-gates internally, so a field
// shared with a live alias (or carried over with an eval-inc) is only
// dec'd, never freed.
func (b *builder) emitFieldDropOnStack(t ast.Type) {
	if at, ok := t.(ast.ArrayType); ok {
		b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
		b.emit(Op{Kind: OpDrop})
		return
	}
	if name, ok := dropFnNameFor(t, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
}

// emitMapSlotDrop reclaims the Map value in local slot `slot` — the
// shared body for the exit sweep and the loop-body re-declaration drop
// (emitVarReinitDropOld). It frees the value column (struct/enum values
// via the generated __drop_map_via_<perValueDrop>; array values via the
// generic __map_drop_values; string values via __drop_map_str_values),
// then any string-key column (__drop_map_str_keys), then the buf +
// handle (__fern_map_drop). Every helper self-guards on the map's own
// rc==1, so a shared map only dec's. Net-zero on the operand stack, so a
// value sitting underneath (a reinit RHS) is left untouched. Callers gate
// on RcFreeEnabled + freeEligible.
func (b *builder) emitMapSlotDrop(slot int32, st ast.StructType) {
	dropValues := "__map_drop_values"
	if name, ok := mapValDropName(st, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW); ok {
		dropValues = name
	} else if len(st.Args) >= 2 {
		// Map[K, string]: reclaim each value's string buffer before freeing
		// the buf + handle. genMapStrValDropFn branches on UseTwoWordStrings
		// for the boxed-cell (wasm + arm64-TwoWord) vs direct-pointer
		// (x86_64 single-word) column shape.
		if _, isStr := st.Args[1].(ast.StringType); isStr {
			dropValues = "__drop_map_str_values"
		}
	}
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: dropValues, I32: 1})
	b.emit(Op{Kind: OpDrop})
	// Map[string, V]: reclaim each key's string buffer. Independent of the
	// value walk above (both self-guard on rc==1); runs before the buf +
	// handle free.
	if len(st.Args) >= 1 {
		if _, isStr := st.Args[0].(ast.StringType); isStr {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: "__drop_map_str_keys", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	}
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
	b.emit(Op{Kind: OpDrop})
}

// localDeclType returns the declared type of a param or local by name.
func (b *builder) localDeclType(name string) (ast.Type, bool) {
	for _, p := range b.fn.Params {
		if p.Name == name {
			return p.Type, true
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			return v.Type, true
		}
	}
	return nil, false
}

// constructionMovesIdent reports whether `name` is MOVED into a CONSTRUCTION in
// e — it appears as a bare-ident payload of a variant constructor or a struct /
// tuple / array literal. Such a pointer-payload store transfers ownership of the
// value WITHOUT a retaining inc, so in `x = Ctor(.., x, ..)` the old `x` is
// handed to the new box's payload. Dropping it on the reassignment (the normal
// overwrite dec) would then leave that payload at rc 0 — which a later
// consuming-match free under-counts (its is_unique gate misses rc 0 and dec's
// to -1). Suppressing the drop in this case is what keeps the rc balanced.
func (b *builder) constructionMovesIdent(e ast.Expr, name string) bool {
	isName := func(x ast.Expr) bool { id, ok := x.(*ast.Ident); return ok && id.Name == name }
	any := func(es []ast.Expr) bool {
		for _, el := range es {
			if isName(el) || b.constructionMovesIdent(el, name) {
				return true
			}
		}
		return false
	}
	switch x := e.(type) {
	case *ast.Call:
		if id, ok := x.Callee.(*ast.Ident); ok && x.Method == nil {
			if _, _, _, isVar := b.lookupVariant(id.Name); isVar {
				return any(x.Args)
			}
		}
	case *ast.TupleLit:
		return any(x.Elems)
	case *ast.ArrayLit:
		return any(x.Elems)
	case *ast.StructLit:
		for _, f := range x.Fields {
			if isName(f.Value) || b.constructionMovesIdent(f.Value, name) {
				return true
			}
		}
	}
	return false
}

func (b *builder) assign(n *ast.Assign) error {
	switch t := n.Target.(type) {
	case *ast.Ident:
		idx, ok := b.locals[t.Name]
		if !ok {
			return fmt.Errorf("ir: cannot assign to %q (no slot)", t.Name)
		}
		// Phase 5b drop-reuse (FBIP): a self-overwrite of an owned
		// struct local with a fresh struct literal of the same type
		// reuses the old box's storage in place when it's uniquely
		// owned (`p = Point{x: p.x + 1, y: p.y}` → no alloc). Handles
		// the whole assignment (old-value drop + construct + store), so
		// it returns early past the normal expr + dec-on-overwrite.
		if done, err := b.tryStructReuseOverwrite(n, t, idx); done {
			return err
		}
		// Phase 5e drop-reuse: the enum analogue — `c = Variant(...)`
		// reuses c's box in place when uniquely owned. Pure alloc-
		// elision (rc-neutral vs the baseline construct + flat dec); see
		// tryEnumReuseOverwrite. Handles the whole assignment, so it
		// returns early past the normal expr + overwrite-dec.
		if done, err := b.tryEnumReuseOverwrite(n, t, idx); done {
			return err
		}
		if err := b.expr(n.Value); err != nil {
			return err
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
			} else if !b.enumRcPayloadsEligibleForValue(n.Value) && b.constructionMovesIdent(n.Value, t.Name) {
				// `x = Ctor(.., x, ..)` (e.g. `acc = Cons(1, acc)`): under the
				// move model the old `x` is MOVED into the new box's payload —
				// its ownership transferred without a retaining inc, so it must
				// NOT be dropped here (the normal overwrite dec would push that
				// payload to rc 0, which a later consuming-match free under-counts
				// — its is_unique gate misses rc 0 and dec's to -1). No drop.
				//
				// Under EnumRcPayloads the payload is INC'd at construction, so
				// the overwrite dec is REQUIRED to balance that inc — this skip
				// is disabled there and the normal enum-overwrite drop below
				// fires.
			} else if sety, isSE := structOrEnumTypeOfLocal(t.Name, b); isSE && ast.RcFreeEnabled && b.freeEligible[t.Name] {
				// Struct / enum reassignment-overwrite — `s = Other{...}` /
				// `e = Variant(...)` ends the old binding's ownership exactly
				// like a scope exit (or a loop reinit) would, so deep-drop the
				// OLD box rather than the flat __fern_rc_dec the catch-all else
				// emits (which neither frees the box nor recurses, leaking the
				// box + nested fields). Shares emitStructEnumSlotDrop with the
				// reinit path — routes through __drop_struct_ / __drop_enum_
				// when droppable, flat dec otherwise (Map handles, non-uniform
				// generic enums). Gated on freeEligible like the array / string
				// siblings: only an OWNED (untainted) local frees here — a
				// borrowed / escaped one keeps the plain dec, so a live alias is
				// never reclaimed out from under. The in-place reuse paths
				// (tryStructReuseOverwrite / tryEnumReuseOverwrite) returned
				// early above, so this is only the genuine-overwrite case.
				// Net-zero on the operand stack, leaving the new RHS value for
				// the store below.
				b.emitStructEnumSlotDrop(idx, sety)
			} else {
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		} else if isStringTypeOfLocal(t.Name, b) && ast.RcFreeEnabled && b.freeEligible[t.Name] {
			// Phase 1e-strings: dec the OLD string buffer before the
			// overwrite, mirroring the exit-sweep string branch (emitDec)
			// and gated identically (RcFreeEnabled && freeEligible). A
			// reassignment ends the old binding's ownership exactly like a
			// scope exit would, so the same eligible-gated str_dec applies.
			// This is what makes `var s = ""; for … { s = s + chunk }`
			// reclaim each intermediate buffer instead of orphaning it —
			// the alias-inc side (needsRcIncOnAlias) already retains string
			// RHSs, so this is its matching release. Without it the inc/dec
			// sides were asymmetric for strings: every string reassignment
			// leaked the prior buffer (safe under the bump allocator, but
			// the rc undercount kept the freelist from reclaiming it).
			//
			// Net-zero on the operand stack (load → str_dec/rc_dec → drop),
			// so the new value sitting underneath is left untouched.
			//   wasm two-word (ptrW==4): load (data,len), __fern_str_dec —
			//     inline-tag / literal-sentinel guards no-op the non-heap
			//     sources — then drop the returned data ptr. Verified under
			//     wasmtime (host-independent), so local == CI.
			//   native single-word x86_64 (ptrW==8, !TwoWordOverride): load
			//     ptr, __fern_rc_dec (SSO low-bit + sentinel guards keep all
			//     sources safe), drop. Verified on the native x86_64 runner.
			//
			// arm64 (ptrW==8 + TwoWordOverride, two-word str_dec) is
			// DELIBERATELY EXCLUDED for now: native-arm64 heap-string
			// reclamation is the RC-perceus plan's deferred slice 5g
			// ("heap-string rc — SSO-blocked", x86_64-only testing caveat).
			// Enabling the overwrite str_dec there over-releases on real
			// arm64 hardware (qemu user-mode masks it), so arm64 keeps its
			// prior safe-leak behaviour — codegen here is byte-identical to
			// main on arm64 — until the native str_dec / cell_free reclaim
			// path is verified on hardware. Re-enable by widening the wasm
			// branch back to ast.UseTwoWordStrings once 5g lands.
			//
			// Gated on freeEligible like the exit dec: an INELIGIBLE
			// (borrowed param / escaped) string is skipped here AND at exit,
			// so the two stay balanced and a borrow is never over-released.
			if b.ptrW == 4 {
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			} else if b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		} else if tt, isTup := tupleTypeOfLocal(t.Name, b); isTup && ast.RcFreeEnabled && b.freeEligible[t.Name] {
			// Tuple reassignment-overwrite — `t = (a, b)` ends the old
			// binding's ownership exactly like a scope exit would, so the
			// same eligible-gated deep-drop applies. Shares the exit sweep's
			// body via emitTupleSlotDrop (is_unique → per-element deep drop →
			// box_free, or a plain box_free for plain-element tuples). Net-
			// zero on the operand stack, so the new RHS value underneath is
			// left in place for the store below.
			b.emitTupleSlotDrop(idx, tt)
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

// isStringTypeOfLocal reports whether the named param / local is
// declared with a string type. Drives the string dec-on-overwrite in
// assign() (Phase 1e-strings) — kept separate from isArrayTypeOfLocal
// because the string dec uses the two-word-aware str_dec helper, not
// the single-word rc_dec the array/struct/enum/closure path emits.
func isStringTypeOfLocal(name string, b *builder) bool {
	for _, p := range b.fn.Params {
		if p.Name == name {
			_, ok := p.Type.(ast.StringType)
			return ok
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			_, ok := v.Type.(ast.StringType)
			return ok
		}
	}
	return false
}

// tupleTypeOfLocal returns the TupleType of a param / local named
// `name` if it is tuple-typed. Used by the dec-on-overwrite to route
// tuple targets through their deep-drop on reassignment, mirroring the
// array / string branches.
func tupleTypeOfLocal(name string, b *builder) (ast.TupleType, bool) {
	for _, p := range b.fn.Params {
		if p.Name == name {
			tt, ok := p.Type.(ast.TupleType)
			return tt, ok
		}
	}
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			tt, ok := v.Type.(ast.TupleType)
			return tt, ok
		}
	}
	return ast.TupleType{}, false
}

// structOrEnumTypeOfLocal returns the declared StructType / EnumType of a
// param / local named `name` if it is one. Drives the struct/enum
// dec-on-overwrite deep-drop in assign() — Map (a StructType named "Map")
// is included but dropFnNameFor declines it, so emitStructEnumSlotDrop
// falls back to the flat dec for it (its self-mutation case is handled
// earlier via isSelfMapMutation).
func structOrEnumTypeOfLocal(name string, b *builder) (ast.Type, bool) {
	t, ok := b.localDeclType(name)
	if !ok {
		return nil, false
	}
	switch t.(type) {
	case ast.StructType, ast.EnumType:
		return t, true
	}
	return nil, false
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
	if _, isStr := b.exprType(e).(ast.StringType); isStr && b.twoWordStrings() {
		// Two-word string ABI (wasm32 + arm64 TwoWordOverride): the
		// value occupies two stack words (data, len), so the retain
		// must go through __fern_str_inc, which tag-checks the inline
		// bit and inc's only the heap data pointer. The single-word
		// __fern_rc_inc fall-through below would pop just the top word
		// (the length) and dereference it as a pointer — a SIGSEGV on
		// literal strings, whose length is a small integer. Gating on
		// b.ptrW==4 (wasm-only) missed arm64 and crashed every aliased
		// string (e.g. generic id[T](x: string) returning its param).
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
	// that outlives the source. Native single-word strings (x86_64,
	// !TwoWordOverride) inc via __fern_rc_inc (emitAliasInc fall-
	// through). SSO inline-tag low-bit guard in __fern_rc_inc (added
	// during Slice 8) keeps short inline strings safe. arm64
	// (TwoWordOverride boxed) excluded — no native str_inc / str_dec
	// runtime, same gating as the rest of the native string-reclaim
	// path.
	if _, isStr := t.(ast.StringType); isStr {
		// arm64 now has __fern_str_inc, so the wasm two-word retain path
		// applies there too. All non-zero ptrW with strings is alias-retained.
		return true
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
	if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
	if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
		if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
		if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
		if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
	if f, ok := t.(ast.FloatType); ok && f.NormalWidth() == 64 {
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
		// Map[K, string] get retain (two-word ABI — wasm + arm64-
		// TwoWordOverride): the returned Option now co-owns the
		// string buffer alongside the map's cell, so __fern_str_inc
		// (which returns the (data, len) pair for the store below).
		// Balanced by the caller's dec of the gotten string and the
		// map's drop dec.
		if _, isStr := vType.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
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
	// Map[K, string] get_or retain (two-word ABI — wasm + arm64-
	// TwoWordOverride; boxed V): the returned (data, len) pair is
	// the string the column was holding (or our just-allocated
	// fallback cell). The caller will co-own the buffer alongside
	// the map's cell, so __fern_str_inc it. Balances the caller's
	// later dec and the map drop's column-walk dec. Mirrors
	// emitMapGetRebox's boxed-V retain — same correctness
	// rationale, same gating.
	if _, isStr := vType.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
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
