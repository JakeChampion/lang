# The construction-retain matrix: what is left, and what the last cell taught

*2026-08-26*

Written after #7548 and #7558 took the matrix from 12 leaking cells to 10. The
per-slice records are `2026-08-26-arrstruct-append-built-producer.md`,
`2026-08-26-arrstruct-counted-field-share.md`,
`2026-08-26-arrenum-producer-and-append.md` and
`2026-08-26-arrenum-counted-field-share.md`. This one carries the map of what
remains and a full diagnosis of the next cell, so the next attempt starts from
measurements rather than a reading.

## The 10 remaining cells

| group | cells | note |
|---|---|---|
| `str` | `__local` `__param` `__fieldread` | retain gated on slit_reclaim + the whole-program STRFLDOK verdict |
| `str_arr` | `__local` `__param` `__fieldread` | the same verdict, string-array flavour |
| `enum` | `__param` | param enum bare ident |
| `enum_arr` | `__param` `__fieldread` | |
| `struct_arr` | `__param` | |

Two natural groupings, and both are bigger than one cell:

- **The four `__param` cells** (`enum`, `enum_arr`, `struct_arr`, plus the `str`
  ones). `slot_is_reclaimable_arrstruct` refuses a slot index below `s.n_params`
  outright, and `slot_is_reclaimable_arrenum` reads a credit no param slot
  carries. Probably one shared question — a param is the caller's, so any credit
  here is really about ownership transfer — and probably larger than it looks.
- **`str` / `str_arr`, 6 of the 10**, hang off the whole-program STRFLDOK
  verdict, which `struct_routes_field_reclaim_at`'s header calls out as the thing
  every analysis deciding a string-field store must consult. Widest group; wants
  its own scoping pass and a registry-level change rather than a lowering one.

## `enum_arr__fieldread`, diagnosed in full

`var q: P = P { f: mkv(i), n: i }; var p: P = P { f: q.f, n: i };` where
`f: E[]`. 600 allocs / 300 frees — half the frees missing, and the worst single
cell left.

It is **not** the missing-retain slice it looks like. Three findings, each
traced rather than reasoned, and the first two hypotheses were wrong:

**1. The struct credit IS granted.** Tracing every gate in the credit expression
(`st_esc_ok`, `reassigned_from_alias`, `bas_ok`, `struct_returned_bare`,
`struct_arg_to_handback`) shows `q` and `p` passing all of them, identically to
the CLEAN struct twin `cr_sa_fieldread`.

**2. The release is withheld by NODEEP**, on both holders. Tracing
`emit_struct_field_drops`' early-returns:

| probe | slot 1 | slot 3 |
|---|---|---|
| one holder, call-init field (clean) | routes=true nodeep=**false** | — |
| struct-array twin (clean) | routes=true nodeep=**false** | routes=true nodeep=**false** |
| enum-array (leaks) | routes=true nodeep=**true** | routes=true nodeep=**true** |

`routes` is fine throughout — `struct_has_reclaim_array_field` does admit
enum-array fields.

**3. There are TWO markers, for two different reasons**, and both are correct as
the code stands:

- `p` (init `P { f: q.f, … }`) by `struct_lit_unretained_borrow_field`, whose
  exclusion list covers scalar / struct / enum / leaksafe-array / struct-array
  field types. Enum-array is absent, so the read counts as an un-retained borrow.
- `q` by `optstruct_body_moves_field`, which names MOVE positions positively; a
  field read sitting in a struct-literal field IS such a position, so `q` reads
  as having moved its field out.

So the fix is three coupled parts: retain the read, and stop each marker firing —
each only where that retain actually fires.

### The half-fix was built, measured, and reverted

Extending the `is_array_type_name` fallback to `ExprFieldAccess` does emit the
inc (`__fern_rc_inc` appears where it was absent) and **moves nothing** — 600/300
unchanged, no regression. Reverted rather than shipped: on its own it is an
unbalanced inc that guarantees the buffer never reaches 0, making the leak
structurally worse while the census number stays identical.

### Why the obvious alignment is unsafe

Tempting: make the retain WIDER than the exclusion so the exclusion can never
outrun it. Over-retaining is normally the safe direction — a leak, not a crash.

Not here. `__fern_rc_inc` deliberately has **no scalar guard**; irlower's own note
records that "a scalar reaching rc_inc is a compiler bug, and the missing guard
is what turns it into an immediate SIGSEGV instead of a silent no-op that
corrupts eight bytes and returns" (#7368, where a slot held 0x80000000 and it
dereferenced 0x7FFFFFF8). Widening the retain past what the slot types justify
trades a leak for a crash class.

### The shape that works

The codebase already has the mechanism: at the alias-bind site, where the retain
is emitted at lowering time, it DROPS the `"NODEEP:"` row
(`drop_reclaim_row("NODEEP:" + …)`). Doing the same at the field-read retain
makes the two sides the same set **by construction**, needing no two predicates
to agree. The work is finding the var-statement lowering site holding both the
binding key and the emitted-retain fact — `lower_expr(ExprStructLit)` has the
retain but not the binding.

### It is not enum-specific

`is_enum_array_field_type` bottoms out in `is_enum_like_name`, a NEGATIVE test
(not scalar, not struct, not array, not generic, not tuple). A `pub type A | B`
UNION name passes it. Verified: a union-typed array field read measures the
identical 600/300, 13600 bytes.

So `parser.Stmt[]` is in this class, which puts `FuncDecl.body`,
`StmtIf.then_body` / `else_body`, `ExprLambda.body` and `Module.top_stmts` inside
it. **Do not widen the outer struct-literal admission gate** as a first move: it
would engage `if (!fav_ok) { return s.fail(); }` across all of them, which is
very likely why that gate excludes enum arrays today.

## Instruments for this class

Established in #7558 and unchanged. In order of what each can see:

- census → **blind, and actively misleading**: removing the move gate took
  `moved_ret` from 500/100 to 500/400, which reads as an improvement and is a
  double free
- `__rc_underflow_count()` → blind, because this class FREES element boxes rather
  than deccing them, so no counter is bumped
- `TestSelfHostStage2FixpointArm64` → caught the arrstruct version of this bug,
  blind to the enum one, because the compiler's own source lacks the shape
- reading the value back → catches it

Any change granting an enum-array release needs the last one: read the payload
after the callee returns, with allocation churn in between so freed memory is
reused, and check the value. The template is the `moved_uaf` case in
`internal/e2eselfhost/self_host_arrenum_field_share_test.go`, which segfaults
(139) against native and interp's 25 when its gate is removed.
