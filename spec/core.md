# Fern Core — the instruction set

Status: normative index, gated by `TestCoreOpsIndexIsAccurate` and
`TestCoreOpEffectsMatchTheModel` in `internal/ir`.

`grammar.ebnf` says what a Fern program looks like. `diagnostics.md`
says which programs are refused. `semantics.md` indexes the handful of
promises the policy docs make about the ones that are accepted. None of
them says what a Fern program *is*.

That definition, when it is written, will not be written over the
surface language. It will be written over **Fern Core** — and Fern Core
already exists: `internal/ir` is a target-agnostic stack machine that
every backend consumes, and the surface language is defined by lowering
into it. `docs/SPECIFICATION-RESEARCH.md` §Layer 3 lays out the three
pieces that need to follow, in order: the op set, its typing rules, and
a small-step semantics over a store.

**This file is the first of the three, and only the first.** It
enumerates the instructions and says what each one does to the operand
stack. It says nothing about what any of them *means*.

## Why the op set is worth writing down on its own

Because it is the part that is already true and is currently unreadable.
The instruction set exists only as a Go `const` block, and a reader who
wants to know what `call_dyn` expects on the stack has to reconstruct it
from three emitters. That is the same position the syntax was in before
`grammar.ebnf`: a description of *a* program, not of the language.

It is also the part a specification cannot start without. A typing rule
is a rule about an op, and a reduction rule reduces one — so the op set
is the vocabulary both are written in, and an op set with a hole in it
produces a semantics with a hole in it that nothing can detect.

## Notation

An effect is written `pops → pushes`, **deepest operand first** — so
`i f → —` means an integer slot below a float slot, and the float is on
top. `—` on either side means nothing.

| Symbol | One | Meaning |
| --- | --- | --- |
| `i` | slot | integer-shaped: `i32`, `i64`, or any pointer |
| `f` | slot | float-shaped: `f32` or `f64` |
| `*` | slot | one slot, class unconstrained |
| `s` | value | a whole string — **two** slots under the two-word ABI (`ptrW == 4`), one otherwise |
| `T` | value | a shape the op's context determines; the Notes say which context |
| `R` | value | the callee's result shape |
| `A…` | values | the call's arguments: as many slots as their types occupy, which exceeds the argument count when a string is passed |
| `*…` | slots | exactly `I32` slots, class unconstrained |

The IR distinguishes only these two classes, and deliberately no more:
`WidthPtr` is resolved by the backend, and nothing anywhere separates a
pointer from an integer of the same width. A column claiming operand
*widths* would be describing a type system the IR does not carry — see
`internal/ir/verifystack.go` for the same decision made in code.

## The instructions

`Imm.` lists the `Op` fields the instruction reads. `Pos` is omitted
throughout: every op may carry it, and only `line` is about it.

### Constants

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `const.i32` | `— → i` | `I32` | |
| `const.i64` | `— → i` | `I64` | |
| `const.f32` | `— → f` | `F32` | |
| `const.f64` | `— → f` | `F64` | |
| `const.str` | `— → s` | `Str` | The literal's value, not an index. Two slots under the two-word ABI. |
| `const.func` | `— → i` | `I32` | Function-table index. |
| `const.vtable` | `— → i` | `Str`, `Str2` | Address of the static vtable for the (Trait, Concrete) pair. `docs/DYN-TRAITS.md` §4.2.1. |
| `enum.sentinel` | `— → i` | `I32` | Address of a shared static 4-byte cell holding the tag. Payloadless variants construct without allocating. |

### Conversions

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `extend.i32_s` | `i → i` | | Sign-extend to `i64`. |
| `extend.i32_u` | `i → i` | | Zero-extend to `i64`. |
| `wrap.i64` | `i → i` | | Keep the low 32 bits. |
| `promote.f32` | `f → f` | | To `f64`. |
| `demote.f64` | `f → f` | | To `f32`. |
| `convert.i32` | `i → f` | `Width`, `Unsigned` | `Width` is the **destination** float width. |
| `convert.i64` | `i → f` | `Width`, `Unsigned` | |
| `trunc.f32` | `f → i` | `Width`, `Unsigned` | `Width` is the **destination** integer width. Saturating, and `NaN → 0` — `docs/FLOAT-SEMANTICS.md`, claims `FS-01`–`FS-03`. |
| `trunc.f64` | `f → i` | `Width`, `Unsigned` | |
| `i32.reinterpret_f32` | `f → i` | | Bits, unchanged. |
| `f32.reinterpret_i32` | `i → f` | | |
| `i64.reinterpret_f64` | `f → i` | | |
| `f64.reinterpret_i64` | `i → f` | | |

