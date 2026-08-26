# A string handed to a struct-literal field keeps no claim of its own

`var src: string = w("k"); var p: P = P { f: src, n: i };` with `src` read
afterwards freed 100 of 300 boxes over 100 rounds. Native freed all of them.
This is the `local` cell of the construction-retain matrix's `str` column.

## Both sides already fired

The literal RETAINS the field — the `ExprStructLit` lowering's
`cfft == "string" && slit_reclaim` arm — and `__struct_drop_P` decs it back. Both
are in the emitted asm, one `__fern_rc_inc` and one `__fern_str_free` inside the
drop, and they balance.

What was missing is the SOURCE's own claim. `src`'s use at the struct-literal
field reads to the escape walker as a store into a container, so it never earned
`"STR:"`, and the reference it still holds at scope exit was never swept. inc 1,
dec 1, and a live box nothing releases.

That is the whole of the defect, and it took a diff of two emitted functions to
see it. The counts do not distinguish "no retain and no drop" from "a retain and
a drop that balance while a third reference leaks" — both read as a leak of the
same size.

## The move site is the gate

With `src` dead after the literal the store MOVES it: `moves_local_at` elides the
retain at the lowering site and `__struct_drop_P` alone frees the box. Already
correct, and granting the source a release there would be an over-release rather
than a leak. So the forgiveness is granted only where the retain actually fires —
the same co-extensivity rule the alias bind and the alias reassign are held to.

`reclaimable_names_of` already carries `msites`, so the credit pass can ask the
same question the lowering site asks. It gained `sfok` (`FnSigs.strfld_ok_types`)
to ask the routing half; the single caller passes `sg.strfld_ok_types`, the same
list the construction-side `slit_reclaim` reads.

Two more conditions, one per remaining way the pair could come apart:

- **The holder must have earned its own struct credit.** A RETURNED holder runs
  no field drop, so the retain would have nothing to give it back — releasing
  the source there frees a box the caller's struct still points at.
- **The type must route field reclaim.** Otherwise `__struct_drop_<T>` carries no
  string arm and never reaches the field.

Ordering makes a classifier mismatch a sound leak rather than a double free: the
exit sweep's struct loop (irlower.fern, the `j` loop) runs BEFORE its string loop
(the `sj` loop), so the field drop always precedes the local's free. That is the
same guarantee the closure and tuple interlocks rest on, and it is why `"SFLD:"`
joins `"OK:"` / `"TUP:"` / `"TUPE:"` in `clo_ok` rather than becoming a fourth
walker.

The tag is built PER CANDIDATE rather than seeded into `clo_ok` once, because the
move gate inside it is a question about this name's use at this literal, not
about the holder alone.

## Measured, x86, 100 rounds, against native

| shape | before | after | native |
|---|---|---|---|
| field source read after the literal | 300 / 100 | **300 / 300** | 200 / 200 |
| holder in a nested block | 300 / 100 | **300 / 300** | 200 / 200 |
| holder in a conditional | 250 / 50 | **250 / 250** | 150 / 150 |
| source read back after churn | 900 / 700 | **900 / 900** | 500 / 500 |
| moved source (control) | 300 / 300 | 300 / 300 | 200 / 200 |
| escaping holder (negative control) | 300 / 100 | 300 / 100 | 200 / 200 |
| source in two fields (negative control) | 300 / 100 | 300 / 100 | 200 / 200 |

Answers unchanged and matching native on all seven. `__rc_underflow_count()` is
0 on all seven. `FERN_SANITIZE=1` reports neither a use-after-free nor a double
free on any of them — the two negative controls report their leak and nothing
else. Native's lower alloc counts are its own const-folding, not a reclaim
difference.

The churn row is what carries the soundness. Counts alone read 900/900 whether
the release is correct or an over-release, so that row reads the source back as a
VALUE after the holder has died with three fresh strings allocated in between: a
box freed too early is reused before the read and the answer stops matching
native's.

The self-host still compiles itself under `FERN_STRICT_IR=1`, 26,635,087 bytes,
no bails.

## The refusals that hold

`escaping_holder_still_refused` and `source_used_twice_still_refused` are what
make this a carve-out rather than a blanket accept. The second is the subtler of
the two: the escape walker's skip is per-STATEMENT, so a second use of the name
in the same literal would be waved through with the first — the sole-use check is
what stops it, and the row is what says so.

## What is left in the `str` column

`str__param` and `str__fieldread` do NOT move with this, and each fails for its
own reason rather than a shared one:

- `str__param` loses the box in the CALLER: `keep` is a string local in `main`
  passed to a callee whose param escapes into the field. The callee's retain and
  drop balance; `main` refuses `keep` its own release. The caller-side sibling of
  #7553, one sink kind over.
- `str__fieldread` emits no `__struct_drop_P` at all — neither holder in that
  cell is swept. A different failure from this one, and it needs its own
  measurement rather than an assumption that this fix generalises.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_struct_field_str_source_test.go`.
