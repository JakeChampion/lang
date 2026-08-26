# The array-of-structs producer registry refused the append-built return

*2026-08-26*

## What was measured

Scoping the construction-retain matrix's `struct_arr__local` cell, the halves were
split as its header asks. The alias-bind half is clean; the field-share half
leaks. Reducing the field-share half to one round inverted the expected answer:

| probe | native | self-host |
|---|---|---|
| share taken at runtime | 5/5 | **5/5** |
| share NOT taken at runtime | 4/4 | 4/2, 88 B |

The sharing path was already correct. Stripping the struct literal out entirely
left the leak standing:

```fern
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function round(i: i32): i32 {
    var src: Inner[] = mkv(i);
    return src.len() + src[0].k;
}
```

4 allocs, 2 frees, 88 bytes per round, unbounded, against 0 on native and interp.
So the matrix cell was not measuring a construction-retain hole at all — the
struct literal in it is incidental, and its `mkv` is the whole story.

## Cause

`fn_returns_fresh_arrstruct` proved a producer by requiring every return to be a
fresh array LITERAL (`arrstruct_lit_is_fresh`, `bare_ok=false`). `mkv` returns an
append-built local, so it registered nothing, so `collect_fresh_arrstruct_names`
refused the consumer's `var src: Inner[] = mkv(i)`, so the slot took the shallow
buffer dec and every element box and element array field stranded.

The one-statement diff that isolated it — the same producer written to return the
literal directly measures 3/3 clean.

The asymmetry is with the class's own local credit: `ARRSTRUCTA:` (#6535) already
trusts an append-built local inside one frame. Only the RETURN of one was refused.

This is the arrstruct twin of #7335, which found and fixed the identical hole in
the `arrarr` "AAC:" registry. The wording there applies unchanged: *returning a
local is the form real code has — anything that builds elements before handing
them back cannot use the literal form — so the refused case was the common one.*

## Fix

`body_has_nonfresh_arrstruct_return` now also accepts a return of a local proven
by `arrstruct_producer_ret_local`: declared in this body from an array literal of
fresh struct literals (`[]` included), every reassignment a self-store whose
element is a fresh struct literal, and no escape other than the return
(`body_unsafe_for_allow_ret`, the gate `append_built_row_local_kind` already uses
for the row twin).

Strictness is the registry's question, not the local credit's. Inside one frame
an appended bare IDENT is fine — the append retains it and both owners walk under
`__fern_rc_is_unique`. A returned container's counted co-owner would be a local of
the frame being left, which is why `arrstruct_lit_is_fresh` takes `bare_ok=false`
here; the append-built arm inherits that, via `arrstruct_fresh_store_elem` rather
than the counted-share `arrstruct_self_store_elem`.

## Measured after

| probe | before | after |
|---|---|---|
| `dp_sa_call` (bare local from producer) | 4/2, 88 B | **4/4** |
| `producer_returns_local` (200 rounds) | 1200/400 | **1200/1200** |
| `producer_returns_literal` | 1000/1000 | 1000/1000 |
| `literal_init` | 1000/1000 | 1000/1000 |
| `producer_bare_ident_elem` | refused | refused, safe leak |
| `producer_foreign_rebind` | refused | refused, safe leak |
| `producer_local_escapes` | refused | refused, safe leak |
| `sibling_alias` | 203/201 | 203/201, no underflow |

Every exit code matches native and interp. Nothing exits 99 — #7335 records that
widening its registry alone took the same shape from 34 to 99 because the credit
was name-keyed; arrstruct's is site-keyed already (#7253), and `sibling_alias`
pins that rather than assuming it.

## What this did NOT move

The construction-retain matrix is unchanged: all 35 cells still measure as pinned.
`struct_arr__local` and `__param` stay `leak` because binding the source into a
struct-literal FIELD revokes its credit through the escape gate, which is a
separate question — the source's share there is counted (the field retain is
emitted; the sharing path measured clean above), so granting the source its own
rc-gated walk is the next slice. Half of that pairing is not half a fix: with one
owner gated and the other walking statically, both sweeps free the buffers and it
surfaces as exit 99, invisible in the census.

The enum-array sibling leaks the same way (`var src: E[] = mkv(i)`, 4/2) and is a
larger slice: `ARRENUM` has neither an append-built local credit nor a producer
registry, so it needs both halves the struct side already had.

`return mkv(i).len()` — a producer result consumed as a temporary with no local
bind — leaks the whole structure (4/1) on both the fixed and unfixed compiler.
There is no name to credit there; a separate question.

## Gates

`TestSelfHostArrStructProducerX86_64` (new, 72 s), `TestSelfHostRcPlanDiff`,
`TestSelfHostConstructionRetainMatrixX86_64`, `TestSelfHostContainerSinkMatrixX86_64`,
`TestSelfHostArrArrProducer*`, the arrstruct family, `TestSelfHostLeakMatrix`
(46 s), and the three x86-64 fixpoints (368 s). All green.
