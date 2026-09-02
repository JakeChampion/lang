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

## The obvious fix is a use-after-free — do not take it

`lower_stmt_match` already has the release site and the helper: after the outer
`end`, `if (match_scrut_is_map_get(m, ms)) { ms = emit_scalar_enum_box_free(ms,
scrut_slot); }`. Adding a second gate for the enum-field shape
(`enum_field_read_type(m.scrutinee, s).len() > 0`, `@` bindings refused as the
map_get gate refuses them) is a four-line change, and it looks right:

- `w_inlineenum` and `v_inline` go to **200/200, live_bytes 0**
- the matrix cell goes 200/0 to **200/100** (4,800 B, half closed)
- every previously-clean control stays clean
- `__rc_underflow_count()` stays 0 — every cell still exits 53, not 99

It is still wrong. Under `FERN_SANITIZE`, where nothing is recycled and a
touched freed block traps:

```
fern-sanitizer: use-after-free (touched a quarantined block)
```

The unfixed baseline is UAF-clean under the same leg; the scrutinee release
ALONE introduces it, with the freshbox credit not applied. So the field read is
a **borrow**, not a counted alias: the box stays owned by the struct, and
releasing `$mscrut` frees it while the struct still holds it. The refcount never
goes negative, which is why the underflow counter and the leak census both pass
a program that is reading freed memory.

That kills the whole "release the scrutinee" direction, and it is the third
hypothesis this one cell has defeated.

## What the sanitize leg is for

`TestSelfHostLeakMatrixX86_64` compiles each cell twice — once for the census,
once under `FERN_SANITIZE` — and says so itself: *"a latent defect the census
could not see; fix it, never pin it"*. The census sees only the leak direction.
Any fix here has to clear BOTH legs, and a green census plus a zero underflow
counter is not evidence of soundness. It was the only thing standing between
this change and a pushed use-after-free.

## What is NOT established

Stacking the freshbox credit (`freshbox_ret_fns_of`) on top of the scrutinee
release takes the matrix cell to 200/200 and the whole x86-64 matrix to 134
clean/clean rows. That result is built on the unsound release, so it proves
nothing about the credit on its own — measured alone, it still moves the matrix
by zero rows. Do not read the 134 as a green light for either half.

## Next lead

The struct's deep drop IS emitted in the leaking form — `__struct_drop_H` is
called the same number of times in all three variants — so the missing release
is not a suppressed `emit_struct_field_drops`. What differs is the
`__fern_arr_dec` count between the never-read control (12), the bound form (9)
and the leaking one (8), and only the last pair differs by a single edit.

Look for what the match-on-field does to `h`'s own eligibility, not for a
release to add at the match. And validate under `FERN_SANITIZE` from the first
build, not at the end.