### Locals

A local is one index in one flat space — parameters, then declared
locals, then the lowering pass's scratch slots — and one variable is one
index however many stack slots its value takes.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `local.load` | `— → T` | `I32`, `Width` | `T` is the local's declared shape. `Width: WidthString` forces the two-word pair for a slot whose type does not say so. |
| `local.store` | `T → —` | `I32`, `Width` | |
| `local.tee` | `T → T` | `I32`, `Width` | Store, and leave the value. |

### Integer arithmetic and comparison

`Width` selects `i32` (0, the default) or `i64`; `Unsigned` selects the
`_u` reading where one exists. Every operation here is total and wraps
at the operand width — `docs/INTEGER-SEMANTICS.md`, claims `IS-01`–`IS-07`.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `add` | `i i → i` | `Width` | |
| `sub` | `i i → i` | `Width` | |
| `mul` | `i i → i` | `Width` | |
| `div_s` | `i i → i` | `Width`, `Unsigned` | `x / 0 == 0`; `MIN / -1 == MIN`. Never traps. |
| `rem_s` | `i i → i` | `Width`, `Unsigned` | `x % 0 == x`; `MIN % -1 == 0`. |
| `and` | `i i → i` | `Width` | |
| `or` | `i i → i` | `Width` | |
| `xor` | `i i → i` | `Width` | |
| `shl` | `i i → i` | `Width` | Count masked to `& 31` / `& 63`. |
| `shr_s` | `i i → i` | `Width`, `Unsigned` | Arithmetic when signed, logical when `Unsigned`. |
| `not` | `i → i` | | Logical `!` — 1 iff the operand is zero. |
| `clz` | `i → i` | `Width` | Defined at zero: yields the operand width. |
| `ctz` | `i → i` | `Width` | Defined at zero: yields the operand width. |
| `popcnt` | `i → i` | `Width` | |
| `eq` | `i i → i` | `Width` | |
| `ne` | `i i → i` | `Width` | |
| `lt_s` | `i i → i` | `Width`, `Unsigned` | |
| `le_s` | `i i → i` | `Width`, `Unsigned` | |
| `gt_s` | `i i → i` | `Width`, `Unsigned` | |
| `ge_s` | `i i → i` | `Width`, `Unsigned` | |

### Float arithmetic and comparison

Ordinary arithmetic is IEEE 754 and portable; the edges are
**deliberately** unspecified — `docs/FLOAT-SEMANTICS.md`, claims
`FS-04`–`FS-06`.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `f.add` | `f f → f` | `Width` | |
| `f.sub` | `f f → f` | `Width` | |
| `f.mul` | `f f → f` | `Width` | |
| `f.div` | `f f → f` | `Width` | |
| `f.neg` | `f → f` | `Width` | |
| `f.eq` | `f f → i` | `Width` | Comparisons yield an integer-shaped bool. |
| `f.ne` | `f f → i` | `Width` | |
| `f.lt` | `f f → i` | `Width` | |
| `f.le` | `f f → i` | `Width` | |
| `f.gt` | `f f → i` | `Width` | |
| `f.ge` | `f f → i` | `Width` | |

### Memory

Linear, byte-addressed. Arrays, strings, structs and enum boxes all live
here.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `load_u8` | `i → i` | | Zero-extended byte. |
| `load` | `i → i` | `Width` | `Width: WidthString` loads the two-word pair instead. |
| `store` | `i i → —` | `Width` | Address below value. `Width: WidthString` stores a pair, so three slots. |
| `f.load` | `i → f` | `Width` | |
| `f.store` | `i f → —` | `Width` | |
| `store_i8` | `i i → —` | | Writes the low byte. |
| `alloc` | `i → i` | | Pops a byte size, pushes the base pointer. |
| `match_tag` | `i → i` | | Pops a scrutinee, pushes its variant tag. |

### Strings

