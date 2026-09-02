# 2026-09-02 — `stored_struct` leaks the scrutinee of `match (h.e)`

`enum_scalar__callarg__stored_struct` is the only self-host row in either leak
matrix. Its note named a mechanism; so did the first correction to that note.
Both were wrong, and the controls that killed them are cheap enough that they
should have been run first.

## The shape, narrowed

Every row is native / self-host, `allocs`/`frees`, x86-64.

| variant                                              | native  | self-host          |
|------------------------------------------------------|---------|--------------------|
| struct of scalars only                                | 100/100 | 100/100 clean      |
| struct holding an **array** field, field READ         | 200/200 | 200/200 clean      |
| struct holding an enum, field **never read**          | 200/200 | 200/200 clean      |
| struct holding an enum, **scalar** field read         | 200/200 | 200/200 clean      |
| `var t: E = h.e;` then `match (t)`                    | 200/200 | 200/200 clean      |
| **`match (h.e)`** — match on the field access itself  | 200/200 | 200/**101**, 3,960 B |
| the matrix cell (the above, via a returning callee)   | 200/200 | 200/**0**, 8,800 B |

The trigger is matching **directly on a struct's enum-typed field access**.
Binding that same field to a local first and matching the local is clean, so
the scrutinee temporary is what goes unreleased — not the field, not the
struct.

## What that rules out

**Not the call taint rule.** The original note said native credits the callee
via `findReturnsFreshBox`. Returning that analysis's result set empty, before
its fixpoint runs so nothing is ever credited, leaves the cell at
`allocs=200 frees=200 live_bytes=0`. Native does not use it here. The call is
not even necessary to see the leak: the inline form has no callee at all.

**Not the struct drop.** The first correction to the note said the self-host's
struct drop never releases an enum-typed field. The never-read row disproves
it: the identical struct, identically constructed, is 200/200 clean when
nothing matches on the field. `emit_struct_field_drops` and its
`emit_struct_enum_field_payload_drops` arm are doing their job.

The call form is strictly worse than the inline one — 0 frees rather than 101
— so routing the struct through a callee costs the enum's own reclaim as well.
That is a second effect and nothing here explains it; do not assume one fix
covers both.

## The cost of not measuring first

A subset port of `findReturnsFreshBox` into `irlower.fern` got built before any
of the above was run: `freshbox_ret_fns_of`, a knock-out fixpoint over returns
that are struct / tuple / array literals, threaded through
`rc_fe_rhs_tainted`'s user-call arm. It compiles and is sound, and it moves the
matrix **not at all** — 133 clean/clean and the same single gap, before and
after. It is not landed. An analysis whose stated purpose is disproven and
whose effect is unmeasured is surface, not progress.

`CLAUDE.md` says to verify tracker state against the code because issues here
have repeatedly lagged reality. A gap list is a tracker, and so is a note
someone (here, me) wrote one round earlier.

## Where the release goes missing

`lower_stmt_match` lowers the scrutinee into a fresh scratch local:

```fern
var ms: LowerState = lower_expr(m.scrutinee, s);
var scrut: string = "$mscrut" + util.i32_to_string(ms.locals.len());
ms = ms.add_local(scrut);
ms = ms.emit(ir.op_store_local(scrut_slot));
```

`$mscrut` is written in exactly one place and read in none: grep finds the
construction above and one unrelated comment, and nothing releases the slot.
For a bare-ident scrutinee that is right — the load is a borrow and nothing is
owed. For a field access it is not, and the missing release is what the counts
below show.

WHICH SITE incurs the owed count is NOT established here. It could be the field
read (a dup-at-extract alias) or the struct literal's own construction retain
(`fav_alias_inc`); the closest pair emits the same number of incs either way, so
the asm does not separate them. That distinction is exactly what the fix's gate
turns on, so it has to be settled before the fix, not assumed.

The emitted asm says the same thing, and says which helper is missing. The two
programs differ only by the bind, and their `main` bodies differ by exactly one
call:

```
$ diff <(calls in w_inlineenum) <(calls in r_bindthenmatch)
<       8 call __fn___fern_arr_dec
>       9 call __fn___fern_arr_dec
```

One `__fern_arr_dec`, and it is the ONLY differing call. Not a `__struct_drop`
— that count is identical on both sides, which is the same thing the never-read
control says.

Only that pair is comparable. The counts are static call sites in `main`, and
the other programs differ structurally — the never-read control emits no match
at all and reads 12 — so a count is evidence only against a program that
differs in one edit.

## Next lead

Release `$mscrut` after the match when the scrutinee's lowering took a counted
alias, and ONLY then: an unconditional release would over-free a bare-ident
scrutinee, turning a leak into a use-after-free. The gate wants whatever
predicate already decides the container-read alias_inc — the `rc_fe_rhs_tainted`
FieldAccess arm calls that the "dup-at-extract" pair. `r_bindthenmatch` is the
clean control proving the released path exists and the bound form takes it.

Every leak-matrix cell asserts `__rc_underflow_count() != 0 -> 99`, so an
over-release shows up as a wrong exit across many cells rather than as silence.
Use that as the detector.

Then account for the call form's extra 101 frees separately.

The four arm64 rows reading `leak clean` are native-arm64 `#7446` gaps where
the self-host is AHEAD, and are untouched by this.
