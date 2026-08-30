# The result axis, and the phi it exposed (2026-08-30)

`2026-08-30-unit-holder-set.md` ended by naming the next slice: 1,527
call results over the census-clean fixtures were `UnitUnknown`, because
`rcsigs.go` models what a callee does to its ARGUMENTS and says in its
own header that the result axis is not modelled. #7786's definition of
the signature table asks for both halves.

`internal/ir/rcresults.go` is the second half. It also broke something
loose that had nothing to do with it.

## Five answers, not a boolean

A boolean "does the caller own the result" merges facts that decide
whether an emitted release is correct, inert, or corruption:

| | header at `[ptr-8]` | what a release does |
| --- | --- | --- |
| `RcResultOwned` | live `rc=1` | reclaims |
| `RcResultImmortal` | `0x80000000` | nothing — every helper short-circuits on the high bit |
| `RcResultRaw` | none | reads a neighbouring object's payload |

**Nineteen of the twenty-one `Result` / `Option` / `IoError`-returning
helpers are immortal.** Calling them owned would emit a dec at every
call site that cannot possibly reclaim. Two further buckets —
`RcResultBorrow` and `RcResultOperand` — carry the element addresses and
the argument-renaming family.

## The names lie, and that is the method

Every entry was read off the helper's body. Three that a name rule gets
wrong, each caught by reading:

- **`__alloc_u8`** sits beside the raw allocator in the inert list and
  is not raw: it lays down `cap@-12`, `rc=1@-8`, `len@-4`.
- **`__fern_read_dir_raw`** puts a used-byte count at `buf-4`, which is
  exactly where `__alloc_u8` puts its length — and it is NOT that
  layout. It is `__fern_alloc(cap+4)` with a hand-written prefix and no
  rc word. I classified it owned from the offset, then read
  `buildReadDirRawBody` and corrected it.
- **`__fern_arg_at` and `__fern_env_at`** have the same signature and
  opposite answers: `env_at` copies through `__fern_str_copy` and hands
  back an owned string, `arg_at` returns a view straight into
  `argv_buf`. `__fern_str_copy`'s own doc names that view as the thing
  it exists to convert.

## The gates

All 147 wasm runtime helpers are classified, enumerated by the same loop
`TestRcSigsCoverEveryRuntimeHelper` uses, so a new helper now fails
BOTH axes until someone answers both questions. Three more checks, each
a second record disbelieving the first:

- a bucket claiming a pointer must have a single `i32` result — settled
  by the registry's own valtype, no reading required, and it rejects the
  33 float and void returns mechanically;
- every name in `runtime.go`'s `helperAllocBoxCallers` must read
  immortal, so a new box caller fails here rather than silently reading
  as owned;
- the two axes must agree about aliasing: `ResultIsOperand` on one side
  iff `RcResultOperand` on the other.

**`providedSigs` is not gated on this axis, and that is a stated gap.**
68 of its 269 names remain, and they are one coherent set — the Map,
Cell, MapIter and Array builtins, whose bodies are Fern rather than
runtime. Their ownership is the interprocedural fixpoint's answer, and a
table entry would be a second model of the same fact.

## What it measured, and the surprise

| | flagged | unplaced call results |
| --- | --- | --- |
| before this slice | 8.49% — 62 of 730 | 1,527 |
| + the result axis | **11.64%** — 85 of 730 | 498 |
| + the phi transfer | **2.05%** — 15 of 730 | 498 |

The middle row is the honest one. Placing 1,029 previously-unknown call
results made the walk **worse**: it converted "cannot say" into "says,
and is sometimes wrong", and a new `call x27` class appeared,
concentrated in `int____int_to_string_u64`.

Reading that one function is what found the real defect:

```
v25 = call "__alloc_u8", v24              ; a fresh owned buffer
v31 = phi v25 [block 11], v63 [block 15]  ; threaded through the loop
```

Everything after the join names the PHI. The release lands on `v31`, and
a walk keyed on `v25` never sees its own disposal.

**That is the limitation `2026-08-30-ownership-signature-table.md`
already recorded from the other side** — *"`aliasesOf` does not cross
phis, and that is the real limitation … this is per-path accounting,
which is the certifier, not a widening of the closure."* This is the
certifier, so the rule belongs here: a value that feeds a phi hands its
unit to the phi, attributed to the EDGE so it only loses the unit on a
path that reaches the join. The same shape as the store and the closure
capture, and the same shape as the interior-address fix one level down —
follow the object to the name the code actually uses.

It closed `alloc x151` and `call x27` **together**, which is why the
result axis is not what the 2.05% is evidence for. The axis is worth the
1,527 → 498; the phi rule is worth 11.64% → 2.05%; and the axis is what
surfaced the phi.

## What is left

`make_closure`, 102 findings, the only class remaining. A closure cell
is 32 bytes from `__fern_alloc_rc1` at rc=1 and lowering does not always
emit its release; closure reclamation is already on
`docs/TEST-GATES.md`'s live gap list, so whether the walk is missing a
transfer or the compiler a drop is genuinely open. It is the next thing
to read one of.

The 498 remaining unplaced results are defined callees, which is the
fixpoint's half: `ownership_returns.go` proves `ReturnBorrowed` and has
no way to say `ReturnOwned`, because `clsOwned` is a union of "allocates"
and "not understood" and only the second may block a proof. Splitting it
is the follow-up, and the 4,492 `OpAlloc`-blocked classifications it
already counts are the number to move.