These take whole string *values*, so each operand is a pair wherever the
target's ABI makes a string a pair.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `str.eq` | `s s → i` | | |
| `str.cmp` | `s s → i` | | Three-way: negative / 0 / positive. Byte order, ties broken by length. |
| `str.concat` | `s s → s` | | Allocates. |
| `str.len` | `s → i` | | The single seam a small-string optimisation would change. |

### Structured control flow

Scopes are lexical and branches address them by **relative depth** — 0
is innermost — so the op list carries no label numbering. The rules are
wasm's: `block` is a forward target, `loop` a backward one, and `if`
runs its then-arm when the popped value is non-zero.

`I32` on `block` / `loop` / `if` is a **block type**, not a count: one
of `void` (0), `i32` (1), `f32` (2), `i64` (3), `f64` (4), or the
two-word `i32 i32` string pair (5). It says what the scope leaves on
the stack when control falls off its end.

The effects below are the scopes' operand effects. What a scope leaves
behind is its block type's shape, which `end` restores.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `block` | `— → —` | `I32` | Opens a forward-exit scope. |
| `loop` | `— → —` | `I32` | Opens a backward-branch scope. A branch to it restarts it and carries nothing. |
| `if` | `i → —` | `I32` | Opens a conditional scope. |
| `else` | `— → —` | | Switches the innermost `if` to its else arm. An `if` with a non-void block type must have one. |
| `end` | `— → —` | | Closes the innermost scope and pushes its block type's shape. |
| `br` | `— → —` | `I32` | Branch to relative depth `I32`. Everything after it in the scope is unreachable. |
| `br_if` | `i → —` | `I32` | Branch when the popped value is non-zero. |

### Calls

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `call` | `A… → R` | `I32`, `Str`, `ArgTypes` | `Str` names the callee, `I32` counts arguments — not slots. |
| `call_indirect` | `A… i → R` | `I32`, `Sig` | Table index on top. `Sig` is the static signature of the function-typed value dispatched through. |
| `call_dyn` | `i A… i → R` | `I32`, `Sig` | Receiver data below the arguments, vtable word on top; `I32` is the method's slot in the vtable. `docs/DYN-TRAITS.md` §4.2.1. |
| `call_closure_direct` | `A… i → R` | `I32`, `Str`, `ArgTypes` | Environment pointer on top. The defunctionalised form of a `call_indirect` whose receiver was provably monomorphic. |
| `call_direct_pair` | `A… → i i` | `I32`, `Str`, `ArgTypes` | The callee returns a `(tag, payload)` pair in registers. |

A callee that the program does not define — a builtin, or a runtime
helper — has its signature in the backends rather than in the IR.
`internal/caps` is the authority on which names exist;
`internal/ir/verifyprovided.go` is a second, independent record of their
slot shapes, kept deliberately separate from the emitters' own tables so
that the two can disagree.

### Results

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `return` | `T → —` | | `T` is the enclosing function's result shape. |
| `return_void` | `— → —` | | |
| `return_pair` | `i i → —` | | Returns a `(tag, payload)` pair. |
| `make_some.i32` | `i → i i` | | Register-form `Option[T]` / `Result[T, E]` construction, scoped to word-sized payloads. The heap-boxed form still exists for values stored in fields, arrays and maps; the pair form fires when the value flows call-return-style. |
| `make_none.i32` | `— → i i` | | |
| `make_ok.i32` | `i → i i` | | |
| `make_err.i32` | `i → i i` | | |

### Closures and `dyn`

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `make_closure` | `*… → i` | `I32`, `CaptureSlots` | Allocates the environment block, packs `I32` captures into it, and pairs it with the function index. |
| `make_env` | `*… → i` | `I32`, `CaptureSlots` | The environment alone, for closures whose every caller was defunctionalised, so the `{fn_idx, env_ptr}` pair is dead. |
| `box_dyn` | `i i → i` | | Packs `[data, vtable]` into a heap cell on the native backends. Never emitted on wasm, which keeps the fat pointer inline. |

### Reference counting

Dedicated ops rather than calls to the matching runtime helper, so that
rc traffic is structurally visible to the IR passes that fuse and elide
it. All three are pass-through-shaped. What they *mean* is the piece
this file does not have — see below.

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `rc.inc` | `i → i` | `Str` | Returns its argument. Sentinel- and static-guarded. |
| `rc.dec` | `i → i` | `Str` | Returns its argument. Drops one reference; may free. |
| `rc.is_unique` | `i → i` | `Str` | 1 iff the refcount is 1. A sentinel or static answers 0. |

