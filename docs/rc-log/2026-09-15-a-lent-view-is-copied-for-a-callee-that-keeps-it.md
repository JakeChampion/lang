# A lent view is copied for a callee that keeps it

The second half of #9407. With the `own` array boundary fixed
(`2026-09-15-an-own-array-consumed-across-the-mixed-boundary.md`), the
sanitized produced compiler compiled `lexer.fern` without an abort — and the
plain one still emitted `xorl; movq $o, %rcx; subq` for `0 - 1` at the same
three sites. A seven-line program reproduces it: `var v: i32 = -1;` compiles,
through the produced compiler, to `movq $e, %rcx`, and through the sanitized
produced compiler to the right text.

## What the trace said

The constant op's `str` field was not overwritten. It held a view box, intact,
whose bytes pointer led into a block with this history (`FERN_RC_TRACE=1`,
`FERN_RC_TRACE_DEEP=1`, pointers resolved through `nm -n`):

| event | site | caller | one above |
|---|---|---|---|
| allocated, 32 bytes | `__fern_str_concat` | `constfold.lit_int` | `constfold.fold_unary` |
| the view box, 24 bytes | `constfold.lit_int` | `constfold.fold_node` | `fold_block$wrap0` |
| freed | `__fern_str_free` | `constfold.fold_node` | `fold_block$wrap0` |
| reissued | `checker.vb_holds_local`, then `treeshake`, then `lexical` | | |

`lit_int` builds the literal's text (`util.i64_to_string(v)`, the 32-byte
block), slices the sign off it (`slice_unchecked(s, 1, s.len())`, the view
box) and hands the view to `num_lit`, which stores it in the `ExprNumber`.
The produced `lit_int` then releases `s` on its way out, as the ledger says
it should: `s` is a unit of that frame's own, and nothing in the graph reads
it afterwards. The stored view reads the block after five later owners.

The sanitizer is silent because nothing touches a freed rc: the read is of
bytes, through a box that was never freed, and the quarantine keeps the bytes
where they were. A hardware watchpoint on the source byte never fired for the
same reason — the source string's bytes were never written; the literal's
text was a DIFFERENT view, the folder's, over a different source. Dump the
op's `str` pointer before trusting the shape the assembly suggests.

## What #9328 had left

#9328 found that a callee lent a view may keep its BOX, and answered it at the
release: the frame that sliced the view released the handed box with the
counted `__fern_str_free`, which stands down on a view's immortal count. That
closed the box's double free and said nothing about the bytes, which belong to
the view's source. A source the caller does not own outlives the frame (the
lexer's `l.src` is the caller's `src`); a source the frame built is released
by it, and a view stored past that read is reissued memory.

## What landed

A callee that may keep what it is lent (`semsource.handers`, the same body
property #9328 read) is handed a COPY: `semsource.lend` defines `v + ""`
where it defined the `str_as` retag, an owned string the ledger counts as a
unit of the caller's own. The callee's retain of what it keeps is a real
increment of a counted box; the caller's release of its copy at exit leaves
that reference standing; the source string is released as before, and no
view into it outlives the frame. An indirect call has no body to ask, so it
is handed a copy for every lent view.

The machinery the release-side answer needed goes with it: the `lent` set,
`escaping_args` and the mask it wrote into a call's `imm` (the verifier now
holds a call's immediate at zero), `ssaunits.handed_on`, `Plan.shared`, and
the `shared` case in `ssarc.drops`.

`TestSelfHostSemanticSourceRC` gains `lit_bytes`: the folder's shape, then
sixty-four allocations sized to reuse the released block, then a read of the
stored text; it answered wrong through the produced lowering before this and
right after. The AST lowering answers it right by leaking the source (200
bytes on the reproducer), which the production test's leak comparison reads
as the produced bodies freeing more.

## Measured

The compiler built through the semantic path (7,568 of 8,294 declarations
produced), on `lexer.fern` and the 200-declaration input: assembly
byte-identical to the AST build's on both, where the build before this
differed by 150 lines on `lexer.fern`; its sanitized build compiles
`lexer.fern` without an abort. The copies cost nothing the instruments read:
`lexer.fern` 9.4 s at a 6,865 MB peak against 9.2 s and 6,895 MB before, the
200-declaration input 6.4 s at 5,566 MB against 6.9 s and 5,580 MB, and the
sanitized build's live-at-exit 29.22 MB against 29.18 MB.

The stored-view reproducer (`lit_int` over a churn of sixty-four strings),
x86-64, through the rebuilt compiler:

| lowering | answer | live at exit |
|---|---|---|
| AST | right | 200 bytes in 6 blocks |
| produced, before | wrong (`2|42`) | 24 bytes |
| produced, after | right | 0 |
| produced with `lit_int` or `num_lit` on the AST lowering | right | 2,248 bytes |

The mixed rows leak what the AST-lowered frame leaves, as every mixed
configuration does; the answer is right in each.

The fixture, with the four lowering files checked out from before the fix:
`lit_bytes(1)` printed 1822 for 1804 and left 24 bytes live, on x86-64 and
arm64 alike; the byte sum reads the reissued block, and the 24 bytes are the
handed box the counted release stood down on.

## Traps

**A read of freed bytes is invisible to the sanitizer.** The quarantine
catches an rc touched after its free; a byte read through a live view is
neither. When the sanitized build answers right and the plain one wrong, the
fault is bytes, and the instrument is the trace paired with a dump of the
pointer the wrong output was printed from.

**Reissue is deterministic, so a fixture can pin it.** The freelist is per
word count; allocations of the same size after the release land on the block.
`lit_bytes` allocates sixty-four strings around the size of the source and
reads the stored text after them, which is what turns "sometimes right" into
an answer the test can compare.

**`FERN_SEM_IR=0` turns the path ON.** Every switch here is read by presence
(`match (env(...))`), so a leg meant to be the AST lowering has to unset the
variable, not zero it. Two legs of one comparison were both produced before
this was noticed.
