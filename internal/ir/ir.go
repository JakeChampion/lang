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
//     match/break/continue), function calls (direct + indirect),
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
	"sort"
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
	// any cast whose source is unsigned.
	OpExtendI32S  // (i32) → i64 (sign-extend)
	OpExtendI32U  // (i32) → i64 (zero-extend, for unsigned)
	OpWrapI64     // (i64) → i32
	OpFPromoteF32 // (f32) → f64
	OpFDemoteF64  // (f64) → f32
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

	// Bit-counting intrinsics. Each consumes one integer of `Width`
	// (32 or 64) and produces an i32 count. Every target has these as
	// instructions — wasm as single opcodes (i32.clz / i32.ctz /
	// i32.popcnt), arm64 as clz / rbit+clz / cnt+addv, x86-64 as
	// lzcnt / tzcnt / popcnt — where the portable SWAR sequences they
	// replace are 12-15 ALU ops.
	//
	// Zero is defined: OpClz and OpCtz of 0 return the operand width
	// (32 or 64), matching wasm's semantics and what the SWAR code
	// they replace produced. Every backend gets that for free from its
	// instruction, which is why x86-64 selects LZCNT/TZCNT (defined at
	// zero) over the same-opcode bsr/bsf (undefined at zero).
	OpClz      // (i32|i64) → i32, count of leading zero bits
	OpCtz      // (i32|i64) → i32, count of trailing zero bits
	OpPopcount // (i32|i64) → i32, count of set bits

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

	// `dyn Trait` runtime dispatch (docs/DYN-TRAITS.md §4.2.1).
	//
	// OpConstVtable pushes the i32 address of the static vtable for
	// a (Trait, Concrete) pair — an array of i32 function-table
	// indices, one slot per non-associated trait method in
	// declaration order. The boxing of a concrete value into a
	// `dyn Trait` lowers the concrete value (one word = data), then
	// emits OpConstVtable, leaving the inline two-word `[data,
	// vtable]` fat pointer on the stack. Trait/Concrete in Op.Str /
	// Op.Str2.
	OpConstVtable // () → i32 (vtable address)

	// OpCallDyn dispatches a `dyn Trait` method call. Stack on entry
	// is `[data, args..., vtable]`: the receiver data word, the
	// (already-lowered) method args, and the receiver's vtable word
	// on top. The backend pops the vtable, loads slot `Op.I32` (the
	// method's index among the trait's non-associated methods,
	// `+ slot*4`), and `call_indirect`s through the resulting table
	// index with `[data, args...]`. Op.Sig is the receiver-first
	// method signature (receiver = i32 concrete pointer, uniform
	// across concrete types).
	OpCallDyn // (data, args..., vtable) → result | ()

	// OpBoxDyn packs a boxed one-word `dyn Trait` value on the native
	// (ptrW==8) backends (docs/DYN-TRAITS.md §4.2.2). Stack on entry is
	// `[data, vtable]` (vtable on top, just like the wasm inline form);
	// the backend allocates a `2*ptrW`-byte heap cell via the normal
	// `__fern_alloc` path, stores `data` at +0 and `vtable` at +ptrW,
	// and pushes the single cell pointer. Never emitted on wasm, which
	// keeps the inline two-word fat pointer instead.
	OpBoxDyn // (data, vtable) → i32 (cell ptr)

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

	// Dedicated refcount ops (#4402 opt 2). Every rc-inc / rc-dec /
	// is-unique probe used to be an OpCallDirect to the matching
	// runtime helper, invisible to IR passes except by Str match.
	// The dedicated kinds make rc traffic structurally visible
	// (dup/drop fusion, token threading) and give backends a seam
	// to inline the fast path (sentinel test + in-place RMW) with
	// the helper call as the fallback body.
	//
	// All three are pass-through-shaped like the helpers they
	// replace: one pointer popped, one word pushed (rc_inc / rc_dec
	// return their argument; is_unique returns the 0/1 flag). Str
	// still carries the runtime-helper symbol and I32 the arg count
	// (always 1), so helper-reachability scans and the backends'
	// call-lowering paths stay shared with OpCallDirect.
	OpRcInc      // (ptr) → ptr (pass-through; bumps rc, sentinel-guarded)
	OpRcDec      // (ptr) → ptr (pass-through; drops one reference)
	OpRcIsUnique // (ptr) → i32 (1 iff rc == 1; sentinel/static → 0)

	// OpLine is a zero-effect source-position marker (#5537 slice 2):
	// it carries a statement's Pos but consumes/produces nothing and emits
	// no machine code. Native backends turn it into a DWARF `.loc` directive
	// under -g so the line table maps addresses to source lines; every other
	// consumer ignores it. Emitted by the builder only when the
	// EmitLineMarkers lower-option is set (native -g), so ordinary builds —
	// and the self-host byte-identical fixpoint — never see it.
	OpLine // () → () (source-line marker; Pos is the payload)
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
	case OpClz:
		return "clz"
	case OpCtz:
		return "ctz"
	case OpPopcount:
		return "popcnt"
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
	case OpConstVtable:
		return "const.vtable"
	case OpCallDyn:
		return "call_dyn"
	case OpBoxDyn:
		return "box_dyn"
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
	case OpRcInc:
		return "rc.inc"
	case OpRcDec:
		return "rc.dec"
	case OpRcIsUnique:
		return "rc.is_unique"
	case OpLine:
		return "line"
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
	// name. For OpConstVtable it carries the Trait name. Empty otherwise.
	Str string
	// Pos points back at the source position the lowering pass was
	// processing when this op was emitted. Backends use it to drive
	// DWARF .loc / WASM debug-line info; the field is zero for ops
	// the lowering pass synthesised without an obvious source span
	// (e.g. trailing implicit returns).
	Pos ast.Position
	// Ext holds the RARELY-populated payload fields (Str2 / Sig /
	// ArgTypes / CaptureSlots — see OpExt). On a driver-scale program
	// under 1% of ops carry any of them, but as inline fields they cost
	// every op 64 bytes: at ~12.5M ops for a self-host driver that was
	// ~800 MB of live heap right at the emit's memory peak. Nil for the
	// overwhelming majority of ops; read through the accessor methods
	// (Str2() / Sig() / ArgTypes() / CaptureSlots()), which are
	// nil-safe. The Ext block is written once at construction and never
	// mutated afterwards, so sharing a pointer across op copies is safe.
	Ext *OpExt
}

// OpExt is Op's side-table for rarely-populated fields — see Op.Ext.
type OpExt struct {
	// Str2 carries OpConstVtable's Concrete type name (Op.Str holds the
	// Trait); together they key the (Trait, Concrete) vtable.
	Str2 string
	// Sig is set on OpCallIndirect / OpCallDyn to the static signature
	// of the function-typed local being dispatched through. Codegen uses
	// it to resolve the right `(type $tN)` clause in the WAT output and
	// the register classes on the natives.
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
	// CaptureSlots is set on OpMakeClosure / OpMakeEnv to the per-capture
	// env-block slot size in bytes (irCaptureSlotSize of each capture's
	// type, in capture order) — the packed layout the CaptureRef loads
	// read. The native backends recompute it from the hoisted target's
	// Captures list; the SSA path, which has no AST at emit time, carries
	// it here so its env-store offsets/widths match the load side. Nil
	// means "one 8-byte slot per capture" (the legacy uniform layout that
	// hand-built SSA closures assume).
	CaptureSlots []int32
}

// Str2 returns Ext.Str2, or "" when the op carries no Ext block.
func (o *Op) Str2() string {
	if o.Ext == nil {
		return ""
	}
	return o.Ext.Str2
}

// Sig returns Ext.Sig, or nil when the op carries no Ext block.
func (o *Op) Sig() *ast.FuncType {
	if o.Ext == nil {
		return nil
	}
	return o.Ext.Sig
}

// ArgTypes returns Ext.ArgTypes, or nil when the op carries no Ext block.
func (o *Op) ArgTypes() []ast.Type {
	if o.Ext == nil {
		return nil
	}
	return o.Ext.ArgTypes
}

// CaptureSlots returns Ext.CaptureSlots, or nil when the op carries no
// Ext block.
func (o *Op) CaptureSlots() []int32 {
	if o.Ext == nil {
		return nil
	}
	return o.Ext.CaptureSlots
}

// Func is a single lowered function: parameter / local list, ops, and
// the return type. ScratchTypes carries the type of each synthetic
// slot the lowering pass / inliner conjured (for ArrayLit / StructLit
// / closure helpers and for inlined callees' params,
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
	// InlineHint carries the source-level `@inline` / `@noinline`
	// attribute through to the Inline pass (#4412 Rec §14).
	InlineHint ast.InlineHint
}

// ExternFunc is a body-less function bound to a WASM-component import via an
// `@import("wasi:iface@x.y.z", "wit-func-name")` attribute (bring-your-own
// WIT, P4 — docs/WIT-BRING-YOUR-OWN.md). It is NOT a defined function: it
// carries no Ops and is never emitted into the code section. The wasm backend
// declares it as a core wasm function import of (Iface, WITName) with a
// signature derived from Params/ReturnType, and a call to Name resolves to
// that import's funcidx. Other backends ignore it (extern WIT imports are
// component-model-only).
type ExternFunc struct {
	Name       string
	Iface      string
	WITName    string
	Params     []ast.Param
	ReturnType ast.Type
	// ParamRecords maps a parameter index to the flattened scalar-field layout
	// of a record (struct) parameter (P4c — docs/WIT-BRING-YOUR-OWN.md). A
	// record lowers to a canonical `record` whose fields flatten to their core
	// types, so the wasm backend's param wrapper loads each field from the Fern
	// struct and pushes it. nil/absent for a non-record param; absent for a
	// record the layout pass can't flatten (sub-word / composite fields, or
	// more than maxFlatExternRecordFields fields), which the wasm backend then
	// rejects with a clear error.
	ParamRecords map[int][]ExternRecordField
	// ResultRecord is the layout of a record (struct) RESULT (P4c), or nil if
	// the result isn't a lowerable record. A multi-field record flattens to >1
	// core value, so the canonical ABI returns it indirectly through a
	// return-area pointer; the wasm result wrapper reads each field from the
	// area and materializes a Fern struct.
	ResultRecord *ExternRecordResult
	// ParamEnums maps a parameter index to the flattened option/result layout
	// of an enum parameter (P4c). A Fern Option[T]/Result[T,E] is a heap box
	// `[tag:i32 @0][payload @off]`; the canonical option/result flattens to
	// (disc:i32, payload). nil/absent for a non-enum (or unlowerable) param.
	ParamEnums map[int]*ExternEnumParam
	// ResultEnum is the option/result layout of an enum RESULT (P4c), or nil.
	// A multi-arm variant flattens to > 1 core value, so it returns indirectly
	// through a return-area pointer (disc + payload); the wasm result wrapper
	// reads them and materializes a Fern enum box (remapping the discriminant).
	ResultEnum *ExternEnumParam
	// ParamPlainEnums marks a parameter index whose type is a "plain" enum — a
	// user enum with only payloadless variants (a C-style enum), which maps to a
	// WIT `enum`. A Fern payloadless enum value is a pointer to a 4-byte sentinel
	// `[tag:i32 @0]`, and the canonical WIT enum flattens to a single i32
	// discriminant, so the wasm param wrapper reads `i32.load(ptr)` and pushes
	// it. The Fern variant order must match the WIT enum case order (no remap).
	ParamPlainEnums map[int]bool
	// ResultPlainEnumN is the variant count of a plain (payloadless / C-style)
	// enum RESULT — a WIT `enum` return — or 0 if the result isn't one. The WIT
	// enum is returned as a single i32 discriminant; the wasm result wrapper maps
	// it back to a Fern payloadless enum value by selecting the matching static
	// per-tag sentinel (`[tag:i32 @0]`), so no heap box is allocated.
	ResultPlainEnumN int
	// Async marks an `@import(...) async function` — a WASI Preview-3
	// component-model-async import. Its call is colorless: the composer
	// lowers it with `canon lower async` (result delivered via a return
	// area) and the wasm backend wraps the call to await the result, so a
	// plain `dep()` returns the value. The enclosing function must be
	// `async`-lifted (own a task). Set from ast.FuncDecl.Async. See
	// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
	Async bool
	// StreamResultElem is set (to the element type) when the async import's
	// result is a `stream[T]` (the checker rewrote ReturnType to `T[]` and
	// stashed the element here). Non-nil means the result is delivered
	// incrementally — the wasm backend drives `stream.read` + the await loop to
	// collect a `T[]`, rather than the single-block list-result lowering. nil
	// otherwise. See docs/STREAM-TYPE-SURFACE.md.
	StreamResultElem ast.Type
	// StreamParamElems maps a parameter index to its element type when an async
	// import param is `stream[T]` (the checker rewrote it to `T[]`). Non-empty
	// means those params are produced as streams — the wasm backend creates a
	// stream and write-streams the eager array's elements over the wire. The
	// mirror of StreamResultElem.
	StreamParamElems map[int]ast.Type
}

// ExternEnumParam describes a flattened option/result `@import` parameter
// (P4c). The wasm wrapper loads the Fern box's tag (i32 @0), remaps it for
// option (whose Fern Some=0/None=1 is the reverse of canonical none=0/some=1),
// pushes the canonical discriminant, then loads + pushes the payload from
// PayloadOffset. Result needs no remap (Fern Ok=0/Err=1 matches canonical).
type ExternEnumParam struct {
	RemapDisc     bool
	PayloadType   ast.Type
	PayloadOffset int32
	// Variants is set for a MIXED-WIDTH variant (some payloaded arm is 32-bit
	// core, another 64-bit — join i64, per-arm coerce) OR a MULTI-FIELD variant
	// (some arm carries ≥2 payloads — join is SlotCount i32 slots). Indexed by
	// variant/disc index. When nil the single-slot path applies (PayloadType is
	// the join slot, PayloadOffset its box/area offset).
	Variants []ExternEnumVariant
	// SlotCount > 0 marks a MULTI-FIELD variant: the canonical join is SlotCount
	// slots, one per payload position. Each arm's Fields give its payload box
	// offsets (field j → slot j; arms with < SlotCount fields pad the trailing
	// slots with 0). 0 ⇒ single-field (BoxOffset/Type) or non-Variants.
	SlotCount int32
	// SlotTypes is the per-slot canonical *join* type of a MULTI-FIELD variant
	// (len == SlotCount): each slot j's type is the position-wise canonical join
	// of every arm's field j (i32/f32 → i32; otherwise unequal → i64; equal → that
	// type), which decides the slot's flat valtype + the param wrapper's load/coerce.
	// For an all-i32 multi-field variant every slot is i32 (the original shape).
	SlotTypes []ast.Type
	// AreaSize is the canonical return-area size (bytes) of a MULTI-FIELD variant
	// *result* — the variant memory layout `[disc][pad][payload]`, sized to the
	// widest arm's tuple and 8-rounded for the alloc. 0 for non-multi-field.
	AreaSize int32
}

// ExternEnumVariant is one arm's box payload descriptor for a mixed-width or
// multi-field variant (see ExternEnumParam.Variants). For a mixed-width
// (single-payload) variant, BoxOffset is the payload's box byte offset and Type
// its scalar type (nil ⇒ payloadless). For a multi-field variant, Fields holds
// the arm's payload box offsets in order (empty ⇒ payloadless), FieldTypes the
// matching field types (for the width-aware load/coerce), and FieldAreaOffsets
// the field's byte offset in the canonical result return-area (the arm's tuple
// memory layout shifted past the discriminant). The wrapper branches on the disc
// to load/store at the right offset(s).
type ExternEnumVariant struct {
	BoxOffset        int32
	Type             ast.Type
	Fields           []int32
	FieldTypes       []ast.Type
	FieldAreaOffsets []int32
}

// ExternRecordResult is the flattened layout of a record (struct) `@import`
// result: the scalar fields (offsets from the struct value + types) and the
// struct's field-area Size (excluding the rc header). The wasm result wrapper
// allocs the canonical return area + the Fern struct from Size. Direct is set
// when the composite has a single field: the canonical ABI then returns that
// field by value (it fits MAX_FLAT_RESULTS=1), so the raw import returns the
// field's core valtype directly rather than through a trailing return-area
// pointer, and the wrapper materializes the one-field struct from that value.
//
// The canonical return-area (memory) layout can differ from the Fern struct's:
// a Fern struct stores every sub-64-bit int in a 4-byte slot, while the
// canonical record packs s8/s16/u8/u16 at their natural 1-/2-byte size +
// alignment. So each field carries both its Fern struct Offset and its
// CanonicalOffset (into the return area), and the result holds the canonical
// CanonicalSize of the return area alongside the Fern struct Size. For
// word-only records (i32/i64/f32/f64) the two layouts coincide.
type ExternRecordResult struct {
	Fields        []ExternRecordField
	Size          int32
	CanonicalSize int32
	Direct        bool
}

// ExternRecordField is one scalar field of a record `@import` parameter,
// flattened to the canonical ABI: Offset is the field's byte offset from the
// struct *value* (the user-visible data pointer, i.e. base + rc header — field
// reads index straight off it), CanonicalOffset is its byte offset in the
// canonical return-area memory layout (used for record results, where sub-word
// fields pack tighter than the Fern struct's 4-byte slots), and Type decides
// the load op + the flat core valtype the wasm wrapper uses.
type ExternRecordField struct {
	Offset          int32
	CanonicalOffset int32
	Type            ast.Type
	// DerefPath supports a nested-record PARAM leaf to arbitrary depth: each entry
	// is a byte offset to dereference, in order, before the final leaf load. An
	// empty/nil path means a direct field (load at `[struct+Offset]`); a path of
	// `[o0, o1, …]` loads `[[[…[struct+o0]+o1]…]+Offset]` (deref each offset, then
	// the leaf at Offset). nil for a direct field and for every record-result field.
	DerefPath []int32
	// Nested supports a nested-record RESULT field (one level): when non-nil,
	// this outer field is itself a record. Nested.Fields are the inner scalar
	// leaves (their CanonicalOffset is the ABSOLUTE offset in the outer return
	// area; Offset is the inner struct's field offset), and Nested.Size is the
	// inner Fern struct's field-area size. The result wrapper allocs the inner
	// struct, fills it from the return area, and stores its pointer at the outer
	// field's Offset. nil for a scalar field.
	Nested *ExternRecordResult
}

// maxFlatExternRecordFields caps how many fields a flattened record parameter
// may have. The canonical ABI passes a record inline only while its flattened
// core values stay within MAX_FLAT_PARAMS (16); beyond that it goes through
// memory, which this slice doesn't lower. Each supported field flattens to one
// core value, so the field count is the flat count.
const maxFlatExternRecordFields = 16

// compositeFieldTypes returns the in-order field/element types of a record
// (struct) or tuple type, WITHOUT the flattenable-scalar check — so a field that
// is itself a composite is returned as-is (used by externRecordParamLeaves to
// recurse into a nested record). ok=false if t is neither a known struct nor a
// tuple.
func compositeFieldTypes(t ast.Type, info *checker.Info) ([]ast.Type, bool) {
	switch x := t.(type) {
	case ast.StructType:
		sd, ok := info.Structs[x.Name]
		if !ok {
			return nil, false
		}
		types := make([]ast.Type, len(sd.Fields))
		for i, f := range sd.Fields {
			types[i] = f.Type
		}
		return types, true
	case ast.TupleType:
		return append([]ast.Type{}, x.Elems...), true
	}
	return nil, false
}

// externRecordParamLeaves flattens a record/tuple `@import` PARAMETER to its
// scalar leaves, recursing into nested record/tuple fields to **arbitrary depth**
// (the canonical ABI flattens a nested record inline). Each leaf carries its load
// path: a direct field is `{DerefPath:nil, Offset}` (load at struct+Offset); a
// field nested N levels deep is `{DerefPath:[o0,…,o(N-1)], Offset:leaf}` (deref
// each offset in turn from the outer struct, then load the leaf at +Offset).
// Total leaves capped at maxFlatExternRecordFields.
func externRecordParamLeaves(t ast.Type, info *checker.Info) ([]ExternRecordField, bool) {
	leaves, ok := externParamLeavesRec(t, info, nil)
	if !ok || len(leaves) == 0 || len(leaves) > maxFlatExternRecordFields {
		return nil, false
	}
	return leaves, true
}

// externParamLeavesRec collects the scalar leaves of composite type `t`, with
// `prefix` the chain of deref offsets reaching `t` from the outermost struct. A
// scalar field becomes a leaf carrying `prefix` as its DerefPath; a nested
// composite field extends the prefix by the field's offset and recurses. ok=false
// if any field is neither a flattenable scalar nor a known composite.
func externParamLeavesRec(t ast.Type, info *checker.Info, prefix []int32) ([]ExternRecordField, bool) {
	top, ok := compositeFieldTypes(t, info)
	if !ok || len(top) == 0 {
		return nil, false
	}
	topOffs, _ := tupleElemLayout(top, 4)
	var leaves []ExternRecordField
	for i, ft := range top {
		if externRecordFieldSupported(ft) {
			leaves = append(leaves, ExternRecordField{DerefPath: prefix, Offset: topOffs[i], Type: ft})
			continue
		}
		// Nested composite: extend the deref path by this field's offset (a fresh
		// slice so siblings/leaves never alias) and recurse.
		childPrefix := append(append([]int32{}, prefix...), topOffs[i])
		sub, subOK := externParamLeavesRec(ft, info, childPrefix)
		if !subOK {
			return nil, false
		}
		leaves = append(leaves, sub...)
	}
	return leaves, true
}

// externEnumParamLayout describes how to flatten an option/result `@import`
// parameter, or ok=false if t isn't a lowerable one. Handles Option[T] (one
// scalar arg; discriminant remapped) and Result[T, E] (two equal-width scalar
// args; no remap) where the payload is a 32-/64-bit integer or float. The
// payload's box offset comes from payloadLayout (the slot after the i32 tag).
func externEnumParamLayout(t ast.Type, info *checker.Info, ptrW int) (*ExternEnumParam, bool) {
	et, ok := t.(ast.EnumType)
	if !ok {
		return nil, false
	}
	var payload ast.Type
	remap := false
	switch et.Name {
	case "Option":
		if len(et.Args) != 1 {
			return nil, false
		}
		payload, remap = et.Args[0], true
	case "Result":
		// First slice: ok and err must be the same-width scalar, so the box has
		// a single payload slot and the canonical join is that one type.
		if len(et.Args) != 2 || !externScalarTypeEq(et.Args[0], et.Args[1]) {
			return nil, false
		}
		payload = et.Args[0]
	default:
		return nil, false
	}
	if !externRecordFieldSupported(payload) {
		return nil, false
	}
	offs, _ := payloadLayout([]ast.Type{payload}, 1, ptrW)
	return &ExternEnumParam{RemapDisc: remap, PayloadType: payload, PayloadOffset: offs[0]}, true
}

// externVariantParamLayout describes a general user-enum `@import` PARAMETER
// that flattens like Result: (disc, payload). It accepts a user enum (named in
// info.Enums) with at least one payloaded variant, where every payloaded variant
// carries exactly one scalar payload; payloadless variants are allowed. The
// canonical `variant` flattens to (disc:i32, payload-join).
//
// Three payload shapes are lowered:
//   - Uniform: every payload is the same kind+width T, so the canonical join is
//     T and PayloadType is T (an f32 payload passes as an f32, etc.).
//   - Non-uniform but same core WIDTH (e.g. `{ i(s32), f(f32) }` — both 32-bit,
//     or `{ l(s64), d(f64) }` — both 64-bit): the canonical join is the integer
//     bit-container of that width (i32 or i64), so PayloadType is that synthetic
//     int. Lowering just moves the payload's bits (`i32.load`/`i64.load` of the
//     box slot); the consumer reinterprets per the disc. This works without any
//     per-arm branch because same-width payloads sit at the same box offset and
//     a bit-load is value-preserving for both int and float arms.
//   - Mixed core WIDTH (a 32-bit and a 64-bit arm, e.g. `{ i(s32), l(s64) }`):
//     the canonical join is i64, and each arm lives at its OWN box offset and
//     needs coercion (a 32-bit arm extends to / wraps from i64). PayloadType is
//     i64 and Variants carries each arm's box offset + type; the wrapper branches
//     on the disc to load/store at the right offset+width (appendVariantParam-
//     PayloadI64 / appendVariantResultStore).
//
// Multi-payload (multi-field) variants are still deferred. Option/Result are
// handled by externEnumParamLayout; an all-payloadless enum by externPlainEnumParam
// (this requires ≥1 payloaded variant). The wrapper reads the tag at +0; for a
// payloadless-case
// value (a sentinel) the payload read yields ignored garbage (the host drops it
// for that disc), so it's harmless.
func externVariantParamLayout(t ast.Type, info *checker.Info, ptrW int) (*ExternEnumParam, bool) {
	et, ok := t.(ast.EnumType)
	if !ok {
		return nil, false
	}
	ed, ok := info.Enums[et.Name]
	if !ok || len(ed.Variants) == 0 {
		return nil, false
	}
	maxFields := 0
	anyPayloaded := false
	for _, v := range ed.Variants {
		if len(v.Payloads) > maxFields {
			maxFields = len(v.Payloads)
		}
		if len(v.Payloads) >= 1 {
			anyPayloaded = true
		}
	}
	if !anyPayloaded {
		return nil, false // all-payloadless ⇒ a plain enum (externPlainEnumParam)
	}
	if maxFields >= 2 {
		// Multi-field variant: a WIT case wraps ≥2 values in a tuple, which the
		// canonical ABI joins position-wise into maxFields flat slots. Each slot j's
		// join type is the fold of join(field-j) over every arm (i32/f32 → i32;
		// other unequal pairs → i64; equal → that type), so a slot may be i32, i64,
		// f32, or f64. The param side pushes each slot from the matched arm's box
		// field, coercing to the slot type; the result side reads the canonical
		// variant *memory* layout (each arm's tuple packed past the discriminant).
		// Scoped to 32-/64-bit numeric/float fields (sub-word fields would pack at
		// 1-/2-byte canonical sizes — a separate slice). Each arm: field j → slot j;
		// shorter arms pad trailing slots with 0.
		variants := make([]ExternEnumVariant, len(ed.Variants))
		slots := make([]ast.Type, maxFields)
		haveSlot := make([]bool, maxFields)
		payloadAlign := int32(1)
		for i, v := range ed.Variants {
			for _, p := range v.Payloads {
				if !externMultiFieldVariantFieldOK(p) {
					return nil, false // sub-word / unsupported field in a multi-field arm
				}
			}
			if len(v.Payloads) == 0 {
				continue
			}
			boxOffs, _ := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
			variants[i] = ExternEnumVariant{
				Fields:     boxOffs,
				FieldTypes: append([]ast.Type{}, v.Payloads...),
			}
			for j, p := range v.Payloads {
				if a := externCanonicalFieldSizeAlign(p); a > payloadAlign {
					payloadAlign = a
				}
				ft := externCanonicalFlatType(p)
				if !haveSlot[j] {
					slots[j], haveSlot[j] = ft, true
				} else {
					slots[j] = externCanonicalFlatJoin(slots[j], ft)
				}
			}
		}
		// Canonical variant memory layout (result side): a 1-byte discriminant
		// (≤256 cases — the multi-field scope), then the payload aligned to the
		// widest field. Each arm's fields sit at its own tuple offsets past there.
		payloadOff := alignUp(1, payloadAlign)
		areaSize := payloadOff
		for i, v := range ed.Variants {
			if len(v.Payloads) == 0 {
				continue
			}
			areaOffs := make([]int32, len(v.Payloads))
			pos := payloadOff
			for j, p := range v.Payloads {
				sz := externCanonicalFieldSizeAlign(p)
				pos = alignUp(pos, sz)
				areaOffs[j] = pos
				pos += sz
			}
			variants[i].FieldAreaOffsets = areaOffs
			if pos > areaSize {
				areaSize = pos
			}
		}
		areaSize = alignUp(areaSize, 8) // __fern_alloc is 8-aligned; covers a max-width load
		return &ExternEnumParam{
			RemapDisc:   false,
			PayloadType: ast.NumberType{Width: 32, Signed: true},
			SlotCount:   int32(maxFields),
			SlotTypes:   slots,
			AreaSize:    areaSize,
			Variants:    variants,
		}, true
	}
	// Single-field arms (0 or 1 payload each).
	var firstPayload ast.Type
	uniform := true
	mixedWidth := false
	for _, v := range ed.Variants {
		if len(v.Payloads) != 1 {
			continue
		}
		p := v.Payloads[0]
		if !externRecordFieldSupported(p) {
			return nil, false
		}
		if firstPayload == nil {
			firstPayload = p
		} else {
			if externCanonicalCoreWidth(firstPayload) != externCanonicalCoreWidth(p) {
				mixedWidth = true // 32-bit and 64-bit arms — join is i64, per-arm coerce
			}
			if !externScalarTypeEq(firstPayload, p) {
				uniform = false // non-uniform — join is a bit-container
			}
		}
	}
	if mixedWidth {
		// The canonical join of a mixed-width arm set is i64; each arm is coerced
		// to/from it (a 32-bit arm extends/wraps) and lives at its own box offset.
		i64Slot := ast.NumberType{Width: 64, Signed: true}
		areaOffs, _ := payloadLayout([]ast.Type{i64Slot}, 1, ptrW)
		variants := make([]ExternEnumVariant, len(ed.Variants))
		for i, v := range ed.Variants {
			if len(v.Payloads) == 1 {
				boxOffs, _ := payloadLayout([]ast.Type{v.Payloads[0]}, 1, ptrW)
				variants[i] = ExternEnumVariant{BoxOffset: boxOffs[0], Type: v.Payloads[0]}
			}
		}
		return &ExternEnumParam{RemapDisc: false, PayloadType: i64Slot, PayloadOffset: areaOffs[0], Variants: variants}, true
	}
	// Single-slot path: the canonical join slot is the payload type itself when
	// uniform, else the integer bit-container of the shared core width.
	slot := firstPayload
	if !uniform {
		slot = ast.NumberType{Width: int(externCanonicalCoreWidth(firstPayload)), Signed: true}
	}
	offs, _ := payloadLayout([]ast.Type{slot}, 1, ptrW)
	return &ExternEnumParam{RemapDisc: false, PayloadType: slot, PayloadOffset: offs[0]}, true
}

// externVariantResultLayout describes a general user-enum `@import` RESULT that
// flattens like Result to (disc, payload) and is returned indirectly. It accepts
// a user enum with a uniform payload — every payloaded variant carries exactly
// one scalar of the same kind+width T — and **allows payloadless variants** (a
// mixed `variant`, e.g. `{ circle(s32), empty }`); ≥1 must be payloaded. The
// uniform payload makes the canonical join T, so it reuses
// buildExternEnumResultWrapper (which materializes a Fern enum box `[rc][tag@0]
// [payload@off]`) with no discriminant remap. A payloadless case is materialized
// as that same box with an unused payload — exactly how option/result results
// already materialize their payloadless arm (None / payloadless Err), so it's a
// tag-correct, match-correct value (the box's unused payload is never read).
// Option/Result themselves are handled by externEnumParamLayout.
func externVariantResultLayout(t ast.Type, info *checker.Info, ptrW int) (*ExternEnumParam, bool) {
	// Same shape rules as a variant parameter: a uniform single-scalar payload
	// across the payloaded variants, payloadless variants allowed, ≥1 payloaded.
	return externVariantParamLayout(t, info, ptrW)
}

// externPlainEnumParam reports whether t is a "plain" enum — a user enum (named
// in info.Enums) with at least one variant and *every* variant payloadless (a
// C-style enum). Such an enum maps to a WIT `enum`, which flattens to a single
// i32 discriminant. A Fern payloadless enum value is a pointer to a 4-byte
// sentinel `[tag:i32 @0]`, so the wasm wrapper reads `i32.load(ptr)` for the
// canonical disc (param) or allocs a `[tag]` box from it (result, deferred).
// Option/Result are excluded — their Some/Ok variants carry a payload, so they
// fail the all-payloadless test (and are handled by externEnumParamLayout).
func externPlainEnumParam(t ast.Type, info *checker.Info) bool {
	et, ok := t.(ast.EnumType)
	if !ok {
		return false
	}
	ed, ok := info.Enums[et.Name]
	if !ok || len(ed.Variants) == 0 {
		return false
	}
	for _, v := range ed.Variants {
		if len(v.Payloads) != 0 {
			return false
		}
	}
	return true
}

// externScalarTypeEq reports whether two extern-supported scalar types are the
// same kind + width (used to gate Result[T, E] to a single payload slot).
func externScalarTypeEq(a, b ast.Type) bool {
	switch x := a.(type) {
	case ast.NumberType:
		y, ok := b.(ast.NumberType)
		return ok && x.NormalWidth() == y.NormalWidth()
	case ast.FloatType:
		y, ok := b.(ast.FloatType)
		return ok && x.NormalWidth() == y.NormalWidth()
	}
	return false
}

// externCanonicalCoreWidth returns the canonical core width (32 or 64) a scalar
// flattens to: 64 for a 64-bit integer or f64, 32 for everything narrower (i8…i32,
// bool, f32). Two scalars of the same core width share a single canonical flat
// slot — and, since a Fern enum box lays out same-width payloads at the same
// offset, a single box payload offset — which is what lets a non-uniform variant
// with same-width arms reuse the one-slot (disc, payload) wrapper.
func externCanonicalCoreWidth(t ast.Type) int32 {
	switch x := t.(type) {
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return 64
		}
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return 64
		}
	}
	return 32
}

// externMultiFieldVariantFieldOK gates a field type a multi-field `variant` arm
// may carry: a 32-/64-bit integer or a float. Each flattens to a single core
// value (i32/i64/f32/f64) and packs at its natural 4-/8-byte size + alignment in
// the canonical memory layout, so the join + return-area offset arithmetic stays
// width-only. Sub-word integers (s8/s16/u8/u16) — which the canonical ABI packs
// at 1/2 bytes — are deferred for multi-field arms.
func externMultiFieldVariantFieldOK(t ast.Type) bool {
	switch x := t.(type) {
	case ast.NumberType:
		w := x.NormalWidth()
		return w == 32 || w == 64
	case ast.FloatType:
		return true
	}
	return false
}

// externCanonicalFlatType returns the canonical flat type a scalar lowers to:
// i64 for a 64-bit integer, f64 for an f64, f32 for an f32, and i32 for every
// narrower integer. It is the per-position input to externCanonicalFlatJoin.
func externCanonicalFlatType(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return ast.FloatType{Width: 64}
		}
		return ast.FloatType{Spelling: "f32"}
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return ast.NumberType{Width: 64, Signed: true}
		}
	}
	return ast.NumberType{Width: 32, Signed: true}
}

// externCanonicalFlatJoin computes the Component-Model `join` of two canonical
// flat types (each already i32/i64/f32/f64 from externCanonicalFlatType): equal
// types join to themselves; an {i32, f32} pair joins to i32 (a 32-bit bit
// container); every other unequal pair joins to i64.
func externCanonicalFlatJoin(a, b ast.Type) ast.Type {
	ka, kb := externFlatKind(a), externFlatKind(b)
	if ka == kb {
		return a
	}
	if (ka == flatI32 && kb == flatF32) || (ka == flatF32 && kb == flatI32) {
		return ast.NumberType{Width: 32, Signed: true}
	}
	return ast.NumberType{Width: 64, Signed: true}
}

// flatKind enumerates the four canonical flat core types.
const (
	flatI32 = iota
	flatI64
	flatF32
	flatF64
)

// externFlatKind classifies a canonical flat type into one of the four core
// kinds (flatI32/flatI64/flatF32/flatF64).
func externFlatKind(t ast.Type) int {
	switch x := t.(type) {
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return flatF64
		}
		return flatF32
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return flatI64
		}
	}
	return flatI32
}

// alignUp rounds x up to the next multiple of a (a power of two ≥ 1).
func alignUp(x, a int32) int32 {
	if a <= 1 {
		return x
	}
	return (x + a - 1) / a * a
}

// externRecordFieldSupported reports whether a record/tuple field type is one
// this slice flattens as a PARAMETER: an 8-/16-/32-/64-bit integer or a float.
// Each flattens to a single core value — i64 for a 64-bit int, f32/f64 for
// floats, i32 for everything narrower (the canonical ABI flattens s8/s16/u8/u16
// to i32). A Fern struct stores every sub-64-bit int in a 4-byte slot, so the
// param wrapper reads a sub-word field with a width+sign-aware load
// (i32.load8_s/u, i32.load16_s/u) to produce the correctly extended i32. Sub-word
// fields in a *result* are still rejected (see externRecordResultLayout): the
// canonical return-area packs them tightly (1/2-byte, different offsets) than
// the Fern struct's 4-byte slots, which needs a separate slice. bool and
// composites are deferred.
func externRecordFieldSupported(t ast.Type) bool {
	switch x := t.(type) {
	case ast.NumberType:
		w := x.NormalWidth()
		return w == 8 || w == 16 || w == 32 || w == 64
	case ast.FloatType:
		return true
	case ast.BoolType:
		// bool flattens to a single i32 core value (param) and is one byte in the
		// canonical record memory layout (result) — handled like an unsigned
		// 8-bit: a Fern bool is 0/1 in a 4-byte slot, so the field helpers read it
		// with i32.load8_u and size it at 1 byte canonically.
		return true
	}
	return false
}

// externRecordLayout flattens a record (struct) or tuple `@import` parameter
// type to its scalar fields' offsets + types, or returns ok=false if it can't
// be lowered: 1..maxFlatExternRecordFields scalar fields. Offsets are measured
// from the composite value (the user-visible data pointer), so the wasm wrapper
// loads each field straight off it — the same indexing a `p.field` read uses.
func externRecordLayout(t ast.Type, info *checker.Info) ([]ExternRecordField, bool) {
	// Params flatten to scalar leaves, recursing one level into a nested
	// record/tuple field (the canonical ABI inlines a nested record). Each leaf
	// carries its load path (DerefOffset for the nested case).
	return externRecordParamLeaves(t, info)
}

// externRecordResultLayout flattens a record (struct) or tuple `@import`
// *result* to its scalar fields' (Fern + canonical) offsets + types + sizes, or
// ok=false if it can't be lowered. Requires 1..maxFlatExternRecordFields scalar
// fields. A multi-field composite flattens to > 1 core value, so it returns
// indirectly through a return-area pointer; a single-field composite flattens
// to exactly one core value (fits MAX_FLAT_RESULTS=1) and so the canonical ABI
// returns it by value — recorded as Direct, the raw import returns the field's
// valtype directly rather than via a trailing area pointer.
//
// Each field gets two offsets: its Fern struct Offset (4-byte slots, where a
// `p.field` read finds it) and its CanonicalOffset (the canonical memory layout
// of the return area, where sub-word fields pack tighter). For word-only records
// the two coincide.
// externFieldsAllFlat reports whether every field is a direct scalar (no nested
// composite) — the shape the P6 tuple-result export wrapper handles.
func externFieldsAllFlat(fields []ExternRecordField) bool {
	for i := range fields {
		if fields[i].Nested != nil {
			return false
		}
	}
	return true
}

func externRecordResultLayout(t ast.Type, info *checker.Info) (*ExternRecordResult, bool) {
	top, ok := compositeFieldTypes(t, info)
	if !ok || len(top) < 1 || len(top) > maxFlatExternRecordFields {
		return nil, false
	}
	fields, fernSize, canonEnd, maxAlign, leaves, ok := externResultLayoutRec(top, info, 0)
	if !ok || leaves < 1 || leaves > maxFlatExternRecordFields {
		return nil, false
	}
	directScalar := len(fields) == 1 && fields[0].Nested == nil
	if leaves == 1 && !directScalar {
		// A single-leaf record flattens to one core value and so is returned by
		// value — but the by-value (Direct) wrapper can't reconstruct a nested
		// struct, so reject a single-leaf-via-nested record (a niche shape).
		return nil, false
	}
	csize := alignUpI32(canonEnd, maxAlign)
	return &ExternRecordResult{Fields: fields, Size: fernSize, CanonicalSize: csize, Direct: directScalar && leaves == 1}, true
}

// externResultLayoutRec lays out the in-order field/element types `top` of a
// composite result into the canonical return area starting at byte `canonStart`,
// returning the per-field layout, the composite's Fern struct size (4-byte
// slots), the end canonical position, the composite's max field alignment, the
// total scalar-leaf count, and ok. A scalar field is placed directly (Fern slot
// Offset + CanonicalOffset). A nested composite field recurses **to arbitrary
// depth**: the canonical ABI inlines the nested record's leaves at the nested
// record's alignment, while on the Fern side it lives in a separate inner struct
// (ExternRecordField.Nested) whose pointer is the outer field. ok=false if any
// field is neither a flattenable scalar nor a known composite.
func externResultLayoutRec(top []ast.Type, info *checker.Info, canonStart int32) (fields []ExternRecordField, fernSize, canonEnd, maxAlign int32, leaves int, ok bool) {
	fernOffs, fernSize := tupleElemLayout(top, 4)
	fields = make([]ExternRecordField, 0, len(top))
	canonPos := canonStart
	maxAlign = 1
	for i, ft := range top {
		if externRecordFieldSupported(ft) { // direct scalar leaf
			sz := externCanonicalFieldSizeAlign(ft)
			if sz > maxAlign {
				maxAlign = sz
			}
			canonPos = alignUpI32(canonPos, sz)
			fields = append(fields, ExternRecordField{Offset: fernOffs[i], CanonicalOffset: canonPos, Type: ft})
			canonPos += sz
			leaves++
			continue
		}
		// Nested composite: align to its own alignment, recurse to lay out its
		// leaves inline, then round its end up to its alignment (canonical struct
		// tail padding).
		inner, innerOK := compositeFieldTypes(ft, info)
		if !innerOK || len(inner) == 0 {
			return nil, 0, 0, 0, 0, false
		}
		innerAlign := externCompositeAlign(ft, info)
		if innerAlign > maxAlign {
			maxAlign = innerAlign
		}
		canonPos = alignUpI32(canonPos, innerAlign)
		innerFields, innerFernSize, innerEnd, _, innerLeaves, innerRecOK := externResultLayoutRec(inner, info, canonPos)
		if !innerRecOK {
			return nil, 0, 0, 0, 0, false
		}
		canonPos = alignUpI32(innerEnd, innerAlign)
		leaves += innerLeaves
		fields = append(fields, ExternRecordField{
			Offset: fernOffs[i],
			Nested: &ExternRecordResult{Fields: innerFields, Size: innerFernSize},
		})
	}
	return fields, fernSize, canonPos, maxAlign, leaves, true
}

// externCompositeAlign returns the canonical-ABI alignment of a composite type:
// the max alignment of its fields, recursing into nested composites. Returns 1
// for a non-composite (the caller only uses it on composite fields).
func externCompositeAlign(t ast.Type, info *checker.Info) int32 {
	types, ok := compositeFieldTypes(t, info)
	if !ok {
		return 1
	}
	a := int32(1)
	for _, ft := range types {
		var fa int32
		if externRecordFieldSupported(ft) {
			fa = externCanonicalFieldSizeAlign(ft)
		} else {
			fa = externCompositeAlign(ft, info)
		}
		if fa > a {
			a = fa
		}
	}
	return a
}

// alignUpI32 rounds n up to a multiple of a (a a power of two ≥ 1).
func alignUpI32(n, a int32) int32 {
	if rem := n % a; rem != 0 {
		return n + (a - rem)
	}
	return n
}

// externCanonicalFieldSizeAlign returns the canonical-ABI memory size + alignment
// (in bytes) of a flattenable record field type: u8 → 1, s32/u32/f32 → 4,
// s64/u64/f64 → 8. Size == alignment for these scalars.
func externCanonicalFieldSizeAlign(t ast.Type) int32 {
	switch x := t.(type) {
	case ast.NumberType:
		switch x.NormalWidth() {
		case 8:
			return 1
		case 64:
			return 8
		}
		return 4
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return 8
		}
		return 4
	case ast.BoolType:
		return 1 // canonical bool is one byte
	}
	return 4
}

// ExternExport binds a defined function (Name) to the WIT world export
// (Iface, WITName) it implements, via an `@export(...)` attribute (P6 —
// docs/WIT-BRING-YOUR-OWN.md). Unlike ExternFunc the function keeps its body
// and is lowered normally into Funcs; ExternExport just records the binding so
// the wasm backend can surface a core export the composer lifts.
type ExternExport struct {
	Name    string
	Iface   string
	WITName string
	// ResultEnum is the option/result layout when an `@export` returns an
	// Option[T] / Result[T,E] (a WIT `option` / `result`), else nil. The
	// canonical sum flattens to (disc, payload) > 1 core value, so the result is
	// returned indirectly through a return area; the wasm backend surfaces a
	// wrapper that reads the Fern enum box and writes (disc:u8@0, payload@off),
	// with the option discriminant remapped (P6 — docs/WIT-BRING-YOUR-OWN.md).
	// Resolved here during lowering, where checker.Info is in scope.
	ResultEnum *ExternEnumParam
	// ResultTuple is the layout when an `@export` returns a tuple `(A, B, …)` (a
	// WIT `tuple`), else nil. A multi-element tuple flattens to > 1 core value, so
	// it returns indirectly through a return area; the wasm backend surfaces a
	// wrapper that reads the Fern tuple's elements and writes them at the
	// canonical offsets (P6). Records (named WIT types) are deferred — they need
	// the exported-instance type-export machinery — so this is set only for
	// tuples. Resolved here during lowering, where checker.Info is in scope.
	ResultTuple *ExternRecordResult
}

// Program is the lowered form of an entire ast.Program.
type Program struct {
	Funcs []*Func
	// Externs lists the body-less `@import` functions (extern WASM-component
	// imports). They are kept out of Funcs so every backend's defined-function
	// machinery is unaffected; only the wasm backend consults them.
	Externs []*ExternFunc
	// Exports lists the `@export`-bound functions (P6 — bind a Fern function to
	// a WIT world export, docs/WIT-BRING-YOUR-OWN.md). Each entry pairs the
	// (defined) function's Name with the world export (Iface, WITName) it
	// implements. The wasm backend surfaces a core export `Iface#WITName` for
	// each so the world-driven composer can lift it as the named world export.
	Exports []ExternExport
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
	// Vtables lists the per-(trait, concrete-type) method dispatch
	// tables a `dyn Trait` value's vtable word points at — one entry
	// per concrete type that implements a trait used in a `dyn` type
	// anywhere in the program. Each backend emits these as static data
	// (a pointer/table-index array of the impl methods in trait
	// declaration order) and a `dyn` method call loads slot k from the
	// receiver's vtable word. Populated by collectVtables once the
	// compiled-backend `dyn` slices wire it in; nil until then (the IR
	// still rejects `dyn` on compiled backends — docs/DYN-TRAITS.md §4.2).
	Vtables []VtableDecl
}

// VtableDecl is the static dispatch table for one (trait, concrete-type)
// pair: the concrete type's implementations of the trait's methods, in
// the trait's declaration order, so slot k is always the same method for
// every type implementing the trait. A `dyn Trait` fat pointer's vtable
// word points at the emitted table for its boxed concrete type.
type VtableDecl struct {
	Trait    string         // trait name (mangled, as in Info.Traits)
	Concrete string         // concrete type name (mangled, as in Info.Impls)
	Methods  []VtableMethod // dispatchable methods, trait declaration order
	// Drop is the concrete type's recursive-drop function name
	// (dropFnNameFor(C) → __drop_struct_<C> / __drop_enum_<…> /
	// __drop_tuple_<…>), or "" when C needs no drop (a flat scalar
	// struct). The RC slices (docs/DYN-TRAITS.md §4.4) emit this as a
	// TRAILING vtable slot at index len(Methods) — a backend's drop
	// helper reads vtable[len(Methods)] and call_indirects it (or
	// skips on a null sentinel) to run the erased concrete destructor.
	// Trailing keeps the method slot indices (0..n-1) unchanged, so
	// OpCallDyn's slot math is untouched and a backend that hasn't
	// wired RC yet simply omits the slot (harmless). For a merged
	// `dyn A + B` vtable the drop slot is at the MERGED method count
	// (len(Methods) across the whole set).
	Drop string
}

// VtableMethod is one slot of a VtableDecl: the trait method name and the
// mangled concrete function that implements it (the `recv`-taking flat
// function every backend already emits for a receiver method).
type VtableMethod struct {
	Method string // trait method name (slot identity)
	Func   string // mangled impl function, e.g. "__method_Circle_area"
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
	case OpCallDirect, OpCallClosureDirect, OpCallDirectPair,
		OpRcInc, OpRcDec, OpRcIsUnique:
		return fmt.Sprintf("%s %s argc=%d", op.Kind, op.Str, op.I32)
	case OpCallIndirect:
		return fmt.Sprintf("%s argc=%d", op.Kind, op.I32)
	case OpConstVtable:
		return fmt.Sprintf("%s %s/%s", op.Kind, op.Str, op.Str2())
	case OpCallDyn:
		return fmt.Sprintf("%s slot=%d", op.Kind, op.I32)
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

// dynSigResolver returns a type-rewriter that resolves a dyn-call signature's
// generic type parameters (`T`) and pinned associated types (`Self::Item`)
// against the receiver's pins — so OpCallDyn's signature is concrete (the wasm
// seam can't classify a bare ParamType / ProjType). `dynTrait` is the owning
// trait; `mcs` carries the receiver `DynTraitType` (with Args / AssocBindings).
// A nil/empty-pin receiver yields an identity rewriter.
func dynSigResolver(info *checker.Info, dynTrait string, mcs *ast.MethodCallSite) func(ast.Type) ast.Type {
	typeParams := map[string]ast.Type{}
	assoc := map[string]ast.Type{}
	if mcs != nil {
		if dt, ok := mcs.Receiver.(ast.DynTraitType); ok {
			for i, tr := range dt.Traits {
				if tr != dynTrait {
					continue
				}
				if td, ok := info.Traits[tr]; ok {
					args := dt.ArgsFor(i)
					for k, tp := range td.TypeParams {
						if k < len(args) {
							typeParams[tp] = args[k]
						}
					}
				}
				for _, b := range dt.AssocFor(i) {
					assoc[b.Name] = b.Type
				}
				break
			}
		}
	}
	if len(typeParams) == 0 && len(assoc) == 0 {
		return func(t ast.Type) ast.Type { return t }
	}
	var rw func(ast.Type) ast.Type
	rw = func(t ast.Type) ast.Type {
		switch x := t.(type) {
		case ast.ParamType:
			if r, ok := typeParams[x.Name]; ok {
				return r
			}
			return t
		case ast.ProjType:
			if r, ok := assoc[x.Name]; ok {
				return r
			}
			return ast.ProjType{Base: rw(x.Base), Name: x.Name}
		case ast.ArrayType:
			return ast.ArrayType{Elem: rw(x.Elem)}
		case ast.SliceType:
			return ast.SliceType{Elem: rw(x.Elem)}
		case ast.TupleType:
			out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
			for i := range x.Elems {
				out.Elems[i] = rw(x.Elems[i])
			}
			return out
		case ast.StructType:
			if len(x.Args) == 0 {
				return x
			}
			args := make([]ast.Type, len(x.Args))
			for i := range x.Args {
				args[i] = rw(x.Args[i])
			}
			return ast.StructType{Name: x.Name, Args: args}
		case ast.EnumType:
			if len(x.Args) == 0 {
				return x
			}
			args := make([]ast.Type, len(x.Args))
			for i := range x.Args {
				args[i] = rw(x.Args[i])
			}
			return ast.EnumType{Name: x.Name, Args: args}
		}
		return t
	}
	return rw
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

// rejectDowncast scans a program for any `e as? T` downcast
// (DowncastExpr) that this backend cannot lower. The fallible downcast
// lowers to a runtime vtable-pointer compare reusing the slice-2 vtable
// infrastructure (docs/DYN-TRAITS.md §9), so it is supported on every
// backend that supports `dyn` (dynSupported). Two cases still reject
// cleanly rather than miscompiling:
//
//   - a backend that has NOT lifted its `dyn` gate (!dynSupported) — the
//     vtable infrastructure the compare needs isn't emitted there;
//   - a non-struct/enum (primitive/string) downcast target — slice-1
//     scope is struct/enum only (the checker already blocks primitives
//     with E060; this is a defensive belt-and-braces guard).
func rejectDowncast(prog *ast.Program, info *checker.Info, dynSupported bool) error {
	const unsupportedBackend = "'as?' downcast (dyn Trait → concrete) is not yet supported on this backend; run it on the interpreter (fern -interp)"
	const primTarget = "'as?' downcast target must be a concrete struct or enum (slice 1); primitive targets are not yet supported on compiled backends"
	var rejected error
	ast.WalkProgram(prog, func(n ast.Node) bool {
		dc, ok := n.(*ast.DowncastExpr)
		if !ok {
			return true
		}
		if !dynSupported {
			rejected = fmt.Errorf("ir: %s", unsupportedBackend)
			return false
		}
		// Multi-trait `dyn A + B` downcast lowers via the MERGED-vtable
		// address (dynVtableSetKey(dc.Traits)) — the same merged cell a
		// multi-trait coercion of T stores — so the vtable-pointer compare
		// is exact for any trait set (docs/DYN-TRAITS.md §10). emitDowncast
		// keys OpConstVtable by the set, byte-identical to single-trait for
		// a 1-element set (dynVtableSetKey of a 1-element set is the bare
		// trait name).
		// Struct/enum target only (slice-1 scope). A primitive/string
		// target carries no runtime type tag in the compiled
		// representations, so the vtable-pointer compare can't recover it.
		if name, ok := downcastTargetName(dc.Target); !ok || isPrimitiveConcrete(info, name) {
			rejected = fmt.Errorf("ir: %s", primTarget)
			return false
		}
		return true
	})
	return rejected
}

// downcastTargetName returns the concrete type-name string for a
// struct/enum downcast target (the `T` in `e as? T`), matching the
// spelling collectVtables / OpConstVtable use for the (trait,concrete)
// vtable. Returns ("", false) for any non-struct/enum target.
func downcastTargetName(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, true
	case ast.EnumType:
		return x.Name, true
	}
	return "", false
}

// dynVtableSetKey returns the canonical key string for a (possibly
// multi-trait) `dyn` trait set, used as the `VtableDecl.Trait` /
// `OpConstVtable.Str` identity for a MERGED vtable. The traits are
// joined in their already-sorted set order with '+' (which can never
// appear in a Fern identifier or a mangled name, so the join is
// unambiguous). A single-trait set returns the bare trait name
// UNCHANGED — so single-trait vtable keys, OpConstVtable emission, and
// backend lookups are byte-for-byte identical to before merged vtables
// existed (docs/DYN-TRAITS.md §10).
func dynVtableSetKey(traits []string) string {
	if len(traits) == 1 {
		return traits[0]
	}
	return strings.Join(traits, "+")
}

// dynDropFnName returns the per-trait-set `__drop_dyn_<set>` destructor
// helper symbol for a `dyn` value (docs/DYN-TRAITS.md §4.4). The set key
// can contain '+' (the multi-trait join, e.g. "A+B"); '+' is fine as a
// wasm function name but is NOT a valid GAS assembler-label character, so
// it is sanitized to "_x_" here — the SAME transform dynVtableLabel applies
// to native vtable cell labels. The transform is applied at every site that
// names the helper (the definition in buildDynDropHelpers and the
// OpCallDirect targets in the dec/drop arms), so the symbol is consistent.
// A single-trait set is a bare trait name (no '+'), so single-trait helper
// names are byte-for-byte unchanged.
func dynDropFnName(traits []string) string {
	return "__drop_dyn_" + strings.ReplaceAll(dynVtableSetKey(traits), "+", "_x_")
}

// dynTraitSetsUsed collects every `dyn` trait SET (the whole sorted
// trait list) named in a `dyn Trait` / `dyn A + B` type anywhere in the
// program — function signatures, local-var annotations, struct fields,
// enum-variant payloads, cast targets, and nested inside composite types
// (a `dyn Trait` type-argument like `Result[_, dyn E]`, or a `dyn Trait`
// field/payload). The result maps the set key (dynVtableSetKey) → the
// sorted trait list, so collectVtables can emit one merged vtable per
// multi-trait set actually used. Single-trait sets are included too (key
// == the trait name). Missing one makes its vtable cell emit empty and a
// dispatch on such a value calls a garbage pointer (see #3213).
func dynTraitSetsUsed(prog *ast.Program) map[string][]string {
	sets := map[string][]string{}
	record := func(traits []string) {
		if len(traits) == 0 {
			return
		}
		sets[dynVtableSetKey(traits)] = traits
	}
	var walk func(t ast.Type)
	walk = func(t ast.Type) {
		switch x := t.(type) {
		case ast.DynTraitType:
			record(x.Traits)
		case ast.ArrayType:
			walk(x.Elem)
		case ast.SliceType:
			walk(x.Elem)
		case ast.TupleType:
			for _, e := range x.Elems {
				walk(e)
			}
		case ast.StructType:
			// `dyn` nested in a generic struct's type-argument, e.g.
			// `Holder[dyn Shape]`.
			for _, a := range x.Args {
				walk(a)
			}
		case ast.EnumType:
			// `dyn` nested in an enum's type-argument — the common case
			// being `Result[_, dyn Error]` / `Option[dyn Shape]`.
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.FuncType:
			for _, p := range x.Params {
				walk(p)
			}
			walk(x.Result)
		}
	}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			for _, p := range x.Params {
				walk(p.Type)
			}
			walk(x.ReturnType)
			if x.Receiver != nil {
				walk(x.Receiver.Type)
			}
		case *ast.Var:
			walk(x.Type)
		case *ast.StructDecl:
			// A `dyn Trait` stored in a struct field — the field may be
			// the only place a trait appears (#3213).
			for _, f := range x.Fields {
				walk(f.Type)
			}
		case *ast.EnumDecl:
			for _, v := range x.Variants {
				for _, p := range v.Payloads {
					walk(p)
				}
			}
		case *ast.ConstDecl:
			walk(x.Type)
		case *ast.CastExpr:
			// `x as dyn Trait` — the coercion target names the trait.
			walk(x.Target)
		}
		return true
	})
	return sets
}

// dynTraitMethodPrefix returns the number of vtable slots that precede
// trait `owner`'s method block in the MERGED vtable for trait set
// `traits` (sorted set order). For a single-trait set this is always 0
// (the owner is the only trait), so single-trait slot math is unchanged.
// For `dyn A + B`, a method owned by B sits after all of A's
// non-associated methods, so its global slot = prefix(B) + index-in-B.
func dynTraitMethodPrefix(info *checker.Info, traits []string, owner string) int {
	prefix := 0
	for _, tr := range traits {
		if tr == owner {
			break
		}
		td, ok := info.Traits[tr]
		if !ok {
			continue
		}
		for i := range td.Methods {
			if !td.Methods[i].Assoc {
				prefix++
			}
		}
	}
	return prefix
}

// isPrimitiveConcrete reports whether a `dyn` coercion's concrete
// type-name string names a primitive / `string` type rather than a
// struct or enum. A struct/enum value is already a heap pointer (so its
// `data` word can hold it directly); a primitive/string value is not, so
// it is uniformly heap-boxed into a value cell and dispatched through an
// unboxing wrapper (docs/DYN-TRAITS.md §4.2.3). The check is purely "not
// a known struct and not a known enum" — every other concrete an impl
// can name (i32/i64/u32/u64/f32/f64/boolean/string) is primitive.
func isPrimitiveConcrete(info *checker.Info, concrete string) bool {
	if info == nil {
		return false
	}
	if _, isStruct := info.Structs[concrete]; isStruct {
		return false
	}
	if _, isEnum := info.Enums[concrete]; isEnum {
		return false
	}
	return true
}

// astTypeForConcreteName maps a primitive/`string` concrete type-name
// string (as recorded in DynCoercion.Concrete / VtableDecl.Concrete) to
// the ast.Type the IR layout helpers (payloadSlotSize / payloadStoreOpFor
// / payloadLoadOpFor) expect. Mirrors checker.methodTypeName's spellings.
// Returns nil for an unrecognised name (a struct/enum concrete — callers
// gate on isPrimitiveConcrete before calling).
func astTypeForConcreteName(name string) ast.Type {
	switch name {
	case "i32":
		return ast.NumberType{Width: 32, Signed: true}
	case "i64":
		return ast.NumberType{Width: 64, Signed: true}
	case "u32":
		return ast.NumberType{Width: 32, Signed: false}
	case "u64":
		return ast.NumberType{Width: 64, Signed: false}
	case "usize":
		return ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}
	case "f32":
		return ast.FloatType{Width: 32}
	case "f64":
		return ast.FloatType{Width: 64}
	case "boolean":
		return ast.BoolType{}
	case "string":
		return ast.StringType{}
	}
	return nil
}

// dynboxWrapperName is the synthesized unboxing-wrapper function name for
// a (primitive concrete, trait method) pair. The wrapper's dispatch-facing
// signature is `(boxptr, args...) -> result`: it loads the concrete value
// from the value-box and calls the real `__method_<C>_<m>` with the
// unboxed receiver (docs/DYN-TRAITS.md §4.2.3).
func dynboxWrapperName(concrete, method string) string {
	return "__dynbox_" + concrete + "_" + method
}

// traitVtableSlots returns the vtable slots for ONE trait's
// non-associated methods (declaration order) over `concrete`. A
// struct/enum slot points at the real receiver method; a
// primitive/string slot points at the unboxing wrapper
// (docs/DYN-TRAITS.md §4.2.3). This is the per-trait building block both
// single-trait and merged (`dyn A + B`) vtables concatenate.
func traitVtableSlots(info *checker.Info, td *ast.TraitDecl, concrete string, prim bool) []VtableMethod {
	var methods []VtableMethod
	for _, m := range td.Methods {
		if m.Assoc {
			continue
		}
		if prim {
			methods = append(methods, VtableMethod{Method: m.Name, Func: dynboxWrapperName(concrete, m.Name)})
			continue
		}
		fn := info.Methods[concrete+"."+m.Name]
		if fn == "" {
			// Fall back to the conventional mangled name the
			// receiver-hoist produces; an empty entry would make
			// the slot un-dispatchable.
			fn = "__method_" + concrete + "_" + m.Name
		}
		methods = append(methods, VtableMethod{Method: m.Name, Func: fn})
	}
	return methods
}

// collectVtables builds one VtableDecl per (dyn trait SET, concrete-type)
// pair where the set is used in a `dyn Trait` / `dyn A + B` type
// somewhere in the program and the concrete type implements EVERY trait
// in the set. For a single-trait set the table is the trait's methods in
// declaration order (slot k names the same method for every implementing
// type — byte-identical to the pre-merged behaviour, since the key is the
// bare trait name). For a multi-trait set `dyn A + B` the table is the
// CONCATENATION of the per-trait tables in the set's sorted order
// (`[ A-methods…, B-methods… ]`), so a method owned by B sits at global
// slot len(A.methods)+idx — the merged-vtable design (docs/DYN-TRAITS.md
// §10). The result is deterministically ordered (set key, then concrete,
// both by name) so codegen emits identical static data run to run.
//
// This intentionally over-approximates: it emits a vtable for every
// implementor (of all traits in the set), not only the concrete types
// that actually flow into a `dyn` value. Unused vtables are dead static
// data the linker/backend can drop. See docs/DYN-TRAITS.md §4.2.
func collectVtables(prog *ast.Program, info *checker.Info) []VtableDecl {
	sets := dynTraitSetsUsed(prog)
	if len(sets) == 0 {
		return nil
	}
	// Deterministic order over set keys.
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []VtableDecl
	for _, key := range keys {
		traits := sets[key]
		// Every trait in the set must be known; skip the whole set if any
		// is missing (a malformed program the checker already errored on).
		ok := true
		for _, tr := range traits {
			if _, found := info.Traits[tr]; !found {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Concrete types: the intersection of every trait's implementors
		// (a concrete coerces to `dyn A + B` only if it impls A AND B).
		// For a single-trait set this is just that trait's implementors.
		concreteSet := map[string]bool{}
		for c, impld := range info.Impls[traits[0]] {
			if impld {
				concreteSet[c] = true
			}
		}
		for _, tr := range traits[1:] {
			impls := info.Impls[tr]
			for c := range concreteSet {
				if !impls[c] {
					delete(concreteSet, c)
				}
			}
		}
		concretes := make([]string, 0, len(concreteSet))
		for c := range concreteSet {
			concretes = append(concretes, c)
		}
		sort.Strings(concretes)
		for _, concrete := range concretes {
			prim := isPrimitiveConcrete(info, concrete)
			var methods []VtableMethod
			for _, tr := range traits {
				methods = append(methods, traitVtableSlots(info, info.Traits[tr], concrete, prim)...)
			}
			// Record the concrete type's drop fn for the trailing drop slot
			// (docs/DYN-TRAITS.md §4.4). A primitive concrete's `data` is a
			// heap-boxed VALUE CELL (boxPrimitiveDynValue), so its dtor is the
			// generated __drop_dynprim_<prim>, which frees that cell (#4351 —
			// before this the slot was the null sentinel and every prim
			// coercion leaked its cell). For a
			// struct/enum concrete, dropFnNameFor names its recursive drop
			// (or returns "" for a flat scalar struct that needs none); nil
			// registries are fine here — a `dyn` concrete is always a
			// non-generic struct/enum, neither of which needs the
			// generic-enum / tuple registry, and the worklist regenerates
			// the body from info.Structs / info.Enums by name.
			drop := ""
			if prim {
				drop = "__drop_dynprim_" + concrete
			}
			if !prim {
				var ct ast.Type
				if _, ok := info.Structs[concrete]; ok {
					ct = ast.StructType{Name: concrete}
				} else if _, ok := info.Enums[concrete]; ok {
					ct = ast.EnumType{Name: concrete}
				}
				if ct != nil {
					// ct is always a concrete struct/enum here, so the
					// DynTraitType arm is never reached — ptrW==0 / dynSupported
					// false are both moot (the struct/enum drop names don't gate
					// on ptrW).
					if name, ok := dropFnNameFor(ct, info, nil, nil, 0, false); ok {
						drop = name
					}
				}
			}
			out = append(out, VtableDecl{Trait: key, Concrete: concrete, Methods: methods, Drop: drop})
		}
	}
	return out
}

// realImplMethodName resolves the mangled receiver-method a (concrete,
// method) slot dispatches to — `info.Methods[C.m]`, falling back to the
// conventional `__method_<C>_<m>` (mirrors collectVtables' resolution).
func realImplMethodName(info *checker.Info, concrete, method string) string {
	if fn := info.Methods[concrete+"."+method]; fn != "" {
		return fn
	}
	return "__method_" + concrete + "_" + method
}

// buildDynboxWrappers synthesizes the unboxing-wrapper `Func`s that a
// primitive/string concrete's vtable slots point at (docs/DYN-TRAITS.md
// §4.2.3). For every (primitive concrete, trait method) pair that
// collectVtables routed through `dynboxWrapperName`, it emits a function
//
//	__dynbox_<C>_<m>(boxptr: i32, args...) -> result {
//	    return __method_<C>_<m>(load_concrete(boxptr), args...);
//	}
//
// The wrapper loads the concrete value from the heap value-box (using the
// concrete type's own load width — two words for a wasm string, 8 bytes
// for i64/f64) and calls the real method with the unboxed value as the
// receiver. The dispatch-facing signature is (boxptr, args...) -> result,
// so OpCallDyn's receiver-first contract lines up: it passes the box
// pointer (the `dyn` value's `data` word) as arg 0, and the wrapper does
// the unbox. Deterministically ordered for stable codegen.
func buildDynboxWrappers(info *checker.Info, ptrW int, vtables []VtableDecl) ([]*Func, error) {
	if info == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []*Func
	for _, vt := range vtables {
		if !isPrimitiveConcrete(info, vt.Concrete) {
			continue
		}
		concreteType := astTypeForConcreteName(vt.Concrete)
		if concreteType == nil {
			return nil, fmt.Errorf("ir: dyn over %q: no value-box layout for concrete type %q", vt.Trait, vt.Concrete)
		}
		td, ok := info.Traits[vt.Trait]
		if !ok {
			continue
		}
		// Trait method declarations carry the receiver-first param list +
		// result, the source of truth for the wrapper's signature and the
		// real method's arg types.
		methByName := map[string]*ast.TraitMethod{}
		for i := range td.Methods {
			methByName[td.Methods[i].Name] = &td.Methods[i]
		}
		for _, vm := range vt.Methods {
			wname := dynboxWrapperName(vt.Concrete, vm.Method)
			if seen[wname] {
				continue
			}
			seen[wname] = true
			tm, ok := methByName[vm.Method]
			if !ok || len(tm.Params) == 0 {
				return nil, fmt.Errorf("ir: dyn wrapper for %s.%s: trait method missing or has no receiver", vt.Concrete, vm.Method)
			}
			// Method args after the receiver (tm.Params[0] is `self`).
			argTypes := make([]ast.Type, 0, len(tm.Params)-1)
			for _, p := range tm.Params[1:] {
				argTypes = append(argTypes, p.Type)
			}
			// Wrapper params: boxptr (i32) + the method args, in order.
			params := make([]ast.Param, 0, 1+len(argTypes))
			params = append(params, ast.Param{Name: "__boxptr", Type: ast.NumberType{Width: 32, Signed: true}})
			for i, at := range argTypes {
				params = append(params, ast.Param{Name: fmt.Sprintf("__a%d", i), Type: at})
			}
			fn := &Func{Name: wname, Params: params, ReturnType: tm.Result}
			emit := func(op Op) { fn.Ops = append(fn.Ops, op) }
			// value = load(boxptr) with the concrete type's load width
			// (two-word for wasm string; 8 bytes for i64/f64).
			emit(Op{Kind: OpLoadLocal, I32: 0})
			emit(payloadLoadOpFor(concreteType, ptrW))
			// Push the method args (param slots 1..n).
			for i := range argTypes {
				emit(Op{Kind: OpLoadLocal, I32: int32(i + 1)})
			}
			// call __method_<C>_<m>(value, args...). The real method's
			// param types are (concreteReceiver, args...): ArgTypes drives
			// the backend's operand-stack slot accounting (a string
			// receiver / arg occupies two slots).
			callArgTypes := make([]ast.Type, 0, 1+len(argTypes))
			callArgTypes = append(callArgTypes, concreteType)
			callArgTypes = append(callArgTypes, argTypes...)
			emit(Op{
				Kind: OpCallDirect,
				Str:  realImplMethodName(info, vt.Concrete, vm.Method),
				I32:  int32(1 + len(argTypes)),
				Ext:  &OpExt{ArgTypes: callArgTypes},
			})
			if tm.Result == nil {
				emit(Op{Kind: OpReturnVoid})
			} else {
				if _, isVoid := tm.Result.(ast.VoidType); isVoid {
					emit(Op{Kind: OpReturnVoid})
				} else {
					emit(Op{Kind: OpReturn})
				}
			}
			out = append(out, fn)
		}
	}
	return out, nil
}

// buildDynDropHelpers synthesizes the per-trait-set `__drop_dyn_<set>`
// destructor helpers that reclaim a `dyn Trait` value's erased concrete
// `data` object (docs/DYN-TRAITS.md §4.4). Two representations, selected
// by ptrW; the IR ops are shared, only the operand-stack shape and the
// cell-free differ:
//
//	wasm (ptrW==4, slice 4a — INLINE two-word). A `dyn` value is the
//	inline `[data, vtable]` fat pointer, so the helper takes both words:
//
//	    __drop_dyn_<set>(data: i32, vtable: i32) {
//	        let d = vtable[methodCount*4];            // function-table index
//	        if (d != 0) { call_indirect[d](data); }   // run concrete dtor
//	    }
//
//	There is no separate cell, so the helper stops after the concrete drop.
//
//	natives (ptrW==8, DynSupported — x86-64 slice 4b — BOXED one-word).
//	A `dyn` value is a single pointer to a `{data@0, vtable@ptrW}` cell, so
//	the helper takes the cell ptr, RELOADS data+vtable from it (before any
//	free!), dispatches the dtor, then frees the 16-byte cell:
//
//	    __drop_dyn_<set>(cell: i64) {
//	        let data   = cell[0];
//	        let vtable = cell[ptrW];
//	        let d = vtable[methodCount*ptrW];         // absolute fn pointer
//	        if (d != 0) { d(data); }                  // run concrete dtor
//	        __free(cell, 2*ptrW);                     // reclaim the box cell
//	    }
//
// The drop slot is TRAILING at index `methodCount` (= the merged method
// count for the set), so OpCallDyn's method slot math is untouched. A null
// sentinel (0) means the concrete needs no drop (a flat scalar struct, or
// a primitive concrete whose value lives inline behind `data`). The
// concrete drop (`__drop_struct_<C>` / `__drop_enum_<C>`) frees `data` and
// everything it transitively owns (e.g. a String field) and self-guards on
// rc==1; the vtable word is static data — never inc/dec'd. Reload-before-
// free is load-bearing on the natives: freeing the cell first would read a
// reclaimed (possibly reused) cell for data/vtable — a use-after-free.
//
// Dispatch reuses OpCallDyn (slot = methodCount, sig (ptr)->ptr — every
// generated drop fn returns the box pointer): it rebuilds the same
// `[data, vtable]` stack OpCallDyn expects, pops the vtable, loads the slot
// (wasm: table index + call_indirect; natives: absolute ptr + direct call).
// The returned box pointer is dropped.
//
// One helper per trait SET actually used (dynTraitSetsUsed); a set with no
// implementors still emits a helper (its method count is well-defined and
// a no-implementor set is unreachable at runtime, but the symbol must
// resolve where a `dyn` of that set is dec'd). Declines on a native
// backend that hasn't opted in (arm64, slice 4c): no helper, no drop slot
// read, `dyn` keeps leaking — no dangling call.
func buildDynDropHelpers(prog *ast.Program, info *checker.Info, ptrW int, dynRcSupported bool) []*Func {
	if (ptrW != 4 && !dynRcSupported) || info == nil {
		return nil
	}
	boxed := ptrW != 4 // natives: a `dyn` value is the boxed cell ptr.
	sets := dynTraitSetsUsed(prog)
	if len(sets) == 0 {
		return nil
	}
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// OpCallDyn dispatches the concrete dtor; the dtor sig is (ptr)->ptr
	// (every generated drop fn takes the heap ptr and returns it).
	// NumberType is the right one-word shape for the receiver/result on
	// both targets.
	dropSig := &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	var out []*Func
	for _, key := range keys {
		traits := sets[key]
		// Merged method count = total non-associated methods across the
		// set, in sorted order — the same count collectVtables uses for
		// len(Methods), so the drop slot lands at exactly that index.
		methodCount := 0
		known := true
		for _, tr := range traits {
			td, ok := info.Traits[tr]
			if !ok {
				known = false
				break
			}
			for i := range td.Methods {
				if !td.Methods[i].Assoc {
					methodCount++
				}
			}
		}
		if !known {
			continue // malformed set the checker already errored on
		}
		fn := &Func{Name: dynDropFnName(traits), ReturnType: ast.VoidType{}}
		emit := func(op Op) { fn.Ops = append(fn.Ops, op) }
		if boxed {
			// natives (boxed): one param = the cell ptr. Reload data (cell+0)
			// and vtable (cell+ptrW) into fresh locals BEFORE any free, then
			// dispatch + free the cell. Slots: 0 = cell (param), 1 = data,
			// 2 = vtable.
			fn.Params = []ast.Param{{Name: "__dcell", Type: ast.NumberType{}}}
			// NULL-guard the cell pointer. The drop sites zero-init a `dyn`
			// slot (Phase 1d-v), so the first loop-iteration reinit drop and a
			// never-assigned slot at exit pass cell==0; on the natives that
			// would segfault on the cell[0] deref below (wasm address 0 reads
			// 0 harmlessly, so the inline helper needs no guard). `if (cell !=
			// 0) { ... }` makes a null drop a no-op — the same NULL-guard the
			// other native rc drops rely on (__fern_rc_dec / __fern_arr_dec).
			emit(Op{Kind: OpLoadLocal, I32: 0})
			emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			// data = cell[0]
			emit(Op{Kind: OpLoadLocal, I32: 0})
			emit(Op{Kind: OpLoad, Width: WidthPtr})
			emit(Op{Kind: OpStoreLocal, I32: 1})
			// vtable = cell[ptrW]
			emit(Op{Kind: OpLoadLocal, I32: 0})
			emit(Op{Kind: OpConstI32, I32: int32(ptrW)})
			emit(Op{Kind: OpAdd})
			emit(Op{Kind: OpLoad, Width: WidthPtr})
			emit(Op{Kind: OpStoreLocal, I32: 2})
			// d = vtable[methodCount*ptrW] (absolute fn pointer of the dtor).
			emit(Op{Kind: OpLoadLocal, I32: 2})
			emit(Op{Kind: OpConstI32, I32: int32(methodCount * ptrW)})
			emit(Op{Kind: OpAdd})
			emit(Op{Kind: OpLoad, Width: WidthPtr})
			// if (d != 0) run the concrete destructor on `data`.
			emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			emit(Op{Kind: OpLoadLocal, I32: 1}) // data
			emit(Op{Kind: OpLoadLocal, I32: 2}) // vtable
			emit(Op{Kind: OpCallDyn, I32: int32(methodCount), Ext: &OpExt{Sig: dropSig}})
			emit(Op{Kind: OpDrop}) // discard the ptr the dtor returns
			emit(Op{Kind: OpEnd})
			// __free(cell, 2*ptrW) — reclaim the box cell itself. __free is
			// (base, size); the pushed result is meaningless, so drop it to
			// keep the operand stack balanced.
			emit(Op{Kind: OpLoadLocal, I32: 0})
			emit(Op{Kind: OpConstI32, I32: int32(2 * ptrW)})
			emit(Op{Kind: OpCallDirect, Str: "__free", I32: 2})
			emit(Op{Kind: OpDrop})
			emit(Op{Kind: OpEnd}) // close the cell != 0 guard
			emit(Op{Kind: OpReturnVoid})
			out = append(out, fn)
			continue
		}
		// wasm (inline two-word): two params = data, vtable.
		fn.Params = []ast.Param{
			{Name: "__ddata", Type: ast.NumberType{}},
			{Name: "__dvtbl", Type: ast.NumberType{}},
		}
		// d = vtable[methodCount*4] (function-table index of the dtor).
		emit(Op{Kind: OpLoadLocal, I32: 1})
		emit(Op{Kind: OpConstI32, I32: int32(methodCount * 4)})
		emit(Op{Kind: OpAdd})
		emit(Op{Kind: OpLoad}) // 4-byte word
		// if (d != 0) run the concrete destructor on `data`.
		emit(Op{Kind: OpIf, I32: BlockTypeVoid})
		emit(Op{Kind: OpLoadLocal, I32: 0}) // data
		emit(Op{Kind: OpLoadLocal, I32: 1}) // vtable
		emit(Op{Kind: OpCallDyn, I32: int32(methodCount), Ext: &OpExt{Sig: dropSig}})
		emit(Op{Kind: OpDrop}) // discard the box ptr the dtor returns
		emit(Op{Kind: OpEnd})
		emit(Op{Kind: OpReturnVoid})
		out = append(out, fn)
	}
	return out
}

// LowerOption tweaks LowerWith's behaviour for a particular backend.
// The zero set of options is the historical default (used by every
// existing caller): `dyn Trait` is supported only on wasm (ptrW==4).
type LowerOption func(*lowerOpts)

type lowerOpts struct {
	// dynSupported lets a ptrW==8 native backend opt into boxed
	// `dyn Trait` DISPATCH lowering. ptrW alone cannot discriminate
	// x86-64 from arm64 (both are 8), so the backend threads its
	// capability in explicitly. BOTH natives pass it (slices 2c + 2d);
	// see docs/DYN-TRAITS.md §4.2.2.
	dynSupported bool
	// dynRcSupported lets a ptrW==8 native backend opt into Perceus RC
	// of boxed `dyn Trait` values (the per-set __drop_dyn_<set> helper,
	// the trailing vtable drop slot, and the dec/drop sweep arms —
	// docs/DYN-TRAITS.md §4.4). A STRICT subset of dynSupported: dispatch
	// shipped on both natives (2c/2d) but RC lands one backend at a time
	// (x86-64 = slice 4b passes it; arm64 = slice 4c does not yet, so
	// arm64 keeps leaking `dyn` — harmless). wasm RC (slice 4a) keys on
	// ptrW==4 and never needs this.
	dynRcSupported bool
	// emitLineMarkers makes the builder emit a zero-effect OpLine at each
	// statement boundary, carrying its source Pos, so a native backend can
	// build a DWARF .debug_line table under -g (#5537 slice 2). Off by
	// default: ordinary builds — and the self-host byte-identical fixpoint —
	// never see OpLine.
	emitLineMarkers bool
}

// DynSupported marks the calling backend as able to lower `dyn Trait`
// DISPATCH (boxed one-word on natives, §4.2.2). Both x86-64 and arm64
// pass it (slices 2c + 2d). wasm never needs it — its ptrW==4 gate
// already lifts on its own.
func DynSupported() LowerOption { return func(o *lowerOpts) { o.dynSupported = true } }

// DynRcSupported marks the calling backend as able to RECLAIM boxed
// `dyn Trait` values via Perceus RC (the __drop_dyn_<set> helper + the
// trailing vtable drop slot, §4.4). A strict subset of DynSupported:
// x86-64 passes it (slice 4b); arm64 does not yet (slice 4c — it keeps
// leaking `dyn`, which is harmless). wasm RC keys on ptrW==4 directly.
func DynRcSupported() LowerOption { return func(o *lowerOpts) { o.dynRcSupported = true } }

// EmitLineMarkers makes the lowering emit OpLine source-position markers at
// statement boundaries so a native backend can build a DWARF .debug_line
// table (#5537 slice 2). Native backends pass it under -g; ordinary builds
// leave it off and never see OpLine.
func EmitLineMarkers() LowerOption { return func(o *lowerOpts) { o.emitLineMarkers = true } }

// LowerWith is the pointer-width-aware variant. `ptrW` is 4 on
// wasm32 and 8 on arm64; it sizes pointer-typed enum payloads,
// struct fields, array elements, and closure captures so heap
// addresses survive arm64-darwin's >= 4 GiB heap.
func LowerWith(prog *ast.Program, info *checker.Info, ptrW int, opts ...LowerOption) (*Program, error) {
	var lo lowerOpts
	for _, opt := range opts {
		opt(&lo)
	}
	// `dyn Trait` (runtime trait objects) representation by target
	// (docs/DYN-TRAITS.md §4.2):
	//   - wasm (ptrW==4): inline two-word `[data, vtable]` fat pointer
	//     + per-(trait,concrete) vtable data segments (§4.2.1).
	//   - x86-64 (ptrW==8, DynSupported): BOXED one-word — a single
	//     heap pointer to a `{data, vtable}` cell, dispatched through a
	//     vtable of absolute function pointers (§4.2.2).
	//   - arm64 / any other ptrW==8 backend without DynSupported: no
	//     lowering yet, so reject `dyn` here with a clear message
	//     rather than miscompiling it.
	// The IR layer is the single choke point for every compiled
	// backend. collectVtables (below) populates prog.Vtables for the
	// backends that lifted their gate.
	dynSupported := ptrW == 4 || lo.dynSupported
	// RC of `dyn` is a strict subset of dispatch: wasm (slice 4a) + a
	// native that opted in via DynRcSupported (x86-64 slice 4b). arm64
	// (slice 4c) lifts dispatch but not RC, so it still leaks `dyn`.
	dynRcSupported := ptrW == 4 || lo.dynRcSupported
	if !dynSupported {
		if err := rejectDynTrait(prog); err != nil {
			return nil, err
		}
	}
	// Multi-trait trait objects (`dyn A + B`) lower through the MERGED
	// vtable on every backend that supports `dyn`: collectVtables emits a
	// concatenated (trait-set, concrete) table and OpCallDyn indexes the
	// global slot (per-trait prefix + index-in-trait) — docs/DYN-TRAITS.md
	// §10. A backend that has NOT lifted its single-trait `dyn` gate
	// (a future ptrW==8 backend, or a test harness omitting DynSupported())
	// still rejects ALL `dyn` (including multi-trait) via rejectDynTrait
	// above. The one multi-trait sub-case still rejected on supporting
	// backends is the `as? T` downcast (rejectDowncast, below) — its
	// per-trait vtable-pointer compare doesn't yet understand the merged
	// table.
	// The `e as? T` fallible downcast (DowncastExpr) now lowers to a
	// runtime vtable-pointer compare on every backend that supports `dyn`
	// (the (trait,concrete) vtable that uniquely tags the concrete type is
	// the slice-2 infrastructure — docs/DYN-TRAITS.md §9). A backend that
	// has NOT lifted its `dyn` gate (a hypothetical future ptrW==8 backend,
	// or a test harness omitting DynSupported()) still rejects it cleanly,
	// and — defensively — a non-struct/enum target (the checker already
	// blocks primitives with E060) is rejected too.
	if err := rejectDowncast(prog, info, dynSupported); err != nil {
		return nil, err
	}
	// Block-expressions (`if`/`match` value branches `{ stmts; tail }`)
	// now lower on every compiled backend (slice 2): the builder lowers
	// the leading statements through the normal statement-lowering path
	// (in a fresh shadowrename frame so block-local `var`s get their own
	// slots) and then lowers the trailing expression as the block's
	// value. Block-local RC is handled by the existing function-exit dec
	// sweep — every block-local `var` is registered in `info.Locals[fn]`
	// (checkBlockExpr → checkStmt), gets a zero-init'd slot, and is
	// dec'd at scope exit exactly like a top-level local; the Tail value
	// is produced after the leading stmts and (when it references a
	// block-local) is alias-inc'd / moved by the normal Var/Ident rules,
	// so it survives the exit sweep without double-free. No reject gate
	// here anymore — see the *ast.BlockExpr arm in (*builder).expr.
	// Automatic drop for owned WIT resource handles (P5 slice 3): insert
	// `defer <drop>(h);` for each kept `own R` local and synthesize the
	// `[resource-drop]` import functions. Runs before eraseHandleTypes because
	// it analyses the handle types (docs/WIT-BRING-YOUR-OWN.md).
	insertResourceDrops(prog, info)
	// Erase WIT resource handles (`own R` / `borrow R`) to plain i32 before
	// any lowering reads a type: a handle is an opaque scalar at the canonical
	// ABI and the checker has already enforced its type-safety (P5 —
	// docs/WIT-BRING-YOUR-OWN.md). This is the single choke point, so no
	// backend's width/op classification ever sees a HandleType.
	eraseHandleTypes(prog, info)
	// Erase the borrowed-string view type `str` to plain StringType (#4813):
	// a view lowers to exactly the string box shape, and the checker has
	// already enforced the borrow discipline, so no backend sees StrType.
	eraseSurfaceTypes(prog, info)
	// Rename shadowed local variables so each Var declaration
	// in a function carries a name that's globally unique
	// within the function. The IR's per-name `b.locals` slot
	// lookup is otherwise blind to scoping — two nested
	// `var x: i64` declarations would collapse onto a single
	// slot and the outer reads would silently see the inner
	// store's value. Runs before closureconv so the closure
	// pass sees post-rename names everywhere.
	shadowrename.Rename(prog, info)
	// Box captured-and-mutated scalar locals into 1-element array cells so a
	// closure's write to a captured outer i32/bool/f64 is shared by reference
	// (the interpreter's closures-as-counters semantics; #2896). Runs after
	// shadowrename (names are unique, so a closure's reference to a boxed name
	// is unambiguous) and before closureconv (which then captures the cell
	// pointer by reference). No-op for functions without such a capture.
	closureconv.BoxMutatedCaptures(prog, info)
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
	// Per-(trait,type) dispatch tables for `dyn Trait` values. Empty for
	// every program today: the rejectDynTrait gate above returns before
	// this point whenever `dyn` is used, so only non-dyn programs reach
	// here (collectVtables returns nil for them). The wiring is in place
	// so lifting the gate per backend (docs/DYN-TRAITS.md §4.2) populates
	// the field with no further plumbing.
	out := &Program{PairForm: pairForm, PtrW: ptrW, Vtables: collectVtables(prog, info)}
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
	trmcFuncs, trmcConsumeSafe := findTrmcFuncs(prog, info, ptrW, pairForm)
	// Borrow inference (BorrowInferEnabled): per-function per-param escape facts.
	// Both the definition side (paramOwnedByDefault) and the call site
	// (calleeParamOwnedByDefault) consult this so they agree on which
	// owned-by-default params are kept borrowed.
	paramEscapes := inferParamEscapes(prog, info)
	// Per-callee: string params retained only through counted constructions, so
	// a caller may release its own reference (see inferParamCountedRetain).
	paramCountedRetain := inferParamCountedRetain(prog, info)
	readOnlyComparators := computeReadOnlyComparators(info)
	// #4873: per-function param positions whose buffers the callee may grow
	// in place — drives the caller-side containment bracket in callBody.
	growParams := computeGrowParams(prog, info)
	for _, fn := range prog.Funcs {
		// Body-less `@import` functions are extern WASM-component imports, not
		// defined functions: record their signature in out.Externs and skip
		// lowering (there is no body). The wasm backend turns each into a core
		// import; a call to fn.Name resolves to that import's funcidx.
		if fn.ImportIface != "" {
			ef := &ExternFunc{
				Name:             fn.Name,
				Iface:            fn.ImportIface,
				WITName:          fn.ImportWITName,
				Params:           fn.Params,
				ReturnType:       fn.ReturnType,
				Async:            fn.Async,
				StreamResultElem: fn.StreamResultElem,
				StreamParamElems: fn.StreamParamElems,
			}
			// Precompute the flattened layout of any record (struct) parameter
			// while info.Structs is in scope; the wasm backend (which has no
			// info) reads it to lower the canonical record param (P4c).
			for i, p := range fn.Params {
				if layout, ok := externRecordLayout(p.Type, info); ok {
					if ef.ParamRecords == nil {
						ef.ParamRecords = map[int][]ExternRecordField{}
					}
					ef.ParamRecords[i] = layout
				} else if el, ok := externEnumParamLayout(p.Type, info, ptrW); ok {
					if ef.ParamEnums == nil {
						ef.ParamEnums = map[int]*ExternEnumParam{}
					}
					ef.ParamEnums[i] = el
				} else if externPlainEnumParam(p.Type, info) {
					if ef.ParamPlainEnums == nil {
						ef.ParamPlainEnums = map[int]bool{}
					}
					ef.ParamPlainEnums[i] = true
				} else if el, ok := externVariantParamLayout(p.Type, info, ptrW); ok {
					if ef.ParamEnums == nil {
						ef.ParamEnums = map[int]*ExternEnumParam{}
					}
					ef.ParamEnums[i] = el
				}
			}
			if rr, ok := externRecordResultLayout(fn.ReturnType, info); ok {
				ef.ResultRecord = rr
			} else if re, ok := externEnumParamLayout(fn.ReturnType, info, ptrW); ok {
				ef.ResultEnum = re
			} else if externPlainEnumParam(fn.ReturnType, info) {
				ef.ResultPlainEnumN = len(info.Enums[fn.ReturnType.(ast.EnumType).Name].Variants)
			} else if re, ok := externVariantResultLayout(fn.ReturnType, info, ptrW); ok {
				ef.ResultEnum = re
			}
			out.Externs = append(out.Externs, ef)
			continue
		}
		f, err := lowerFunc(fn, info, ptrW, lo.dynRcSupported, lo.emitLineMarkers, pairForm, closureCaps, genEnumDrops, genTupleDrops, returnsNoParamEscape, trmcFuncs, trmcConsumeSafe, paramEscapes, paramCountedRetain, readOnlyComparators, growParams)
		if err != nil {
			return nil, err
		}
		out.Funcs = append(out.Funcs, f)
		if fn.ExportIface != "" {
			// `@export` function: lowered normally (it has a body), with the
			// world-export binding recorded for the wasm backend / composer (P6).
			// An Option/Result result needs a wrapper (Fern enum box → canonical
			// (disc, payload) return area), resolved here where checker.Info is in
			// scope. Only the single-payload form (no general variant) is surfaced.
			exp := ExternExport{Name: fn.Name, Iface: fn.ExportIface, WITName: fn.ExportWITName}
			if re, ok := externEnumParamLayout(fn.ReturnType, info, ptrW); ok && re.Variants == nil && re.SlotCount == 0 {
				exp.ResultEnum = re
			} else if _, isTuple := fn.ReturnType.(ast.TupleType); isTuple {
				// A tuple result returns indirectly; a record (named WIT type) needs
				// the exported-instance type-export machinery, so only tuples here.
				// Scoped to flat (no nested-tuple element) tuples, matching the
				// composer's all-primitive element requirement.
				if rr, ok := externRecordResultLayout(fn.ReturnType, info); ok && !rr.Direct && externFieldsAllFlat(rr.Fields) {
					exp.ResultTuple = rr
				}
			}
			out.Exports = append(out.Exports, exp)
		}
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
		// Sorted: these thunks are appended to out.Funcs, so ranging the
		// map directly put them in Go's map iteration order and the emitted
		// function order changed run to run (#6077). The SET is unaffected —
		// only the order in which equally-eligible thunks are appended.
		closureDropNames := make([]string, 0, len(closureCaps))
		for name := range closureCaps {
			closureDropNames = append(closureDropNames, name)
		}
		sort.Strings(closureDropNames)
		for _, name := range closureDropNames {
			caps := closureCaps[name]
			if !hasRcCapture(caps, ptrW, lo.dynRcSupported) && !makeClosureTargets[name] {
				continue
			}
			thunk := genClosureDropThunk(name, caps, ptrW, info, genEnumDrops, genTupleDrops, lo.dynRcSupported)
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
					strings.HasPrefix(op.Str, "__drop_arr_dyn_") ||
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
		// Seed the worklist with the concrete-destructor drop fns named in
		// the `dyn` vtable drop slots (docs/DYN-TRAITS.md §4.4). The
		// __drop_dyn_<set> helper reaches these by an indirect call through
		// the vtable, not a named OpCallDirect, so enqueueCalls above never
		// sees them — without this seeding a struct/enum behind `dyn` whose
		// __drop_struct_/__drop_enum_ body is reached ONLY via the vtable
		// would be referenced (by name in the vtable cell) but never
		// generated. wasm (slice 4a) + x86-64 (slice 4b, DynRcSupported)
		// both append the drop slot and need this seeding; arm64 lifts
		// dispatch but not RC yet (slice 4c), so it records no Drop and
		// needs no seeding.
		if dynRcSupported {
			for _, vt := range out.Vtables {
				if vt.Drop == "" || queued[vt.Drop] {
					continue
				}
				queued[vt.Drop] = true
				work = append(work, vt.Drop)
			}
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
			} else if prim := strings.TrimPrefix(name, "__drop_dynprim_"); prim != name {
				// Primitive/string-concrete `dyn` value-cell drop (#4351):
				// frees the boxPrimitiveDynValue cell the fat pointer's `data`
				// word points at. Only ever referenced from a vtable drop slot
				// (seeded above when dynRcSupported).
				fn = genDynPrimDropFn(prim, ptrW)
				if fn == nil {
					continue
				}
			} else if dynDrop := strings.TrimPrefix(name, "__drop_arr_dyn_"); dynDrop != name {
				// `dyn Trait[]` outer drop (docs/DYN-TRAITS.md §7.8): per
				// element run the per-set `__drop_dyn_<set>` destructor
				// (embedded in the name; always emitted by
				// buildDynDropHelpers), then free the outer buffer. The
				// helper is representation-aware (native one-word cell ptr
				// vs wasm two-word inline) and the per-element drop is void.
				fn = genArrDynDropFn(dynDrop, ptrW)
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
				fn = genEnumDropFn(en, ed, info, ptrW, genEnumDrops, genTupleDrops, lo.dynRcSupported)
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
				fn = genTupleDropFn(mangled, tt, info, ptrW, genEnumDrops, genTupleDrops, lo.dynRcSupported)
				if fn == nil {
					continue
				}
			} else {
				sn := strings.TrimPrefix(name, "__drop_struct_")
				sd, ok := info.Structs[sn]
				if !ok {
					continue // routing only names structs it verified exist
				}
				fn = genStructDropFn(sn, sd, info, ptrW, genEnumDrops, genTupleDrops, lo.dynRcSupported)
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
	// `dyn` over a primitive/string: synthesize the unboxing wrappers the
	// vtable slots point at (docs/DYN-TRAITS.md §4.2.3). Each loads the
	// boxed concrete value and calls the real `__method_<C>_<m>`. Appended
	// in lockstep — an AST stub into prog.Funcs (codegen pairs prog.Funcs[i]
	// with out.Funcs[i] by index) and the IR Func into out.Funcs.
	dynWrappers, wrErr := buildDynboxWrappers(info, ptrW, out.Vtables)
	if wrErr != nil {
		return nil, wrErr
	}
	for _, fn := range dynWrappers {
		prog.Funcs = append(prog.Funcs, &ast.FuncDecl{
			Name:       fn.Name,
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       &ast.Block{},
		})
		out.Funcs = append(out.Funcs, fn)
	}
	// `dyn Trait` RC: synthesize the per-trait-set `__drop_dyn_<set>`
	// destructor helpers the Perceus dec/drop sweep calls at a `dyn`
	// value's last use / scope exit (docs/DYN-TRAITS.md §4.4). wasm
	// (ptrW==4, slice 4a — inline) + x86-64 (DynRcSupported, slice 4b —
	// boxed) + arm64 (DynRcSupported, slice 4c — boxed, the structural
	// mirror of x86-64). buildDynDropHelpers no-ops only when a ptrW==8
	// backend omits DynRcSupported (none today). Appended in lockstep
	// like the other synthesized helpers.
	for _, fn := range buildDynDropHelpers(prog, info, ptrW, lo.dynRcSupported) {
		prog.Funcs = append(prog.Funcs, &ast.FuncDecl{
			Name:       fn.Name,
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       &ast.Block{},
		})
		out.Funcs = append(out.Funcs, fn)
	}
	// Dead map-reclamation cull. `__map_drop_values` (the core/map value-drop
	// helper) is loaded only when a real Map value is created — `map_new` / a map
	// literal pull core/map in. A program that uses a Map-typed struct field or
	// enum payload only as a TYPE (e.g. a `JsonValue[]` holding only scalar
	// `JString` variants — `JObject(Map[…])` is never constructed) never loads it,
	// yet the generated `__drop_enum_`/`__drop_struct_` body still emits the
	// map-reclamation call for that payload (genEnumDropFn / appendMapDrop). Since
	// no Map value can exist, that call site is DEAD, but the static reference
	// would fail as `unknown callee "__map_drop_values"` at wasm build (undefined
	// symbol on the register backends). Drop the dead calls: `__map_drop_values`
	// is `ptr -> ptr` (self-guards on rc==1), so removing the op is stack-neutral —
	// the still-present `__fern_map_drop` (a backend runtime helper, always
	// available) runs on the same unreachable pointer and is itself dead. When any
	// live map exists `__map_drop_values` is loaded, so this pass is a no-op and no
	// reachable reclamation is ever removed. This restores the "Map-in-enum deep
	// drop is a documented safe leak" invariant (see enumRcPayloadsEligible) for
	// the array-element / accumulator reclaim path that generates the drop fn
	// regardless of that eligibility gate.
	mapDropLoaded := false
	for _, f := range out.Funcs {
		if f.Name == "__map_drop_values" {
			mapDropLoaded = true
			break
		}
	}
	if !mapDropLoaded {
		for _, f := range out.Funcs {
			filtered := f.Ops[:0]
			for _, op := range f.Ops {
				if op.Kind == OpCallDirect && op.Str == "__map_drop_values" {
					continue
				}
				filtered = append(filtered, op)
			}
			f.Ops = filtered
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
	// A function taken as a value (a MakeClosure / indirect-call
	// target) must NOT use the two-word (tag, payload) pair-form
	// return ABI: indirect calls go through the single-word heap-box
	// ABI (addClosureSigType / the native indirect-call seam thread
	// one slot per result), so a pair-form return would mismatch the
	// call-site signature and corrupt the stack (segfault on natives,
	// validation error on wasm). Heap-form keeps the function's return
	// shape and the indirect-call ABI in agreement. See #2753.
	// Address-taken functions: a function whose name appears as a
	// value rather than only as a direct call. closureconv leaves a
	// top-level function passed as a value as a bare Ident (it does
	// NOT wrap it in a MakeClosure), so detect both forms: a
	// MakeClosure target, or an Ident naming a function that is not a
	// Call's callee. Over-detecting here only forgoes the pair-form
	// optimization (heap-form is always correct), so it's safe.
	calleeIdents := map[*ast.Ident]bool{}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if c, ok := n.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok {
				calleeIdents[id] = true
			}
		}
		return true
	})
	closureTargets := map[string]bool{}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MakeClosure:
			if x.FuncName != "" {
				closureTargets[x.FuncName] = true
			}
		case *ast.Ident:
			if !calleeIdents[x] {
				if _, isFunc := info.FuncSigs[x.Name]; isFunc {
					closureTargets[x.Name] = true
				}
			}
		}
		return true
	})
	for {
		grew := false
		for _, fn := range prog.Funcs {
			if out[fn.Name] {
				continue
			}
			if closureTargets[fn.Name] {
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
// findTrmcFuncs returns the set of functions that will lower via the TRMC
// hole-passing loop (detectTrmc + TrmcEnabled), and the consume-safe SUBSET
// whose owned-by-default scrutinee is reclaimed per-cell inside the loop
// (trmcShapeConsumeSafe — scalar head payloads, the FBIP list-map case).
//
// A TRMC function's custom loop exit bypasses the normal param exit-sweep, so
// Slice 2 (OwnedByDefault) excludes a plain TRMC function's parameters from
// owned-by-default — they keep the borrow model. A consume-safe TRMC function
// instead frees its scrutinee cell-by-cell as the loop advances, so its
// scrutinee parameter IS owned-by-default at the CALL site (the caller retains
// an aliased arg) — the definition side still skips the exit-sweep (the loop,
// not the sweep, does the freeing).
func findTrmcFuncs(prog *ast.Program, info *checker.Info, ptrW int, pairForm map[string]bool) (trmc, consumeSafe map[string]bool) {
	trmc = map[string]bool{}
	consumeSafe = map[string]bool{}
	if !ast.TrmcEnabled {
		return trmc, consumeSafe
	}
	for _, fn := range prog.Funcs {
		lb := &builder{info: info, fn: fn, ptrW: ptrW, pairForm: pairForm, thisIsPair: pairForm[fn.Name]}
		sh := lb.detectTrmc()
		if sh == nil {
			continue
		}
		trmc[fn.Name] = true
		if lb.trmcShapeConsumeSafe(sh) {
			consumeSafe[fn.Name] = true
		}
	}
	return trmc, consumeSafe
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
	// COW self-reassign carve-out (#4357's map-intermediate leak): a
	// `m = m.set(..)` / `m = m.insert(..)` / `m = m.cleared()` / `a =
	// a.with(..)` statement — the isSelfMapMutation shape, receiver
	// occurring EXACTLY once in the RHS — does not end the local's
	// freshness. The cow mutator returns either the SAME handle (rc==1
	// in-place) or a fresh copy of it, so by induction a handle that
	// started fresh (the init check below) stays param-free through any
	// number of such rebinds.
	//
	// What the mutation STORES matters too: Map_set moves keys/values in
	// UNCOUNTED (the escape-taint model), and the fresh-credit reclaim
	// (emitMapSlotDrop) deep-frees the value column and string keys —
	// so a param-derived store would be freed out from under the caller.
	// Hence every NON-receiver argument must itself be escape-free
	// against the declared key/value/element slot type (scalar slots
	// pass trivially — the diagnosed `Map[i32, i32]` builder shape;
	// `m.insert(k, param_array)` correctly disqualifies). An array
	// `.with` element is additionally inc'd at the store
	// (emitArraySet's needsRcIncOnAlias) so its deep walk balances, but
	// the same escape-free requirement is applied uniformly — simpler
	// and strictly conservative. An unannotated declaration (no slot
	// types to check against) rejects the carve-out.
	//
	// Cow-assigns to a PARAM can't be admitted here structurally —
	// params aren't Var decls, so they never enter the candidate set,
	// and `mk(m: Map..) { m = m.insert(..); return m; }` (whose rc==1
	// in-place path would hand back the CALLER's handle) stays
	// unproven. Keyed by node identity so any OTHER occurrence of the
	// name still taints.
	cowArgSlots := func(calleeName string, declared ast.Type, nargs int) ([]ast.Type, bool) {
		switch calleeName {
		case "__method_Map_clear":
			return nil, nargs == 1 // receiver only
		case "__method_Map_set":
			st, ok := declared.(ast.StructType)
			if !ok || st.Name != "Map" || len(st.Args) != 2 || nargs != 3 {
				return nil, false
			}
			return []ast.Type{st.Args[0], st.Args[1]}, true // key, value
		case "__method_Array_set":
			at, ok := declared.(ast.ArrayType)
			if !ok || nargs != 3 {
				return nil, false
			}
			return []ast.Type{ast.NumberType{}, at.Elem}, true // index, element
		case "__method_Array_push":
			// `a = a.append(v)` — __fern_arr_push_grow has the same COW
			// contract as the `.with` cow helper above: the SAME buffer at
			// rc==1 (in-place, length bumped) and a fresh copy otherwise. So
			// a buffer that started fresh stays param-free across the rebind.
			// Args are (receiver, element), so the slot list aligns with
			// Args[1:] as one element slot.
			at, ok := declared.(ast.ArrayType)
			if !ok || nargs != 2 {
				return nil, false
			}
			return []ast.Type{at.Elem}, true // element
		}
		return nil, false
	}
	cowUse := map[*ast.Ident]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		asn, ok := n.(*ast.Assign)
		if !ok {
			return true
		}
		tid, ok := asn.Target.(*ast.Ident)
		if !ok || !isSelfCowRebind(asn.Value, tid.Name) {
			return true
		}
		decl, isDecl := decls[tid.Name]
		if !isDecl || decl.Type == nil {
			return true
		}
		call := asn.Value.(*ast.Call)
		callee := call.Callee.(*ast.Ident)
		slots, ok := cowArgSlots(callee.Name, decl.Type, len(call.Args))
		if !ok {
			return true
		}
		for i, slot := range slots {
			// Args[0] is the receiver; slots align with Args[1:]. No
			// freshLocals set is passed (empty map) — an argument that is
			// itself a fresh local is conservatively rejected rather than
			// entangling this collection with the fixpoint below.
			if !exprNoParamEscape(call.Args[i+1], slot, info, variantPayloads, q, nil) {
				return true
			}
		}
		var occurrences []*ast.Ident
		ast.Walk(asn.Value, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == tid.Name {
				occurrences = append(occurrences, id)
			}
			return true
		})
		if len(occurrences) == 1 {
			cowUse[tid] = true
			cowUse[occurrences[0]] = true
		}
		return true
	})
	// Distinct-target append (#5608): `var ys = xs.append(v)`. The same COW
	// induction as the self-rebind above — the result is xs's own buffer (the
	// rc==1 in-place path) or a fresh copy of it — so the receiver occurrence
	// does not end xs's freshness either, and the rc pairing balances on both
	// paths: in-place the grow helper bumps xs to rc 2 and ys's owner drops it
	// back, on the copy path xs stays at rc 1 and its exit sweep frees the
	// orphan.
	//
	// Sound ONLY when this is the receiver's single occurrence in the whole
	// body. The in-place path mutates xs's buffer in place, so any later read
	// of xs would observe the longer array — the #4827 hazard appendForcesCopy
	// guards at emit time. Requiring one occurrence means no later read exists,
	// which is strictly stronger than what appendForcesCopy needs, so the two
	// cannot disagree.
	// Built lazily on the first candidate: most functions contain no
	// distinct-target append, and this walk is on the compile-hot path.
	var allOccurrences map[string][]*ast.Ident
	occurrencesOf := func(name string) []*ast.Ident {
		if allOccurrences == nil {
			allOccurrences = map[string][]*ast.Ident{}
			ast.Walk(fn.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					allOccurrences[id.Name] = append(allOccurrences[id.Name], id)
				}
				return true
			})
		}
		return allOccurrences[name]
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		v, ok := n.(*ast.Var)
		if !ok || v.Init == nil {
			return true
		}
		call, ok := v.Init.(*ast.Call)
		if !ok {
			return true
		}
		callee, ok := call.Callee.(*ast.Ident)
		if !ok || callee.Name != "__method_Array_push" || len(call.Args) != 2 {
			return true
		}
		recv, ok := call.Args[0].(*ast.Ident)
		if !ok || recv.Name == v.Name {
			return true // the self-rebind form is handled above
		}
		decl, isDecl := decls[recv.Name]
		if !isDecl || decl.Type == nil {
			return true // a param receiver never qualifies (not a Var decl)
		}
		slots, ok := cowArgSlots(callee.Name, decl.Type, len(call.Args))
		if !ok {
			return true
		}
		if !exprNoParamEscape(call.Args[1], slots[0], info, variantPayloads, q, nil) {
			return true
		}
		// A `var x = …` declaration contributes no Ident node for x (Var.Name
		// is a string), so a single occurrence means the receiver is used
		// exactly once — here — and never read again.
		if occ := occurrencesOf(recv.Name); len(occ) == 1 && occ[0] == recv {
			cowUse[recv] = true
		}
		return true
	})
	tainted := map[string]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && !inReturn[id] && !cowUse[id] {
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
	case *ast.StringLit:
		// A string literal is a static sentinel (immortal, below-heap) — no
		// parameter heap can flow through it. Without this case every
		// constructor embedding a literal string field (`S { name: "x" }`)
		// lost its escape-free verdict, and each local bound to its call
		// result stayed borrow-tainted — the enum-payload-struct string-field
		// leak (#4355): the whole box chain (enum box + payload struct box +
		// its string) was never swept.
		return true
	case *ast.Binary:
		// String concatenation BYTE-COPIES both operands into a fresh owned
		// buffer — the result aliases neither operand, so no param heap
		// escapes through it even when an operand IS a param (the same
		// provenance-free-fresh rule rhsTainted's IsStringConcat case
		// encodes). Non-concat binaries are scalar-valued and only reach
		// here for a non-scalar slot — conservatively rejected as before.
		return x.IsStringConcat
	case *ast.SliceExpr:
		// A STRING slice copies into a fresh owned buffer (not a view) —
		// same freshness as concat. Array slices are views into their
		// source and stay rejected.
		return x.IsString
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
		// `map_new(cap)` constructs a FRESH empty map: its handle and bucket
		// buffer are newly allocated and its arguments are scalars (the cap
		// hint plus the injected keyKind/valKind tags), so no parameter heap
		// can flow through the result. Admitting it here is what lets a
		// cow-threaded map builder (`var m = map_new(8); m = m.insert(..);
		// return m;`) prove its return fresh (#4357's map-intermediate leak) —
		// without it the builtin (absent from q) rejected the init and the
		// local never entered freshLocals.
		if id.Name == "map_new" {
			return true
		}
		// `string_from_bytes_unchecked(buf)` always COPIES — into an
		// inline-tagged register value at <= 7 bytes, into a fresh rc1 heap
		// buffer above — so the result aliases neither `buf` nor anything
		// reachable from it. Same provenance-free-fresh rule as the string
		// concat / slice arms above, just spelled as a builtin. (This is the
		// copy #5931's allocator untaint rests on: it is what makes the input
		// buffer dead at the return.)
		//
		// Admitting it is what lets the int-to-string family prove fresh at all.
		// `core/int.int_to_string` / `__int_to_string_u64` / `int_to_string_radix`
		// all END in this call, so without it every one of them was rejected here,
		// and with them every wrapper above them — `to_string`, `to_hex`,
		// `to_binary`. That verdict propagates: ownedCallResultType refuses to
		// reclaim a `__`-prefixed method result unless the callee is proven
		// fresh-returning, so `x.to_binary()` handed to another call could not be
		// stashed and dec'd, and leaked (#5942).
		if id.Name == "string_from_bytes_unchecked" {
			return true
		}
		// `xs.append(v)` returns the receiver's OWN buffer (the rc==1 in-place
		// path) or a fresh copy of it — never anything derived from the element
		// argument's heap. So the result is param-free exactly when the
		// receiver is, provided the element itself can't strand a param alias
		// in the buffer. Mirrors rhsTainted's receiver-aliasing arms for
		// `__method_Map_set` / `__method_Array_set`, and is the piece that lets
		// `var ys = xs.append(v); return ys;` prove fresh (#5608). Without it
		// the generic any-arg rule below rejected the call and the caller's
		// binding fell back to a non-freeing dec.
		if id.Name == "__method_Array_push" && len(x.Args) == 2 {
			var elem ast.Type
			if at, isArr := slot.(ast.ArrayType); isArr {
				elem = at.Elem
			}
			return exprNoParamEscape(x.Args[0], slot, info, variantPayloads, q, freshLocals) &&
				exprNoParamEscape(x.Args[1], elem, info, variantPayloads, q, freshLocals)
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
	// The struct/enum-keyed get (`__map_get_keyed_impl`, #2671) goes
	// through the SAME emitMapGetRebox heap-box call site, so it needs
	// the identical exclusion — otherwise its pair-form return shape
	// mismatches the call's heap-box ABI and segfaults on natives.
	if fn.Name == "__map_get_impl" || fn.Name == "__map_get_keyed_impl" {
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
	case *ast.Loop:
		return allReturnsArePairFormShape(x.Body, names, pairForm)
	case *ast.For:
		return allReturnsArePairFormShape(x.Body, names, pairForm)
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

// loopFrame records one enclosing loop's label and its break/continue
// target depths, so a labeled `break`/`continue` can resolve past
// intervening loops.
type loopFrame struct {
	label  string
	breakD int32
	contD  int32
}

// findLoopFrame returns the innermost enclosing loop frame whose label
// matches, or nil if none does.
func (b *builder) findLoopFrame(label string) *loopFrame {
	for i := len(b.loopFrames) - 1; i >= 0; i-- {
		if b.loopFrames[i].label == label {
			return &b.loopFrames[i]
		}
	}
	return nil
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
	// selfPushMoveCall is the exact `a.append(v)` RHS call node of a
	// self-append assignment (`a = a.append(v)`, isSelfArrayPushLocal),
	// set by the Assign lowering just before it lowers the RHS and
	// cleared after. emitArrayPush consults it (by node identity) to keep
	// that one push on the plain MOVE-semantics __fern_arr_push_grow: the
	// self-append's overwrite reclaim is a buffer-only __fern_arr_dec
	// that pairs with a non-retaining copy, while every other push of an
	// rc-tracked-element array uses the element-RETAINING grow variants
	// (#3425). Node identity keeps nested appends inside the pushed
	// value on the retaining path.
	selfPushMoveCall ast.Expr
	// selfStrAppendBin is the exact `s + rhs` RHS node of a string
	// self-append assignment (`s = s + rhs`, isSelfStrAppendLocal), set
	// by the Assign lowering just before it lowers the RHS and cleared
	// after. The concat lowering consults it (by node identity) to emit
	// __fern_str_append instead of OpStrConcat — the in-place-when-unique
	// append that turns the pervasive `var out = ""; loop { out = out +
	// piece }` stdlib idiom from an allocate-and-copy per piece into a
	// memcpy into the existing buffer's size-class slack (#5637 option 3).
	// selfStrAppendDone records that the concat site actually took that
	// branch, so assign() knows the helper has consumed (and reclaimed)
	// the old buffer and its own dec-on-overwrite must be skipped.
	selfStrAppendBin  ast.Expr
	selfStrAppendDone bool
	// appendOrder caches the ident-occurrence order of the current
	// function's body (lazily, per fn) so emitArrayPush can ask whether an
	// ident append operand is its LAST use without rebuilding the order at
	// every push site. appendOrderFn is the fn the cache was built for.
	// appendInPlaceOK (built in the same refresh) is the set of push calls
	// exempt from the reused-after forced copy — see inPlacePushes.
	appendOrder     identOrder
	appendOrderFn   *ast.FuncDecl
	appendInPlaceOK map[*ast.Call]bool
	// callArgDies (rebuilt in the same refresh) marks the ident args that
	// die at each call via the strict self-reassign shape — see
	// callArgDeaths. Read by the #4873 caller-side grow bracket.
	callArgDies map[*ast.Call]map[string]bool
	// growParams[name][i] is the growParamKind bitmask for parameter i of
	// function `name` — the positions whose argument buffer(s) the callee
	// may mutate in place through the rc==1 fast paths (computeGrowParams,
	// #4873). callBody brackets surviving args at those positions with an
	// rc inc/dec pair so the callee's uniqueness gate takes the copy path.
	growParams map[string][]uint8
	// paramEscapes[name][i] is true when parameter i of function `name` can
	// escape (inferParamEscapes). Borrow inference (BorrowInferEnabled) keeps a
	// NON-escaping owned-by-default param borrowed — no caller inc, no callee
	// dec — read consistently on both the definition and call sides so they agree.
	paramEscapes map[string][]bool
	// paramCountedRetain[fn][i] is true when string parameter i of `fn` is
	// retained only by counted constructions, so an argument passed there needs
	// no conservative escape taint (inferParamCountedRetain).
	paramCountedRetain map[string][]bool
	// readOnlyComparators is the set of Eq/Hash trait method names
	// (`__method_<T>_eq` / `__method_<T>_hash`). They are read-only by
	// contract, so they BORROW their params even under the owned model
	// (borrow inference off) — the type-erased Map runtime calls them on
	// BORROWED stored keys via a function value, so an owned-model exit-dec
	// would free the map's own key (corruption). Gated on the escape facts
	// proving non-escape, so it never elides a real ownership transfer. #2671.
	readOnlyComparators map[string]bool
	// trmcFuncs is the set of functions lowered via TRMC (findTrmcFuncs); Slice 2
	// excludes their params from owned-by-default (their exit bypasses the sweep).
	trmcFuncs map[string]bool
	// trmcConsumeSafe is the subset of trmcFuncs whose owned-by-default scrutinee
	// can be consumed in the loop (scalar head payloads); those DO take
	// owned-by-default at the call site, balanced by the loop's per-cell free.
	trmcConsumeSafe map[string]bool
	// TRMC-consuming loop state (set by emitTrmc): trmcConsuming gates the
	// per-cell free, trmcStillSlot holds the still-freeing flag (1 until the
	// first shared cell), trmcScrutEnum/Slot identify the walked scrutinee.
	trmcConsuming bool
	trmcStillSlot int32
	trmcScrutEnum ast.EnumType
	trmcScrutSlot int32
	// rc is the per-function Perceus RC plan: every decision table the
	// analyses in rc_analysis.go compute up front (computeRcAnalyses),
	// consulted by lowering when it emits incs / decs / drops / moves /
	// reuse. See the rcPlan field docs for each table's contract.
	rc rcPlan
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
	// enumDropTagSlot is the ONE per-function tag-stash scratch slot every
	// inline enum slot-drop (emitEnumSlotDrop's variant-plan tier) shares —
	// allocated on first use, -1 until then (#4476). The stash is written
	// and read strictly within a single drop sequence and sequences never
	// interleave, so sharing is exact; per-invocation allocation was what
	// interleaved sweep scratch with body scratch and blocked converting
	// the exit dec sweep to post-lowering insertion.
	enumDropTagSlot int32
	// depth is the current control-stack depth (number of open
	// block/loop/if scopes). Used to compute relative branch
	// distances for break/continue.
	depth int32
	// breakStack and contStack track the depth-after-open of the
	// scopes that `break` / `continue` should target. From a current
	// depth M, `br (M - stored)` lands at the right scope.
	breakStack []int32
	contStack  []int32
	// loopFrames mirrors the loop (while/for) nesting — one entry per
	// enclosing loop, innermost last — carrying each loop's label (empty
	// when unlabeled) and its break/continue target depths. A labeled
	// `break`/`continue` resolves by scanning this for the named loop.
	loopFrames []loopFrame
	// curPos is the source position of the AST node currently being
	// lowered. emit() stamps it onto every op so backends can drive
	// per-statement DWARF / .loc directives.
	curPos ast.Position
	// emitLineMarkers, when set (native -g), makes stmt() emit an OpLine
	// marker at each new source line; lastLineMark dedups consecutive
	// statements sharing a line.
	emitLineMarkers bool
	lastLineMark    int
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
	// dynRcSupported is the backend's `dyn Trait` RC capability flag
	// (LowerOption DynRcSupported, threaded from LowerWith). Only matters
	// for ptrW==8: x86-64 (slice 4b) reclaims boxed `dyn` values, arm64
	// (slice 4c, not landed) still leaks them — even though BOTH natives
	// pass DynSupported for dispatch. On ptrW==4 (wasm, slice 4a) the
	// `dyn` RC arms fire via ptrW==4 and ignore this. The builder-side
	// Perceus arms key on b.dynReclaim() (= ptrW==4 || dynRcSupported) —
	// docs/DYN-TRAITS.md §4.4.
	dynRcSupported bool
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
	// dynCoerceDone guards the `dyn Trait` coercion-boxing check at the
	// top of `expr`: when an expression is a key in info.DynCoercions,
	// the first visit lowers the concrete value (one word = data) and
	// appends OpConstVtable. To avoid re-triggering on the recursive
	// lowering of the same expression, the expr is marked here before
	// recursing. See docs/DYN-TRAITS.md §4.2.1.
	dynCoerceDone map[ast.Expr]bool
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

// dynBoxed reports whether `dyn Trait` values use the BOXED one-word
// native representation (a single heap pointer to a `{data, vtable}`
// cell) rather than the wasm inline two-word `[data, vtable]` fat
// pointer. True on natives (ptrW==8), false on wasm (ptrW==4). See
// docs/DYN-TRAITS.md §4.2.2.
func (b *builder) dynBoxed() bool {
	return b.ptrW != 4
}

// dynReclaim reports whether the current backend reclaims `dyn Trait`
// values (Perceus RC, docs/DYN-TRAITS.md §4.4). True on wasm (ptrW==4,
// slice 4a) and on a native backend that opted in via DynSupported
// (x86-64, slice 4b). arm64 (ptrW==8, no DynSupported, slice 4c) is
// false and keeps leaking `dyn`. The builder-side Perceus arms key on
// this so a backend without a `__drop_dyn_<set>` helper never emits a
// dangling call to one.
func (b *builder) dynReclaim() bool {
	return b.ptrW == 4 || b.dynRcSupported
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
	case *ast.Loop:
		collectDefers(x.Body, out)
	case *ast.For:
		collectDefers(x.Init, out)
		collectDefers(x.Step, out)
		collectDefers(x.Body, out)
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
// Called from `Return` lowering, the `?` error path, and the
// implicit-return path at the end of `lowerFunc`. Plain `defer`s
// run on EVERY exit; `errdefer`s (OnError) are skipped here and
// replayed separately by emitErrDeferCleanup on the error paths.
func (b *builder) emitDeferCleanup() error {
	return b.emitDeferCleanupKind(false)
}

// emitErrDeferCleanup replays only the `errdefer`s (OnError) —
// in reverse source order, each guarded by its active flag. It
// runs in ADDITION to emitDeferCleanup, and only on error exits:
// the `?` operator's None/Err propagation and a `return` of a
// failure variant from an Option/Result-returning function.
func (b *builder) emitErrDeferCleanup() error {
	return b.emitDeferCleanupKind(true)
}

// emitDeferCleanupKind is the shared body: replay the registered
// defers in reverse source order, restricted to those whose
// OnError matches `onError`.
func (b *builder) emitDeferCleanupKind(onError bool) error {
	for i := len(b.defers) - 1; i >= 0; i-- {
		if b.defers[i].OnError != onError {
			continue
		}
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

// hasErrDefers reports whether any registered defer is an
// `errdefer`. Gating every errdefer-specific emission on this
// keeps functions without an errdefer byte-identical to before.
func (b *builder) hasErrDefers() bool {
	for _, d := range b.defers {
		if d.OnError {
			return true
		}
	}
	return false
}

// isOptionOrResultType reports whether t is `Option[...]` or
// `Result[...]` — the return types whose failure variant
// (None / Err, tag 1) triggers an errdefer on `return`.
func isOptionOrResultType(t ast.Type) bool {
	et, ok := t.(ast.EnumType)
	return ok && (et.Name == "Option" || et.Name == "Result")
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
// isArraySetCall reports whether c is a desugared `arr.with(i, v)` —
// `__method_Array_set(arr, i, v)`.
func isArraySetCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	return ok && id.Name == "__method_Array_set" && len(c.Args) == 3
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

// captureSlotSizes returns the per-capture env-slot byte sizes (in capture
// order) for a hoisted closure's Captures list — the packed layout stamped onto
// OpMakeClosure / OpMakeEnv so the SSA backend, which has no AST at emit time,
// can pack its env stores at the same offsets/widths the CaptureRef loads read.
// Returns nil for a captureless closure (no env block).
func captureSlotSizes(caps []ast.Param, ptrW int) []int32 {
	if len(caps) == 0 {
		return nil
	}
	slots := make([]int32, len(caps))
	for i, c := range caps {
		slots[i] = irCaptureSlotSize(c.Type, ptrW)
	}
	return slots
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

// tupleEnumMangler is the shared escape table for tuple + enum
// instantiation mangling. `[`/`]` carry enum type args, `(`/`)` carry
// tuple-element lists, and `,` separates either; a fn-typed element
// (`(i32) => i32`) carries `=>`, a dyn-trait element can carry `+`
// (trait composition), `=` (pinned associated types), and `:` (a
// ProjType base); all collapse to `[A-Za-z0-9_]` tokens so the result
// is a valid wasm/asm symbol and no two distinct types compress to the
// same mangled name. (`=>` is listed before `=`/`>` so the arrow wins
// at a position where both would match — strings.Replacer matches in
// argument order.)
var tupleEnumMangler = strings.NewReplacer(
	"[", "_LB_",
	"]", "_RB_",
	"(", "_LP_",
	")", "_RP_",
	",", "_C_",
	"=>", "_ARROW_",
	"=", "_EQ_",
	"+", "_PLUS_",
	":", "_CO_",
	"<", "_LT_",
	">", "_GT_",
	" ", "",
)

// enumDropLoad is one pointer-shaped payload slot the enum drop
// must dec: the offset from the box's data pointer plus the
// payload's static type (which decValueOnStack uses to pick the
// recursive array drop vs a flat dec).
type enumDropLoad struct {
	off int32
	typ ast.Type
}

// variantDrop is the per-variant drop plan for the non-uniform enum
// box reclamation path: the runtime tag that selects this variant, the
// droppable payload loads, and the heap-box payload size to free.
type variantDrop struct {
	tag   int
	loads []enumDropLoad
	size  int32
}

func lowerFunc(fn *ast.FuncDecl, info *checker.Info, ptrW int, dynRcSupported bool, emitLineMarkers bool, pairForm map[string]bool, closureCaps map[string][]ast.Param, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, returnsNoParamEscape map[string]bool, trmcFuncs, trmcConsumeSafe map[string]bool, paramEscapes map[string][]bool, paramCountedRetain map[string][]bool, readOnlyComparators map[string]bool, growParams map[string][]uint8) (*Func, error) {
	out := &Func{
		Name:       fn.Name,
		Params:     fn.Params,
		Locals:     info.Locals[fn],
		ReturnType: fn.ReturnType,
		Captures:   fn.Captures,
		InlineHint: fn.InlineHint,
	}
	b := &builder{
		info:                 info,
		fn:                   fn,
		out:                  out,
		locals:               map[string]int32{},
		scratchType:          map[int32]ast.Type{},
		ptrW:                 ptrW,
		dynRcSupported:       dynRcSupported,
		emitLineMarkers:      emitLineMarkers,
		pairForm:             pairForm,
		closureCaps:          closureCaps,
		genEnumDrops:         genEnumDrops,
		genTupleDrops:        genTupleDrops,
		returnsNoParamEscape: returnsNoParamEscape,
		trmcFuncs:            trmcFuncs,
		trmcConsumeSafe:      trmcConsumeSafe,
		paramEscapes:         paramEscapes,
		paramCountedRetain:   paramCountedRetain,
		readOnlyComparators:  readOnlyComparators,
		growParams:           growParams,
		thisIsPair:           pairForm[fn.Name],
		dynCoerceDone:        map[ast.Expr]bool{},
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
	b.enumDropTagSlot = -1
	// Per-function Perceus RC plan — every decision analysis (consumed
	// params, borrow-aware free eligibility, moves, array-set incs, reuse)
	// computed up front in one place; see rc_analysis.go (#4393).
	b.computeRcAnalyses()
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
	for _, v := range info.Locals[fn] {
		if !rcTrackedSlotType(v.Type) {
			// `dyn Trait` slots are in the exit sweep's tracked set
			// (dynReclaim-gated) but not in rcTrackedSlotType — zero them too,
			// so a conditionally-declared dyn local's exit sweep and a loop-var
			// dyn's first-iteration reinit drop see the NULL cell
			// __drop_dyn_<set> already guards, instead of stack garbage
			// (#4495 — segfaulted on x86-64). The helper's null guard was
			// written assuming this zero-init existed. Natives only: wasm
			// locals are zero by spec, and its inline two-word [data, vtable]
			// slot has a different store arity than the boxed one-word cell.
			if _, isDyn := v.Type.(ast.DynTraitType); !(isDyn && b.dynReclaim() && b.ptrW == 8) {
				continue
			}
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
	// Zero-init every defer / errdefer active flag. The flag is set
	// to 1 only when its statement is reached at runtime; the per-
	// exit cleanup reads it to skip a defer registered inside a
	// branch that didn't run. The slot is otherwise uninitialised
	// stack space on the natives, and in a function that builds an
	// enum return value (e.g. `return Err(...)`) that slot can hold
	// non-zero garbage — spuriously firing an unreached defer. (The
	// wasm locals are zero by spec, so this only corrects the
	// natives, but emitting unconditionally keeps the backends in
	// lockstep.) Pre-zero so the active-flag guard is sound.
	for _, slot := range b.deferSlots {
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
	}
	// Consuming-owned-match bindings (#4400): pre-allocate each binding's slot
	// and zero it, so (a) bindingSlot reuses this slot when the arm binds (the
	// scratchType stamp gives it the right single-word shape), and (b) the
	// exit-sweep dec added for these names sees a NULL — not stack garbage —
	// when the binding's arm never ran. Same safety-zero contract as the
	// rc-tracked local zeroing above; sorted for deterministic slot order.
	if len(b.rc.consumingBindings) > 0 {
		names := make([]string, 0, len(b.rc.consumingBindings))
		for nm := range b.rc.consumingBindings {
			names = append(names, nm)
		}
		sort.Strings(names)
		for _, nm := range names {
			slot := b.allocSlot()
			b.locals[nm] = slot
			b.scratchType[slot] = b.rc.consumingBindings[nm]
			b.emit(Op{Kind: OpConstI32, I32: 0})
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
	}
	// Consumed-threaded ARRAY params carry their ownership as an explicit bit
	// rather than as an entry retain, because a retain costs them the in-place
	// append (isConsumedArrayParam). Allocate and zero the bit here, in
	// parameter order, so the slot numbering is deterministic.
	for _, p := range fn.Params {
		if !b.isConsumedArrayParam(p.Name) {
			continue
		}
		slot := b.allocSlot()
		b.locals[consumedArrayFlagName(p.Name)] = slot
		b.emit(Op{Kind: OpConstI32, I32: 0})
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
	}
	// Consumed-threaded param entry-incs are no longer emitted here: they are
	// the first RC insertion converted to true post-lowering []Op insertion
	// (insertConsumedParamEntryIncs, rc_insert.go — #4393 slice 4), spliced at
	// this exact prologue boundary after the whole body has been lowered.
	// Capture the boundary: everything before it (the rc-slot and defer-flag
	// zero-init) is the entry prologue.
	entryIncAt := len(out.Ops)
	entryIncPos := b.curPos
	// Perceus precise drops (garbage-free): drop an owned rc local right after
	// its last use instead of at the exit sweep, lowering peak memory. Iterate
	// the function body's top-level statements directly (the Block case is a
	// bare loop) so we can splice the per-statement precise drops in; a local
	// declared in a NESTED block is keyed by statement node instead and
	// spliced by that Block case (computeNestedDrops). Both tables are nil
	// when RcFreeEnabled is off, so this is identical to b.stmt(fn.Body) on
	// the no-free path.
	b.rc.preciseDrops = b.computePreciseDrops()
	b.rc.nestedDrops = b.computeNestedDrops()
	if b.tryEmitTrmc() {
		// TRMC took over the whole body (a single `match`); skip normal
		// statement lowering. Scratch-type recording below still runs.
	} else {
		for i, st := range fn.Body.Stmts {
			if err := b.stmt(st); err != nil {
				return nil, err
			}
			for _, name := range b.rc.preciseDrops[i] {
				b.emitPreciseDrop(name)
			}
		}
	}
	// If the body falls off the end, emit an implicit return so the
	// downstream consumer doesn't have to check. Run any
	// registered defers first — same shape as the explicit
	// Return path.
	//
	// This MUST run before ScratchTypes is sized below: its
	// emitRcDecLocalsAtExit can allocate fresh scratch slots (e.g. the
	// tag stash of an enum-param exit-drop on a fall-off path), and a
	// slot allocated after the count is taken would be referenced but
	// never declared — an out-of-bounds local on wasm (#2828).
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
			// By the DECLARED float width, not F32 unconditionally.
			// wasm's stack is typed, so an f64-returning function whose
			// body falls off the end (a `match` the checker proves
			// exhaustive still has no fall-through arm here) was handed
			// an `f32.const 0` against an f64 result and the whole
			// module was rejected at instantiation — "expected f64,
			// found f32". That took out every program returning
			// Option[f64] through a match, including std/test (#6192).
			if ft, ok := fn.ReturnType.(ast.FloatType); ok && ft.Width == 64 {
				b.emit(Op{Kind: OpConstF64, F64: 0})
			} else {
				b.emit(Op{Kind: OpConstF32, F32: 0})
			}
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
	// Post-lowering RC insertion (#4393 slice 4): splice the consumed-param
	// entry retain-incs into the finished op stream at the prologue boundary
	// captured above. Runs on the lowered []Op — the first piece of RC
	// insertion converted off the in-build path — and allocates no slots, so
	// it is safe before (or after) the scratch sizing below.
	b.insertConsumedParamEntryIncs(entryIncAt, entryIncPos)
	// Record the type of every synthetic slot the lowering pass
	// conjured beyond the user-visible params + locals — ArrayLit
	// / StructLit / closure helpers each added entries to
	// the locals map. Most are i32 (heap pointers or integer tags);
	// match-arm bindings of float-typed payloads register a
	// FloatType in scratchType so wasm declares the local as f32.
	//
	// Use the standalone nextSlot counter (rather than
	// `len(b.locals)`) so two match arms that share a binding
	// name don't fool the count by overwriting the same map
	// entry — both still consume distinct slot indices. Sized AFTER
	// the implicit-return emission above, which can allocate the last
	// scratch slot (the enum-param fall-off drop's tag stash, #2828).
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
	// Differential-harness probe (#4482): hand the finished per-function
	// rcPlan to the hook, if armed — nil (and free) outside tests / the
	// differential gate. After everything above, so preciseDrops is filled
	// and every table is final.
	if RcPlanHook != nil {
		RcPlanHook(fn.Name, b.dumpRcPlan())
	}
	// `fip` / `fbip` verify-and-enable (plan E2', fip_verify.go): check the
	// ops just emitted against the annotation's allocation budget — every
	// fresh (un-reuse-paired) allocation beyond the graded allowance is an
	// E068 error. Runs on the raw per-function stream, before any later
	// pass reshapes it, and is read-only over the emitted result.
	if err := verifyFipAllocs(fn, out); err != nil {
		return nil, err
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

// paramVerdict is THE per-(function, param) ownership classification (#4478):
// one ladder, consulted by both the definition side (paramOwnedByDefault) and
// the call site (calleeParamOwnedByDefault), so the two can never disagree on
// which params carry the owned-by-default inc/dec pair. It replaces the
// paramBorrowable / paramOwnedByDefault / calleeParamOwnedByDefault predicate
// trio, whose def/call agreement was previously by convention (each site
// re-intersecting paramEscapes / readOnlyComparators / trmcFuncs /
// trmcConsumeSafe). The consumed-threaded promotion (rc.consumedParams) stays
// a separate def-side-only table: it changes only callee-internal reclamation,
// never the call ABI, and is checked alongside the verdict at its sites.
type paramVerdict uint8

const (
	// paramVerdictNotOwnedType: the parameter's TYPE is outside the
	// owned-by-default set (scalar, or a string/array/Map-bearing composite —
	// isOwnedByDefaultType). No inc/dec pair on either side.
	paramVerdictNotOwnedType paramVerdict = iota
	// paramVerdictOwned: the callee reclaims the param at exit; the caller
	// retains an aliased argument.
	paramVerdictOwned
	// paramVerdictBorrowed: borrow inference (BorrowInferEnabled) proved the
	// param non-escaping — the owned inc/dec pair is redundant (the value
	// cannot outlive the call frame; the caller still owns and reclaims it).
	paramVerdictBorrowed
	// paramVerdictReadOnlyComparator: an Eq / Hash trait method param borrows
	// even under the owned model. The type-erased Map runtime calls hash_fn /
	// eq_fn on its BORROWED stored keys via a function value, so an
	// owned-model exit-dec there would free the map's own key (corruption at
	// scale). Gated on the same escape facts as the borrow model, so owned
	// and borrow agree on them. #2671.
	paramVerdictReadOnlyComparator
	// paramVerdictTrmcExcluded: the function lowers via TRMC and its exit
	// bypasses the param sweep — not owned in the body, not owned at the
	// call site.
	paramVerdictTrmcExcluded
	// paramVerdictTrmcConsume: a consume-safe TRMC callee frees its scrutinee
	// cell-by-cell in the loop, so it IS owned at the CALL site no matter
	// what the escape analysis says (borrow inference must not flip it to
	// borrowed — the caller would also reclaim the arg the loop already
	// freed: a double free). Still excluded on the definition side (the TRMC
	// exit bypasses the sweep; the loop is the reclamation).
	paramVerdictTrmcConsume
)

// paramVerdict classifies parameter `i` (declared type `t`) of function
// `fnName` — the single ownership ladder both sides read. Precedence:
// type-eligibility, then TRMC (consume-safe before plain — the consume-safe
// call-site ownership overrides the escape facts), then the borrow facts.
func (b *builder) paramVerdict(fnName string, t ast.Type, i int) paramVerdict {
	if !b.isOwnedByDefaultType(t) {
		return paramVerdictNotOwnedType
	}
	if b.trmcConsumeSafe[fnName] {
		return paramVerdictTrmcConsume
	}
	if b.trmcFuncs[fnName] {
		return paramVerdictTrmcExcluded
	}
	if esc, ok := b.paramEscapes[fnName]; ok && i < len(esc) && !esc[i] {
		if ast.BorrowInferEnabled {
			return paramVerdictBorrowed
		}
		if b.readOnlyComparators[fnName] {
			return paramVerdictReadOnlyComparator
		}
	}
	return paramVerdictOwned
}

// paramOwnedByDefault: a parameter of the CURRENT function is owned-by-default
// (reclaimed by this function's exit sweep) exactly when its verdict is Owned.
func (b *builder) paramOwnedByDefault(t ast.Type, i int) bool {
	return b.paramVerdict(b.fn.Name, t, i) == paramVerdictOwned
}

// calleeParamOwnedByDefault: the CALLEE reclaims this parameter (so the caller
// must retain an aliased arg). True for Owned and — call-site only — for the
// consume-safe TRMC verdict, whose loop frees the scrutinee cell-by-cell.
func (b *builder) calleeParamOwnedByDefault(callee string, t ast.Type, i int) bool {
	v := b.paramVerdict(callee, t, i)
	return v == paramVerdictOwned || v == paramVerdictTrmcConsume
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
	if dName, paired := b.rc.reuseSources[callNode]; paired && callNode != nil {
		var dSize int32
		reuseSrcOffs, reuseSrcTypes, dSize = b.reuseSourceLayout(dName)
		if b.rc.consumingMatchReuse[callNode] {
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
		// payload (b.rc.moveSites, markConstructionMoves' enum case) skips the inc.
		// Inc-and-passthrough (leaves the value on the stack for the store).
		// Consuming-match reuse stores moved-out bindings back, so it's excluded.
		if b.enumRcPayloadsEligible(enumName) && !b.rc.consumingMatchReuse[callNode] &&
			needsRcIncOnAlias(a, b) && !b.rc.moveSites[a] {
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

// emitDowncast lowers `e as? T` (DowncastExpr) where `e: dyn Trait` and
// `T` is a struct/enum implementing the trait, to `Option[T]`
// (docs/DYN-TRAITS.md §9). The (trait,concrete) vtable pointer uniquely
// tags the concrete type, so the runtime test is a vtable-pointer
// compare against the static `__vtable_<Trait>_<T>` address: equal →
// Some(data) (data is the concrete heap pointer, which IS the T value —
// no unbox for a struct/enum target); else → None.
//
// The `data`/`vtable` extraction reuses exactly the dispatch lowering's
// pattern (see (*builder).call's DynTrait branch), branching on
// b.dynBoxed(): boxed natives deref a `{data@0, vtable@ptrW}` cell;
// inline wasm pops the high word of the two-word `[data, vtable]` value.
// The Some/None construction reuses the ordinary heap-box enum
// representation (Some via emitOptionSomeFromSlot, None via the shared
// payloadless OpEnumSentinel), so the result is an ordinary Option value
// a `match` reads with the same tag-at-[ptr+0] load as any other.
func (b *builder) emitDowncast(n *ast.DowncastExpr) error {
	if b.info == nil {
		return fmt.Errorf("ir: 'as?' downcast lowering requires checker info")
	}
	if n.Trait == "" {
		return fmt.Errorf("ir: 'as?' downcast missing trait (checker did not stamp DowncastExpr.Trait)")
	}
	concrete, ok := downcastTargetName(n.Target)
	if !ok {
		return fmt.Errorf("ir: 'as?' downcast target %v is not a struct/enum type", n.Target)
	}
	// Extract the receiver's `data` and `vtable` words into i32 scratch
	// slots. Representation-dependent (docs/DYN-TRAITS.md §4.2), mirroring
	// the dispatch lowering's extraction exactly.
	dataSlot := b.allocSlot()
	b.scratchType[dataSlot] = ast.NumberType{Width: 32}
	vtSlot := b.allocSlot()
	b.scratchType[vtSlot] = ast.NumberType{Width: 32}
	if b.dynBoxed() {
		// Boxed one-word (natives, §4.2.2): receiver is a pointer to a
		// `{data @0, vtable @ptrW}` cell. Stash the cell, deref both words.
		if err := b.expr(n.Inner); err != nil {
			return err
		}
		cellTmp := b.allocSlot()
		b.scratchType[cellTmp] = ast.NumberType{Width: 32}
		b.emit(Op{Kind: OpStoreLocal, I32: cellTmp})
		// data = load(cell + 0)
		b.emit(Op{Kind: OpLoadLocal, I32: cellTmp})
		b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})
		// vtable = load(cell + ptrW)
		b.emit(Op{Kind: OpLoadLocal, I32: cellTmp})
		b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		b.emit(Op{Kind: OpStoreLocal, I32: vtSlot})
	} else {
		// Inline two-word (wasm, §4.2.1): receiver lowers to `[data,
		// vtable]`. OpStoreLocal pops one word at a time: pop vtable
		// (high), then data (low).
		if err := b.expr(n.Inner); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: vtSlot})
		b.emit(Op{Kind: OpStoreLocal, I32: dataSlot})
	}
	// Compare the receiver's vtable word against the target's static
	// vtable address. OpConstVtable pushes the SAME address a coercion of
	// T would pair with, so the pointer-identity compare is exact. Key by
	// the whole trait SET (dynVtableSetKey): for a single-trait `dyn A`
	// downcast this is the bare trait name — byte-identical to before — so
	// it selects `__vtable_<A>_<T>`; for a multi-trait `dyn A + B` downcast
	// it selects the MERGED vtable `__vtable_<A+B>_<T>` that a multi-trait
	// coercion of T stores, so the compare matches exactly when the
	// runtime concrete is T (docs/DYN-TRAITS.md §10).
	setKey := n.Trait
	if len(n.Traits) > 0 {
		setKey = dynVtableSetKey(n.Traits)
	}
	b.emit(Op{Kind: OpLoadLocal, I32: vtSlot})
	b.emit(Op{Kind: OpConstVtable, Str: setKey, Ext: &OpExt{Str2: concrete}})
	b.emit(Op{Kind: OpEq})
	// Result Option[T] heap-box pointer, built in either arm into a
	// shared scratch slot (a void if-block keeps the operand-stack model
	// simple across backends; the result is loaded after OpEnd).
	resultSlot := b.allocSlot()
	b.scratchType[resultSlot] = ast.NumberType{Width: 32}
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	// hit → Some(data): data is the concrete heap pointer == the T value.
	if err := b.emitOptionSomeFromSlot(n.Target, dataSlot); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpElse})
	// miss → None (payloadless variant 1 — the shared static sentinel).
	b.emit(Op{Kind: OpEnumSentinel, I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.emit(Op{Kind: OpEnd})
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	return nil
}

// emitOptionSomeFromSlot builds an `Option[T].Some(v)` heap box where the
// payload value `v` is already lowered into the i32 scratch slot
// `dataSlot` (rather than an ast.Expr). It mirrors emitEnumNew's box
// layout for the single-payload Some variant (varIdx 0): an 8-byte rc
// header, the tag at [base+rcHeader], the payload at
// [base+rcHeader+offset]. Used by the downcast lowering, whose `data`
// word is the concrete heap pointer recovered from the `dyn` value. No
// payload inc is emitted (leak-mode dyn, like the rest of the dyn
// lowering — docs/DYN-TRAITS.md §4.4 RC follow-up).
func (b *builder) emitOptionSomeFromSlot(payloadType ast.Type, dataSlot int32) error {
	const rcHeaderBytes = 8
	offsets, size := payloadLayout([]ast.Type{payloadType}, 1, b.ptrW)
	b.emit(Op{Kind: OpConstI32, I32: size + rcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.scratchType[baseSlot] = ast.NumberType{Width: 32}
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// rc = 1 at [base+0].
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// tag = 0 (Some) at [base+rcHeader].
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStore})
	// payload = data at [base+rcHeader+offset].
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offsets[0]})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: dataSlot})
	b.emit(payloadStoreOpFor(payloadType, b.ptrW))
	// Push the user-visible data pointer (= base + rc header).
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	return nil
}

// boxPrimitiveDynValue heap-boxes the primitive/string value currently on
// top of the operand stack into a fresh value cell, leaving the cell
// POINTER on the stack as the `dyn` fat pointer's `data` word
// (docs/DYN-TRAITS.md §4.2.3). The cell is sized + stored via the concrete
// type's own layout helpers, so a two-word wasm string boxes as two words,
// an i64/f64 as 8 bytes, etc. The cell carries no rc header (leak-mode,
// like the existing dyn box). `concrete` is the primitive type-name string
// from the coercion site; the caller has already gated isPrimitiveConcrete.
func (b *builder) boxPrimitiveDynValue(concrete string) error {
	ct := astTypeForConcreteName(concrete)
	if ct == nil {
		return fmt.Errorf("ir: dyn coercion of %q: no value-box layout for primitive concrete", concrete)
	}
	size := payloadSlotSize(ct, b.ptrW)
	// Stash the value (one or two words) into a typed scratch so the
	// subsequent alloc — which may trigger heap init/grow and clobber the
	// operand stack model — doesn't strand it.
	valSlot := b.allocSlot()
	b.scratchType[valSlot] = ct
	b.locals[fmt.Sprintf("__dynbox_val_%d", valSlot)] = valSlot
	b.emit(Op{Kind: OpStoreLocal, I32: valSlot})
	// Allocate the value cell (no rc header — leak-mode dyn box).
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpAlloc})
	cellSlot := b.allocSlot()
	b.scratchType[cellSlot] = ast.NumberType{Width: 32}
	b.locals[fmt.Sprintf("__dynbox_cell_%d", cellSlot)] = cellSlot
	b.emit(Op{Kind: OpStoreLocal, I32: cellSlot})
	// cell[0] = value (concrete store width: two-word for a wasm string).
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: valSlot})
	b.emit(payloadStoreOpFor(ct, b.ptrW))
	// Leave the cell pointer on the stack as `data`.
	b.emit(Op{Kind: OpLoadLocal, I32: cellSlot})
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
		// Literal / range arm: build the boolean test as a Binary
		// AST node so the existing OpStrEq / OpEq / OpGeS dispatch
		// handles each scrutinee type uniformly. A plain literal is
		// `scrutinee == literal`; a range is `scrutinee >= lo && scrutinee
		// <op> hi`. The scrutinee is stashed under a synthetic local name
		// (already in b.locals) so Ident lookup hits scrSlot.
		var cond ast.Expr
		if arm.RangeHi != nil {
			cond = b.rangeMatchCond(arm.P, arm.Literal, arm.RangeHi, arm.RangeInclusive, scrSlot, tagT)
		} else {
			cond = &ast.Binary{
				P:           arm.P,
				Op:          "==",
				Left:        &ast.Ident{P: arm.P, Name: literalMatchScrName(scrSlot)},
				Right:       arm.Literal,
				IsStringCmp: isStringType(tagT),
				IsFloat:     isFloatType(tagT),
			}
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

// anyArmAtBinding reports whether any arm carries an `@` binding
// (`n @ Variant(...)`), which forces the heap-form match path.
func anyArmAtBinding(arms []*ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.AtBinding != "" {
			return true
		}
	}
	return false
}

// emitStructMatch lowers a `match` on a struct-typed scrutinee (the checker
// stamped n.StructMatch). Each arm is a struct pattern `S { x, y }` that
// binds the named fields irrefutably, so the match is an if-chain: cache the
// scrutinee pointer, then per arm load each bound field from `[ptr+offset]`
// into a scoped binding slot, run the optional guard (fall through on false),
// and run the body on a match. Bindings mirror the enum-match load path
// (bindingSlotScoped + payloadLoadOpFor), reading the borrowed scrutinee's
// fields without a transfer inc.
func (b *builder) emitStructMatch(n *ast.Match) error {
	tagT := b.exprType(n.Tag)
	st, ok := tagT.(ast.StructType)
	if !ok {
		return fmt.Errorf("ir: struct match scrutinee is not a struct type (compiler bug)")
	}
	sd, ok := b.info.Structs[st.Name]
	if !ok {
		return fmt.Errorf("ir: struct match on unknown struct %q", st.Name)
	}
	offMap, _ := structFieldLayout(sd.Fields, b.ptrW)
	scrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__struct_match_scr_%d", scrSlot)] = scrSlot
	if tagT != nil {
		b.scratchType[scrSlot] = tagT
	}
	if err := b.expr(n.Tag); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: scrSlot})
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
		b.openBlock(BlockTypeVoid)
		armEndD := b.depth
		armRestores := []func(){}
		// `@` binding: bind the whole matched struct (the scrutinee pointer)
		// to the at-name, borrowed like the field binds below.
		if arm.AtBinding != "" {
			atSlot, atRestore := b.bindingSlotScoped(arm.AtBinding, tagT)
			armRestores = append(armRestores, atRestore)
			b.emit(Op{Kind: OpLoadLocal, I32: scrSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
		}
		for i, name := range arm.Bindings {
			bt := ast.Type(ast.NumberType{})
			if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
				bt = arm.BindingTypes[i]
			}
			field := name
			if i < len(arm.FieldNames) && arm.FieldNames[i] != "" {
				field = arm.FieldNames[i]
			}
			off, ok := offMap[field]
			if !ok {
				return fmt.Errorf("ir: struct match field %q not in %s layout (compiler bug)", field, st.Name)
			}
			slot, restore := b.bindingSlotScoped(name, bt)
			armRestores = append(armRestores, restore)
			b.emit(Op{Kind: OpLoadLocal, I32: scrSlot})
			b.emit(Op{Kind: OpConstI32, I32: off})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
		if arm.Guard != nil {
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.emit(Op{Kind: OpNot})
			b.brTo(armEndD, true)
		}
		if err := b.stmt(arm.Body); err != nil {
			return err
		}
		for i := len(armRestores) - 1; i >= 0; i-- {
			armRestores[i]()
		}
		b.brTo(matchEndD, false)
		b.closeScope()
	}
	b.closeScope()
	return nil
}

// rangeMatchCond builds the boolean test for a range-pattern arm
// (`lo..hi` / `lo..=hi`): `scrutinee >= lo && scrutinee <op> hi`, where
// <op> is `<` for an exclusive `..` and `<=` for an inclusive `..=`. The
// comparison Binaries are stamped with the scrutinee's integer width /
// signedness (or float-ness) directly — they bypass the checker, which
// never sees these synthesised nodes. Both bound expressions were already
// checked + settled (Literal as the low bound, RangeHi as the high).
func (b *builder) rangeMatchCond(p ast.Position, lit, rangeHi ast.Expr, inclusive bool, scrSlot int32, tagT ast.Type) ast.Expr {
	cmp := func(op string, bound ast.Expr) *ast.Binary {
		c := &ast.Binary{P: p, Op: op, Left: &ast.Ident{P: p, Name: literalMatchScrName(scrSlot)}, Right: bound, IsFloat: isFloatType(tagT)}
		if nt, ok := tagT.(ast.NumberType); ok {
			c.IntWidth = nt.Width
			c.IsUnsigned = !nt.Signed
		}
		if ft, ok := tagT.(ast.FloatType); ok {
			c.FloatWidth = ft.Width
		}
		return c
	}
	hiOp := "<"
	if inclusive {
		hiOp = "<="
	}
	return &ast.Binary{P: p, Op: "&&", Left: cmp(">=", lit), Right: cmp(hiOp, rangeHi)}
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
				if err := b.emitCountedYield(arm.Body); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
				b.brTo(exitDepth, false)
				b.closeScope()
				continue
			}
			if err := b.emitCountedYield(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(exitDepth, false)
			continue
		}
		var cond ast.Expr
		if arm.RangeHi != nil {
			cond = b.rangeMatchCond(arm.P, arm.Literal, arm.RangeHi, arm.RangeInclusive, scrSlot, tagT)
		} else {
			cond = &ast.Binary{
				P:           arm.P,
				Op:          "==",
				Left:        &ast.Ident{P: arm.P, Name: literalMatchScrName(scrSlot)},
				Right:       arm.Literal,
				IsStringCmp: isStringType(tagT),
				IsFloat:     isFloatType(tagT),
			}
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
			if err := b.emitCountedYield(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(exitDepth, false)
			b.closeScope()
			b.closeScope()
			continue
		}
		b.openIf(BlockTypeVoid)
		if err := b.emitCountedYield(arm.Body); err != nil {
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

// emitStructMatchExpr is the expression-form counterpart of emitStructMatch:
// struct-pattern arms bind fields irrefutably and each arm body is an Expr
// stored into a result slot (mirroring emitLiteralMatchExpr's result handling).
func (b *builder) emitStructMatchExpr(n *ast.MatchExpr) error {
	tagT := b.exprType(n.Tag)
	st, ok := tagT.(ast.StructType)
	if !ok {
		return fmt.Errorf("ir: struct match-expr scrutinee is not a struct type (compiler bug)")
	}
	sd, ok := b.info.Structs[st.Name]
	if !ok {
		return fmt.Errorf("ir: struct match-expr on unknown struct %q", st.Name)
	}
	offMap, _ := structFieldLayout(sd.Fields, b.ptrW)
	scrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__struct_match_scr_%d", scrSlot)] = scrSlot
	if tagT != nil {
		b.scratchType[scrSlot] = tagT
	}
	if err := b.expr(n.Tag); err != nil {
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: scrSlot})

	resultType := ast.Type(ast.NumberType{})
	if n.IsFloat {
		resultType = ast.FloatType{}
	}
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
				if err := b.emitCountedYield(arm.Body); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
				b.brTo(matchEndD, false)
				b.closeScope()
				continue
			}
			if err := b.emitCountedYield(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(matchEndD, false)
			continue
		}
		b.openBlock(BlockTypeVoid)
		armEndD := b.depth
		armRestores := []func(){}
		// `@` binding: the whole matched struct (scrutinee pointer), borrowed.
		if arm.AtBinding != "" {
			atSlot, atRestore := b.bindingSlotScoped(arm.AtBinding, tagT)
			armRestores = append(armRestores, atRestore)
			b.emit(Op{Kind: OpLoadLocal, I32: scrSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
		}
		for i, name := range arm.Bindings {
			bt := ast.Type(ast.NumberType{})
			if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
				bt = arm.BindingTypes[i]
			}
			field := name
			if i < len(arm.FieldNames) && arm.FieldNames[i] != "" {
				field = arm.FieldNames[i]
			}
			off, ok := offMap[field]
			if !ok {
				return fmt.Errorf("ir: struct match-expr field %q not in %s layout (compiler bug)", field, st.Name)
			}
			slot, restore := b.bindingSlotScoped(name, bt)
			armRestores = append(armRestores, restore)
			b.emit(Op{Kind: OpLoadLocal, I32: scrSlot})
			b.emit(Op{Kind: OpConstI32, I32: off})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
		if arm.Guard != nil {
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.emit(Op{Kind: OpNot})
			b.brTo(armEndD, true)
		}
		if err := b.emitCountedYield(arm.Body); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
		for i := len(armRestores) - 1; i >= 0; i-- {
			armRestores[i]()
		}
		b.brTo(matchEndD, false)
		b.closeScope()
	}
	b.closeScope()
	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	return nil
}

// isTupleMatch / isTupleMatchExprArms report whether any arm carries a
// tuple pattern — the dispatch cue for a tuple-typed scrutinee (the
// checker guarantees the remaining arms are tuple patterns or `_`).
func isTupleMatch(arms []*ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.TupleElems != nil {
			return true
		}
	}
	return false
}

func isTupleMatchExprArms(arms []*ast.MatchExprArm) bool {
	for _, arm := range arms {
		if arm.TupleElems != nil {
			return true
		}
	}
	return false
}

// tupleMatchPrep caches the scrutinee tuple pointer in a scratch slot.
// The pointer is BORROWED (no inc, no free), mirroring the enum-match
// heap-form scrutinee; bindings extracted on the matched path are
// borrows of the box's elements the same way enum payload bindings are.
func (b *builder) tupleMatchPrep(tag ast.Expr, elems []ast.Type) (ptrSlot int32, offs []int32, elemName []string, err error) {
	ptrSlot = b.allocSlot()
	b.locals[fmt.Sprintf("__tup_match_p_%d", ptrSlot)] = ptrSlot
	if err := b.expr(tag); err != nil {
		return 0, nil, nil, err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
	offs, _ = tupleElemLayout(elems, b.ptrW)
	elemName = make([]string, len(elems))
	return ptrSlot, offs, elemName, nil
}

// tupleMatchElemIdent returns (lazily creating) a synthetic typed
// scratch local holding element k of the scrutinee, for use as the left
// side of a literal-comparison Binary — the exact emitLiteralMatch
// scrutinee-slot shape, which settles string (OpStrEq) / float compares
// uniformly, including the two-word string ABI.
func (b *builder) tupleMatchElemIdent(ptrSlot int32, offs []int32, elems []ast.Type, elemName []string, k int) string {
	if elemName[k] != "" {
		return elemName[k]
	}
	s := b.allocSlot()
	name := fmt.Sprintf("__tup_match_e_%d", s)
	b.locals[name] = s
	b.scratchType[s] = elems[k]
	elemName[k] = name
	b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
	b.emit(Op{Kind: OpConstI32, I32: offs[k]})
	b.emit(Op{Kind: OpAdd})
	b.emit(payloadLoadOpFor(elems[k], b.ptrW))
	b.emit(Op{Kind: OpStoreLocal, I32: s})
	return name
}

// tupleMatchArmCond builds the arm's literal-element condition as an
// AST expression (nil when the arm is irrefutable — all binders / `_`).
func (b *builder) tupleMatchArmCond(arm ast.Position, tupleElems []ast.TuplePatElem, ptrSlot int32, offs []int32, elems []ast.Type, elemName []string) ast.Expr {
	var cond ast.Expr
	for k, el := range tupleElems {
		if el.Literal == nil || k >= len(elems) {
			continue
		}
		name := b.tupleMatchElemIdent(ptrSlot, offs, elems, elemName, k)
		eq := &ast.Binary{
			P:           arm,
			Op:          "==",
			Left:        &ast.Ident{P: arm, Name: name},
			Right:       el.Literal,
			IsStringCmp: isStringType(elems[k]),
			IsFloat:     isFloatType(elems[k]),
		}
		if cond == nil {
			cond = eq
		} else {
			cond = &ast.Binary{P: arm, Op: "&&", Left: cond, Right: eq}
		}
	}
	return cond
}

// tupleMatchBindArm extracts the arm's binder elements from the tuple
// box into their binding slots (same load shape as the enum-match
// heap-form payload binds — borrowed, no inc). The returned restore
// undoes any cross-shape temporary name remaps (#4510, see
// bindingSlotScoped) — callers invoke it right after lowering the arm
// body.
func (b *builder) tupleMatchBindArm(tupleElems []ast.TuplePatElem, bindingTypes []ast.Type, ptrSlot int32, offs []int32) func() {
	restores := []func(){}
	for k, el := range tupleElems {
		if el.Name == "" || k >= len(offs) {
			continue
		}
		bt := ast.Type(ast.NumberType{})
		if k < len(bindingTypes) && bindingTypes[k] != nil {
			bt = bindingTypes[k]
		}
		slot, restore := b.bindingSlotScoped(el.Name, bt)
		restores = append(restores, restore)
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(Op{Kind: OpConstI32, I32: offs[k]})
		b.emit(Op{Kind: OpAdd})
		b.emit(payloadLoadOpFor(bt, b.ptrW))
		b.emit(Op{Kind: OpStoreLocal, I32: slot})
	}
	return func() {
		for i := len(restores) - 1; i >= 0; i-- {
			restores[i]()
		}
	}
}

// emitTupleMatch lowers a `match` on a tuple-typed scrutinee: cache the
// tuple pointer, then per arm test the literal elements (an if-else-if
// chain like emitLiteralMatch), bind the binder elements on the matched
// path, run the optional guard with bindings in scope, and branch out
// of the exit block.
func (b *builder) emitTupleMatch(n *ast.Match) error {
	tup, ok := b.exprType(n.Tag).(ast.TupleType)
	if !ok {
		return fmt.Errorf("ir: tuple-pattern match scrutinee is not tuple-typed (compiler bug)")
	}
	ptrSlot, offs, elemName, err := b.tupleMatchPrep(n.Tag, tup.Elems)
	if err != nil {
		return err
	}
	b.openBlock(BlockTypeVoid)
	exitDepth := b.depth
	for _, arm := range n.Arms {
		if arm.IsWildcard {
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
		cond := b.tupleMatchArmCond(arm.P, arm.TupleElems, ptrSlot, offs, tup.Elems, elemName)
		if cond != nil {
			if err := b.expr(cond); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
		}
		// Bind BEFORE the guard so the guard sees the arm's names.
		restoreBinds := b.tupleMatchBindArm(arm.TupleElems, arm.BindingTypes, ptrSlot, offs)
		// `@` binding: the whole matched tuple (scrutinee pointer), borrowed.
		restoreAt := func() {}
		if arm.AtBinding != "" {
			atSlot, r := b.bindingSlotScoped(arm.AtBinding, tup)
			restoreAt = r
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
		}
		if arm.Guard != nil {
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
		}
		if err := b.stmt(arm.Body); err != nil {
			return err
		}
		restoreAt()
		restoreBinds()
		b.brTo(exitDepth, false)
		if arm.Guard != nil {
			b.closeScope()
		}
		if cond != nil {
			b.closeScope()
		}
	}
	b.closeScope() // exit block
	return nil
}

// emitTupleMatchExpr is the expression-form counterpart of
// emitTupleMatch: same per-arm test/bind/guard shape, but each arm body
// is an Expr stored into a result slot before branching out (mirroring
// emitLiteralMatchExpr's result handling).
func (b *builder) emitTupleMatchExpr(n *ast.MatchExpr) error {
	tup, ok := b.exprType(n.Tag).(ast.TupleType)
	if !ok {
		return fmt.Errorf("ir: tuple-pattern match scrutinee is not tuple-typed (compiler bug)")
	}
	ptrSlot, offs, elemName, err := b.tupleMatchPrep(n.Tag, tup.Elems)
	if err != nil {
		return err
	}
	// Result slot — typed from the first non-polymorphic arm body,
	// mirroring emitLiteralMatchExpr.
	resultType := ast.Type(ast.NumberType{})
	for _, arm := range n.Arms {
		if arm == nil || arm.Body == nil {
			continue
		}
		t := b.exprType(arm.Body)
		if t == nil {
			continue
		}
		if nt, isNum := t.(ast.NumberType); isNum && nt.Polymorphic {
			continue
		}
		if ft, isFloat := t.(ast.FloatType); isFloat && ft.Polymorphic {
			continue
		}
		resultType = t
		break
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
				if err := b.emitCountedYield(arm.Body); err != nil {
					return err
				}
				b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
				b.brTo(exitDepth, false)
				b.closeScope()
				continue
			}
			if err := b.emitCountedYield(arm.Body); err != nil {
				return err
			}
			b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
			b.brTo(exitDepth, false)
			continue
		}
		cond := b.tupleMatchArmCond(arm.P, arm.TupleElems, ptrSlot, offs, tup.Elems, elemName)
		if cond != nil {
			if err := b.expr(cond); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
		}
		restoreBinds := b.tupleMatchBindArm(arm.TupleElems, arm.BindingTypes, ptrSlot, offs)
		// `@` binding: the whole matched tuple (scrutinee pointer), borrowed.
		restoreAt := func() {}
		if arm.AtBinding != "" {
			atSlot, r := b.bindingSlotScoped(arm.AtBinding, tup)
			restoreAt = r
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
		}
		if arm.Guard != nil {
			if err := b.expr(arm.Guard); err != nil {
				return err
			}
			b.openIf(BlockTypeVoid)
		}
		if err := b.emitCountedYield(arm.Body); err != nil {
			return err
		}
		restoreAt()
		restoreBinds()
		b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
		b.brTo(exitDepth, false)
		if arm.Guard != nil {
			b.closeScope()
		}
		if cond != nil {
			b.closeScope()
		}
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

// bindingSlot shape classes. Two same-named bindings may share a local
// slot only when the backend gives both the SAME physical slot type —
// on wasm a local is typed (i32/i64/f32/f64) and a mixed-width share
// miscompiles: the local's valtype comes from the final scratchType
// stamp, so the other arm's store/load is ill-typed and the module
// fails validation ("type mismatch: expected i64, found i32" — the
// TestExternVariant{MixedWidth,NonUniform}ResultCustomProvider shape,
// where `match (classify(n)) { I(v) => …, L(v) => … }` binds an i32 `v`
// and an i64 `v` in sibling arms).
const (
	bindingShapeOneWord = iota // i32-class: bool, i32-width ints, pointer-shaped composites, handles, fn ptrs
	bindingShapeI64
	bindingShapeF32
	bindingShapeF64
	bindingShapeTwoWord // string under the two-word ABI / inline dyn pair on wasm32
)

// bindingSlotShape classifies the physical slot type a binding of type t
// occupies, mirroring the wasm backend's valtypeFor/slotValtypes: a
// two-word logical slot (fan-out at OpLoadLocal/OpStoreLocal — a string
// under the two-word ABI, or an inline `dyn Trait` pair on wasm32),
// otherwise the scalar valtype class. Native backends use untyped 8-byte
// slots, so the scalar split is conservative there (a cross-width mix
// takes the scoped fresh-slot path instead of sharing) — but the IR is
// backend-shared, so the guard must satisfy the strictest consumer.
func bindingSlotShape(t ast.Type, ptrW int) int {
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return bindingShapeTwoWord
	}
	if _, isDyn := t.(ast.DynTraitType); isDyn && ptrW == 4 {
		return bindingShapeTwoWord
	}
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return bindingShapeI64
		}
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return bindingShapeF64
		}
		return bindingShapeF32
	}
	return bindingShapeOneWord
}

// bindingSlot returns the local slot for a match / if-let / let-else arm
// binding `name` of static type bt. When the name ALREADY has a slot — a
// `var` of the same name elsewhere in the function (pre-allocated at
// entry and covered by the zero-init safety net) or an earlier arm's
// same-named binding — the slot is REUSED instead of freshly allocated.
//
// Why reuse is load-bearing: the return / function-exit dec sweep
// resolves names through b.locals at the moment each return is LOWERED.
// A fresh binding slot permanently shadows the var's pre-allocated slot,
// so every later-lowered return sweeps the ARM's slot — which is never
// written on paths that don't enter that arm. The sweep then rc_dec's
// uninitialized stack garbage; when the leftover value happens to look
// like a heap pointer (past the null / low-address / sentinel guards) it
// decrements a random block's rc word — a layout-dependent heap
// corruption. Observed in the wild as the self-host driver miscompiling
// `match(read_file(..)) { Ok(s) => { write(s); .. } }` (a dangling
// .Lir_main_* label): irlower's alias_names_in_stmt binds its StmtAssign
// arm payload as `a`, shadowing the `var a: string[]` accumulators bound
// in sibling arms, and the wildcard arm's return swept the unwritten
// binding slot. Reusing the entry-zeroed slot makes that sweep a
// null-guarded no-op on unentered paths and keeps every same-named
// var / binding on one slot — matching the zero-init pass's "one slot
// per name" model.
//
// Slot-shape guard: two bindings may share an index only when the
// backend gives both the same physical slot type (bindingSlotShape) —
// two-word vs one-word, and on wasm the scalar valtype class too (an
// i32 arm binding sharing an i64 var's slot fails module validation).
// Mixed shapes fall back to a fresh slot; on the SCOPED variant below the
// name remap is temporary (restored when the binding's scope ends), so
// the shadowing hazard the fresh slot used to introduce (#4510 — reads,
// the entry zero-init, and later-lowered exit sweeps splitting one name
// across two slots, observed as the self-host interp's ExprTuple loop
// trapping its own arm64 bounds check) cannot outlive the arm.
func (b *builder) bindingSlot(name string, bt ast.Type) int32 {
	slot, _ := b.bindingSlotScoped(name, bt)
	return slot
}

// bindingSlotScoped is bindingSlot plus a restore closure for bindings
// whose visibility ends with their arm / Then body (match arms, if-let,
// tuple-match binds). On the same-shape reuse path and for a first-sight
// name the restore is a no-op — those mappings are deliberately
// permanent (sibling arms share the slot; the exit sweep resolves the
// name through it). On the CROSS-SHAPE fresh-slot path (#4510: a
// two-word string/dyn var vs a one-word binding, arm64/wasm) the remap
// is temporary: callers invoke restore right after lowering the arm
// body, reinstating the shadowed slot so everything lowered afterwards
// — reads of the outer name, the exit dec sweep at later returns —
// resolves the var's own (entry-zeroed) slot again. Let-else bindings
// outlive their statement and keep the plain bindingSlot (permanent
// remap IS their semantics).
func (b *builder) bindingSlotScoped(name string, bt ast.Type) (int32, func()) {
	if slot, ok := b.locals[name]; ok {
		if bindingSlotShape(b.slotShapeType(slot), b.ptrW) == bindingSlotShape(bt, b.ptrW) {
			b.scratchType[slot] = bt
			return slot, func() {}
		}
		prev := slot
		fresh := b.allocSlot()
		b.locals[name] = fresh
		b.scratchType[fresh] = bt
		return fresh, func() { b.locals[name] = prev }
	}
	slot := b.allocSlot()
	b.locals[name] = slot
	b.scratchType[slot] = bt
	return slot, func() {}
}

// slotShapeType returns the type that sizes `slot`'s physical storage in
// the backend: the declared param / info.Locals type for slots in those
// ranges, the scratchType stamp for scratch-range slots. bindingSlot's
// two-word shape guard MUST compare against this — lowerFunc's entry
// loops never stamp scratchType for param / var slots, so reading
// b.scratchType directly returns nil there, which reads as single-word
// and let a pointer-shaped match binding reuse a same-named `var`'s
// TWO-WORD string slot (wasm ptrW==4 + arm64 TwoWordOverride). The
// backend then fanned every OpLoadLocal / OpStoreLocal of the binding
// into two words while the IR balanced for one — an operand-stack
// underflow that read garbage into the binding (observed as the
// self-host interp's `parser.ExprTuple(t)` arm trapping its own bounds
// check on arm64 once a sibling arm gained `var t: string`, #4497).
func (b *builder) slotShapeType(slot int32) ast.Type {
	if int(slot) < len(b.fn.Params) {
		return b.fn.Params[slot].Type
	}
	locals := b.info.Locals[b.fn]
	if idx := int(slot) - len(b.fn.Params); idx < len(locals) {
		return locals[idx].Type
	}
	return b.scratchType[slot]
}

func (b *builder) stmt(s ast.Stmt) error {
	b.curPos = s.Pos()
	// Under native -g, mark each new source line so the backend can build a
	// DWARF .debug_line row. OpLine consumes/produces nothing and emits no
	// machine code; it just carries the Pos.
	if b.emitLineMarkers && b.curPos.Line > 0 && b.curPos.Line != b.lastLineMark {
		b.lastLineMark = b.curPos.Line
		b.emit(Op{Kind: OpLine, Pos: b.curPos})
	}
	switch n := s.(type) {
	case *ast.Block:
		for _, ss := range n.Stmts {
			if err := b.stmt(ss); err != nil {
				return err
			}
			// Precise drops for locals declared in THIS block, released at
			// their last use inside it rather than at the exit sweep
			// (computeNestedDrops) — the nested-scope sibling of the
			// top-level splice in lowerFunc.
			for _, name := range b.rc.nestedDrops[ss] {
				b.emitPreciseDrop(name)
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
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			bindingSlots[i] = b.bindingSlot(name, bt)
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
			pairRestores := []func(){}
			for i, name := range n.Bindings {
				bt := ast.Type(ast.NumberType{})
				if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
					bt = n.BindingTypes[i]
				}
				slot, restore := b.bindingSlotScoped(name, bt)
				pairRestores = append(pairRestores, restore)
				b.emit(Op{Kind: OpLoadLocal, I32: payloadSlot})
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
			}
			if err := b.stmt(n.Then); err != nil {
				return err
			}
			// Bindings are scoped to Then — undo any cross-shape
			// temporary remaps (#4510) before Else / following code.
			for i := len(pairRestores) - 1; i >= 0; i-- {
				pairRestores[i]()
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
		ifletRestores := []func(){}
		for i, name := range n.Bindings {
			bt := ast.Type(ast.NumberType{})
			if i < len(n.BindingTypes) && n.BindingTypes[i] != nil {
				bt = n.BindingTypes[i]
			}
			slot, restore := b.bindingSlotScoped(name, bt)
			ifletRestores = append(ifletRestores, restore)
			b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
			b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}
		if err := b.stmt(n.Then); err != nil {
			return err
		}
		// Bindings are scoped to Then — undo any cross-shape temporary
		// remaps (#4510) before the Else branch / following code lowers.
		for i := len(ifletRestores) - 1; i >= 0; i-- {
			ifletRestores[i]()
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
		b.loopFrames = append(b.loopFrames, loopFrame{label: n.Label, breakD: breakD, contD: loopD})
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.loopFrames = b.loopFrames[:len(b.loopFrames)-1]
		b.breakStack = b.breakStack[:len(b.breakStack)-1]
		b.contStack = b.contStack[:len(b.contStack)-1]
		b.brTo(loopD, false) // unconditional back-edge
		b.closeScope()       // close loop
		b.closeScope()       // close break-block
	case *ast.Loop:
		// Same `block`/`loop` shape as While, minus the cond check —
		// `loop { … }` is unconditional by construction, so there is
		// no br_if exit to emit; only an explicit `break` inside the
		// body reaches the outer block.
		b.openBlock(BlockTypeVoid)
		breakD := b.depth
		b.openLoop(BlockTypeVoid)
		loopD := b.depth
		b.breakStack = append(b.breakStack, breakD)
		b.contStack = append(b.contStack, loopD)
		b.loopFrames = append(b.loopFrames, loopFrame{label: n.Label, breakD: breakD, contD: loopD})
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.loopFrames = b.loopFrames[:len(b.loopFrames)-1]
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
		b.loopFrames = append(b.loopFrames, loopFrame{label: n.Label, breakD: breakD, contD: contD})
		if err := b.stmt(n.Body); err != nil {
			return err
		}
		b.loopFrames = b.loopFrames[:len(b.loopFrames)-1]
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
		if n.Label != "" {
			fr := b.findLoopFrame(n.Label)
			if fr == nil {
				return fmt.Errorf("ir: break label %q not found (compiler bug — should be checker-rejected)", n.Label)
			}
			b.brTo(fr.breakD, false)
			break
		}
		if len(b.breakStack) == 0 {
			return fmt.Errorf("ir: break outside of a loop (compiler bug — should be checker-rejected)")
		}
		b.brTo(b.breakStack[len(b.breakStack)-1], false)
	case *ast.Continue:
		if n.Label != "" {
			fr := b.findLoopFrame(n.Label)
			if fr == nil {
				return fmt.Errorf("ir: continue label %q not found (compiler bug — should be checker-rejected)", n.Label)
			}
			b.brTo(fr.contD, false)
			break
		}
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
			// errdefer: a `return` is an error exit only when the
			// returned value is the failure variant (None / Err,
			// tag 1) of an Option/Result. Functions with an
			// errdefer are kept out of the pair-form ABI (see the
			// pair-form eligibility check), so the stashed value is
			// always a heap box with its tag at offset 0 — read it
			// and replay the errdefers under that branch. Gated on
			// hasErrDefers so non-errdefer functions are unchanged.
			if b.hasErrDefers() && isOptionOrResultType(b.fn.ReturnType) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpLoad})             // tag @ box+0
				b.emit(Op{Kind: OpConstI32, I32: 1}) // failure variant idx
				b.emit(Op{Kind: OpEq})
				b.openIf(BlockTypeVoid)
				if err := b.emitErrDeferCleanup(); err != nil {
					return err
				}
				b.closeScope()
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
		// Move-on-return for a swept `dyn Trait` local (#4351): the exit
		// sweep's DynTraitType arm drops unconditionally — __drop_dyn_<set>
		// runs the concrete dtor and, on the natives, frees the {data,vtable}
		// cell outright; there is no rc header to net against a transfer inc.
		// Returning a bare dyn local MOVES the value to the caller, so
		// sweeping it here handed back a freed cell — the caller's next
		// dispatch read reclaimed memory (segfault on the natives, a garbage
		// dispatch on wasm). Exclude it from the sweep; no inc is emitted
		// (dyn values are not rc-counted), so this is a pure move. dyn locals
		// sit outside isOwnedRcLocal / needsRcIncOnAlias (dyn cells must
		// never see __fern_rc_inc — they carry no header), hence the
		// dedicated branch rather than widening those predicates.
		if id, ok := n.Value.(*ast.Ident); ok && len(b.defers) == 0 &&
			b.dynReclaim() && b.localIsDynTrait(id.Name) {
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
		// `return f(…, p, …)` handing an `own` param straight on to another
		// `own` parameter: the callee took the reference, so this return's
		// sweep must not also release it. Per-site, so the OTHER returns keep
		// sweeping p — which is what lets the transfer be claimed at all on a
		// branchy function (computeReturnOwnMoves, #6125).
		b.emitRcDecLocalsAtExitExcept(b.rc.returnOwnMove[n])
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
		// Dead-alias cancellation (#4402 opt 1): a proven borrowed-view
		// alias skips the transfer inc — its exit-sweep dec is equally
		// elided (emitRcDecLocalsAtExitExcept), a net-zero pair.
		if needsRcIncOnAlias(n.Init, b) && !b.rc.moveSites[n] && !b.rc.borrowedAliasSites[n] {
			b.emitAliasInc(n.Init)
		}
		// Phase 5h: release the slot's previous value before this
		// (re-)init store. For a loop-body `var` this reclaims the prior
		// iteration's allocation instead of leaking it; for a once-run
		// `var` the zero-init makes it a NULL-guarded no-op. The new value
		// is on the stack underneath — emitVarReinitDropOld is net-zero —
		// so it survives for the store below.
		//
		// A borrowed-view alias site (#4402 opt 1) skips this too: the
		// slot only ever holds the (un-inc'd) borrowed pointer, so a
		// loop-repeated decl would otherwise dec the source's buffer once
		// per iteration with no matching inc.
		if !b.rc.borrowedAliasSites[n] {
			b.emitVarReinitDropOld(n.Name, idx)
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
		//
		// Phase 4 move-on-destructure: when Init is an owned rc tuple
		// local at its last use (b.rc.moveSites[n] set in
		// computeMovedLocals), the alias inc and the source's exit-sweep
		// dec cancel — move the source into the temp instead.
		if needsRcIncOnAlias(n.Init, b) && !b.rc.moveSites[n] {
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
		// Recover the per-name element types + offsets. Tuple mode
		// reads them off the synthetic temp's tuple type; struct mode
		// (n.Fields set) reads the field offset off the struct layout
		// and the concrete element type off the checker-registered
		// binding local. Both feed the identical per-name load loop.
		var elemTypes []ast.Type
		var offs []int32
		if n.Fields != nil {
			var stName string
			for _, v := range b.info.Locals[b.fn] {
				if v.Name == n.TempName {
					if t, ok := v.Type.(ast.StructType); ok {
						stName = t.Name
					}
					break
				}
			}
			sd, ok := b.info.Structs[stName]
			if !ok {
				return fmt.Errorf("ir: struct destructure temp %q has unknown struct type (compiler bug)", n.TempName)
			}
			offMap, _ := structFieldLayout(sd.Fields, b.ptrW)
			for i, fname := range n.Fields {
				off, ok := offMap[fname]
				if !ok {
					return fmt.Errorf("ir: struct destructure field %q not in layout (compiler bug)", fname)
				}
				offs = append(offs, off)
				var et ast.Type
				for _, v := range b.info.Locals[b.fn] {
					if v.Name == n.Names[i] {
						et = v.Type
						break
					}
				}
				elemTypes = append(elemTypes, et)
			}
		} else {
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
			offs, _ = tupleElemLayout(tup.Elems, b.ptrW)
			elemTypes = tup.Elems
		}
		for i, name := range n.Names {
			nameIdx, ok := b.locals[name]
			if !ok {
				return fmt.Errorf("ir: destructure name %q has no slot (compiler bug)", name)
			}
			b.emit(Op{Kind: OpLoadLocal, I32: tempIdx})
			b.emit(Op{Kind: OpConstI32, I32: offs[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(elemTypes[i], b.ptrW))
			// Dup-on-projection: a pointer-shaped element is extracted by
			// reference (the load copies the box's stored pointer without
			// an inc). The binding now co-owns it alongside the tuple box,
			// so bump the rc — the binding (an owned, untainted rc local)
			// will dec/free it at scope exit, balanced by the tuple's
			// deep-drop dec of the same element. Without the dup the
			// binding and the tuple's drop would both release one
			// reference for a single count (double free / underflow).
			if _, isStr := elemTypes[i].(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
				// Two-word string element (wasm + arm64-TwoWordOverride):
				// dup via __fern_str_inc (consumes + re-pushes the
				// (data, len) pair) so the binding co-owns the buffer
				// alongside the tuple box. Without it the tuple's deep-
				// drop __fern_str_dec would free the buffer under the
				// still-live binding (UAF).
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 2})
			} else if _, isStr := elemTypes[i].(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				// Native single-word string element: dup via __fern_rc_inc
				// so the binding co-owns the buffer alongside the tuple
				// box's later dec.
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
			} else if arrElemIsRcTracked(elemTypes[i]) {
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
	case *ast.Match:
		// Tuple-pattern match: the scrutinee is a tuple (the checker
		// dispatched to `checkTupleMatch`). Lower via emitTupleMatch —
		// per-arm element eq-tests + binder extraction.
		if isTupleMatch(n.Arms) {
			if err := b.emitTupleMatch(n); err != nil {
				return err
			}
			break
		}
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
		// Struct-pattern match: the scrutinee is a struct (the checker
		// stamped n.StructMatch and dispatched to `checkStructMatch`). A
		// struct-pattern arm binds fields irrefutably, so lower as an
		// if-chain — bind fields, run the (optional) guard, fall through
		// to the next arm on a guard-false, run the body on a match.
		if n.StructMatch != "" {
			if err := b.emitStructMatch(n); err != nil {
				return err
			}
			break
		}
		// Lower a `match` to: store the scrutinee pointer, load
		// its tag once, then for each arm test `tag == k` and
		// branch in. On match, the arm body runs with payload
		// fields loaded into freshly-bound locals; we then break
		// out of the whole match.
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
		// An `@` binding needs the whole scrutinee box, so it forces the
		// heap-form path (the pair-form fast path splits the value into a
		// (tag, payload) pair with no single box pointer to bind).
		pairFormScrutinee := b.isPairFormScrutinee(n.Tag) && !anyArmAtBinding(n.Arms)
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
		// Consuming owned match (#4400): the scrutinee is an OWNED-BY-DEFAULT
		// enum parameter at its last use. The arms release the box per arm via
		// the drop-specialized emitOwnedConsumingArmDrop (unique → shallow
		// free, payload counts transfer to the bindings; shared → dup the
		// counted bindings + flat dec) and zero the param slot so the exit
		// sweep no-ops. Disjoint from the `own`-param path above (that one
		// MOVES payloads, E051-guarded); guarded / wildcard arms skip the
		// release and leave the box to the exit sweep.
		consumeOwnedName := ""
		if !pairFormScrutinee {
			consumeEnum, consumeScrut = b.ownParamEnumScrutinee(n.Tag)
		}
		if !pairFormScrutinee && !consumeScrut {
			// computeConsumingOwnedMatches never admits an `own` param, so the
			// two consuming paths are disjoint by construction.
			if name, isConsuming := b.rc.consumingOwnedMatches[n]; isConsuming {
				if et, isEnum := b.exprStaticType(n.Tag).(ast.EnumType); isEnum {
					consumeEnum = et
					consumeOwnedName = name
				}
			}
		}
		if !pairFormScrutinee && !consumeScrut && consumeOwnedName == "" {
			bts := make([][]ast.Type, 0, len(n.Arms))
			for _, arm := range n.Arms {
				bts = append(bts, arm.BindingTypes)
			}
			scrutEnum, reclaimScrut = b.reclaimableMatchScrutinee(n.Tag, bts, nil)
		}
		b.openBlock(BlockTypeVoid)
		matchEndD := b.depth
		// NB: matchEndD is NOT pushed onto b.breakStack. A `match` is
		// not a `break` target — a user `break` inside an arm must
		// exit the enclosing loop, matching the interpreter and the
		// checker (which rejects `break` whose only enclosing
		// construct is a match). The arms reach the
		// match end via the explicit `brTo(matchEndD, …)` calls below,
		// not through the break stack; pushing it here would shadow
		// the loop's break target and turn `break` into a no-op that
		// only falls out of the match (an infinite loop for
		// `while (true) { match … { … => break } }`).
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
			armRestores := []func(){}
			for i, name := range arm.Bindings {
				bt := ast.Type(ast.NumberType{})
				if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
					bt = arm.BindingTypes[i]
				}
				slot, restore := b.bindingSlotScoped(name, bt)
				armRestores = append(armRestores, restore)
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
			// `@` binding: bind the whole matched value (the scrutinee box
			// pointer) to the at-name. Pair-form is disabled when any arm has
			// an `@` binding (the pairFormScrutinee gate), so ptrSlot always
			// holds the box here. Borrowed, like the payload binds.
			if arm.AtBinding != "" {
				atSlot, atRestore := b.bindingSlotScoped(arm.AtBinding, b.exprType(n.Tag))
				armRestores = append(armRestores, atRestore)
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
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
				// zero-alloc FBIP). The pairing is DECIDED at analysis time now
				// (computeConsumingMatchReuse fills rc.reuseSources +
				// rc.consumingMatchReuse); an unregistered arm frees the box (C1).
				if reuseCtor := b.consumingReuseCtor(arm, consumeEnum); reuseCtor == nil || !b.rc.consumingMatchReuse[reuseCtor] {
					b.emitConsumingMatchBoxFree(ptrSlot, consumeEnum)
				}
			}
			// Consuming owned match (#4400): with the bindings copied into
			// their slots, release the scrutinee box here — the Koka
			// drop-specialization. Unique: shallow box free (the counted
			// payload references transfer to the bindings — dup/dec pairs
			// cancelled statically). Shared: dup each qualifying pointer
			// binding (it becomes a second counted owner) and flat-dec the
			// box. Then zero the PARAM slot so the exit sweep's deep-drop
			// no-ops; guarded arms skip (no release on a fall-through path)
			// and leave the box to the exit sweep.
			if consumeOwnedName != "" && !pairFormScrutinee && arm.Guard == nil {
				var dupSlots []int32
				for _, bname := range arm.Bindings {
					if _, owned := b.rc.consumingBindings[bname]; !owned {
						continue
					}
					if slot, ok := b.locals[bname]; ok {
						dupSlots = append(dupSlots, slot)
					}
				}
				b.emitOwnedConsumingArmDrop(ptrSlot, consumeEnum, dupSlots, consumeOwnedName)
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
			// SAME-shape bindings stay in b.locals after the arm
			// finishes: the IR only cares about slot indices
			// (already stamped into emitted ops), and
			// `scratchCount` at the end of lowerFunc must reflect
			// every slot we ever wrote. Two arms with overlapping
			// binding names won't clash at runtime — at most one
			// arm body runs per match. CROSS-shape bindings undo
			// their temporary remap here (#4510) so the outer
			// name's own slot is visible again to everything
			// lowered after this arm.
			for i := len(armRestores) - 1; i >= 0; i-- {
				armRestores[i]()
			}
			b.brTo(matchEndD, false) // jump past remaining arms
			b.closeScope()           // end outer per-arm block
		}
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
	// `dyn Trait` coercion (boxing): when this expression is a recorded
	// concrete→`dyn Trait` site, lower the concrete value (one word =
	// data), then push the vtable address. The dynCoerceDone guard
	// prevents the recursive lower of the same expression from
	// re-triggering. Representation (docs/DYN-TRAITS.md §4.2):
	//   - wasm (inline two-word): leave `[data, vtable]` on the stack —
	//     the slot itself holds the fat pointer (§4.2.1).
	//   - natives (boxed one-word): emit OpBoxDyn to pop `[data,
	//     vtable]`, allocate a `{data, vtable}` heap cell, and push the
	//     single cell pointer (§4.2.2).
	if b.info != nil && b.info.DynCoercions != nil && b.dynCoerceDone != nil && !b.dynCoerceDone[e] {
		if dc, ok := b.info.DynCoercions[e]; ok {
			b.dynCoerceDone[e] = true
			if err := b.expr(e); err != nil {
				return err
			}
			// Uniform primitive boxing (docs/DYN-TRAITS.md §4.2.3): a
			// struct/enum concrete's value is already a heap pointer, so it
			// is the `data` word directly. A primitive/string concrete's
			// value is not pointer-shaped (and may be wider than one slot —
			// i64/f64, or a two-word string), so heap-box it into a value
			// cell: `data` becomes a one-word pointer to the cell, and the
			// vtable slots point at unboxing wrappers that reload it.
			if isPrimitiveConcrete(b.info, dc.Concrete) {
				if err := b.boxPrimitiveDynValue(dc.Concrete); err != nil {
					return err
				}
			}
			// Key the vtable by the whole trait SET: for a single-trait
			// coercion dynVtableSetKey(dc.Traits) == dc.Trait, so this is
			// byte-identical to before; for a multi-trait `dyn A + B`
			// coercion it selects the MERGED (concatenated) vtable
			// collectVtables emitted for the set (docs/DYN-TRAITS.md §10).
			b.emit(Op{Kind: OpConstVtable, Str: dynVtableSetKey(dc.Traits), Ext: &OpExt{Str2: dc.Concrete}})
			if b.dynBoxed() {
				b.emit(Op{Kind: OpBoxDyn})
			}
			return nil
		}
	}
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
	case *ast.DowncastExpr:
		return b.emitDowncast(n)
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
				case dw == 8:
					// dw==8 is always u8 now (i8 was removed) —
					// zero-extend/mask.
					b.emit(Op{Kind: OpConstI32, I32: 0xFF})
					b.emit(Op{Kind: OpAnd})
					// dw == 32: the 32-bit pattern is the value.
				}
			case sw <= 32 && dw == 64:
				// Sub-i32 (u8) → i64 first widens to i32, then
				// extends to i64. u8 is always unsigned, so no
				// sign-extend step is needed for a sub-i32 source;
				// the mask was already applied at the producing
				// store.
				if srcInt.IsSigned() {
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
			if dstInt.IsPointerWidth() {
				realW = b.ptrW * 8 // resolve usize to target's ptr width
			}
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
			case realW == 8:
				// Always u8 now (i8 was removed) — zero-extend/mask.
				b.emit(Op{Kind: OpConstI32, I32: 0xFF})
				b.emit(Op{Kind: OpAnd})
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
					// data_ptr at offset 0. It's a full
					// pointer-width field (8 bytes native /
					// 4 wasm32), so load at WidthPtr; a plain
					// i32 load would truncate a high pointer.
					b.emit(Op{Kind: OpLoad, Width: WidthPtr})
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
	case *ast.UnitLit:
		// The unit value is a constant, not an absence: it occupies a
		// slot so an enum payload holding it loads and stores like any
		// other single-word value.
		b.emit(Op{Kind: OpConstI32, I32: 0})
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
		// Counted yields (#4399 sink 4): see emitCountedYield. Slice
		// yields stay uncounted views and keep their escape taint.
		if err := b.emitCountedYield(n.Then); err != nil {
			return err
		}
		b.elseBranch()
		if err := b.emitCountedYield(n.Else); err != nil {
			return err
		}
		b.closeScope()
	case *ast.MatchExpr:
		// Tuple-pattern match-expr: scrutinee is a tuple — see
		// emitTupleMatchExpr.
		if isTupleMatchExprArms(n.Arms) {
			if err := b.emitTupleMatchExpr(n); err != nil {
				return err
			}
			return nil
		}
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
		// Struct-pattern match-expr: scrutinee is a struct (checker
		// stamped n.StructMatch) — see emitStructMatchExpr.
		if n.StructMatch != "" {
			if err := b.emitStructMatchExpr(n); err != nil {
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
					if err := b.emitCountedYield(arm.Body); err != nil {
						return err
					}
					b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
					b.brTo(matchEndD, false)
					b.closeScope()
					continue
				}
				if err := b.emitCountedYield(arm.Body); err != nil {
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
			exprArmRestores := []func(){}
			for i, name := range arm.Bindings {
				bt := ast.Type(ast.NumberType{})
				if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil {
					bt = arm.BindingTypes[i]
				}
				slot, restore := b.bindingSlotScoped(name, bt)
				exprArmRestores = append(exprArmRestores, restore)
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpConstI32, I32: offsets[i]})
				b.emit(Op{Kind: OpAdd})
				b.emit(payloadLoadOpFor(bt, b.ptrW))
				b.emit(Op{Kind: OpStoreLocal, I32: slot})
			}
			// `@` binding: bind the whole matched value (scrutinee box pointer).
			if arm.AtBinding != "" {
				atSlot, atRestore := b.bindingSlotScoped(arm.AtBinding, b.exprType(n.Tag))
				exprArmRestores = append(exprArmRestores, atRestore)
				b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
				b.emit(Op{Kind: OpStoreLocal, I32: atSlot})
			}
			if arm.Guard != nil {
				if err := b.expr(arm.Guard); err != nil {
					return err
				}
				b.emit(Op{Kind: OpNot})
				b.brTo(outerArmD, true)
			}
			if err := b.emitCountedYield(arm.Body); err != nil {
				return err
			}
			// Undo cross-shape temporary remaps (#4510) once the arm
			// body is lowered; same-shape mappings persist as before.
			for i := len(exprArmRestores) - 1; i >= 0; i-- {
				exprArmRestores[i]()
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
		// Reclaim a FRESH owned source box once the `?` consumes it — the
		// try-operator sibling of the match-scrutinee reclaim. Two disjoint
		// fresh-box shapes (see reclaimableTryScrutinee / tryPairReboxSize):
		// a heap-form owned call result, and a pair-form callee's result
		// reboxed by emitRepackPairAsHeapBox (the TryOp site does not
		// suppress the rebox, so each evaluation allocated a fresh rc=1 box
		// that was never dec'd — one leaked box per `?`). Freed at each edge
		// where the box provably dies: the success path after the payload
		// load, the Option failure path (the propagated None is a fresh
		// sentinel, the source box is dead), and a pair-form enclosing
		// Result failure path after the (tag, payload) copy-out. A heap-form
		// enclosing Result failure FORWARDS the box, so it is never freed
		// there.
		tryEnum, reclaimTry := b.reclaimableTryScrutinee(n)
		var tryReboxSize int32
		tryReboxFresh := false
		if !reclaimTry {
			tryReboxSize, tryReboxFresh = b.tryPairReboxSize(n.Inner)
		}
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
		// `?` always leaves via the error path (None/Err propagation),
		// so this is exactly where `errdefer` rollbacks fire.
		if err := b.emitErrDeferCleanup(); err != nil {
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
			// The failure box is dead here (a fresh None replaces it). A
			// heap-form fresh inner's None is a static sentinel (no box, no
			// leak); a PAIR-FORM inner's rebox is a real rc=1 heap box that
			// leaked one allocation per failed `?` — free it.
			if tryReboxFresh {
				b.emitTryBoxFreeSized(ptrSlot, tryReboxSize)
			}
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
				// The (tag, payload) pair is copied out for OpReturnPair, so
				// a FRESH source box dies here rather than being forwarded —
				// free it (tag==1 proven: Err-variant size for the heap-form
				// shape, repack size for the pair rebox). An Err pointer
				// payload's reference MOVES into the returned pair, so the
				// shallow free dangles nothing.
				if reclaimTry {
					b.emitTryBoxFreeVariant(ptrSlot, tryEnum, 1)
				} else if tryReboxFresh {
					b.emitTryBoxFreeSized(ptrSlot, tryReboxSize)
				}
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
					b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
		// Success edge of the fresh-source-box reclaim (see the gate above
		// the inner lowering): the payload was extracted, the box is dead.
		// tag==0 is proven here, so the heap-form shape frees with the
		// SUCCESS variant's exact size; the pair-form shape frees the rebox
		// with the repack's size. The extracted payload sits on the operand
		// stack beneath the net-zero free sequence, untouched — a scalar was
		// copied out, a pointer payload's reference MOVED to the consumer.
		if reclaimTry {
			b.emitTryBoxFreeVariant(ptrSlot, tryEnum, 0)
		} else if tryReboxFresh {
			b.emitTryBoxFreeSized(ptrSlot, tryReboxSize)
		}
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
		// in place. The slice index path is excluded (n.IsSlice); the STRING
		// path takes the string-shaped stash below instead — its element is
		// always a u8, so the same "the loaded scalar can't alias the buffer"
		// argument applies.
		strIdxSlot := int32(-1)
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
		if n.IsString {
			var err error
			if strIdxSlot, err = b.stashOwnedStringOperand(n.Array); err != nil {
				return err
			}
		} else if idxContainerSlot < 0 {
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
					loadOp = OpLoadByte
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
			// On wasm (ptrW==4) `dyn Trait` elements are inline two-word
			// `[data, vtable]` fat pointers (docs/DYN-TRAITS.md §4.2.1):
			// load both words via the WidthString fan-out, the same as a
			// two-word string. (IsPointerType is true for dyn, so this
			// must override the WidthPtr branch above.) On natives
			// (ptrW==8) a `dyn` element is a boxed one-word pointer
			// (§4.2.2) — the WidthPtr branch above already handles it.
			if _, isDyn := elemType.(ast.DynTraitType); isDyn && b.ptrW == 4 {
				loadWidth = WidthString
			}
		}
		if n.IsString {
			b.emit(Op{Kind: OpCallDirect, Str: "__str_idx", I32: 2})
			b.emit(Op{Kind: OpLoadByte})
		} else if n.IsSlice {
			// Slice-index variants per stride: __slice_idx_1
			// for byte slices, __slice_idx (= 4) for the
			// historical layout, __slice_idx_8 for i64/f64.
			sliceHelper := "__slice_idx"
			switch stride {
			case 1:
				sliceHelper = "__slice_idx_1"
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
			// __arr_idx_8 for i64/f64. String indexing routes
			// through __str_idx (above); the byte-array case
			// is plain `base + i`.
			helper := "__arr_idx"
			switch stride {
			case 1:
				helper = "__arr_idx_1"
			case 8:
				helper = "__arr_idx_8"
			case 16:
				helper = "__arr_idx_16"
			}
			// Bounds-check elision (#4380 lever 3): a caller that has
			// statically proven the index in range (currently the
			// ForEach desugar's synthetic `iter[idx]`) sets n.Unchecked,
			// routing to the `_nc` ("no check") helper variant — the same
			// address compute minus the len-load + compare + trap. Only
			// the array path honours it; string/slice keep their checks.
			if n.Unchecked {
				helper = helper + "_nc"
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
		b.decStashedStringTemps(strIdxSlot)
	case *ast.SliceExpr:
		// String slicing: copy into a fresh length-prefixed
		// string. Owns its bytes (matches the rest of the
		// language's string semantics — no separate view type
		// for strings yet). Bounds-check happens inside the
		// helper.
		if n.IsString {
			// __str_slice copies bytes OUT of its source and leaves that
			// buffer alone, so an owned-temp source (`f(x)[a:b]`) is
			// reclaimed by nobody unless stashed — one leaked buffer per
			// slice. The slot doubles as the default-`high` length read,
			// which otherwise re-evaluates Source (calling `f` twice).
			slSrc, err := b.stashOwnedStringOperand(n.Source)
			if err != nil {
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
				if slSrc >= 0 {
					b.emit(Op{Kind: OpLoadLocal, I32: slSrc})
				} else if err := b.expr(n.Source); err != nil {
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
			b.emit(Op{Kind: OpCallDirect, Str: "__str_slice", I32: 3, Ext: &OpExt{ArgTypes: []ast.Type{ast.StringType{}, ast.NumberType{}, ast.NumberType{}}}})
			b.decStashedStringTemps(slSrc)
			break
		}
		// Lower `arr[low:high]` to:
		//   src      = source (evaluated once)
		//   len'     = __slice_range(low or 0, high or len(src), len(src))
		//   data_ptr = (src or *src) + low * stride
		//   slice    = __slice_make(data_ptr, len')
		// Both bounds default lazily — `low` falls back to 0,
		// `high` falls back to len(source). `__slice_range` is the
		// construction-time bounds check (#5419): it traps (exit
		// 134) unless 0 <= low <= high <= len(src) and returns
		// high - low. Without it an oversized `high` materialised a
		// view past the source, and the access-time `__slice_idx`
		// check — which compares against the slice's own len —
		// happily read out of bounds. (The parser reserves the
		// bare `a[:]` form, so at least one bound is present.)
		if err := b.expr(n.Source); err != nil {
			return err
		}
		srcSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_slice_src_%d", srcSlot)] = srcSlot
		b.emit(Op{Kind: OpStoreLocal, I32: srcSlot})

		// Source length: a slice carries its len at header + ptrW
		// (after the pointer-width data field); an owned array at
		// the standard data_ptr - 4 prefix.
		b.emit(Op{Kind: OpLoadLocal, I32: srcSlot})
		if n.SourceIsSlice {
			b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
			b.emit(Op{Kind: OpAdd})
			b.emit(Op{Kind: OpLoad})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: 4})
			b.emit(Op{Kind: OpSub})
			b.emit(Op{Kind: OpLoad})
		}
		srcLenSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_slice_srclen_%d", srcLenSlot)] = srcLenSlot
		b.emit(Op{Kind: OpStoreLocal, I32: srcLenSlot})

		loSlot := int32(-1)
		if n.Low != nil {
			if err := b.expr(n.Low); err != nil {
				return err
			}
			loSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__sl_slice_lo_%d", loSlot)] = loSlot
			b.emit(Op{Kind: OpStoreLocal, I32: loSlot})
		}

		// len' = __slice_range(lo, hi, srcLen) — checked.
		lenSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__sl_slice_len_%d", lenSlot)] = lenSlot
		if loSlot >= 0 {
			b.emit(Op{Kind: OpLoadLocal, I32: loSlot})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: 0})
		}
		if n.High != nil {
			if err := b.expr(n.High); err != nil {
				return err
			}
		} else {
			b.emit(Op{Kind: OpLoadLocal, I32: srcLenSlot})
		}
		b.emit(Op{Kind: OpLoadLocal, I32: srcLenSlot})
		b.emit(Op{Kind: OpCallDirect, Str: "__slice_range", I32: 3})
		b.emit(Op{Kind: OpStoreLocal, I32: lenSlot})

		// data_ptr = source data + low * stride. For sub-slicing,
		// dereference first: data_ptr lives at slice + 0. It's a
		// full pointer-width field (8 bytes on native, 4 on
		// wasm32), so load at WidthPtr — a plain i32 load would
		// truncate a high .rodata / heap pointer (the
		// as_bytes-in-.so / arm64-darwin bug). Stride defaults to 4
		// for the historical i32 layout but drops to 1 / 2 / 8 for
		// byte / halfword / wide-element slices per
		// ast.ElemSizeBytes; skip the multiply when stride == 1.
		stride := int32(4)
		if n.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(n.ElemType, b.ptrW))
		}
		b.emit(Op{Kind: OpLoadLocal, I32: srcSlot})
		if n.SourceIsSlice {
			b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		}
		if loSlot >= 0 {
			b.emit(Op{Kind: OpLoadLocal, I32: loSlot})
			if stride != 1 {
				b.emit(Op{Kind: OpConstI32, I32: stride})
				b.emit(Op{Kind: OpMul})
			}
			b.emit(Op{Kind: OpAdd})
		}
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
			// b.rc.moveSites[el]).
			if needsRcIncOnAlias(el, b) && !b.rc.moveSites[el] {
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
			b.emitMapCall("__method_Map_set", 3, n.KeyType)
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
		if dName, paired := b.rc.reuseSources[n]; paired {
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
			// (markConstructionMoves sets b.rc.moveSites[elem]).
			if needsRcIncOnAlias(elem, b) && !b.rc.moveSites[elem] {
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
		if dName, paired := b.rc.reuseSources[n]; paired && updBaseSlot < 0 {
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
						b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
			// owned rc local at its last use (b.rc.moveSites set by
			// markConstructionMoves), skip the inc — the local's
			// reference is moved into the field and its exit-sweep dec is
			// skipped to match.
			if needsRcIncOnAlias(f.Value, b) && !b.rc.moveSites[f.Value] {
				b.emitAliasInc(f.Value)
			}
			// Issue #2763: a Map-typed field initialised by a COW mutator
			// result (`Struct { m: s.m.insert(...) }`) may be the SAME handle
			// the borrowed source still owns — the COW mutates in place at
			// rc<=1. needsRcIncOnAlias is false for a Call, so the value is
			// stored as a move with no retain; the new container would then
			// alias the source's buffer and a later drop of either frees it
			// out from under the other (use-after-free → segfault). Clone the
			// map so the container owns an independent buffer. A fresh
			// `map_new()` result is NOT a mutator call, so it still moves in
			// (no needless copy / no leak). The fast ownership-flow-aware
			// inc-only path is the Perceus port's job (roadmap goal 2).
			//
			// Issue #4871: the same aliasing arises one `var` removed —
			// `var m = s.m.insert(...); Struct { m: m }` — where the field value
			// is a plain ident, not a direct call, so isMapMutatorCall misses it.
			// borrowedMapFieldResults flags such a local (mutator with a
			// field-access receiver); clone it too, but only when it is MOVED
			// into the field (its last use): a moved local is not exit-dec'd, so
			// the container's field keeps the original at rc==1 for the
			// container's own drop while the struct owns the clone. A non-move
			// (live-after) ident would still be exit-dec'd — cloning there would
			// free the aliased buffer early — so it is left to the Perceus port.
			if isMapType(fieldType(sd.Fields, f.Name)) &&
				(isMapMutatorCall(f.Value) || b.isBorrowedMapFieldResultMove(f.Value)) {
				b.emit(Op{Kind: OpCallDirect, Str: "__map_clone", I32: 1})
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
			// (markConstructionMoves sets b.rc.moveSites[capExpr]).
			if needsRcIncOnAlias(capExpr, b) && !b.rc.moveSites[capExpr] {
				b.emitAliasInc(capExpr)
			}
		}
		b.emit(Op{Kind: OpMakeClosure, Str: n.FuncName, I32: int32(len(n.Captures)),
			Ext: extCaptureSlots(captureSlotSizes(b.closureCaps[n.FuncName], b.ptrW))})
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
	case *ast.BlockExpr:
		// A block-expression `{ stmts; tail }` (an `if`/`match` value
		// branch) runs its leading statements, then yields `tail`'s value.
		// Lower it by composing the EXISTING statement- and expression-
		// lowering machinery — no new IR op:
		//
		//   1. Lower each leading statement through `b.stmt` — the same
		//      path a normal `{ }` block's statements take. Block-local
		//      `var`s already have their own pre-allocated, zero-init'd
		//      slots: shadowrename gave the block its own frame (so a `k`
		//      here doesn't collide with a `k` in a sibling branch), and
		//      checkBlockExpr → checkStmt registered each in
		//      `info.Locals[fn]`, which `lowerFunc` turned into a slot.
		//   2. Lower `Tail` as an expression, leaving its value on the
		//      operand stack as the BlockExpr's result.
		//
		// RC / ownership: block-local `var`s are dropped by the existing
		// function-exit dec sweep (`emitRcDecLocalsAtExit`) exactly like
		// any other local — there's no separate scope-exit drop here, so
		// there's nothing to order against the Tail value. When `Tail`
		// references a block-local (e.g. `{ var s = a + b; s }`), the
		// normal Ident-load rules apply: a bare-ident tail is the value
		// being returned, and the slot's exit dec is balanced against the
		// reference the caller now holds (the result is consumed by the
		// enclosing if/match-expr's result slot / block, which is itself
		// covered by the surrounding lowering). This mirrors how a normal
		// block whose final action produces the result interacts with the
		// exit sweep. The checker (E061) guarantees a non-nil Tail in
		// value position. A nil Tail is legal ONLY when the block
		// diverges: its statements always exit early (`return` /
		// `break` / `continue`), so it has no trailing value and the
		// checker typed it `never` (#4522). Lower the statements only —
		// the diverging terminal (OpReturn / OpBr) makes any enclosing
		// consumer (the store into `var x = { …; return … }`, the merge
		// after an if/match arm) unreachable, and the ssa lift skips it,
		// so there is no value to push. A non-diverging nil Tail is a
		// compiler bug (checker E061 should have rejected it), but the
		// same statement-only lowering is the safe thing to emit.
		for _, st := range n.Stmts {
			if err := b.stmt(st); err != nil {
				return err
			}
		}
		if n.Tail == nil {
			return nil
		}
		return b.expr(n.Tail)
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
		// A top-level function name in value position (`(dbl, 1)`) is a
		// function reference — pointer-shaped, same slot-sizing rationale
		// as the variant case above (and the MakeClosure case below).
		// Locals/params were already checked, so this cannot shadow one.
		if ft, ok := b.info.FuncSigs[x.Name]; ok {
			return ft
		}
	case *ast.CaptureRef:
		// Captured variable references carry their resolved
		// outer-scope type on the AST node — needed when the
		// closure body asks "what struct/tuple is this?" for
		// field-access offset resolution.
		return x.Type
	case *ast.MakeClosure:
		// A closureconv-rewritten lambda produces a closure value — a
		// heap pointer to the closure pair — so it is pointer-shaped.
		// Without this case an enclosing TupleLit / StructLit sized the
		// element slot at the payloadSlotSize(nil) 4-byte default while
		// the read/drop side used the DECLARED (fn, …) layout's 8-byte
		// slot: the store packed the neighbouring element 4 bytes below
		// where the load expects it, and the tuple drop rc_dec'd the two
		// misaligned halves as one garbage pointer → segfault. The
		// params/result don't matter for slot sizing; an empty FuncType
		// classifies as IsPointerType.
		return &ast.FuncType{}
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
		// by $args / $string_from_bytes_unchecked / $__str_concat / etc.
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
		// outputs cascade through `$string_from_bytes_unchecked`'s
		// inline-output path. The callee's return type comes
		// off `info.FuncSigs` (populated by the checker for
		// every user fn + every stdlib / builtin signature).
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
	case *ast.CaptureRef:
		// A captured value inside a closure body: closure conversion
		// stamps the resolved outer-scope type. Without this,
		// `capturedArr[i].field` — including a boxcapture cell's
		// `cell[0].field` — peels nothing and fieldOwner errors with
		// `field access on unresolved struct ""`.
		return x.Type
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
	// Map / MapIter accessors return a bare K / V whose CONCRETE type is
	// the call's TypeArg, not the generic FuncSig result (`__method_Map_*`
	// is registered with a type-variable result). Without this, a struct
	// value flowing into `m.get_or(k, d).field` — or `it.value().field` /
	// `it.key().field` — resolves to an empty struct name and codegen
	// aborts with "field access on unresolved struct". The checker stamps
	// TypeArgs = [K, V] on every map method call.
	switch id.Name {
	case "__method_Map_get_or", "__method_MapIter_value":
		if len(c.TypeArgs) >= 2 {
			return c.TypeArgs[1]
		}
	case "__method_MapIter_key":
		if len(c.TypeArgs) >= 1 {
			return c.TypeArgs[0]
		}
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
// for a string concat (`a + b`, which always copies into a fresh buffer),
// a string slice (which copies its bytes out), and a call returning
// string: a Fern return value is owned (+1) at the call site, which is
// why the general drop machinery already reclaims a call result that is
// discarded as a statement or consumed as an argument. Idents / field /
// index reads (borrowed views) and literals (static .rodata) are NOT
// owned temps — freeing them would corrupt a live value, so they read
// false. Used by the concat lowering to dec its operand intermediates.
//
// The call case is what keeps `"n = " + n.to_string()` from leaking the
// to_string buffer once per join: OpStrConcat borrows its operands and
// copies out of them, so nothing else in the pipeline ever drops them.
// It only bites above the 7-byte small-string threshold — shorter
// results are inline-tagged and never allocated, which is why the leak
// hid behind small numbers.
func (b *builder) isOwnedStringTemp(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Binary:
		return x.IsStringConcat
	case *ast.SliceExpr:
		_, isStr := b.exprType(x).(ast.StringType)
		return isStr
	case *ast.Call:
		if x.IsVariantCall {
			return false
		}
		_, isStr := b.exprType(x).(ast.StringType)
		return isStr
	}
	return false
}

// stashOwnedStringOperand lowers `e` as an operand of a BORROWING string
// op — one that reads its operand's bytes and leaves that buffer alone
// (OpStrConcat, OpStrEq, __str_idx, __str_slice). When `e` is an owned
// temp nothing else will ever reclaim it, so it is spilled to a scratch
// slot and re-pushed; the returned slot lets the caller dec it once the op
// has read it. Returns -1 when there is nothing to reclaim (a borrowed
// operand, or reclaim off) — so free-off lowering stays byte-identical.
func (b *builder) stashOwnedStringOperand(e ast.Expr) (int32, error) {
	if err := b.expr(e); err != nil {
		return -1, err
	}
	if !ast.RcFreeEnabled || !b.isOwnedStringTemp(e) {
		return -1, nil
	}
	sl := b.allocSlot()
	b.locals[fmt.Sprintf("__strtmp_%d", sl)] = sl
	b.scratchType[sl] = ast.StringType{}
	b.emit(Op{Kind: OpStoreLocal, I32: sl}) // pop (data,len) → slot
	b.emit(Op{Kind: OpLoadLocal, I32: sl})  // re-push for the borrowing op
	return sl, nil
}

// decStashedStringTemps releases the slots stashOwnedStringOperand handed
// back, skipping the -1 "nothing stashed" entries. __fern_str_dec is the
// right helper on EVERY ptrW: two-word ABIs (wasm + arm64-TwoWord) consume
// the (data,len) pair, and native single-word (x86_64) frees the buffer at
// rc==1 (else defers to __fern_rc_dec) — its inline-tag / SSO / literal
// guards make it safe for short strings that never heap-allocated. Native
// previously used the dec-only __fern_rc_dec here, which decremented but
// never freed, so a nested/chained concat leaked one buffer per join
// (docs/IR-SELFCOMPILE-OOM-FINDINGS.md).
func (b *builder) decStashedStringTemps(slots ...int32) {
	for _, sl := range slots {
		if sl < 0 {
			continue
		}
		b.emit(Op{Kind: OpLoadLocal, I32: sl})
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
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
	// Skipped for sub-i32 (u8, width 8) results: the fold paths
	// emit + return early, bypassing the wrap-narrowing the main
	// path applies below. e.g. `a * 16u8` strength-reduces to a
	// shift and would escape the `& 0xff` mask. Sub-i32 arithmetic
	// is rare, so forgoing the fold there costs nothing measurable
	// and keeps the wrap semantics correct.
	subI32 := n.IntWidth == 8
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
		//
		// Better still, when the LEFT operand is such a temp the concat can
		// GROW it rather than copy-then-free it: `a + b + c` builds the
		// inner `(a + b)` and then appends `c` into that buffer's slack
		// (#5637). consumeLeftTemp marks that case — the temp is then not
		// stashed at all, because __fern_str_append takes ownership of it
		// and there is no later dec to keep a pointer alive for.
		//
		// Ordering is why this is unconditionally safe where the named-
		// accumulator form needs care: the consumed value is an unnameable
		// intermediate created by evaluating Left, and the append runs after
		// BOTH operands are evaluated, so no other expression can observe it
		// between its creation and its consumption.
		consumeLeftTemp := ast.RcFreeEnabled && b.strAppendAvailable() &&
			b.isOwnedStringTemp(n.Left) && ast.Expr(n) != b.selfStrAppendBin
		stash := func(e ast.Expr, consumed bool) (int32, error) {
			if consumed {
				return -1, b.expr(e)
			}
			return b.stashOwnedStringOperand(e)
		}
		slL, err := stash(n.Left, consumeLeftTemp)
		if err != nil {
			return err
		}
		slR, err := stash(n.Right, false)
		if err != nil {
			return err
		}
		// A marked string self-append (`s = s + piece`) takes
		// __fern_str_append: when `s`'s buffer is uniquely held and the
		// grown length still classes to the same allocator block, the
		// piece is memcpy'd into the slack and the SAME buffer comes back
		// still at rc==1; every other case falls back to the plain concat
		// and releases the consumed accumulator itself. Either way the
		// helper owns the old buffer, which is what assign() reads
		// selfStrAppendDone for. See isSelfStrAppendLocal. The left
		// operand of that shape is a bare ident, so it is never a stashed
		// owned temp — only `piece` can be, and its reclaim below is
		// unchanged.
		//
		// consumeLeftTemp is the same helper applied to a nested concat's
		// own intermediate: it grows that buffer instead of allocating a
		// fresh one and freeing it, so a chain allocates ONCE rather than
		// once per join. Its release is the __fern_str_dec the helper runs
		// on its fallback path — exactly the dec the loop below would have
		// emitted for the stashed temp.
		if ast.Expr(n) == b.selfStrAppendBin || consumeLeftTemp {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_append", I32: 2})
			if !consumeLeftTemp {
				b.selfStrAppendDone = true
			}
		} else {
			b.emit(Op{Kind: OpStrConcat})
		}
		// The operand is a fresh sole-owner temp (isOwnedStringTemp), so
		// freeing at rc==1 is balanced.
		b.decStashedStringTemps(slL, slR)
		return nil
	}
	if n.IsStringCmp {
		// Same shape as concat but for content equality — OpStrEq reads
		// both operands' bytes and leaves their buffers alone, so an owned
		// temp operand (`f(x) == "…"`) needs the same stash-and-dec or it
		// leaks one buffer per comparison. `!=` is the negation of `==`.
		slL, err := b.stashOwnedStringOperand(n.Left)
		if err != nil {
			return err
		}
		slR, err := b.stashOwnedStringOperand(n.Right)
		if err != nil {
			return err
		}
		b.emit(Op{Kind: OpStrEq})
		if n.Op == "!=" {
			b.emit(Op{Kind: OpNot})
		}
		b.decStashedStringTemps(slL, slR)
		return nil
	}
	if err := b.expr(n.Left); err != nil {
		return err
	}
	if err := b.expr(n.Right); err != nil {
		return err
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
	if isSatOp(n.Op) {
		return b.satBinary(n)
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
	// Sub-i32 (u8) arithmetic wraps to its declared width. `+`, `-`,
	// `*`, `<<` can push the result past 8 bits (e.g. `255u8 + 1u8`
	// → 256), but scalar locals + struct fields are stored
	// full-width (only array elements narrow via the store8 op),
	// so without an explicit narrow the out-of-range value leaks —
	// a `u8` var would hold 256 and a later widening cast /
	// comparison (which assumes "every store narrows", see the
	// cast lowering) reads garbage. u8 is always unsigned, so mask
	// back to width. The other ops (`/ % & | ^ >>`) can't exceed
	// the operands' width.
	if w == 8 {
		switch op {
		case OpAdd, OpSub, OpMul, OpShl:
			b.emit(Op{Kind: OpConstI32, I32: int32((1 << w) - 1)})
			b.emit(Op{Kind: OpAnd})
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
		// `x & mask → x` only when the mask is this operation's all-ones
		// value. For a 64-bit AND that is -1 (0xFFFFFFFFFFFFFFFF); a
		// 32-bit AND accepts either -1 or 0xFFFFFFFF (an unsigned literal
		// spells the same all-ones mask as 4294967295). An i64 literal
		// 0xFFFFFFFF is NOT all-ones — it CLEARS the high 32 bits — so it
		// must not fold; the width-aware check below excludes it.
		if lok && isAllOnesMask(numL, n.IntWidth) {
			return b.expr(n.Right), true
		}
		if rok && isAllOnesMask(numR, n.IntWidth) {
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
			if k, ok := log2I64(numL); ok && k > 0 {
				if err := b.expr(n.Right); err != nil {
					return err, true
				}
				b.emitShlByConst(n, int32(k))
				return nil, true
			}
		}
		if rok {
			if k, ok := log2I64(numR); ok && k > 0 {
				if err := b.expr(n.Left); err != nil {
					return err, true
				}
				b.emitShlByConst(n, int32(k))
				return nil, true
			}
		}
	}
	return nil, false
}

// isAllOnesMask reports whether v is the all-bits-set mask for an
// integer of the given resolved width (IntWidth 0 defaults to 32).
// A 64-bit all-ones is -1; a 32-bit all-ones is -1 or 0xFFFFFFFF.
func isAllOnesMask(v int64, intWidth int) bool {
	if intWidth == 64 {
		return v == -1
	}
	return v == -1 || v == 0xFFFFFFFF
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
// Returns the full int64 value and true on a hit. It must NOT
// truncate to int32: a 64-bit literal whose low 32 bits are zero
// (e.g. 2^52 = 0x10000000000000) would read as 0 and make
// maybeFoldArithIdentity mis-fold `x | 2^52 → x` (#5567).
func constNumber(e ast.Expr) (int64, bool) {
	if n, ok := e.(*ast.NumberLit); ok {
		return n.Value, true
	}
	if u, ok := e.(*ast.Unary); ok && u.Op == "-" {
		if inner, ok := u.Operand.(*ast.NumberLit); ok {
			return -inner.Value, true
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

// isSatOp reports whether `s` is one of the saturating arithmetic
// operators (`+|` / `-|` / `*|` / `<<|`, #5542). They are integer-only
// and clamp to the operand type's [MIN, MAX] instead of wrapping.
func isSatOp(s string) bool {
	return s == "+|" || s == "-|" || s == "*|" || s == "<<|"
}

// satBinary lowers a saturating integer binary. Both operands are
// already on the operand stack (left below right — the same shape
// the wrapping path consumes), so the first thing it does is spill
// them to scratch slots: every lowering reads each operand two or
// three times.
//
// The tests are formulated as *pre*-checks against the type's MIN /
// MAX rather than as post-hoc overflow-flag reconstruction, so the
// same shape works at every width — including sub-i32 `u8`, whose
// wrapping mask never has to run because a saturated result is in
// range by construction. Every intermediate (`MAX - r` for `r > 0`,
// `MIN - r` for `r < 0`, …) is provably non-overflowing.
//
//	signed   a +| b →  b > 0 && a > MAX - b ? MAX
//	                 : b < 0 && a < MIN - b ? MIN : a + b
//	signed   a -| b →  b < 0 && a > MAX + b ? MAX
//	                 : b > 0 && a < MIN + b ? MIN : a - b
//	unsigned a +| b →  a > MAX - b ? MAX : a + b
//	unsigned a -| b →  a < b ? 0 : a - b
//	unsigned a *| b →  a != 0 && b > MAX / a ? MAX : a * b
//
// Signed `*|` is the one shape a pre-check can't express cheaply
// (four sign quadrants), so it post-checks the wrapping product
// with a division: `a != 0 && (s / a != b || (a == -1 && b == MIN))`.
// The `a == -1 && b == MIN` term is needed because Fern's division
// is total — `MIN / -1` yields `MIN`, so the round-trip spuriously
// agrees on exactly that pair. The clamp direction is the sign of
// the true product, `(a < 0) ^ (b < 0)`.
//
// Division-by-zero in the guarded operand is harmless for the same
// total-division reason: the `a != 0` conjunct discards the result,
// and `x / 0` is defined as 0 rather than a trap
// (docs/INTEGER-SEMANTICS.md).
//
// `<<|` is the odd one out: it post-checks with a round-trip rather
// than a pre-check, because the pre-check bound for the negative side
// would need `ceil(MIN / 2^c)` and an arithmetic shift only gives the
// floor (`-1i8 <<| 31` must clamp to MIN, but `MIN >> 31` is `-1`, so
// `a < MIN >> c` is false). Shifting back is exact at every width:
//
//	a <<| b →  s := a << b; (s >> b) == a ? s
//	                        : signed   ? (a < 0 ? MIN : MAX)
//	                        : /*uns*/    MAX
//
// The count is masked exactly as `<<` masks it (`& 31` / `& 63`), and
// the round-trip shifts by the same masked count, so the check tests
// the shift that actually ran. `>>` is arithmetic for signed operands
// and logical for unsigned, which is what makes the round-trip
// value-preserving in each signedness.
func (b *builder) satBinary(n *ast.Binary) error {
	w := n.IntWidth
	if w != 8 && w != 64 {
		w = 32
	}
	uns := n.IsUnsigned
	wide := w == 64
	bt := BlockTypeI32
	slotT := ast.Type(ast.NumberType{Width: 32})
	if wide {
		bt = BlockTypeI64
		slotT = ast.NumberType{Width: 64}
	}
	var minV, maxV int64
	switch {
	case uns && w == 8:
		minV, maxV = 0, 255
	case uns:
		// All-ones at the op width: -1 reinterpreted unsigned.
		minV, maxV = 0, -1
	case w == 8:
		minV, maxV = -128, 127
	case wide:
		minV, maxV = -9223372036854775808, 9223372036854775807
	default:
		minV, maxV = -2147483648, 2147483647
	}

	spill := func() int32 {
		sl := b.allocSlot()
		b.locals[fmt.Sprintf("__sattmp_%d", sl)] = sl
		b.scratchType[sl] = slotT
		b.emit(Op{Kind: OpStoreLocal, I32: sl})
		return sl
	}
	// Right is on top, so it pops first.
	rs := spill()
	ls := spill()

	konst := func(v int64) {
		if wide {
			b.emit(Op{Kind: OpConstI64, I64: v})
			return
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(v)})
	}
	loadL := func() { b.emit(Op{Kind: OpLoadLocal, I32: ls}) }
	loadR := func() { b.emit(Op{Kind: OpLoadLocal, I32: rs}) }
	// Arithmetic + comparison at the operand width. Comparisons
	// yield an i32 boolean regardless, so the boolean combinators
	// below stay width-free.
	at := func(k OpKind) { b.emit(Op{Kind: k, Width: w, Unsigned: uns}) }
	boolOp := func(k OpKind) { b.emit(Op{Kind: k}) }

	switch n.Op {
	case "+|", "-|":
		add, sub := OpAdd, OpSub
		if n.Op == "-|" {
			// `a -| b` is `a +| (-b)` in structure: the overflow
			// direction flips with b's sign, and the guard bound
			// is reached by adding rather than subtracting b.
			add, sub = OpSub, OpAdd
		}
		if uns {
			if n.Op == "+|" {
				loadL()
				konst(maxV)
				loadR()
				at(OpSub)
				at(OpGtS)
				b.openIf(bt)
				konst(maxV)
				b.elseBranch()
				loadL()
				loadR()
				at(OpAdd)
				b.closeScope()
				return nil
			}
			loadL()
			loadR()
			at(OpLtS)
			b.openIf(bt)
			konst(0)
			b.elseBranch()
			loadL()
			loadR()
			at(OpSub)
			b.closeScope()
			return nil
		}
		// Signed: clamp high when b pushes past MAX, low when it
		// pushes past MIN. `sub` is the bound-adjusting op (the
		// inverse of the arithmetic), `add` the arithmetic itself.
		hiCmp, loCmp := OpGtS, OpLtS
		if n.Op == "-|" {
			hiCmp, loCmp = OpLtS, OpGtS
		}
		loadR()
		konst(0)
		at(hiCmp)
		loadL()
		konst(maxV)
		loadR()
		at(sub)
		at(OpGtS)
		boolOp(OpAnd)
		b.openIf(bt)
		konst(maxV)
		b.elseBranch()
		loadR()
		konst(0)
		at(loCmp)
		loadL()
		konst(minV)
		loadR()
		at(sub)
		at(OpLtS)
		boolOp(OpAnd)
		b.openIf(bt)
		konst(minV)
		b.elseBranch()
		loadL()
		loadR()
		at(add)
		b.closeScope()
		b.closeScope()
		return nil
	case "*|":
		if uns {
			loadL()
			konst(0)
			at(OpNe)
			loadR()
			konst(maxV)
			loadL()
			at(OpDivS)
			at(OpGtS)
			boolOp(OpAnd)
			b.openIf(bt)
			konst(maxV)
			b.elseBranch()
			loadL()
			loadR()
			at(OpMul)
			b.closeScope()
			return nil
		}
		loadL()
		loadR()
		at(OpMul)
		ss := spill()
		loadS := func() { b.emit(Op{Kind: OpLoadLocal, I32: ss}) }
		loadL()
		konst(0)
		at(OpNe)
		loadS()
		loadL()
		at(OpDivS)
		loadR()
		at(OpNe)
		loadL()
		konst(-1)
		at(OpEq)
		loadR()
		konst(minV)
		at(OpEq)
		boolOp(OpAnd)
		boolOp(OpOr)
		boolOp(OpAnd)
		b.openIf(bt)
		loadL()
		konst(0)
		at(OpLtS)
		loadR()
		konst(0)
		at(OpLtS)
		boolOp(OpXor)
		b.openIf(bt)
		konst(minV)
		b.elseBranch()
		konst(maxV)
		b.closeScope()
		b.elseBranch()
		loadS()
		b.closeScope()
		return nil
	case "<<|":
		loadL()
		loadR()
		at(OpShl)
		if w == 8 {
			// Sub-i32 arithmetic runs in 32-bit lanes and only wraps
			// on the way into a u8 slot, so the round-trip below has
			// to see the wrapped value — otherwise `200u8 <<| 1`
			// shifts back to 200 and reports no overflow.
			konst(255)
			at(OpAnd)
		}
		ss := spill()
		loadS := func() { b.emit(Op{Kind: OpLoadLocal, I32: ss}) }
		loadS()
		loadR()
		at(OpShrS)
		loadL()
		at(OpEq)
		b.openIf(bt)
		loadS()
		b.elseBranch()
		if uns {
			konst(maxV)
			b.closeScope()
			return nil
		}
		loadL()
		konst(0)
		at(OpLtS)
		b.openIf(bt)
		konst(minV)
		b.elseBranch()
		konst(maxV)
		b.closeScope()
		b.closeScope()
		return nil
	}
	return fmt.Errorf("ir: unsupported saturating binary %q", n.Op)
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
//	3 = user struct / enum key, hashed + compared by its
//	    derived `hash` / `eq` methods, which the codegen
//	    threads in as per-call function VALUES (see
//	    emitMapCall). The key is pointer-shaped and stored in
//	    the entries column like a string key. The checker
//	    guarantees any struct/enum reaching here implements
//	    both Eq and Hash, so this can classify structurally
//	    without consulting checker.Info. See #2671.
//
// Other key types (tuple / array / slice / float-on-narrow-ptr)
// still aren't supported; they'd need their own runtime
// branches.
func mapKeyKindTag(t ast.Type, ptrW int) int32 {
	switch t.(type) {
	case ast.StringType:
		return 1
	case ast.StructType:
		// Map is itself a StructType{Name:"Map"} but never reaches
		// here as a KEY (it implements neither Eq nor Hash, so the
		// checker rejects it). Every user struct that does reach here
		// is a kind-3 key.
		return 3
	case ast.EnumType:
		return 3
	}
	if isWideScalar(t) && ptrW < 8 {
		return 2
	}
	return 0
}

// mapKeyTypeName returns the nominal type name of a struct/enum map
// key — the basis for its derived `__method_<name>_hash` /
// `__method_<name>_eq` function values. Empty for non-nominal types.
func mapKeyTypeName(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		return x.Name
	}
	return ""
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
		return arrElemStructDropName(at.Elem, info, genEnumDrops, genTupleDrops, ptrW, false)
	}
	// Every other value with a generated recursive drop — concrete user
	// struct (__drop_struct_<V>), concrete enum (__drop_enum_<V>), or a
	// heap-boxed generic-enum instantiation (__drop_enum_<mangled>, recorded
	// in genEnumDrops) — routes through dropFnNameFor, the same dispatch
	// the struct/enum field drops use. Strings / tuples / slices / runtime
	// handles / pair-form generic enums read false and stay non-reclaimed.
	return dropFnNameFor(v, info, genEnumDrops, genTupleDrops, ptrW, false)
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
	// `dyn Trait` runtime dispatch. The checker marked this call with
	// the trait name and left the callee a FieldAccess (`d.area()` where
	// `d: dyn Shape`). Lower the receiver to its inline two-word `[data,
	// vtable]` fat pointer, stash the vtable word, lower the args, push
	// the vtable back, and emit OpCallDyn{Sig, slot}. The receiver ABI is
	// a plain i32 concrete pointer, uniform across concrete types, so one
	// signature serves every implementor. See docs/DYN-TRAITS.md §4.2.1.
	if n.DynTrait != "" {
		fa, ok := n.Callee.(*ast.FieldAccess)
		if !ok {
			return fmt.Errorf("ir: dyn %s call without a field-access callee", n.DynTrait)
		}
		td, ok := b.info.Traits[n.DynTrait]
		if !ok {
			return fmt.Errorf("ir: dyn %s call references an unknown trait", n.DynTrait)
		}
		// Slot index = position of fa.Field among the trait's non-
		// associated methods, in declaration order. Build the receiver-
		// first method signature at the same time.
		slot := -1
		var meth *ast.TraitMethod
		k := 0
		for i := range td.Methods {
			if td.Methods[i].Assoc {
				continue
			}
			if td.Methods[i].Name == fa.Field {
				slot = k
				meth = &td.Methods[i]
				break
			}
			k++
		}
		if slot < 0 || meth == nil {
			return fmt.Errorf("ir: method %q not found on trait %s", fa.Field, n.DynTrait)
		}
		// Global slot in the MERGED vtable: a `dyn A + B` receiver's vtable
		// concatenates the per-trait tables in sorted-set order, so a method
		// owned by a non-first trait sits after every earlier trait's method
		// block. The static trait set is the receiver type the checker
		// stamped on Method.Receiver; n.DynTrait is the owning trait. For a
		// single-trait receiver the prefix is 0 → `slot` is unchanged
		// (docs/DYN-TRAITS.md §10).
		if n.Method != nil {
			if dt, ok := n.Method.Receiver.(ast.DynTraitType); ok && len(dt.Traits) > 1 {
				slot += dynTraitMethodPrefix(b.info, dt.Traits, n.DynTrait)
			}
		}
		// Receiver-first signature: param[0] is the concrete-receiver
		// pointer (an i32 on wasm — any pointer-shaped type lowers to
		// i32). The remaining params + result come from the trait
		// method (its self param dropped). Self never appears outside
		// the receiver slot (object-safety guarantees it), so no Self
		// substitution is needed. A generic trait's type parameters
		// (`get(): T`) and pinned associated types (`get(): Self::Item`)
		// ARE resolved here from the receiver's pins — otherwise the wasm
		// OpCallDyn seam can't classify a bare ParamType / ProjType.
		resolve := dynSigResolver(b.info, n.DynTrait, n.Method)
		params := []ast.Type{ast.StructType{}}
		for _, p := range meth.Params[1:] {
			params = append(params, resolve(p.Type))
		}
		sig := &ast.FuncType{Params: params, Result: resolve(meth.Result)}
		// OpCallDyn's stack contract is uniform across backends:
		// `[data, args..., vtable]`. Only how `data` and `vtable` are
		// obtained from the receiver differs by representation
		// (docs/DYN-TRAITS.md §4.2):
		if b.dynBoxed() {
			// Boxed one-word (natives, §4.2.2): the receiver is a single
			// pointer to a `{data @0, vtable @ptrW}` cell. Stash the cell
			// pointer, deref `data` (+0), lower args, deref `vtable`
			// (+ptrW), then dispatch.
			if err := b.expr(fa.Target); err != nil {
				return err
			}
			cellTmp := b.allocSlot()
			b.scratchType[cellTmp] = ast.NumberType{Width: 32}
			b.emit(Op{Kind: OpStoreLocal, I32: cellTmp})
			// data = load(cell + 0)  → [data]
			b.emit(Op{Kind: OpLoadLocal, I32: cellTmp})
			b.emit(Op{Kind: OpLoad, Width: WidthPtr})
			// args → [data, args...]
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			// vtable = load(cell + ptrW)  → [data, args..., vtable]
			b.emit(Op{Kind: OpLoadLocal, I32: cellTmp})
			b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
			b.emit(Op{Kind: OpAdd})
			b.emit(Op{Kind: OpLoad, Width: WidthPtr})
			b.emit(Op{Kind: OpCallDyn, I32: int32(slot), Ext: &OpExt{Sig: sig}})
			return nil
		}
		// Inline two-word (wasm, §4.2.1): lower the receiver →
		// [data, vtable]; pop the vtable word into a fresh i32 temp
		// (OpStoreLocal pops one word, leaving [data]).
		if err := b.expr(fa.Target); err != nil {
			return err
		}
		vtmp := b.allocSlot()
		b.scratchType[vtmp] = ast.NumberType{Width: 32}
		b.emit(Op{Kind: OpStoreLocal, I32: vtmp})
		// Lower the args → [data, args...].
		for _, a := range n.Args {
			if err := b.expr(a); err != nil {
				return err
			}
		}
		// Push the vtable back → [data, args..., vtable], then dispatch.
		b.emit(Op{Kind: OpLoadLocal, I32: vtmp})
		b.emit(Op{Kind: OpCallDyn, I32: int32(slot), Ext: &OpExt{Sig: sig}})
		return nil
	}
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
		b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
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
			b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
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
				b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
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
			b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
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
		b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
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
			b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: ft}})
			return nil
		}
	}
	if _, ok := n.Callee.(*ast.Ident); !ok {
		return fmt.Errorf("ir: indirect call from non-identifier expression")
	}
	if recv := b.mapCowRetainReceiver(n); recv != nil && !isMapDeleteCall(n) {
		return b.callWithMapCowRetain(n, recv)
	}
	return b.callBody(n)
}

// callWithMapCowRetain lowers a Map COW mutator whose result is the map
// itself (`m.insert(k, v)` / `m.cleared()`) and then applies the COW-seam
// retain — see mapCowRetainReceiver for why it is conditional. The result is
// stashed so the receiver can be re-read for the pointer compare; `recv` is
// an Ident or FieldAccess, so re-reading it is a plain load with no side
// effect, and neither the mutator nor its COW writes the receiver's slot /
// field, so the reload still yields the PRE-call handle.
func (b *builder) callWithMapCowRetain(n *ast.Call, recv ast.Expr) error {
	if err := b.callBody(n); err != nil {
		return err
	}
	resSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mapcow_res_%d", resSlot)] = resSlot
	b.emit(Op{Kind: OpStoreLocal, I32: resSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: resSlot})
	if err := b.expr(recv); err != nil {
		return err
	}
	b.emitMapCowRetainTest(resSlot)
	b.emit(Op{Kind: OpLoadLocal, I32: resSlot})
	return nil
}

// emitMapCowRetainTest consumes the (result, pre-COW receiver) handle pair
// on the stack and inc's the result iff the two are the same handle — i.e.
// iff __map_cow_inplace mutated in place and the receiver's binding still
// names what the call handed back. On the copy branch the result is a fresh
// rc=1 the caller already solely owns, so retaining there would leak.
func (b *builder) emitMapCowRetainTest(resSlot int32) {
	b.emit(Op{Kind: OpEq})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: resSlot})
	b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
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

// resultIsCountedStringAlias admits the stage-(b) arg-temp reclaim for a
// STRING-returning user callee, where resultCannotAliasArg says no. The result
// really can be the argument — `pad_start`'s `if (sl >= n) { return s; }` is the
// canonical shape — so this rests on a different argument: not that the alias is
// impossible, but that it is COUNTED.
//
// `return <param>` emits the return-transfer inc. A param is borrowed, so it is
// never an isOwnedRcLocal, so move-on-return can never cancel that inc away
// (needsRcIncOnAlias's StringType arm supplies it on every backend). So after the
// call the temp's rc is 2 on the pass-through path and 1 on the fresh path, and
// the immediate post-call dec nets it to exactly one owner either way — freeing
// the temp only when the result does not reference it.
//
// Without this, a fresh string temp handed to a string-returning call is never
// reclaimed at all: `(k * 66049).to_binary().pad_start(40, "0")` leaks one block
// per call, while the identical code with the intermediate bound to a `var` does
// not (#5942). It hid from the x86-64 leakcheck suite because ≤ 7-byte strings
// are SSO-inline on the single-word ABI, so short intermediates allocate nothing;
// arm64 and wasm heap-allocate them.
//
// Deliberately narrow, because widening this gate to POINTER results in general
// is the thing that segfaulted the differential oracle before (seeds
// 1392/1596/1836, recorded on reclaimArgTemps above). Two restrictions carry
// that weight:
//
//   - CONCRETE StringType only. The shapes that broke were generic identity
//     returns (`id[T](x)`, `pick[T](c,a,b)`), whose result type is the bare type
//     var `ast.ParamType` — not StringType, so they stay excluded exactly as
//     they are today. A concrete string result cannot hide a type var.
//   - USER-DECLARED callees only (the returnsNoParamEscape map keys every decl in
//     prog.Funcs). A builtin's allocation contract is per-helper rather than the
//     return-transfer model this argument rests on — `random_bytes` hands back a
//     buffer with no rc header at all — so builtins keep their prior safe-leak.
func (b *builder) resultIsCountedStringAlias(name string, t ast.Type) bool {
	if !ast.RcFreeEnabled {
		return false
	}
	if _, isStr := t.(ast.StringType); !isStr {
		return false
	}
	_, isUserFn := b.returnsNoParamEscape[name]
	return isUserFn
}

// growBracketEntry is one buffer the #4873 caller-side containment bracket
// protects across a call: the arg local's slot, and either the arg buffer
// itself (empty fieldPath) or an array buffer reached from a struct arg by
// loading each field offset in fieldPath in turn (intermediate hops are
// struct-box pointers; the final hop is the array buffer).
type growBracketEntry struct {
	slot      int32
	fieldPath []int32
}

// arrayFieldPaths enumerates the offset paths from a struct type to every
// (transitively nested, depth-limited) array field — the buffers a callee
// marked growFieldBufs may grow in place. Struct-typed fields recurse;
// self-referential shapes are cycle-guarded; Map / enum / tuple / string
// fields are skipped (arrays are the only push/set targets).
func (b *builder) arrayFieldPaths(structName string, depth int, seen map[string]bool) [][]int32 {
	if depth <= 0 || seen[structName] {
		return nil
	}
	sd, has := b.info.Structs[structName]
	if !has {
		return nil
	}
	seen[structName] = true
	defer delete(seen, structName)
	offs, _ := structFieldLayout(sd.Fields, b.ptrW)
	var out [][]int32
	for _, fld := range sd.Fields {
		switch ft := fld.Type.(type) {
		case ast.ArrayType:
			out = append(out, []int32{offs[fld.Name]})
		case ast.StructType:
			for _, sub := range b.arrayFieldPaths(ft.Name, depth-1, seen) {
				out = append(out, append([]int32{offs[fld.Name]}, sub...))
			}
		}
	}
	return out
}

// growBracketArgs resolves the #4873 containment bracket for a direct call
// to `calleeName`: for each argument position the callee may grow in place
// (computeGrowParams), a surviving plain-ident argument contributes its
// buffer (growArgBuffer) and/or its struct type's array-field buffers
// (growFieldBufs). Skipped when the arg dies at this call (the strict
// self-reassign shape — keeps the #4838 O(n) accumulator chains on the
// in-place fast path), is a move site, is not an rc-tracked alias, or
// flows into an `own` / owned-by-default position (those transfer or inc
// already).
func (b *builder) growBracketArgs(n *ast.Call, calleeName string) []growBracketEntry {
	gp := b.growParams[calleeName]
	if len(gp) == 0 {
		return nil
	}
	b.curAppendOrder() // refresh callArgDies for the current fn
	ownFlags := b.info.OwnFuncs[calleeName]
	sig := b.info.FuncSigs[calleeName]
	var out []growBracketEntry
	for ai, a := range n.Args {
		if ai >= len(gp) || gp[ai] == 0 {
			continue
		}
		id, isIdent := a.(*ast.Ident)
		if !isIdent {
			continue
		}
		slot, hasSlot := b.locals[id.Name]
		if !hasSlot {
			continue
		}
		if b.callArgDies[n][id.Name] {
			continue
		}
		if b.rc.moveSites[a] {
			continue
		}
		if !needsRcIncOnAlias(a, b) {
			continue
		}
		if ai < len(ownFlags) && ownFlags[ai] {
			continue
		}
		if sig != nil && ai < len(sig.Params) && b.calleeParamOwnedByDefault(calleeName, sig.Params[ai], ai) {
			continue
		}
		if gp[ai]&growArgBuffer != 0 {
			out = append(out, growBracketEntry{slot: slot})
		}
		if gp[ai]&growFieldBufs != 0 && sig != nil && ai < len(sig.Params) {
			if st, isStruct := sig.Params[ai].(ast.StructType); isStruct {
				for _, path := range b.arrayFieldPaths(st.Name, 4, map[string]bool{}) {
					out = append(out, growBracketEntry{slot: slot, fieldPath: path})
				}
			}
		}
	}
	return out
}

// emitGrowBracket emits one side of the #4873 containment bracket: for
// each protected buffer, load it (via the arg slot, plus a pointer-width
// field load for a struct arg's array field) and inc/dec its rc. Net-zero
// on the operand stack; the rc helpers guard null / static sentinels. The
// callee's grow/cow COPY path leaves the operand untouched (the #4827
// forced-copy invariant), so the post-call dec restores the incoming
// count and never frees.
func (b *builder) emitGrowBracket(entries []growBracketEntry, kind OpKind, helper string) {
	for _, e := range entries {
		b.emit(Op{Kind: OpLoadLocal, I32: e.slot})
		for _, off := range e.fieldPath {
			if off > 0 {
				b.emit(Op{Kind: OpConstI32, I32: off})
				b.emit(Op{Kind: OpAdd})
			}
			b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		}
		b.emit(Op{Kind: kind, Str: helper, I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
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
	// dispatching through one of N per-stride stdlib
	// functions. The IR already knows the stride from
	// `ast.ElemSizeBytes(elemType)` and the right store op from
	// `payloadStoreOp(elemType)`; the previous shape compounded
	// boilerplate (5 stdlib bodies + 5 mangled FuncSigs +
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
	// Cell[T] (docs/CELL-TYPE-PLAN.md): a single-slot mutable heap box,
	// lowered as a one-element array box so Perceus RCs the box with no
	// per-slot RC (v1 holds scalars only — E057). `cell_new(v)` allocs +
	// stores; `get` loads slot 0; `set` stores slot 0 in place (no CoW —
	// a cell mutates in place by design) and returns void.
	if id.Name == "cell_new" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			return b.emitCellNew(n)
		}
	}
	if id.Name == "__method_Cell_get" && len(n.Args) == 1 {
		return b.emitCellGet(n)
	}
	if id.Name == "__method_Cell_set" && len(n.Args) == 2 {
		return b.emitCellSet(n)
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
	// builtins. Lower to the dedicated rc ops (#4402 opt 2) with
	// the runtime-side name so backends pick up the matching gate
	// flag. Both accept a u8[] today; Phase 1e will widen to
	// strings / structs / enums / closures. See docs/RC-PERCEUS-PLAN.md.
	if (id.Name == "__rc_inc" || id.Name == "__rc_dec") && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			kind, target := OpRcInc, "__fern_rc_inc"
			if id.Name == "__rc_dec" {
				kind, target = OpRcDec, "__fern_rc_dec"
			}
			b.emit(Op{Kind: kind, Str: target, I32: 1})
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
	// __arr_push_shared_count(): i32 — the rc==1 cliff probe. Reads the
	// counter __fern_arr_push_grow bumps when it copies a buffer that had
	// SPARE CAPACITY, so the copy was forced by an extra reference alone.
	// Same runtime-helper shape as __rc_underflow_count: each backend reads
	// its own store (wasm a linear-memory slot, the natives a BSS global).
	if id.Name == "__arr_push_shared_count" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_push_shared_count", I32: 0})
			return nil
		}
	}
	// __arr_push_shared_bytes(): i64 — the same cliff weighted by bytes
	// copied (oldLen * stride, summed at each crossing). Same runtime-helper
	// shape as the counter beside it; the weight is what ranks one crossing
	// site against another, which the count cannot do.
	if id.Name == "__arr_push_shared_bytes" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_push_shared_bytes", I32: 0})
			return nil
		}
	}
	// __map_hash_seed(): i32 — core/map's per-process string-hash seed
	// (#6194). Same runtime-helper shape as the probes around it: each
	// backend owns the cached word and the lazy CSPRNG draw that fills it.
	if id.Name == "__map_hash_seed" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_hash_seed", I32: 0})
			return nil
		}
	}
	// __heap_bump_bytes(): i64 — Phase 6 measurement probe. Returns the
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
	// Bit-counting intrinsics. Unlike the probes above these are NOT
	// runtime-helper calls — they lower to a single IR op, which is the
	// entire point: the SWAR sequences they replace cost ~19.5 ns per
	// call measured against ~0.7 ns for a hardware instruction, and a
	// call would put most of that back.
	//
	// The 32- and 64-bit forms are separate builtins rather than one
	// overload because the operand width has to reach the backend on
	// Op.Width, and the caller's static type is what fixes it.
	if bitOp, width, ok := bitCountBuiltin(id.Name); ok && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			b.emit(Op{Kind: bitOp, Width: width})
			return nil
		}
	}
	// __memchr(s, byte, from) — a runtime-helper CALL, not an inline op,
	// which is the opposite choice from the bit intrinsics above and for
	// the opposite reason. A bit count is one instruction, so a call would
	// cost more than the work; a byte scan is an unbounded loop, so the
	// call is noise against it and a helper body is the natural home for
	// the vectorised sequence (docs/ATLAS-PLATFORM-PLAN.md §3.1 rule 3 —
	// the vector lifetime must stay inside one emitted body).
	if id.Name == "__memchr" && len(n.Args) == 3 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_memchr", I32: 3})
			return nil
		}
	}
	// __heap_mark() / __heap_release_to(mark) — the one-level arena
	// checkpoint pair. Same runtime-helper shape as __heap_bump_bytes so each
	// backend rewinds its own cursor and snapshots its own freelist heads.
	//
	// These carry the SOURCE builtin name, not the __fern_ runtime name, and
	// each backend rewrites it at the call site. That is load-bearing for
	// release_to: it returns void, and the backends suppress the post-call
	// operand-stack push via callReturnsVoid, which resolves voidness through
	// the checker's FuncSigs — keyed by the source name. Emitting the runtime
	// name here makes that lookup miss, so every release pushes a phantom rax
	// slot and the operand stack drifts (observed as SIGSEGV on garbage
	// pointers well past the arena, several units into a checkpointed emit).
	if id.Name == "__heap_mark" && len(n.Args) == 0 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			b.emit(Op{Kind: OpCallDirect, Str: "__heap_mark", I32: 0})
			return nil
		}
	}
	if id.Name == "__heap_release_to" && len(n.Args) == 1 {
		if _, isLocal := b.locals[id.Name]; !isLocal {
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__heap_release_to", I32: 1})
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
	// `__map_values_impl` stdlib function). Wide V needs
	// to follow each entry's cell pointer and copy the 8
	// payload bytes into a wide-stride result — emitted inline
	// here for the same reason as emitArrayPush: a single
	// codepath instead of a per-stride stdlib clone.
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
	// f64). The stdlib's `__map_keys_impl` uses a 4-byte
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
	// at `slice + ptrW` after the pointer-width data field
	// (+8 native / +4 wasm32). Strings route
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
		tt, ok := b.freshOwnedRcTempType(n.Args[0])
		if !ok {
			// A USER-call receiver (`f(i).len()`) is the same dead-after-
			// consume temp as a concat: the callee's fresh result is created
			// solely for this length read. Reclaim it via the is_unique-gated
			// ownedCallResultType route the discarded-stmt / index-of-fresh /
			// field-access / call-arg sites already use — without this
			// fallback the returned heap box leaked every call (masked below
			// the SSO inline threshold, ~32-128 B per call above it). An
			// aliased return (callee handing back a param) carries the
			// return-transfer inc, so the drop only dec's it.
			tt, ok = b.ownedCallResultType(n.Args[0])
		}
		if ok {
			lenTempSlot = b.allocSlot()
			b.locals[fmt.Sprintf("__lentmp_%d", lenTempSlot)] = lenTempSlot
			b.scratchType[lenTempSlot] = tt
			b.emit(Op{Kind: OpStoreLocal, I32: lenTempSlot})
			b.emit(Op{Kind: OpLoadLocal, I32: lenTempSlot})
		}
		switch id.Name {
		case "__method_slice_len":
			// len lives at slice + ptrW (after the pointer-width data
			// field): +8 on native, +4 on wasm32.
			b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
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
	// keyKind3: a struct/enum key dispatched through its derived
	// hash/eq (see emitMapCall). The key is a raw pointer (never
	// boxed — needBoxK is false), so the boxing helpers handle it as
	// a plain pointer; the only difference is the runtime call routes
	// to the `_keyed` variant. The key-by-key value reclamation
	// predrop gates below probe the map by KEY, which the type-erased
	// i32 lookup would do with the wrong hash for a struct key — so
	// those gates are disabled here (an overwrite leaks the replaced
	// value; struct keys themselves are not yet rc-reclaimed either —
	// a bounded leak, no corruption — tracked as a follow-up). See #2671.
	keyKind3 := len(n.TypeArgs) >= 1 && mapKeyKindTag(n.TypeArgs[0], b.ptrW) == 3
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
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
		ast.RcFreeEnabled && !needBoxK && !keyKind3 &&
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
		ast.RcFreeEnabled && ast.UseTwoWordStrings(b.ptrW) && !needBoxK && !keyKind3 &&
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
		ast.RcFreeEnabled && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) && !needBoxK && !keyKind3 &&
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
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
	if id.Name == "__method_Map_get_or" && len(n.TypeArgs) >= 2 && !keyKind3 &&
		b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) && !needBoxK && !needBoxV {
		if _, isStr := n.TypeArgs[1].(ast.StringType); isStr {
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			b.emit(Op{Kind: OpCallDirect, Str: "__method_Map_get_or", I32: int32(len(n.Args))})
			b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
			return nil
		}
	}
	// Struct/enum (keyKind-3) key: the tail-bound ops (set, has, and
	// non-boxed get_or — get / delete already routed through their
	// keyed-aware helpers above) dispatch through the `_keyed` runtime
	// variant. The struct key is a raw pointer (never boxed); only the
	// hash/eq dispatch differs, so set reuses emitWideMapSet (which
	// handles boxed AND non-boxed V and emits the keyed call via
	// emitMapCall) and get_or reuses emitWideMapGetOr when V is boxed.
	if keyKind3 {
		switch id.Name {
		case "__method_Map_set":
			return b.emitWideMapSet(n, n.TypeArgs[0], n.TypeArgs[1])
		case "__method_Map_has":
			if err := b.expr(n.Args[0]); err != nil {
				return err
			}
			if err := b.expr(n.Args[1]); err != nil {
				return err
			}
			b.emitMapCall("__method_Map_has", 2, n.TypeArgs[0])
			return nil
		case "__method_Map_get_or":
			if needBoxV {
				return b.emitWideMapGetOr(n, n.TypeArgs[0], n.TypeArgs[1])
			}
			for _, a := range n.Args {
				if err := b.expr(a); err != nil {
					return err
				}
			}
			b.emitMapCall("__method_Map_get_or", 3, n.TypeArgs[0])
			// Map[K, string].get_or native single-word retain: the
			// runtime hands back the string data pointer un-retained
			// (see the !keyKind3 inline above) — co-own it so the map's
			// drop doesn't free it from under the caller.
			if _, isStr := n.TypeArgs[1].(ast.StringType); isStr &&
				b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
			}
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
	// MapIter.key() over a struct/enum (keyKind-3) key: __mapiter_key_impl
	// returns the raw key pointer un-retained, but the `for (k, v) in m`
	// loop binds `k` as an OWNED struct/enum and drops it at each
	// iteration's scope exit — which would free the map's own key box and
	// corrupt the map (observed: a later keys()/entries() walk loses the
	// freed entry). rc_inc the returned pointer so the binding's drop
	// balances and the map keeps its key. Target-independent: a struct
	// key is a single pointer on every backend (#2671).
	if id.Name == "__method_MapIter_key" && keyKind3 {
		for _, a := range n.Args {
			if err := b.expr(a); err != nil {
				return err
			}
		}
		b.emit(Op{Kind: OpCallDirect, Str: id.Name, I32: int32(len(n.Args))})
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
		return nil
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
			b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
		(resultCannotAliasArg(b.exprType(n)) || b.returnsNoParamEscape[id.Name] ||
			b.resultIsCountedStringAlias(id.Name, b.exprType(n))) &&
		!b.pairForm[id.Name] && id.Name != "map_new" && !calleeRetainsAnyArg(id.Name)
	var argTempSlots []int32
	var argTempTypes []ast.Type
	// An `own` (consuming) parameter takes ownership of its argument, so the
	// callee — not the caller — reclaims a fresh temp passed there. Suppress the
	// stage-(b) post-call dec at those positions (else the temp is freed twice).
	ownArgFlags := b.info.OwnFuncs[id.Name]
	calleeSig := b.info.FuncSigs[id.Name]
	// ownedByCallee: the callee reclaims this argument — either an explicit `own`
	// param or (Slice 2) an owned-by-default one. Both suppress the stage-(b)
	// caller-side reclaim (the callee frees it) and, for owned-by-default, the
	// caller retains the arg with an inc below so the callee's exit dec balances.
	ownedByCalleeAt := func(ai int) bool {
		if ai < len(ownArgFlags) && ownArgFlags[ai] {
			return true
		}
		return calleeSig != nil && ai < len(calleeSig.Params) && b.calleeParamOwnedByDefault(id.Name, calleeSig.Params[ai], ai)
	}
	ownedByDefaultAt := func(ai int) bool {
		return !(ai < len(ownArgFlags) && ownArgFlags[ai]) &&
			calleeSig != nil && ai < len(calleeSig.Params) && b.calleeParamOwnedByDefault(id.Name, calleeSig.Params[ai], ai)
	}
	for ai, a := range n.Args {
		toOwnParam := ownedByCalleeAt(ai)
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
			if !ok {
				tt, ok = b.appendCopyTempType(a)
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
		// Slice 2 (OwnedByDefault): an owned-by-default argument is reclaimed by
		// the callee at exit. An ALIASED arg (a live local) is retained with an
		// inc so the callee's dec balances and the local keeps its reference; a
		// FRESH temp (a construction / fresh call) is MOVED — its rc=1 transfers
		// to the callee, which frees it (the stage-(b) caller reclaim is already
		// suppressed for it above). Mirrors StructLit field handling. An `own`
		// param is a separate move and is excluded.
		if ownedByDefaultAt(ai) && needsRcIncOnAlias(a, b) && !b.rc.moveSites[a] {
			b.emitAliasInc(a)
		}
		// An explicit `own` position whose argument is one of THIS function's
		// `own` params, at an occurrence move-on-call did NOT claim, is a
		// transfer with nothing behind it: the callee consumes a reference and
		// the exit sweep still decs the param, so the box is released twice.
		// computeMovedLocals only claims the param's textually-LAST occurrence,
		// which on a branchy function is a DIFFERENT one (`if (…) { return
		// consume(a); } return a;` — the consume is not last, the bare `return
		// a` is), so retain here and let the callee's drop spend the extra.
		if ai < len(ownArgFlags) && ownArgFlags[ai] && b.ownArgNeedsRetain(a) {
			b.emitAliasInc(a)
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
	// takes two extra runtime-tag args so the stdlib can branch
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
			// #4873 caller-side containment: rc-bracket surviving args
			// whose buffers the callee may grow in place, so its
			// uniqueness gate takes the copy path and this function's
			// bindings keep interpreter (copy-on-shared) semantics.
			growBracket := b.growBracketArgs(n, id.Name)
			b.emitGrowBracket(growBracket, OpRcInc, "__fern_rc_inc")
			b.emit(Op{Kind: kind, Str: id.Name, I32: argCount, Width: width, Ext: extArgTypes(argTypes)})
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
			// #4873: restore the bracketed args' rc — the callee's copy
			// path left each buffer untouched, so this returns it to the
			// incoming count (the inc preceded it; never frees).
			b.emitGrowBracket(growBracket, OpRcDec, "__fern_rc_dec")
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
	b.emit(Op{Kind: OpCallIndirect, I32: int32(len(n.Args)), Ext: &OpExt{Sig: sig}})
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

// structUpdateFieldInits normalises a self-overwrite struct literal into one
// FieldInit per declared field, in declaration order. A plain literal already
// names every field and is returned untouched (so its lowering is unchanged).
// A self-spread `p = T{ ...p, f: v }` names only the CHANGED fields; each
// un-listed field is filled in with the `p.<name>` the spread means, which
// fieldCarriedFrom then recognises as carried — elided on the reuse branch,
// stored + retained on the fresh-alloc one.
func structUpdateFieldInits(sl *ast.StructLit, sd *ast.StructDecl, t *ast.Ident) []ast.FieldInit {
	if sl.Base == nil {
		return sl.Fields
	}
	out := make([]ast.FieldInit, 0, len(sd.Fields))
	for _, fd := range sd.Fields {
		listed := false
		for _, f := range sl.Fields {
			if f.Name == fd.Name {
				out = append(out, f)
				listed = true
				break
			}
		}
		if !listed {
			out = append(out, ast.FieldInit{
				Name:  fd.Name,
				Value: &ast.FieldAccess{P: sl.P, Target: t, Field: fd.Name},
			})
		}
	}
	return out
}

func structReuseEligible(sd *ast.StructDecl) bool {
	for _, f := range sd.Fields {
		// Scalars of EVERY width are admitted (#4356 divergence 1): wide
		// i64/u64 and f32/f64 fields ride width-correct temp slots in the
		// self-overwrite path (the scratchType stamp sizes the slot and
		// payloadStoreOpFor picks the 8-byte store), and the general reuse
		// path stores fields width-correctly with no temps at all. Only the
		// two-word string shape stays out (its retain/release is per-ABI and
		// its slot fans into two words — a separate slice).
		if _, ok := f.Type.(ast.NumberType); ok {
			continue
		}
		if _, ok := f.Type.(ast.FloatType); ok {
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
// element is a scalar of any width (i32-class, i64/u64, f32/f64 — #4356
// divergence 1) OR a single-word rc-tracked pointer (array / struct / Map /
// enum / closure / tuple). Strings (two-word) stay excluded, exactly as for
// structs; the old-element release rides emitFieldDropOnStack.
func tupleReuseEligible(elems []ast.Type) bool {
	if len(elems) == 0 {
		return false
	}
	for _, e := range elems {
		if _, ok := e.(ast.NumberType); ok {
			continue
		}
		if _, ok := e.(ast.FloatType); ok {
			continue
		}
		if arrElemIsRcTracked(e) {
			continue
		}
		return false
	}
	return true
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
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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

// tryStructReuseOverwrite lowers a self-overwrite `p = T{ ... }` (where
// p is an owned, uniquely-droppable struct local of the same type T,
// every field of which is structReuseEligible) so the new value reuses
// p's old box in place when it's the sole owner — the Phase 5b/5c
// constructor-reuse (FBIP) win. Returns (true, err) when it took the
// reuse path (the caller returns immediately), (false, nil) when the
// shape isn't eligible and normal lowering should proceed.
//
// Soundness:
//   - Gated on b.rc.freeEligible[p] (OWNED, not a borrowed param / alias):
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
	// Struct-update spread `p = T{ ...p, field: v }`: admitted only when the
	// spread base is the assignment target itself, which makes every
	// un-listed field exactly `p.<name>` — i.e. carried, the case step 6b
	// already knows how to place on the fresh-alloc branch. Those synthetic
	// carried FieldInits are materialised below. A spread of any OTHER base
	// (`p = T{ ...q, f: v }`) still defers to the general StructLit lowering:
	// its un-listed fields come from a different box, so the fresh-alloc
	// branch would leave them uninitialised (read back as 0).
	if sl.Base != nil {
		bid, ok := sl.Base.(*ast.Ident)
		if !ok || bid.Name != t.Name {
			return false, nil
		}
	}
	st, ok := b.exprStaticType(t).(ast.StructType)
	if !ok || st.Name != sl.TypeName {
		return false, nil
	}
	sd, ok := b.info.Structs[st.Name]
	if !ok || !structReuseEligible(sd) {
		return false, nil
	}
	if !b.rc.freeEligible[t.Name] {
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
	for _, f := range structUpdateFieldInits(sl, sd, t) {
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
		if isPtr && !carried && needsRcIncOnAlias(f.Value, b) && !b.rc.moveSites[f.Value] {
			b.emitAliasInc(f.Value)
		}
		ts := b.allocSlot()
		b.locals[fmt.Sprintf("__reuse_fld_%d", ts)] = ts
		// Stamp the temp with the field's declared type so the backends
		// size the slot correctly — a wide i64/u64/f32/f64 field (#4356)
		// needs an 8-byte slot and width-matched load/store; the default
		// un-stamped slot is i32 and would truncate (natives) or fail
		// validation (wasm).
		b.scratchType[ts] = fieldType(sd.Fields, f.Name)
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
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
				b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
	if !b.rc.freeEligible[t.Name] {
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
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
		if dropName, ok := arrElemStructDropName(ty.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
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
				helper = "__fern_drop_arr_str"
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
			// Native single-word: free at rc==1 via __fern_str_dec (else
			// defer to __fern_rc_dec). The caller gates ownership
			// (emitVarReinitDropOld: freeEligible/unique/!moved; the call-arg
			// path: provably-fresh owned temp), so the free is balanced.
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
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
	case ast.DynTraitType:
		// `dyn Trait` reclamation on loop-body re-declaration
		// (docs/DYN-TRAITS.md §4.4) — mirrors the exit sweep's DynTraitType
		// branch. Without this a `var d: dyn Shape = C{...}` re-declared each
		// iteration leaks the prior iteration's concrete `data` object (and
		// anything it transitively owns) — the exit sweep only reclaims the
		// final iteration. The per-set __drop_dyn_<set> helper reads the
		// vtable drop slot and dispatches the concrete destructor; the
		// concrete dtor self-guards on rc==1. wasm (ptrW==4, slice 4a) passes
		// the inline two-word `[data, vtable]` (2 args); x86-64 (boxed, slice
		// 4b) passes the cell ptr (1 arg). arm64 leaks `dyn` (slice 4c) and
		// never reaches here (rcTracked is false for it, so the slot is never
		// an owned rc-tracked local the reinit path drops).
		if b.dynReclaim() {
			b.emit(Op{Kind: OpLoadLocal, I32: idx}) // wasm: [data, vtable]; native: cell ptr
			argc := int32(1)
			if b.ptrW == 4 {
				argc = 2
			}
			b.emit(Op{Kind: OpCallDirect, Str: dynDropFnName(ty.Traits), I32: argc})
		}
	}
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
		b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	// Cell value (data ptr on the stack): reclaim through the array
	// machinery keyed on the instantiation's element type — a cell is a
	// one-element array box, not a record. mayFree gates the actual free.
	if st, ok := t.(ast.StructType); ok && st.Name == "Cell" {
		b.emitCellDropOnStack(cellElemOf(t), mayFree)
		return
	}
	// `mayFree` is the borrow-aware permission to return this value's buffer to
	// the freelist. It's true only for OWNED top-level array locals
	// (computeFreeEligible); struct fields and enum payloads always pass false
	// (their borrow-ness isn't tracked, so they never free — conservative).
	if at, ok := t.(ast.ArrayType); ok && mayFree {
		// `dyn Trait[]` (#4351): not in arrElemIsRcTracked (dyn elements are
		// never inc'd — no rc header on the cells), but an ELIGIBLE dyn array
		// releases its elements through the dedicated __drop_arr_dyn_<set>
		// walk (arrElemStructDropName's dyn arm, gated on the backend's
		// dyn-RC capability there). Without this the function-exit sweep fell
		// to the plain box dec below and leaked every element. NATIVES ONLY
		// (ptrW==8) — see the exit-sweep arm's wasm caveat.
		_, elemIsDyn := at.Elem.(ast.DynTraitType)
		if arrElemIsRcTracked(at.Elem) || (elemIsDyn && b.ptrW == 8 && b.dynReclaim()) {
			// Transitive reclamation Stage B: an array of CONCRETE structs drops
			// each element box deeply (via __drop_arr_struct_<Elem> →
			// __drop_struct_<Elem> per element) before freeing the buffer, instead
			// of the flat per-element rc_dec __fern_drop_arr_ptr does. Gated on
			// RcFreeEnabled to match the genfn post-pass.
			if name, ok := arrElemStructDropName(at.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok && ast.RcFreeEnabled {
				b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
				b.emit(Op{Kind: OpDrop})
				return
			}
			if !elemIsDyn {
				b.emit(Op{Kind: OpConstI32, I32: int32(b.ptrW)})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_ptr", I32: 2})
				b.emit(Op{Kind: OpDrop})
				return
			}
			// dyn elements with no generatable walk (flag-off): fall through
			// to the plain dec — __fern_drop_arr_ptr would rc_dec headerless
			// cells.
		}
	}
	// __fern_rc_dec is a void-returning runtime helper but OpCallDirect's
	// codegen always pushes the call's return-value register onto the operand
	// stack; drop the bogus push to keep the stack balanced.
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
		b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
	// `dyn Trait` payload (enum variant-plan / inline struct field): the caller
	// loaded the boxed one-word cell ptr via payloadLoadOpFor. __drop_dyn_<set>
	// returns VOID, so argc is 1 and there is NO trailing OpDrop (unlike the
	// dropFnNameFor branch below, whose drop fns return the ptr). NATIVES ONLY
	// (b.dynRcSupported): wasm's inline two-word `dyn` double-drops when the
	// payload is matched-and-bound, so it stays correct-but-leaking there;
	// see appendChildDrop's dyn arm + docs/DYN-TRAITS.md §7.8.
	if dt, isDyn := t.(ast.DynTraitType); isDyn && b.dynRcSupported {
		b.emit(Op{Kind: OpCallDirect, Str: dynDropFnName(dt.Traits), I32: 1})
		return
	}
	if name, ok := dropFnNameFor(t, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
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
		if name, ok := arrElemStructDropName(at.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
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
			helper = "__fern_drop_arr_str"
		}
		b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
		b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
		b.emit(Op{Kind: OpDrop})
		return
	}
	b.decValueOnStack(t, false)
}

func (b *builder) emitStructEnumSlotDrop(idx int32, ty ast.Type) {
	// A Cell slot reinit / reassign reclaims the old box through the array
	// machinery (dropFnNameFor declines Cell). Owned here, so eligible=true;
	// the helper's null / rc==1 guards are the safety net.
	if st, ok := ty.(ast.StructType); ok && st.Name == "Cell" {
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		b.emitCellDropOnStack(cellElemOf(ty), true)
		return
	}
	if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
	if _, ok := enumVariantDropPlan(ed, b.ptrW, b.dynRcSupported); !ok {
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
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
}

// emitTryBoxFreeVariant frees the `?`-consumed source box at a consume edge
// where the box's tag is statically PROVEN to be `varIdx` (see
// reclaimableTryScrutinee): variant 0 (Ok/Some) on the success path after the
// payload was extracted, variant 1 (Err) on a pair-form enclosing function's
// failure path after the (tag, payload) pair was copied out for OpReturnPair.
// The box is freed with THAT variant's EXACT payload size — uniformity across
// variants is not required, covering Result[string, i32]'s ragged layout.
// SHALLOW: no payload deep-drop — a scalar payload was copied out, a pointer
// payload's reference MOVED to the consumer. A payloadless variant (Option's
// None) is a static sentinel with no box, so it emits nothing. is_unique-
// gated: an aliased box (rc>=2 via the return-transfer inc) is only dec'd,
// never freed. Net-zero on the operand stack — extracted values beneath are
// untouched.
func (b *builder) emitTryBoxFreeVariant(slot int32, et ast.EnumType, varIdx int) {
	ed, ok := b.info.Enums[et.Name]
	if !ok {
		return
	}
	if len(et.Args) > 0 {
		ed = substituteEnumDecl(ed, et.Args)
	}
	if varIdx >= len(ed.Variants) || len(ed.Variants[varIdx].Payloads) == 0 {
		return
	}
	for _, pt := range ed.Variants[varIdx].Payloads {
		if _, isParam := pt.(ast.ParamType); isParam {
			return // unresolved generic payload — size unknown, keep the leak
		}
	}
	_, size := payloadLayout(ed.Variants[varIdx].Payloads, len(ed.Variants[varIdx].Payloads), b.ptrW)
	b.emitTryBoxFreeSized(slot, size)
}

// emitTryBoxFreeSized is emitTryBoxFree's emission half, shared with the
// pair-form rebox free (tryPairReboxSize) whose box size comes from the
// repack layout instead of the enum decl.
func (b *builder) emitTryBoxFreeSized(slot int32, size int32) {
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: slot})
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
}

// tryPairReboxSize reports the exact data size of the heap box
// emitRepackPairAsHeapBox allocates for a PAIR-FORM callee's result at a `?`
// site, when the try's inner is such a direct call. The TryOp lowering does
// not suppress the rebox (unlike Match / IfLet / LetElse scrutinees), so a
// pair-form `mk(pre)?` allocates one fresh rc=1 box per evaluation whose only
// consumer is the try dispatch — dead after the success payload load and
// never dec'd (leaked one box per iteration). The size mirrors
// emitRepackPairAsHeapBox exactly: 8 for an i32-payload pair, 16 when the
// payload is pointer-shaped on an 8-byte target.
func (b *builder) tryPairReboxSize(inner ast.Expr) (int32, bool) {
	if !ast.RcFreeEnabled {
		return 0, false
	}
	call, ok := inner.(*ast.Call)
	if !ok {
		return 0, false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return 0, false
	}
	if _, isLocal := b.locals[id.Name]; isLocal {
		return 0, false
	}
	sig, ok := b.info.FuncSigs[id.Name]
	if !ok || sig == nil || !b.pairForm[id.Name] {
		return 0, false
	}
	boxSize := int32(8)
	if b.payloadWidthForCalleeReturn(sig.Result) == WidthPtr && b.ptrW == 8 {
		boxSize = 16
	}
	return boxSize, true
}

// emitOwnedConsumingArmDrop releases an OWNED-BY-DEFAULT enum scrutinee's box
// inside a consuming-match arm (#4400) — the Koka drop-specialization for the
// COUNTED ownership model, the sibling of emitConsumingMatchBoxFree's move
// model. After the arm's bindings are extracted:
//
//	if __fern_rc_is_unique(box):
//	    __fern_box_free(box, size)      // shallow — the box's counted payload
//	                                    // references transfer to the bindings
//	                                    // (dup + child-dec cancelled statically)
//	else:
//	    __fern_rc_inc(binding) …        // dup: each qualifying pointer binding
//	                                    // becomes a second counted owner
//	    __fern_rc_dec(box)              // release our reference; the box (and
//	                                    // its payload counts) stays with the
//	                                    // other owners
//
// then the PARAM's slot is zeroed so the exit sweep's deep-drop no-ops (the
// helpers' null / low-address guards make a zeroed slot inert). dupSlots holds
// the slots of this arm's consumingBindings — single-word box pointers by
// construction (enum / user struct / tuple; string / array payloads are
// excluded by the isOwnedByDefaultType gate). Uniform-box enums only; anything
// else keeps the box for the exit sweep (safe, and excluded by the analysis
// gates anyway).
func (b *builder) emitOwnedConsumingArmDrop(ptrSlot int32, et ast.EnumType, dupSlots []int32, paramName string) {
	paramSlot, ok := b.locals[paramName]
	if !ok {
		return
	}
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
	b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	// Unique: free the box BUFFER only — the bindings inherit the payload
	// counts. Same shape as the `own`-param shallow free.
	b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpElse})
	// Shared: dup the counted bindings, then release our box reference.
	for _, slot := range dupSlots {
		b.emit(Op{Kind: OpLoadLocal, I32: slot})
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
	// Dead the param slot: the scrutinee is consumed on this path, so the
	// exit sweep (which still visits the param) must see a null.
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: paramSlot})
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
			// Always adopt the substituted decl. The generic decl's payloads are
			// ParamTypes, which uniformEnumDropLoads / uniformEnumBoxSize cannot
			// size or classify, so the uniform path was skipped and the box
			// leaked for a SCALAR instantiation: `var o = Some(k)` allocated 16
			// bytes per construction and never freed them (#5917).
			//
			// The previous gate gave this up on the stated grounds that a scalar
			// instantiation is "pair-form, no box". That conflates two things:
			// pair-form is a per-FUNCTION return ABI (findPairFormFuncs, keyed by
			// function name), describing how a callee hands an Option back — not
			// how an Option LOCAL is represented. Measured, a local is boxed in
			// every shape, including one bound from a pair-form-eligible callee.
			// Substitution is strictly more informative than the generic decl, so
			// there is no shape it makes worse.
			ed = substituteEnumDecl(ed, et.Args)
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
			b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
		// This inline tag-switch drops each variant's payloads via
		// b.dropStructField, which now has a `dyn` arm (right argc + void, no
		// trailing drop) — so an enum-with-dyn-payload reclaims its `dyn` here
		// too on a dyn-RC backend (docs/DYN-TRAITS.md §7.8). The generated
		// __drop_enum_ route handles the per-iteration loop-var reinit drop.
		if plan, ok := enumVariantDropPlan(ed, b.ptrW, b.dynRcSupported); ok {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			// One shared per-function tag stash (#4476): allocated on first
			// use, reused by every later inline enum slot-drop. Sound because
			// the stash is stored and fully consumed within this one drop
			// sequence (the per-variant tag compares below) before any other
			// drop sequence can run.
			if b.enumDropTagSlot < 0 {
				b.enumDropTagSlot = b.allocSlot()
				b.locals[fmt.Sprintf("__enum_drop_tag_%d", b.enumDropTagSlot)] = b.enumDropTagSlot
			}
			tagSlot := b.enumDropTagSlot
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpLoad}) // tag at [data+0]
			b.emit(Op{Kind: OpStoreLocal, I32: tagSlot})
			for _, vd := range plan {
				b.emit(Op{Kind: OpLoadLocal, I32: tagSlot})
				b.emit(Op{Kind: OpConstI32, I32: int32(vd.tag)})
				b.emit(Op{Kind: OpEq})
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				for _, ld := range vd.loads {
					if isMapType(ld.typ) {
						// Map-in-enum DOCUMENTED SAFE LEAK (#4425) — the inline
						// local-drop sibling of genEnumDropFn's skip. A Map-payload
						// variant reclaims via __map_drop_values (dropStructField),
						// which lives in core/map.fern — a program can use the enum
						// WITHOUT importing it (no map operations, e.g. a local
						// `JsonValue` bound to `JString(...)`), so that call was to an
						// unloaded symbol (wasm "unknown callee" / native "undefined
						// label"). Skip the map reclaim: the map leaks (safe), matching
						// the enum's EnumRcPayloads exclusion; the box is freed below.
						continue
					}
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
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpEnd})
			return
		}
	}
	if edOk {
		if loads, ok := uniformEnumDropLoads(ed, b.ptrW); ok {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
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
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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
	if name, ok := dropFnNameFor(t, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
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

// callConsumesIdent reports whether `e` is a direct call to an `own`-parameter
// function that passes `name` to one of its `own` positions — i.e. the call
// CONSUMES `name`, moving it into the callee, which deep-drops it at its own
// exit. A `name = f(.., name, ..)` self-reassign must then NOT also
// overwrite-drop the old `name`: the callee already owns and frees it, so a
// second deep-drop here frees the box twice (the own-struct-param
// move-and-rebind double-free). The mirror of constructionMovesIdent for plain
// function calls.
//
// Method calls are INCLUDED: `s = s.emit(x)` on an `own`-receiver method
// reaches here as a plain Call on the mangled method name with the receiver
// in Args[0] and flags[0] the receiver's own flag, so the loop below covers
// it unchanged. The receiver position MUST get the same suppression as a
// plain own arg — the checker's E051 admission (SelfReassignOwnMoveArg) has
// no method exclusion, and without the matching skip here the callee-side
// receiver drop plus this overwrite-dec net -1 per call: a threaded borrowed
// param (consumedParams entry-inc) loses the CALLER's reference (UAF one
// frame up), and a local receiver's box is freed by the callee and then this
// dec writes through the freed header (freelist corruption — the source of
// the arm64 rc-underflow counts the own-receiver probes surfaced).
func (b *builder) callConsumesIdent(e ast.Expr, name string) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	flags, isOwn := b.info.OwnFuncs[id.Name]
	if !isOwn {
		return false
	}
	for i, a := range call.Args {
		if i < len(flags) && flags[i] {
			if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
				return true
			}
		}
	}
	return false
}

// emitCountedYield lowers a conditional-expression arm body (if-expr /
// match-expr yield) and incs an ALIASED pointer-shaped result — the
// needsRcIncOnAlias shapes — so the expression's value is an OWNED
// reference whichever arm ran (#4399 sink 4). A fresh arm value (call
// result / literal / constructor) reads false and moves out as-is.
// computeFreeEligible drops the escape taint for exactly these shapes.
func (b *builder) emitCountedYield(e ast.Expr) error {
	if err := b.expr(e); err != nil {
		return err
	}
	if needsRcIncOnAlias(e, b) {
		b.emitAliasInc(e)
	}
	return nil
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
		// Mark a self-append RHS (`a = a.append(v)`) before lowering it so
		// emitArrayPush keeps that exact push on the MOVE-semantics plain
		// grow — its overwrite reclaim below is the buffer-only
		// __fern_arr_dec that pairs with a non-retaining copy (see
		// selfPushMoveCall / the #3425 retain routing in emitArrayPush).
		if b.isSelfArrayPushLocal(n.Value, t.Name) {
			b.selfPushMoveCall = n.Value
		}
		// Mark a string self-append RHS (`s = s + piece`) the same way, so
		// the concat lowering emits the in-place-when-unique
		// __fern_str_append rather than OpStrConcat's unconditional
		// allocate-and-copy-both (#5637 option 3). Node identity keeps a
		// nested concat inside `piece` on the plain OpStrConcat path.
		// Saved / restored around the RHS so an assignment nested INSIDE
		// `piece` (assignment is an expression here) runs with its own
		// marking and hands this one's back untouched.
		prevAppendBin, prevAppendDone := b.selfStrAppendBin, b.selfStrAppendDone
		b.selfStrAppendBin, b.selfStrAppendDone = nil, false
		if b.isSelfStrAppendLocal(n.Value, t.Name) {
			b.selfStrAppendBin = n.Value
		}
		err := b.expr(n.Value)
		b.selfPushMoveCall = nil
		strAppended := b.selfStrAppendDone
		b.selfStrAppendBin, b.selfStrAppendDone = prevAppendBin, prevAppendDone
		if err != nil {
			return err
		}
		// Phase 1d: same alias-bump as the Var-binding path —
		// `y = x;` shares an existing array reference, so the
		// new binding needs its own rc. Move-on-alias skips the inc
		// at a move site (see the Var path).
		if needsRcIncOnAlias(n.Value, b) && !b.rc.moveSites[n] {
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
			if flagSlot, hasFlag := b.locals[consumedArrayFlagName(t.Name)]; hasFlag &&
				isSelfArraySetReassign(n.Value, t.Name) && b.isConsumedArrayParam(t.Name) {
				// `p = p.with(i, v)` on a consumed-threaded ARRAY param. Whether
				// an overwrite dec is owed depends on whether THIS frame owns
				// the old buffer, which for such a param is a runtime fact —
				// the ownership bit — not a static one:
				//
				//   - bit 0 (still the caller's borrow): the forced #2832 inc
				//     put the buffer at rc >= 2, so cow_inplace copies and DECS
				//     THE SOURCE ITSELF, cancelling that inc. The frame's books
				//     are square; dec'ing again releases the CALLER's reference.
				//     `bump(xs) { xs = xs.with(0, 99) }` called as `a = bump(a)`
				//     drove the caller's buffer to rc -1 while still emitting
				//     correct output (#6057).
				//   - bit 1 (a copy this frame already owns): the helper's dec
				//     only cancels the forced inc, so the frame's own claim on
				//     the previous copy is still outstanding and MUST be
				//     released, or every loop iteration orphans a buffer.
				//
				// Either way the frame owns what comes back, so the bit is set.
				// No `else` arm for new == old: an in-place cow released
				// nothing and nothing changed hands. (That branch is anyway
				// unreachable here — the forced inc guarantees rc >= 2.)
				//
				// The old grouping under isSelfMapMutation could express
				// neither case: it dec'd purely on "did the pointer change",
				// which is right for __map_cow_inplace — that helper documents
				// leaving "the source handle's rc to the normal dec-on-overwrite
				// at the assignment site" — and wrong for the array helper,
				// which decs its own source.
				setAt, _ := localArrayType(t.Name, b)
				stride := int32(ast.ElemSizeBytesFor(setAt.Elem, b.ptrW))
				newTmp := b.allocSlot()
				b.locals[fmt.Sprintf("__setown_new_%d", newTmp)] = newTmp
				b.emit(Op{Kind: OpStoreLocal, I32: newTmp})
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
				b.emit(Op{Kind: OpNe})
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				b.emit(Op{Kind: OpLoadLocal, I32: flagSlot})
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpConstI32, I32: stride})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpEnd})
				b.emit(Op{Kind: OpConstI32, I32: 1})
				b.emit(Op{Kind: OpStoreLocal, I32: flagSlot})
				b.emit(Op{Kind: OpEnd})
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
			} else if isSelfArraySetReassign(n.Value, t.Name) {
				// Array `.with` self-reassign on anything OTHER than a
				// consumed-threaded param (an owned local, or a local aliasing
				// one). No overwrite dec: __fern_arr_cow_inplace balances the
				// receiver itself on both branches — in place it hands back the
				// same buffer having released nothing, and on the copy branch it
				// DECS the source, which is precisely this frame's claim. The
				// pointer-changed test that serves __map_cow_inplace (whose doc
				// leaves "the source handle's rc to the normal dec-on-overwrite
				// at the assignment site") therefore double-releases here: for
				// `var a = acc; a = a.with(..)` the second dec landed on the
				// CALLER's buffer (#6057).
			} else if isSelfMapMutation(n.Value, t.Name) {
				newTmp := b.allocSlot()
				b.locals[fmt.Sprintf("__selfmap_new_%d", newTmp)] = newTmp
				b.emit(Op{Kind: OpStoreLocal, I32: newTmp}) // stash new (RHS result)
				b.emit(Op{Kind: OpLoadLocal, I32: idx})     // old handle
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp})  // new handle
				b.emit(Op{Kind: OpNe})                      // cow copied?
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpEnd})
				b.emit(Op{Kind: OpLoadLocal, I32: newTmp}) // restore new for the store
			} else if at, isArr := localArrayType(t.Name, b); isArr && ast.RcFreeEnabled && !b.callConsumesIdent(n.Value, t.Name) && (b.rc.freeEligible[t.Name] || b.selfReassignOwnedLocal(n.Value, t.Name, at) || b.isSelfArrayPushLocal(n.Value, t.Name)) {
				// Phase 3 step-4: free the OLD array buffer at rc==0.
				// On a push copy-grow the old buffer's pointer elements
				// were transferred to the new buffer (no inc), so freeing
				// the buffer — not walking elements — is correct; the
				// in-place push (rc bumped to 2) dec's to 1 and doesn't
				// free. This is the O(N²)→O(N) push-loop reclamation.
				//
				// Gated on freeEligible OR selfReassignOwnedLocal: only
				// OWNED array locals free here. Borrowed / borrowed-derived
				// locals (params, and anything aliased from them - e.g. the
				// self-host VM's `ops` threaded through compile_stmt/
				// compile_block) keep the plain dec: the owner upstream
				// still references the buffer (the borrow model gives no
				// caller-side inc, so the rc undercounts the borrow -
				// freeing would be a use-after-free). See computeFreeEligible.
				//
				// The selfReassignOwnedLocal escape mirrors the struct /
				// enum branch below: a self-reassign `a = a.append(x)` /
				// `a = f(a)` taints `a` out of the conservative freeEligible
				// set (the call result may alias it), but the rc-gated buffer
				// free is balanced anyway — __fern_arr_dec only reclaims at
				// rc==0, and typeSelfDropSafe restricts this to arrays whose
				// elements are inc'd at construction (no Maps; strings joined
				// the counted set with the #3425 admission).
				// Without it `a = a.append(x)` in a loop leaked the OLD buffer
				// on every grow (the copy path orphans it; the flat
				// __fern_rc_dec the catch-all else emits never frees) — the
				// dominant churn in the self-host SSA build_func loops.
				stride := int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))
				arrDec := func() {
					b.emit(Op{Kind: OpLoadLocal, I32: idx})
					b.emit(Op{Kind: OpConstI32, I32: stride})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
					b.emit(Op{Kind: OpDrop})
				}
				if b.isConsumedArrayParam(t.Name) {
					b.emitConsumedArrayOverwriteDec(t.Name, arrDec)
				} else {
					arrDec()
				}
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
			} else if b.callConsumesIdent(n.Value, t.Name) {
				// `s = f(.., s, ..)` where f takes s by `own`: s is MOVED into f,
				// which deep-drops it at its own exit. Dropping it here too frees
				// the box twice — the own-struct-param move-and-rebind double-free.
				// No drop: the callee owns it.
				//
				// ARRAY targets reach here too (#6013). This comment used to say
				// they "took the rc-gated __fern_arr_dec branch above, which is
				// double-free-safe" — it is not. rc-gating only means the free
				// happens at rc==0, and after the move there is no reference left
				// to spend: `c = wr(c, …)` with `wr(own buf: i32[])` hands the
				// slot's only reference to the callee, which returns it (the
				// cow_inplace rc==1 path returns the SAME pointer), so the dec
				// takes the live buffer from 1 to 0 and frees what the caller just
				// stored. It read as correct because a freed-then-immediately-
				// reused block still holds its old bytes: the same program was
				// right with two calls and returned a corrupted length with four.
				// The self-append / self-map / construction-move shapes are
				// unaffected — their RHS is a method call or constructor, never an
				// `own`-flagged user function, so callConsumesIdent is false.
			} else if sety, isSE := structOrEnumTypeOfLocal(t.Name, b); isSE && ast.RcFreeEnabled && (b.rc.freeEligible[t.Name] || b.selfReassignOwnedLocal(n.Value, t.Name, sety)) {
				// Struct / enum reassignment-overwrite — `s = Other{...}` /
				// `e = Variant(...)` ends the old binding's ownership exactly
				// like a scope exit (or a loop reinit) would, so deep-drop the
				// OLD box rather than the flat __fern_rc_dec the catch-all else
				// emits (which neither frees the box nor recurses, leaking the
				// box + nested fields). Shares emitStructEnumSlotDrop with the
				// reinit path — routes through __drop_struct_ / __drop_enum_
				// when droppable, flat dec otherwise (non-uniform generic
				// enums). Gated on freeEligible like the array / string
				// siblings: only an OWNED (untainted) local frees here — a
				// borrowed / escaped one keeps the plain dec, so a live alias is
				// never reclaimed out from under. The in-place reuse paths
				// (tryStructReuseOverwrite / tryEnumReuseOverwrite) returned
				// early above, so this is only the genuine-overwrite case.
				// Net-zero on the operand stack, leaving the new RHS value for
				// the store below.
				//
				// A Map handle is COW-aware, because the RHS may BE the handle
				// the slot already holds: __map_cow_inplace returns the
				// receiver unchanged when it mutates in place, so `t =
				// t.insert(a, 1).insert(b, 2)` stores back exactly what it
				// overwrote. The binding therefore owes a release only when
				// the reference genuinely changed hands:
				//
				//   - new != old — the old handle lost this binding's claim.
				//     Release it with the BUF-AND-HANDLE free, not the flat
				//     dec (which frees nothing: every COW-copied table leaked,
				//     1328 B an iteration in the temporary-bound insert loop
				//     of #6227) and not emitMapSlotDrop's full walk (which the
				//     exit sweep and the reinit path use). The column walks
				//     must NOT run here — __map_cow_inplace copies the kv
				//     buffer SHALLOWLY, so the fresh handle being stored
				//     shares the old one's key / value pointers, and freeing
				//     the key column pulls those strings out from under it
				//     (SIGSEGV under qemu-aarch64; #6242 is the shallow copy
				//     itself, and widens this back once it lands). The old BUFFER is
				//     exclusively the old handle's, so that part is
				//     unambiguously owed; __fern_map_drop self-guards on
				//     rc==1, so a still-shared handle only dec's.
				//   - new == old — the same reference carried across the
				//     rebind. A release is owed only if an alias inc created a
				//     second count for it (`m = m2`); a self-mutation created
				//     none, and dec'ing there is the over-release
				//     isSelfMapMutation's COW-aware branch exists to avoid.
				if mst, isMap := sety.(ast.StructType); isMap && mst.Name == "Map" {
					aliasInced := needsRcIncOnAlias(n.Value, b) && !b.rc.moveSites[n]
					newTmp := b.allocSlot()
					b.locals[fmt.Sprintf("__mapow_new_%d", newTmp)] = newTmp
					b.emit(Op{Kind: OpStoreLocal, I32: newTmp})
					b.emit(Op{Kind: OpLoadLocal, I32: idx})
					b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
					b.emit(Op{Kind: OpNe})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					b.emit(Op{Kind: OpLoadLocal, I32: idx})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
					b.emit(Op{Kind: OpDrop})
					if aliasInced {
						b.emit(Op{Kind: OpElse})
						b.emit(Op{Kind: OpLoadLocal, I32: idx})
						b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
						b.emit(Op{Kind: OpDrop})
					}
					b.emit(Op{Kind: OpEnd})
					b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
				} else {
					b.emitStructEnumSlotDrop(idx, sety)
				}
			} else {
				flatDec := func() {
					b.emit(Op{Kind: OpLoadLocal, I32: idx})
					b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
				}
				// A consumed-threaded array param that borrow-taint kept out
				// of freeEligible lands here. It gets no entry retain either
				// (isConsumedArrayParam), so this flat dec would steal the
				// caller's count — the #6021 undercount — unless it is gated
				// on the same ownership flag.
				if b.isConsumedArrayParam(t.Name) {
					b.emitConsumedArrayOverwriteDec(t.Name, flatDec)
				} else {
					flatDec()
				}
			}
		} else if strAppended {
			// `s = s + piece` lowered to __fern_str_append, which took over
			// the old buffer: it either grew it in place (still uniquely
			// held — nothing to release) or copied into a fresh one and ran
			// the same __fern_str_dec this branch would have. Dec'ing again
			// here would over-release. See isSelfStrAppendLocal.
		} else if isStringTypeOfLocal(t.Name, b) && ast.RcFreeEnabled && b.rc.freeEligible[t.Name] {
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
			//     ptr, __fern_str_dec, drop. Its null / inline-SSO /
			//     below-heap-literal guards keep every non-heap source safe,
			//     and at rc==1 it FREES the buffer (box_free at length+1)
			//     rather than merely decrementing. It used __fern_rc_dec
			//     until #5637: rc_dec drops the count to zero and stops, so
			//     every intermediate of the pervasive `s = s + piece`
			//     accumulator was orphaned outright — 757 KB live at exit for
			//     500 iterations of a 6-byte-per-iteration chain. Freeing is
			//     the SAME authorisation the exit sweep already exercises for
			//     these locals (emitDec's StringType arm calls __fern_str_dec
			//     on every ptrW) under the SAME freeEligible gate, so this is
			//     the sweep's rule applied at the overwrite, not a new one.
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
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		} else if tt, isTup := tupleTypeOfLocal(t.Name, b); isTup && ast.RcFreeEnabled && b.rc.freeEligible[t.Name] {
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
		// read path: stride 1 / 4 / 8 picks helper +
		// `i32.store8` / `i32.store` / `f32.store`. Float arrays
		// use OpFStore.
		stride := int32(4)
		storeOp := OpStore
		storeWidth := 0
		if t.ElemType != nil {
			stride = int32(ast.ElemSizeBytesFor(t.ElemType, b.ptrW))
			if nt, ok := t.ElemType.(ast.NumberType); ok {
				switch nt.NormalWidth() {
				case 8:
					storeOp = OpStoreI8
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
				// A boxcapture cell is a shared mutable reference (a closure and
				// the outer scope alias it deliberately), so its `cell[0] = v`
				// write must store IN PLACE — never CoW, which would fork the
				// cell whenever a closure also holds it (rc > 1) and silently
				// drop the sharing. Fall through to the direct in-place store.
				if b.info != nil && b.info.BoxedCells[arrIdent.Name] {
					// (skip the CoW dispatch below)
				} else if slot, isLocal := b.locals[arrIdent.Name]; isLocal && isArrayTypeOfLocal(arrIdent.Name, b) && !isParamName(arrIdent.Name, b) {
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
// isSelfCowRebind reports whether `value` is a self-rebinding COW mutation of
// `targetName`: `m = m.insert(..)` / `m = m.cleared()` / `a = a.with(..)` /
// `a = a.append(..)`. Every one returns the SAME handle when the receiver is
// uniquely owned and a fresh copy otherwise, so a handle that started fresh
// stays param-free across the rebind — which is all computeFreshLocals needs.
//
// Deliberately WIDER than isSelfMapMutation, which the assign-site dec uses:
// there the array-push form takes its own path (isSelfArrayPushLocal → the
// buffer-only __fern_arr_dec that pairs with push's non-retaining copy), so
// the two must stay separate predicates even though the freshness argument is
// identical for both. Merging them would route push through the map COW dec.
func isSelfCowRebind(value ast.Expr, targetName string) bool {
	if isSelfMapMutation(value, targetName) {
		return true
	}
	call, ok := value.(*ast.Call)
	if !ok {
		return false
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Array_push" || len(call.Args) == 0 {
		return false
	}
	recv, ok := call.Args[0].(*ast.Ident)
	return ok && recv.Name == targetName
}

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
	// `__method_Array_set` is `arr.with(i, v)` — like the map mutators it
	// goes through a `*_cow_inplace` helper that returns the SAME handle on
	// rc==1 (no reference released) and a fresh copy on rc>1. So `arr =
	// arr.with(...)` needs the same COW-aware dec (dec the old handle iff a
	// copy happened) as `m = m.set(...)`; an unconditional dec over-releases
	// the in-place handle (the rc-underflow / unbounded-leak the wasm
	// OwnInplaceSort + LiteralAllocReclaim tests caught).
	//
	// A consumed-threaded ARRAY PARAM is the one receiver this cannot serve,
	// and the assign lowering peels it off before reaching here: its
	// ownership is a runtime bit, so "did the pointer change" cannot say
	// whether a dec is owed. See the isSelfArraySetReassign branch (#6057).
	if callee.Name != "__method_Map_set" && callee.Name != "__method_Map_clear" {
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	recv, ok := call.Args[0].(*ast.Ident)
	return ok && recv.Name == targetName
}

// isSelfArraySetReassign reports whether `value` is `name.with(i, v)` — the
// desugared `__method_Array_set` — reassigned back to `name`. Split out of
// isSelfMapMutation because __fern_arr_cow_inplace self-balances the
// receiver's refcount and __map_cow_inplace does not, so the two shapes owe
// opposite things at the assignment site.
func isSelfArraySetReassign(value ast.Expr, targetName string) bool {
	call, ok := value.(*ast.Call)
	if !ok {
		return false
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Array_set" || len(call.Args) == 0 {
		return false
	}
	recv, ok := call.Args[0].(*ast.Ident)
	return ok && recv.Name == targetName
}

// isMapMutatorCall reports whether e is a call to one of the Map COW
// mutators (`m.insert` / `.without` / `.cleared`, mangled to
// __method_Map_set / _delete / _clear). These go through __map_cow_inplace
// and, when the receiver map is uniquely owned (rc<=1 — the borrow case),
// mutate it IN PLACE and return the SAME handle rather than a fresh copy.
// Storing such a result into a freshly-constructed container therefore
// aliases the receiver's buffer; the container must clone it to stay
// independent (issue #2763 — Map field in a value-type struct double-freed).
func isMapMutatorCall(e ast.Expr) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "__method_Map_set", "__method_Map_delete", "__method_Map_clear":
		return true
	}
	return false
}

// isMapDeleteCall reports whether e is `m.without(k)` — the one Map COW
// mutator whose result is a (Map, boolean) TUPLE rather than the map, so the
// COW-seam retain has to be applied to the handle going into the tuple (see
// emitMapDeleteReturningTuple) instead of to the call's own result.
func isMapDeleteCall(e ast.Expr) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	return ok && id.Name == "__method_Map_delete"
}

// mapCowRetainReceiver returns the receiver of a Map COW mutator call that
// owes the COW-seam retain, or nil when it does not.
//
// A mutator's result is meant to be an owned reference like any other call
// result, but __map_cow_inplace only makes it one on the aliased branch:
//
//   - rc > 1 (aliased): a fresh deep copy at rc=1. The result is its only
//     owner — already owned, nothing to retain.
//   - rc <= 1 (sole owner): the SAME handle, un-retained. Whoever takes the
//     result and the receiver's binding then share ONE count, and both
//     release it (#6227 — `var m2 = m.insert(k, v); m = m2;` dropped entries
//     silently; the `without` spelling, whose tuple return cannot be written
//     any other way, freed the handle and SEGV'd on the next probe).
//
// Two conditions make the retain owed, one static per condition:
//
//   - The result is BOUND (mapCowBindSites) — otherwise it is a temporary
//     nobody releases and the retain leaks a whole table per evaluation.
//   - The receiver names a binding that survives the call: an lvalue that is
//     not moved into it. A temporary receiver transfers its own rc=1 to the
//     result and owes nothing.
//
// The runtime half — did COW actually copy? — is emitMapCowRetainTest.
// Restricted to Ident / FieldAccess so the emitted pointer compare can
// re-read the receiver without re-running side effects.
func (b *builder) mapCowRetainReceiver(e ast.Expr) ast.Expr {
	call, ok := e.(*ast.Call)
	if !ok || !isMapMutatorCall(call) || len(call.Args) == 0 {
		return nil
	}
	if !b.rc.mapCowBindSites[call] {
		return nil
	}
	recv := call.Args[0]
	switch recv.(type) {
	case *ast.Ident, *ast.FieldAccess:
	default:
		return nil
	}
	if !isMapType(b.exprType(recv)) || b.rc.moveSites[recv] {
		return nil
	}
	return recv
}

// isBorrowedMapFieldResultMove reports whether e is an ident that (a) names a
// local bound to a Map COW-mutator with a field-access receiver
// (borrowedMapFieldResults — the #4871 alias shape) and (b) is MOVED into the
// field at this occurrence. Both must hold for the clone at the StructLit site
// to be sound: the clone gives the container an independent buffer, and the
// move guarantees the aliasing local is not also exit-dec'd (which would free
// the original out from under the still-live source container).
func (b *builder) isBorrowedMapFieldResultMove(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return b.rc.borrowedMapFieldResults[id.Name] && b.rc.moveSites[e]
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

// strAppendAvailable reports whether this target's backend emits the
// __fern_str_append helper: wasm's two-word ABI (ptrW==4) and native
// single-word x86_64 (ptrW==8 with TwoWordOverride off). arm64 (ptrW==8 +
// TwoWordOverride) has no such helper yet, so it keeps plain OpStrConcat
// and its codegen is byte-identical.
//
// These are also exactly the widths whose assign() string branch releases
// the old buffer on overwrite (wasm __fern_str_dec, native __fern_rc_dec),
// which is what makes suppressing that release for a marked self-append
// balanced rather than a leak — see isSelfStrAppendLocal. arm64 does not
// release there either (its heap-string reclamation is the deferred
// RC-perceus slice 5g), so the two facts coincide by construction.
func (b *builder) strAppendAvailable() bool {
	return b.ptrW == 4 || (b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW))
}

// isSelfStrAppendLocal reports whether `value` is exactly `<name> + rhs` —
// the string self-append that `__fern_str_append` lowers in place (#5637
// option 3). `out = out + piece` is the stdlib's universal string builder
// (`std/unicode`'s _map_case, `std/utf8`'s encode_all, the JSON / CSV
// encoders), and OpStrConcat gives it an allocate-and-copy-everything per
// piece — quadratic bytes and, for the short pieces a per-code-point loop
// appends, allocation-bound at ~600 ns a piece.
//
// Why the in-place append is sound here and not for a general concat: the
// helper CONSUMES the accumulator (the assignment is about to overwrite the
// slot anyway) and only mutates the buffer when it is uniquely owned
// (rc==1) and the grown length still classes to the same allocator block.
// Every other case falls back to the plain concat plus the same
// __fern_str_dec the overwrite would have emitted, so the reclaim is
// unchanged — see the backends' __fern_str_append.
//
// The guards mirror that ownership transfer: RcFreeEnabled, an OWNED
// (freeEligible) string local — a borrowed param's buffer is still the
// caller's, so mutating it in place would corrupt a live value — and an ABI
// that releases on overwrite at all.
func (b *builder) isSelfStrAppendLocal(value ast.Expr, name string) bool {
	if !ast.RcFreeEnabled || !b.strAppendAvailable() {
		return false
	}
	bin, ok := value.(*ast.Binary)
	if !ok || !bin.IsStringConcat {
		return false
	}
	if id, ok := bin.Left.(*ast.Ident); !ok || id.Name != name {
		return false
	}
	return isStringTypeOfLocal(name, b) && b.rc.freeEligible[name]
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
// selfReassignOwnedLocal reports whether `name` is a LOCAL variable (not a
// parameter) being self-reassigned — the RHS mentions `name`, as in
// `s = s.emit(x)` / `s = f(s)`. Such a local owns its slot's value (struct
// fields are always inc'd at construction, so the box rc is accurate — unlike a
// borrowed param, whose box may be an rc undercount), and the old value is being
// replaced, so the rc-gated deep-drop (emitStructEnumSlotDrop: is_unique on the
// box, rc-gated field drops) reclaims it without ever over-releasing a value an
// alias still holds — closing the O(N^2) accumulator leak where `x = x.m()`
// flat-dec'd the old box and orphaned its array/struct fields. The conservative
// freeEligible taint flags such an `x` (the call result may alias it), but that
// aliasing is harmless for the drop precisely because the drop is rc-gated.
func (b *builder) selfReassignOwnedLocal(rhs ast.Expr, name string, ty ast.Type) bool {
	for _, p := range b.fn.Params {
		if p.Name == name {
			return false
		}
	}
	// SOUNDNESS: the deep-drop frees the old value's fields. Array / nested
	// struct / enum / STRING fields are all inc'd at construction (field-init
	// emitAliasInc — needsRcIncOnAlias admits strings, routing two-word ABIs
	// through __fern_str_inc; the struct-update base copy incs every copied
	// pointer field including strings), so the rc-gated drop (is_unique on
	// the box; genStructDropFn's per-ABI freeing __fern_str_dec on string
	// fields) is balanced even when the new value shares them. Strings were
	// excluded here while they were still uncounted at construction — that
	// era is over (#4174 rc-tracked native strings + the genStructDropFn
	// string-field arms), and lifting the exclusion is what un-quadratics
	// the self-host LowerState/EmitState `s = s.emit(op)` threading: with
	// the old box flat-dec'd (never freed) every superseded state pinned the
	// ops array at rc >= 2, so each statement's append cloned the whole
	// accumulated array — the #3425 Effect-A O(ops^2) that kept the merged
	// whole-compiler bundle over the 8 GiB arena. Map fields still have an
	// incomplete (leaky) deep-drop and stay excluded (typeSelfDropSafe).
	//
	// BUT the ARRAY general form `a = f(a)` keeps the string-element
	// exclusion (typeSelfDropSafeArrGeneral): the array branch's overwrite
	// __fern_arr_dec has no identity guard, and a callee that flows its
	// argument through unchanged (the recursive-collector shape —
	// checker.e060_collect_dyn_locals's `a = e060_collect_dyn_locals(body,
	// a)`) can return the very buffer being "superseded". The rc ledger
	// that keeps the string-free element types balanced there does not
	// extend to string elements (their per-site inc/dec discipline is the
	// #4355 open arc), so admitting string[]/nested-string arrays here
	// double-freed the flowed-through buffer under a different size class —
	// the derive-compile freelist corruption. Struct/enum targets keep the
	// full string admission (the deep-drop is box-level is_unique-gated and
	// the return-transfer inc protects identity returns — probed).
	if _, isArr := ty.(ast.ArrayType); isArr {
		if !typeSelfDropSafeNoStrings(ty, b.info, map[string]bool{}) {
			return false
		}
	} else if !typeSelfDropSafe(ty, b.info, map[string]bool{}) {
		return false
	}
	mentions := false
	ast.Walk(rhs, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			mentions = true
		}
		return !mentions
	})
	return mentions
}

// consumedArrayFlagName is the hidden i32 local that tracks, at runtime,
// whether a consumed-threaded ARRAY param's slot still holds the caller's
// borrow (0) or a reference this frame owns (1).
func consumedArrayFlagName(param string) string { return "__ownflag_" + param }

// isConsumedArrayParam reports whether `name` is a parameter that
// computeConsumedParams promoted AND whose type is an array.
//
// These are the promoted params that must NOT get the entry retain
// (insertConsumedParamEntryIncs). The retain is the standard way to say "this
// frame now owns a reference", and it is what balances the first
// reassignment's overwrite dec — but rc==1 is also the uniqueness test
// __fern_arr_push_grow gates its in-place fast path on. A retained array param
// enters at rc 2, so every append inside the function, and inside every
// function it threads the buffer through, takes the copy path and clones the
// whole buffer. That is O(n²): arm64_native's `arm64_le32(buf, v)` (four
// self-appends, once per assembled instruction word) went from 18 MB of arena
// traffic to 8.9 GB on ~900 KB of input when #6021 admitted arrays here, which
// is what TestSelfHostArm64AsmMemoryLinear caught.
//
// So arrays get the ownership bit explicitly instead of encoding it in the
// refcount — see emitConsumedArrayOverwriteDec for the discipline it buys.
func (b *builder) isConsumedArrayParam(name string) bool {
	if !b.rc.consumedParams[name] {
		return false
	}
	for _, p := range b.fn.Params {
		if p.Name == name {
			_, isArr := p.Type.(ast.ArrayType)
			return isArr
		}
	}
	return false
}

// emitConsumedArrayOverwriteDec emits the overwrite dec for `param = <rhs>`
// where `param` is a consumed-threaded ARRAY param. The RHS result is on the
// operand stack; the op sequence consumes it and leaves it back on top, so the
// caller's store is unchanged.
//
// The rule is:
//
//	if (new == old) { dec(old) }                  // same buffer, +1 count
//	else            { if (owned) dec(old)         // ours to release
//	                  owned = 1 }                 // else: the caller's borrow
//
// It rests on one invariant: an rc-tracked RHS hands this slot an OWNED
// reference, so when the new pointer equals the old, exactly one extra count
// was added to that pointer and the dec balances it. Both producers satisfy
// it — __fern_arr_push_grow's in-place path bumps rc 1→2 before returning the
// same buffer, and a callee that returns its own param retains it on the way
// out (the return transfer inc). A DIFFERENT pointer means the old value was
// released by this binding, which is only ours to dec once we have replaced
// the incoming borrow — hence the flag.
//
// The entry-retain alternative encodes that same bit in the refcount and is
// what structs / tuples / enums use; arrays cannot afford it (see
// isConsumedArrayParam).
func (b *builder) emitConsumedArrayOverwriteDec(name string, emitDec func()) {
	idx, hasSlot := b.locals[name]
	flagSlot, ok := b.locals[consumedArrayFlagName(name)]
	if !ok || !hasSlot {
		// No flag was allocated (the prologue only allocates for promoted
		// array params); fall back to the unconditional dec.
		emitDec()
		return
	}
	newTmp := b.allocSlot()
	b.locals[fmt.Sprintf("__ownarr_new_%d", newTmp)] = newTmp
	b.emit(Op{Kind: OpStoreLocal, I32: newTmp})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
	b.emit(Op{Kind: OpNe})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	// Replaced: dec only what this frame owns, then take ownership.
	b.emit(Op{Kind: OpLoadLocal, I32: flagSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	emitDec()
	b.emit(Op{Kind: OpEnd})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: flagSlot})
	b.emit(Op{Kind: OpElse})
	// Same buffer: the RHS added exactly one count to it.
	emitDec()
	b.emit(Op{Kind: OpEnd})
	b.emit(Op{Kind: OpLoadLocal, I32: newTmp})
}

// isSelfArrayPushLocal reports whether `rhs` is `name.append(x)` — lowered to
// `__method_Array_push(name, x)` — for a LOCAL `name` (not a param). This is the
// rc-SAFE subset of an array self-reassign overwrite, and the one that lets the
// array branch's buffer-only __fern_arr_dec reclaim the OLD buffer for element
// types selfReassignOwnedLocal's typeSelfDropSafe rejects (Map-carrying
// elements; historically also string[] before the #3425 string admission) —
// e.g. the self-host SSA builder's `vn = vn.append(..)` over `string[]`,
// which otherwise orphans every grow's buffer.
//
// Why append is safe where the general `x = f(x)` is NOT: __fern_arr_push_grow
// is rc-gated. A uniquely-owned buffer (rc==1) either mutates in place (no old
// buffer to free) or, when full, allocates a fresh buffer and leaves the old at
// rc==1 — so the overwrite's __fern_arr_dec frees exactly the orphan. A
// borrowed-DERIVED local (`var x = param`, alias-inc'd to rc≥2) takes the COPY
// path and the overwrite dec only lowers rc to ≥1 — never freeing the param's
// still-live buffer. A general `x = f(x)` has no such guarantee (f may return a
// borrowed buffer at rc==1 that the result still aliases → buffer-UAF), which is
// why the broad form segfaulted the self-compile; the push form does not.
// __fern_arr_dec is buffer-only (never walks elements), so the overwrite itself
// never releases a shared element — but at rc>1 the old buffer SURVIVES it,
// leaving two live buffers over one set of element references. That is what the
// _move_ grow helpers' rc != 1 retain covers (#3457, emitArrayPush).
func (b *builder) isSelfArrayPushLocal(value ast.Expr, name string) bool {
	// Locals qualify. A BORROWED param never does — its buffer belongs to the
	// caller and is still live, so an in-place grow / orphan-free would UAF the
	// caller's value. An `own` param, by contrast, is callee-owned: the caller
	// transferred it (no entry-inc, freeEligible, uniquely owned at rc==1 when
	// the E051 guard's owned arg was fresh / moved). Its self-append is therefore
	// rc-gated exactly like an owned local — in-place at rc==1, orphan freed on
	// grow — so it reclaims grow intermediates instead of orphaning one buffer
	// per iteration. This is the runtime half of move semantics for threaded
	// array params (docs/RC-ARRAY-MOVE-SEMANTICS-PLAN.md step 3).
	for _, p := range b.fn.Params {
		if p.Name == name && !p.Own {
			return false
		}
	}
	call, ok := value.(*ast.Call)
	if !ok {
		return false
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Array_push" {
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	recv, ok := call.Args[0].(*ast.Ident)
	return ok && recv.Name == name
}

// typeSelfDropSafe reports whether a value of type `t` can be deep-dropped on a
// self-reassign overwrite without risking an uncounted-alias over-release: no
// Map anywhere (its deep drop is incomplete). Arrays / structs / enums / tuples
// / STRINGS of safe types are fine — their pointer payloads are inc'd at
// construction, so the rc-gated drop is balanced. Strings joined the safe set
// once they became rc-counted at every sharing construction site (field-init
// emitAliasInc, struct-update base copy) with genStructDropFn's matching
// per-ABI freeing __fern_str_dec on the drop side — see the
// selfReassignOwnedLocal soundness note (#3425: the exclusion forced every
// string-fielded builder rebind onto the flat non-freeing dec, whose leaked
// boxes pinned the builder's array fields at rc >= 2 and turned each append
// into a whole-array clone).
// typeSelfDropSafeNoStrings is typeSelfDropSafe with the pre-#3425 string
// exclusion kept — the gate for the ARRAY general-form (`a = f(a)`) overwrite
// dec only, whose buffer free has no identity guard and whose string-element
// inc/dec ledger is not yet balanced for the callee-flows-argument-through
// shape (see the selfReassignOwnedLocal soundness note). Struct/enum targets
// use the widened typeSelfDropSafe.
func typeSelfDropSafeNoStrings(t ast.Type, info *checker.Info, seen map[string]bool) bool {
	if _, isStr := t.(ast.StringType); isStr {
		return false
	}
	switch ty := t.(type) {
	case ast.ArrayType:
		return typeSelfDropSafeNoStrings(ty.Elem, info, seen)
	case ast.SliceType:
		return typeSelfDropSafeNoStrings(ty.Elem, info, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !typeSelfDropSafeNoStrings(e, info, seen) {
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
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if !typeSelfDropSafeNoStrings(f.Type, info, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		ed, ok := info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !typeSelfDropSafeNoStrings(pl, info, seen) {
					return false
				}
			}
		}
		return true
	}
	return typeSelfDropSafe(t, info, seen)
}

func typeSelfDropSafe(t ast.Type, info *checker.Info, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true
	case ast.StringType:
		return true
	case ast.ArrayType:
		return typeSelfDropSafe(ty.Elem, info, seen)
	case ast.SliceType:
		return typeSelfDropSafe(ty.Elem, info, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !typeSelfDropSafe(e, info, seen) {
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
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if !typeSelfDropSafe(f.Type, info, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		ed, ok := info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !typeSelfDropSafe(pl, info, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// consumedDropWired reports whether a value of type `t` is fully reclaimed by
// the generated deep-drop machinery (__drop_struct_ / __drop_enum_ /
// __drop_tuple_ / the array / string helpers) on EVERY backend — the
// precondition for promoting a threaded param to callee-owned. It allows
// scalars, strings (the native single-word drop gap in genStructDropFn is now
// closed), and arrays / structs / enums / tuples of wired types; it rejects Map
// (its deep drop is incomplete), slices, closures, and unknown / generic /
// runtime-handle types whose drop is not statically wired.
func consumedDropWired(t ast.Type, info *checker.Info, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType, ast.StringType:
		return true
	case ast.ArrayType:
		return consumedDropWired(ty.Elem, info, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !consumedDropWired(e, info, seen) {
				return false
			}
		}
		return true
	case ast.StructType:
		if ty.Name == "Map" {
			return false
		}
		if seen["s:"+ty.Name] {
			return true
		}
		seen["s:"+ty.Name] = true
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false // runtime handle (Reader / Writer / MapIter) / unknown
		}
		for _, f := range sd.Fields {
			if !consumedDropWired(f.Type, info, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen["e:"+ty.Name] {
			return true
		}
		seen["e:"+ty.Name] = true
		ed, ok := info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !consumedDropWired(pl, info, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

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
	if blk, ok := e.(*ast.BlockExpr); ok {
		// A block-expression leaves a value only when it has a trailing
		// expression. A value-less (void) block — e.g. a `defer { … }`
		// action whose last element is a `;`-statement — pushes nothing,
		// so no drop must follow it (else the wasm stack underflows and
		// the module fails to validate).
		return blk.Tail != nil
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
	// On wasm (ptrW==4) a `dyn Trait` value is an inline two-word
	// `[data, vtable]` fat pointer — two pointer-width slots, like a
	// two-word string (docs/DYN-TRAITS.md §4.2.1). On natives
	// (ptrW==8) it is BOXED one-word: a single heap pointer to a
	// `{data, vtable}` cell (§4.2.2), so it falls through to the
	// one-word pointer branch below.
	if _, isDyn := t.(ast.DynTraitType); isDyn && ptrW == 4 {
		return int32(2 * ptrW)
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
// StructFieldLayout is the exported view of structFieldLayout: it returns the
// byte offset of each field within a struct's heap field-area (excluding the
// rc header — the user-visible data pointer already points past it) and the
// total field-area size. Used by cmd/fern to emit DWARF struct-member DIEs.
func StructFieldLayout(fields []ast.Param, ptrW int) (map[string]int32, int32) {
	return structFieldLayout(fields, ptrW)
}

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
	// On wasm (ptrW==4) `dyn Trait` is an inline two-word `[data,
	// vtable]`; store it with the same two-word fan-out as a string
	// (representation-agnostic — two adjacent i32s). On natives it is
	// a boxed one-word pointer (§4.2.2), handled by the pointer branch
	// below.
	if _, isDyn := t.(ast.DynTraitType); isDyn && ptrW == 4 {
		return Op{Kind: OpStore, Width: WidthString}
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
	// On wasm (ptrW==4) `dyn Trait` array elements are inline two-word
	// `[data, vtable]` fat pointers — two-word fan-out, same as
	// strings. On natives (ptrW==8) they are boxed one-word pointers
	// (§4.2.2), handled by the pointer branch below.
	if _, isDyn := t.(ast.DynTraitType); isDyn && ptrW == 4 {
		return Op{Kind: OpStore, Width: WidthString}
	}
	if ast.IsPointerType(t) {
		return Op{Kind: OpStore, Width: WidthPtr}
	}
	// Scalar-only switch (ptrW-independent; sub-i32 + wide).
	switch ast.ElemSizeBytes(t) {
	case 1:
		return Op{Kind: OpStoreI8}
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
	// On wasm (ptrW==4) `dyn Trait` is an inline two-word `[data,
	// vtable]`; load it with the same two-word fan-out as a string.
	// On natives (ptrW==8) it is a boxed one-word pointer (§4.2.2),
	// handled by the pointer branch below.
	if _, isDyn := t.(ast.DynTraitType); isDyn && ptrW == 4 {
		return Op{Kind: OpLoad, Width: WidthString}
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

	// entriesBase = buf + ast.MapHeaderBytes + cap * 4
	entriesSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mv_entries_%d", entriesSlot)] = entriesSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: ast.MapHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpLoadLocal, I32: capSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpMul})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: entriesSlot})

	// Per-entry stride + V-slot offset come from __ptr_width()
	// so the same IR works on wasm32 (4-byte ptr → stride 8,
	// V-offset 4) and arm64 (8-byte ptr → stride 16, V-offset
	// 8). Matches the stdlib Map runtime's layout exactly.
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

	// entriesBase = buf + ast.MapHeaderBytes + cap * 4
	entriesSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__mk_entries_%d", entriesSlot)] = entriesSlot
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpConstI32, I32: ast.MapHeaderBytes})
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
	// An array of single-word rc-tracked pointer elements (struct / enum /
	// array / tuple / closure) needs rc bookkeeping the scalar path skips:
	//   - the COPY branch of the CoW helper must retain each copied element
	//     (the pointer-aware __fern_arr_cow_inplace_ptr does this), else the
	//     fresh buffer shares the receiver's elements at unchanged rc and
	//     dropping either array frees them out from under the other (UAF);
	//   - overwriting index i must drop the OLD element there (it is being
	//     replaced) and retain the NEW value if it is an alias.
	// Scalar-element arrays keep the byte-identical straight-line path.
	rcTracked := arrElemIsRcTracked(elemType)
	storeOp, storeWidth := arraySetStoreOp(elemType, b.ptrW)
	idxHelper := "__arr_idx"
	switch stride {
	case 1:
		idxHelper = "__arr_idx_1"
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
	// Retain an aliased rc-tracked value: it is now co-owned by the buffer
	// slot (mirrors emitArrayPush / struct-field-init). A fresh temp / moved
	// last-use value transfers its single reference in as-is.
	if rcTracked && needsRcIncOnAlias(n.Args[2], b) && !b.rc.moveSites[n.Args[2]] {
		b.emitAliasInc(n.Args[2])
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
	// buf = __fern_arr_cow_inplace[_ptr](arr, stride)
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	// Receiver live after the call (#2832): rc-inc the buffer so
	// cow_inplace sees rc >= 2 and takes the COPY path, leaving the
	// receiver's buffer untouched. __fern_rc_inc returns its argument, so
	// the pointer stays on the stack for cow_inplace. (cow_inplace's copy
	// path decs back, so the balance is: receiver keeps rc 1, the fresh
	// copy is rc 1.) A move receiver (last use / reassign-self / temp)
	// skips this and keeps the rc==1 in-place fast path.
	if b.rc.arraySetInc[n] {
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
	}
	b.emit(Op{Kind: OpConstI32, I32: stride})
	cowHelper := "__fern_arr_cow_inplace"
	if rcTracked {
		cowHelper = "__fern_arr_cow_inplace_ptr"
	}
	b.emit(Op{Kind: OpCallDirect, Str: cowHelper, I32: 2})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__set_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})
	// Element address: buf + i*stride via the per-stride
	// bounds-check helper.
	b.emit(Op{Kind: OpLoadLocal, I32: bufSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: iSlot})
	b.emit(Op{Kind: OpCallDirect, Str: idxHelper, I32: 2})
	if rcTracked {
		// Stash the element address so we can both read the old element
		// (to drop it) and write the new one at the same slot.
		addrSlot := b.allocSlot()
		b.locals[fmt.Sprintf("__set_addr_%d", addrSlot)] = addrSlot
		b.emit(Op{Kind: OpStoreLocal, I32: addrSlot})
		// Drop the OLD element being overwritten. On the copy branch the
		// helper inc'd it, so this dec balances (the receiver keeps its
		// reference); on the in-place branch it is the sole owner's release
		// (frees on last reference). dropStructField is the type-appropriate
		// deep drop, is_unique-gated internally, so a shared element only decs.
		// Gated on RcFreeEnabled: free-off is the no-reclamation baseline
		// (the old element just leaks), and dropStructField's generated
		// __drop_struct_/__drop_enum_ callees only exist when the flag-gated
		// helper worklist runs — an ungated call here left free-off builds
		// with an undefined __drop_enum_<E> reference (regex_captures).
		if ast.RcFreeEnabled {
			b.emit(Op{Kind: OpLoadLocal, I32: addrSlot})
			b.emit(Op{Kind: OpLoad, Width: storeWidth})
			b.dropStructField(elemType)
		}
		// Store the new element value.
		b.emit(Op{Kind: OpLoadLocal, I32: addrSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: vSlot})
		b.emit(Op{Kind: storeOp, Width: storeWidth})
	} else {
		// Element value.
		b.emit(Op{Kind: OpLoadLocal, I32: vSlot})
		b.emit(Op{Kind: storeOp, Width: storeWidth})
	}
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

// cellElemType returns the cell's element type from the stamped TypeArgs
// (i32 fallback when absent). The element type drives stride / store-width
// selection and — for a `string` element — the rc retain/release wiring.
func (b *builder) cellElemType(n *ast.Call) ast.Type {
	if len(n.TypeArgs) == 1 {
		return n.TypeArgs[0]
	}
	return ast.NumberType{}
}

// cellElemOf returns the element type of a Cell-typed value (i32 fallback
// when its TypeArgs are missing). Used by the drop paths, which see the
// instantiation type (Cell[string] vs Cell[i32]) and route reclamation by
// the element.
func cellElemOf(t ast.Type) ast.Type {
	if st, ok := t.(ast.StructType); ok && st.Name == "Cell" && len(st.Args) == 1 {
		return st.Args[0]
	}
	return ast.NumberType{}
}

// emitCellDropOnStack reclaims a Cell value whose data pointer is already
// on the operand stack. A cell IS a one-element array box (the same
// [cap|rc|len|slot] layout emitCellNew writes), so it reclaims through the
// ARRAY machinery — never the struct/box_free path, whose data-8 base
// assumption mis-frees the cell's 16-byte (array-style) header. A `string`
// element dec's through the string-aware per-element walk; a scalar element
// just frees the box. A non-eligible (borrowed / escaped) cell leaks-safe
// via a plain rc_dec on the box (its rc word lives at data-8, like an
// array's). Net-zero on the operand stack.
func (b *builder) emitCellDropOnStack(elem ast.Type, eligible bool) {
	if !ast.RcFreeEnabled || !eligible {
		b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	stride := int32(ast.ElemSizeBytesFor(elem, b.ptrW))
	if stride == 0 {
		stride = 4
	}
	helper := "__fern_arr_dec"
	if _, isStr := elem.(ast.StringType); isStr {
		if ast.UseTwoWordStrings(b.ptrW) {
			// Two-word string element (wasm + arm64-TwoWordOverride): walk +
			// __fern_str_dec each (data, len), then free the box.
			helper = "__fern_drop_arr_str"
		} else if b.ptrW == 8 {
			// Native single-word string element (x86_64): each element is a
			// single pointer; __fern_drop_arr_ptr walks + __fern_rc_dec's it.
			helper = "__fern_drop_arr_ptr"
		}
	}
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: helper, I32: 2})
	b.emit(Op{Kind: OpDrop})
}

// emitCellNew lowers `cell_new(v)` to a one-element heap box (the same
// layout as a 1-element array literal: [cap|rc|len|slot]), so Perceus RCs
// the box itself via the standard rc word at data-8. Returns the data
// pointer. See docs/CELL-TYPE-PLAN.md.
func (b *builder) emitCellNew(n *ast.Call) error {
	elemType := b.cellElemType(n)
	stride := int32(ast.ElemSizeBytesFor(elemType, b.ptrW))
	if stride == 0 {
		stride = 4
	}
	headerBytes := int32(16)
	if stride > 16 {
		headerBytes = stride
	}
	storeOp := arrayElemStoreOpFor(elemType, b.ptrW)
	b.emit(Op{Kind: OpConstI32, I32: headerBytes + stride})
	b.emit(Op{Kind: OpAlloc})
	baseSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__cell_new_%d", baseSlot)] = baseSlot
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// Header words at data-12 (cap), data-8 (rc), data-4 (len) — all 1,
	// exactly like a uniquely-owned 1-element array.
	writeHeader := func(off, val int32) {
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		if headerBytes != off {
			b.emit(Op{Kind: OpConstI32, I32: headerBytes - off})
			b.emit(Op{Kind: OpAdd})
		}
		b.emit(Op{Kind: OpConstI32, I32: val})
		b.emit(Op{Kind: OpStore})
	}
	writeHeader(12, 1) // cap
	writeHeader(8, 1)  // rc
	writeHeader(4, 1)  // len
	// Slot value at data (base + headerBytes).
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: headerBytes})
	b.emit(Op{Kind: OpAdd})
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	// A `string` element makes the cell CO-OWN the buffer: retain an
	// alias-shaped source (an Ident / field / index the caller still holds)
	// so the cell's drop has a reference to release; a fresh value (concat /
	// literal / call result) or a moved last-use local transfers its single
	// reference and isn't inc'd. Mirrors array-literal / push element retain
	// (needsRcIncOnAlias + emitAliasInc). Scalars hold no buffer — no inc.
	if _, isStr := elemType.(ast.StringType); isStr {
		if needsRcIncOnAlias(n.Args[0], b) && !b.rc.moveSites[n.Args[0]] {
			b.emitAliasInc(n.Args[0])
		}
	}
	b.emit(storeOp)
	// Result: the data pointer.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: headerBytes})
	b.emit(Op{Kind: OpAdd})
	return nil
}

// emitCellGet lowers `c.get()` to a load of slot 0 (the data pointer is
// the slot address). A `string` element is returned BORROWED — the cell
// still owns its slot copy — so retain the returned buffer (the caller's
// binding / drop balances it), exactly as `m.get` / `arr[i]` reads do.
func (b *builder) emitCellGet(n *ast.Call) error {
	elemType := b.cellElemType(n)
	if err := b.expr(n.Args[0]); err != nil {
		return err
	}
	b.emit(payloadLoadOpFor(elemType, b.ptrW))
	if _, isStr := elemType.(ast.StringType); isStr {
		if ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
		} else if b.ptrW == 8 {
			b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
		}
	}
	return nil
}

// emitCellSet lowers `c.set(v)` to an in-place store of slot 0 — no CoW
// (a cell mutates in place by design) — and leaves nothing on the stack
// (the method returns void). For a `string` element this is an OVERWRITE:
// the slot already holds a co-owned buffer, so pre-drop it (balancing the
// retain at its set/construction) before storing the new value, and retain
// an alias-shaped new value. Mirrors the Map[K,string] overwrite pre-drop
// + set retain.
func (b *builder) emitCellSet(n *ast.Call) error {
	elemType := b.cellElemType(n)
	if _, isStr := elemType.(ast.StringType); !isStr {
		// Scalar: plain in-place store, no rc traffic.
		if err := b.expr(n.Args[0]); err != nil { // cell data ptr = slot address
			return err
		}
		if err := b.expr(n.Args[1]); err != nil { // value
			return err
		}
		b.emit(arrayElemStoreOpFor(elemType, b.ptrW))
		return nil
	}
	// String element. Stash the cell pointer so args[0] is evaluated once,
	// then pre-drop the old slot string before storing the new one.
	ptrSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__cell_set_%d", ptrSlot)] = ptrSlot
	if err := b.expr(n.Args[0]); err != nil { // cell data ptr = slot address
		return err
	}
	b.emit(Op{Kind: OpStoreLocal, I32: ptrSlot})
	// Pre-drop the existing slot string (gated like every other dec).
	if ast.RcFreeEnabled {
		b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
		b.emit(payloadLoadOpFor(elemType, b.ptrW))
		if ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
		} else {
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
		}
		b.emit(Op{Kind: OpDrop})
	}
	// Store the new value (addr, value), retaining an alias-shaped source.
	b.emit(Op{Kind: OpLoadLocal, I32: ptrSlot})
	if err := b.expr(n.Args[1]); err != nil { // value
		return err
	}
	if needsRcIncOnAlias(n.Args[1], b) && !b.rc.moveSites[n.Args[1]] {
		b.emitAliasInc(n.Args[1])
	}
	b.emit(arrayElemStoreOpFor(elemType, b.ptrW))
	return nil
}

// curAppendOrder returns the ident-occurrence order for the function
// currently being lowered, rebuilding it only when the function changes.
// emitArrayPush uses it for a last-use test on ident append operands
// (#4827) without paying an O(body) rebuild at every push site.
func (b *builder) curAppendOrder() identOrder {
	if b.appendOrderFn != b.fn {
		b.appendOrder = identOrderOf(b.fn.Body)
		b.appendInPlaceOK = inPlacePushes(b.fn.Body)
		b.callArgDies = callArgDeaths(b.fn)
		b.appendOrderFn = b.fn
	}
	return b.appendOrder
}

// appendForcesCopy reports whether emitArrayPush takes the #4827
// forced-copy path for this `__method_Array_push` call: a non-self-reassign
// append on a plain-IDENT operand that is reused after (not its last
// occurrence) and not in the inPlacePushes exemption set (#4849's
// return-position / borrowed-param self-reassign shapes). Shared between
// emitArrayPush (which emits the rc bump that forces the grow helper's
// copy path) and the stage-(b) arg-temp recognizer appendCopyTempType
// (which reclaims the resulting fresh copy after a borrowing call — the
// remaining forced-copy leak #4849's exemptions don't cover).
func (b *builder) appendForcesCopy(n *ast.Call) bool {
	if ast.Expr(n) == b.selfPushMoveCall {
		return false
	}
	id, ok := n.Args[0].(*ast.Ident)
	return ok && needsRcIncOnAlias(n.Args[0], b) &&
		!b.curAppendOrder().isLast(id) && !b.appendInPlaceOK[n]
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
	// Element retain: pushing an ALIASED pointer element (`ninsts.push(ins)`
	// where ins reads from another array / a field) stores its pointer into the
	// new buffer, so the buffer now CO-OWNS the reference — inc it, mirroring the
	// array-literal element inc (needsRcIncOnAlias) and the struct-field-init
	// inc. Without this the buffer held an uncounted alias: when the source
	// container was reclaimed (e.g. the self-host SSA optimiser overwrites the
	// SFunc whose old blocks' insts the new blocks reuse) the buffer's
	// drop_arr_ptr over-released the shared element. A FRESH element (a call
	// result / literal) isn't an alias and is moved in as-is; a moved last-use
	// owned local likewise transfers its single reference (b.rc.moveSites). Reuse
	// emitAliasInc on the value already on the stack (it returns it for the
	// store below).
	if needsRcIncOnAlias(n.Args[1], b) && !b.rc.moveSites[n.Args[1]] {
		b.emitAliasInc(n.Args[1])
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

	// Value semantics for a non-self-reassign append on a plain-IDENT
	// operand that is REUSED after the append (not its last use). The grow
	// helper's rc==1 in-place fast path mutates the operand buffer's
	// length header and returns the SAME pointer; that's only sound when
	// the binding is not read again. For a reused ident it corrupts later
	// reads — e.g. `var a = walk(path.append(d), …); var b =
	// path.append(d).len();`, where the first append mutates `path` in
	// place and the second sees the longer buffer (interp ≠ compiled,
	// #4827). Here the rc==1 check is not enough: `path` is uniquely
	// referenced (rc 1) yet READ twice, and rc counts references, not
	// uses. Bump the operand's rc across the grow so the helper takes the
	// copy path (fresh buffer, operand untouched), then restore it.
	//
	// Scope is deliberately narrow — a bare ident whose occurrence here is
	// NOT its last in the function body (identOrder). This leaves sound
	// in-place cases on the fast path:
	//   - the ident's LAST use — nothing reads it after, so the mutation
	//     is unobservable (the second `path.append` above);
	//   - a fresh-temporary operand (array literal / call result) — no
	//     other reference exists;
	//   - a field / index operand — notably `S { f: s.f.append(v) }`
	//     functional-update threading (the self-host EmitState `s =
	//     s.emit(x)` shape), which must stay O(1)
	//     (TestWASMSelfReassignFieldBounded) and whose container is
	//     replaced rather than reused;
	//   - the self-reassign form (selfPushMoveCall), whose in-place mutate
	//     pairs with the assign-site overwrite dec — O(1) push loops;
	//   - the inPlacePushes set — self-reassigns of BORROWED params
	//     (`acc = acc.append(v)`, outside selfPushMoveCall's local/own
	//     scope) and single-occurrence RETURN-position appends
	//     (`return acc.append(v)`), where no later intra-function read
	//     can observe the mutation. Without these two, the self-host
	//     compiler's accumulator-threading walkers (`return
	//     acc.append(id.name)` once per visited AST node) copy the whole
	//     accumulated array per append — O(n²) bytes that the leak-mode
	//     bump arena never reclaims, which blew the per-module emit past
	//     the arena ceiling (exit 137) and OOM-killed the CI runners.
	forceCopy := b.appendForcesCopy(n)
	if forceCopy {
		b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}

	// oldLen = i32.load(arr - 4)
	oldLenSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_oldLen_%d", oldLenSlot)] = oldLenSlot
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpConstI32, I32: 4})
	b.emit(Op{Kind: OpSub})
	b.emit(Op{Kind: OpLoad})
	b.emit(Op{Kind: OpStoreLocal, I32: oldLenSlot})

	// buf = __fern_arr_push_grow(arr, oldLen, stride)
	//
	// Element-retain on the grow COPY (#3425): when the elements are
	// rc-tracked pointers (string / struct / enum / array / tuple /
	// closure), the plain helper's raw memcpy would leave the fresh
	// buffer sharing the old buffer's element pointers at unchanged rc.
	// The old buffer is still owned by its holder (e.g. the struct whose
	// field was appended FROM in the `S{f: s.f.append(v)}` functional-
	// update threading); when that holder's walk-drop later ran at the
	// old buffer's rc==1 it dec'd/freed elements the grown copy still
	// referenced — a use-after-free once the element drops actually free
	// (string elements via __fern_drop_arr_str, struct elements via the
	// deep __drop_arr_struct_<E> walks). Route those element types
	// through __fern_arr_push_grow_ptr / _str, which retain each copied
	// element exactly like __fern_arr_cow_inplace_ptr does for `.with`.
	// Gated on RcFreeEnabled: free-off never walk-frees elements, so the
	// plain helper keeps that baseline byte-identical.
	//
	// The self-append form `a = a.append(v)` takes the _move_ siblings
	// instead. Its overwrite reclaim (the Assign array branch,
	// isSelfArrayPushLocal) frees the old buffer with a buffer-only
	// __fern_arr_dec that never walks elements — but that dec only FREES
	// when the buffer was uniquely owned. At rc==1 the copy legitimately
	// inherits the element references and an unconditional retain would
	// leak one per element per grow (the O(1)-heap self-append gate,
	// TestX86_64ArrayPushPtrElemReclaim, catches that). At rc>1 an alias
	// still owns the old buffer, so both buffers hold the same element
	// pointers under a single count and both walk-drops release them —
	// the #3457 over-release. The _move_ helpers retain exactly when the
	// incoming rc != 1, which is precisely "the old buffer survives this
	// grow". The assign lowering marks the exact RHS call node before
	// lowering it (selfPushMoveCall), so nested appends inside the pushed
	// value still take the unconditionally-retaining variants.
	growHelper := "__fern_arr_push_grow"
	if ast.RcFreeEnabled {
		suffix := ""
		if ast.Expr(n) == b.selfPushMoveCall {
			suffix = "_move"
		}
		if _, isStr := elemType.(ast.StringType); isStr {
			if b.twoWordStrings() {
				// Two-word (data, len) elements: pair-walking retain.
				growHelper += suffix + "_str"
			} else if b.ptrW == 8 {
				// Native single-word string elements: plain pointer
				// retain (rc_inc guards SSO / literal / sentinel).
				growHelper += suffix + "_ptr"
			}
		} else if arrElemIsRcTracked(elemType) {
			growHelper += suffix + "_ptr"
		}
	}
	b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: oldLenSlot})
	b.emit(Op{Kind: OpConstI32, I32: stride})
	b.emit(Op{Kind: OpCallDirect, Str: growHelper, I32: 3})
	bufSlot := b.allocSlot()
	b.locals[fmt.Sprintf("__push_buf_%d", bufSlot)] = bufSlot
	b.emit(Op{Kind: OpStoreLocal, I32: bufSlot})

	// Restore the operand's rc bumped above to force the copy path. The
	// grow's copy path leaves the operand untouched, so this returns it
	// to its incoming count (never below 1 — inc preceded it — so no
	// free fires here); `buf` is the fresh copy the append result owns.
	if forceCopy {
		b.emit(Op{Kind: OpLoadLocal, I32: arrSlot})
		b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}

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

// keyedMapFns names the derived hash / eq function VALUES a kind-3
// (struct / enum) map key dispatches through.
type keyedMapFns struct{ hash, eq string }

// keyedMapFnsFor returns the derived hash/eq method names for a
// struct/enum (keyKind-3) map key, or ok=false for a scalar/string
// key. The names are the receiver-hoisted mangling the checker stamps
// onto a `(self: K) hash()` / `(self: K) eq(other: K)` method — for a
// derived key these are synthesised by @derive(Eq, Hash).
func (b *builder) keyedMapFnsFor(kType ast.Type) (keyedMapFns, bool) {
	if mapKeyKindTag(kType, b.ptrW) != 3 {
		return keyedMapFns{}, false
	}
	name := mapKeyTypeName(kType)
	if name == "" {
		return keyedMapFns{}, false
	}
	return keyedMapFns{
		hash: "__method_" + name + "_hash",
		eq:   "__method_" + name + "_eq",
	}, true
}

// emitMapCall emits a Map runtime call, routing a struct/enum
// (keyKind-3) key to the `_keyed` variant — which takes the key
// type's derived hash / eq as two trailing function-value args
// (`hash_fn: (usize)=>i32`, `eq_fn: (usize, usize)=>boolean`) so the
// type-erased runtime can hash + compare the key structurally. Scalar
// / string keys emit the ordinary call unchanged. `baseTarget` is the
// IR-visible `__method_Map_*` name each backend rewrites to its
// `__map_*_impl`; the keyed variant appends `_keyed` (→
// `__map_*_keyed_impl`). `baseArgc` is the non-keyed argument count.
func (b *builder) emitMapCall(baseTarget string, baseArgc int32, kType ast.Type) {
	if kf, ok := b.keyedMapFnsFor(kType); ok {
		b.emit(Op{Kind: OpConstFunc, Str: kf.hash})
		b.emit(Op{Kind: OpConstFunc, Str: kf.eq})
		b.emit(Op{Kind: OpCallDirect, Str: baseTarget + "_keyed", I32: baseArgc + 2})
		return
	}
	b.emit(Op{Kind: OpCallDirect, Str: baseTarget, I32: baseArgc})
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
	b.emitMapCall("__method_Map_set", 3, kType)
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
	b.emitMapCall("__method_Map_get", 2, kType)
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
		b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
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
	//
	// The in-place branch hands back the handle the receiver's binding
	// still names, and the result tuple below stores it as an owned
	// element — so the seam owes a retain (mapCowRetainReceiver). Delete
	// is the one mutator that cannot take the retain at the call result:
	// that result is the tuple box, not the map.
	preSlot := int32(-1)
	if b.mapCowRetainReceiver(n) != nil {
		preSlot = b.allocSlot()
		b.locals[fmt.Sprintf("__del_pre_%d", preSlot)] = preSlot
		b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
		b.emit(Op{Kind: OpStoreLocal, I32: preSlot})
	}
	b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
	b.emit(Op{Kind: OpCallDirect, Str: "__map_cow_inplace", I32: 1})
	b.emit(Op{Kind: OpStoreLocal, I32: mapSlot})
	if preSlot >= 0 {
		b.emit(Op{Kind: OpLoadLocal, I32: mapSlot})
		b.emit(Op{Kind: OpLoadLocal, I32: preSlot})
		b.emitMapCowRetainTest(mapSlot)
	}

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
	b.emitMapCall("__method_Map_delete", 2, kType)

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
	b.emitMapCall("__method_Map_get_or", 3, kType)
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

// extArgTypes wraps a non-nil ArgTypes slice in an OpExt block (nil in →
// nil out, so ops without string-typed args stay Ext-free).
func extArgTypes(ts []ast.Type) *OpExt {
	if ts == nil {
		return nil
	}
	return &OpExt{ArgTypes: ts}
}

// extCaptureSlots wraps a non-nil CaptureSlots slice in an OpExt block
// (nil in → nil out).
func extCaptureSlots(slots []int32) *OpExt {
	if slots == nil {
		return nil
	}
	return &OpExt{CaptureSlots: slots}
}

// bitCountBuiltin maps a bit-counting builtin name to its IR op and operand
// width. Returns ok=false for anything else.
//
// The names are deliberately `__`-prefixed and width-suffixed: they are the
// compiler-intrinsic layer that std/{i32,i64,u32,u64} wrap in the readable
// `count_ones()` / `leading_zeros()` / `trailing_zeros()` methods, not surface
// the language asks users to write.
func bitCountBuiltin(name string) (OpKind, int, bool) {
	switch name {
	case "__clz32":
		return OpClz, 32, true
	case "__clz64":
		return OpClz, 64, true
	case "__ctz32":
		return OpCtz, 32, true
	case "__ctz64":
		return OpCtz, 64, true
	case "__popcount32":
		return OpPopcount, 32, true
	case "__popcount64":
		return OpPopcount, 64, true
	}
	return 0, 0, false
}