### Other

| Op | Effect | Imm. | Notes |
| --- | --- | --- | --- |
| `drop` | `* → —` | `Width` | Discards one slot, or the two-word pair under `Width: WidthString`. **Not** an rc operation: `rc.dec` is. |
| `line` | `— → —` | | A source-position marker carrying `Pos`. Emitted only under native `-g`; it produces no code, and the byte-identical self-host fixpoint never sees one. |

## How this is kept true

Two gates, in `internal/ir` so they can drive the package directly.

`TestCoreOpsIndexIsAccurate` matches the table against the `OpKind`
enum in both directions: every op has exactly one row, every row names a
real op, and no op's mnemonic is `<invalid>` — which is what a new op
added without a `String()` case produces, and the shape in which an
instruction would otherwise arrive in the language undocumented and
unnamed. It also checks that every field named in an `Imm.` cell is a
field `Op` actually has, because a plausible misspelling is exactly what
a reader would take at face value.

`TestCoreOpEffectsMatchTheModel` checks the `Effect` column by
**running** it. For each row it builds the declared operand stack, steps
the verifier's stack model over the instruction, and requires the
resulting stack to be what the row promised — at **both** pointer
widths, which is what gives `s` its teeth: a row claiming `s` is
checked as one slot on the register backends and as two on wasm, and a
row claiming `i` where the real op moves a string pair fails on wasm
only.

The model is `verifystack.go`, which the corpus gate already runs over
every lowered program in `conformance/cases`. So a row here is tied to
the same code that is checked against real compiler output, rather than
to a second transcription of it that could agree with the table and with
nothing else.

## What this does not say

Everything that matters. This file says `rc.dec` pops a pointer and
pushes it back; it does not say that it decrements a count, that
reaching zero frees, what freeing does to a value's fields, or in what
order. It says `alloc` pops a size and pushes a pointer, and nothing
about what makes two pointers different. It says `call` pushes the
callee's result and nothing about when the callee runs.

Those are the store semantics — Layer 3's second and third pieces — and
they are where the bugs actually are. `docs/ALLOCATION-OBSERVABLE.md` is
the only part of the store that is currently pinned at all, and it pins
one shape: that a reclaiming loop does not grow the arena without bound.

Reading this file as a definition of Fern Core would be the same mistake
as reading a wasm opcode table as a definition of WebAssembly. It is the
vocabulary. The sentences are not written yet.

## Instructions the corpus does not reach

`TestCoreOpsAreReachedByTheCorpus` lowers every conformance case and
tallies the instructions that come out. This is the check
`spec/README.md` calls the one that matters most for a normative
document and the one that is easy to leave out: the effects gate above
proves a row agrees with the verifier's model, and proves nothing about
whether any Fern program produces the instruction at all. An invented
row would read exactly like a true one.

Run for the first time it found **19** unreached instructions. Fifteen
were a corpus gap rather than a language fact, and closed by five cases
— `float_arith_ops`, `float_width_conversion`, `float_bit_reinterpret`,
`result_register_pair`, `dyn_trait_dispatch`. Float subtraction and
negation, `f32`/`f64` conversion, all four bit reinterprets, the
register-form `Ok` / `Err`, and every `dyn Trait` dispatch had **no**
conformance coverage; `dyn` had two compile-error cases and nothing that
ran one.

Four remain, and all four are unreachable by construction: they are
produced by IR passes that run *after* lowering, so no source program
can be written that makes `LowerWith` emit them.

| Op | Why |
| --- | --- |
| `local.tee` | Introduced by the tee pass, which fuses a store and a following load. Lowering emits the pair. |
| `call_closure_direct` | Introduced by the defunctionalisation pass when a `call_indirect`'s receiver is provably monomorphic. |
| `make_env` | Introduced by the same pass, when every reader of a closure became a `call_closure_direct` and the `{fn_idx, env_ptr}` pair is dead. |
| `line` | Emitted only under the `EmitLineMarkers` lower option, which is native `-g`. The byte-identical self-host fixpoint depends on ordinary builds never seeing one. |

A row here is checked in both directions: an entry the corpus has
started reaching is reported and must be deleted, so the list shrinks as
cases are added rather than quietly going stale.
