# The un-share copy secures its elements, so every array carry counts

Refs #9012 (main red on `Test e2e self-host`), continues
`2026-09-10-spread-carry-counted.md`.

## What was red

Three `respread` rows — `TestSelfHostArrEnumFieldReadShareX86_64`,
`TestSelfHostArrEnumFieldShareX86_64`, `TestSelfHostArrStructFieldShareX86_64` —
each a struct with an enum[] or struct[] field, a counted field share of it,
and a `P { ...p, … }` over one of the holders:

| row | before the counted carry | after it (#9000) |
| --- | --- | --- |
| arrenum field share | 600 / 600, live 0 | 600 / 200, live 18400 |
| arrenum field-READ share | 700 / 700, live 0 | 700 / 300, live 18400 |
| arrstruct field share | 600 / 600, live 0 | 600 / 200, live 19200 |

The previous entry made the base copy retain every array it carries, which is
what stops a unique box's receiver-field append from growing the base's buffer.
It then credited the copy as sole owner only for scalar-element carries: a
pointer-element buffer the copy later un-shares is copied with its element
pointers duplicated and no count, and a deep drop through the copy would free
the base's elements. So the retain on a string[] / struct[] / enum[] carry was
taken and never paired — the copy held a count nothing released, and the whole
carried structure leaked with it.

## The fix

The count is right; what was missing is the contract behind every copy of a
pointer-element buffer. Two copies duplicated element pointers raw:
`__fern_arr_push`'s un-share of a shared receiver, and the `arr_slice` clone
the value-form `.append` / `.with` take of their receiver. The lowering knows
the element kind at each, so it retains the elements first — ahead of the push
in `lower_field_append_inplace`, the one site that hands a possibly-shared
pointer-element buffer to arr_push,

```
if (!__fern_rc_is_unique(buf)) { __fern_arr_inc_elems(buf); }
```

and on the clone in `lower_arr_append_value` / `lower_arr_with_value`
unconditionally, since a clone always duplicates. `__fern_arr_inc_elems` is one
guarded `rc_inc` per element (x86-64, arm64, wasm bodies; irexec arm;
inventory, `is_fern_helper`, `is_rc_noop`, the per-module export list). After
the copy both buffers hold a count of each element. The release walks were
already gated per buffer AND per element (`__struct_arr_elems_drop_<E>` /
`__enum_arr_elems_drop_<E>` skip a shared element's payload,
`__fern_arrarr_free` decs it), so the last owner frees each element exactly
once and the earlier ones only dec. The two `.with` forms hand back the count
of the one element their store replaces, so a clone holds exactly what it
keeps; the in-place unique arm still leaves the replaced element alone (#8310).

The clone was the finding the fixpoint made. With the field-append site
secured alone, gen1 of `TestSelfHostPerModuleEmitAllFixpointX86_64`
segfaulted on `-per-module-func-counts`: crediting a spread copy credits its
OVERRIDES too, and `LowerState { ...st, ops: st.ops.append(op) }` is a clone
of a pointer-element field the new box then released deep, elements under
`st`. The same shape with no spread was already an over-release on main —
`var p: P = P { f: q.f.append(E.B), n: 1 }` in a branch, then `q.f[0]` read
back: exit 99 (rc underflow) on main's compiler, the interpreter's 69 here.

With that, `spread_copy_field_counted` counts every array kind, and the copy
is a counted holder released by the same walk as an explicit
`P { f: q.f, … }` holder. Nothing static distinguishes them any more, so the
three refusals written against the uncounted carry are gone:
`arrstruct_share_holder_respread`, `arrenum_share_holder_respread` (and the
by-name "N:" rows and holder collectors only they read), and the field-type
row in `enum_arr_field_share_read`. `spread_sites` keeps its "FT:" rows for the
STRING limb: a string field is retained only when the type routes field
reclaim, so an unrouted spread still mints an uncounted co-owner there.

## Measured (self-host x86-64 unless said, 100 rounds)

- The three rows above: all 600 / 600 resp. 700 / 700 at live 0, exits the
  interpreter's. The field-READ row needs the field-type refusal lifted —
  with the by-name gates lifted but that one kept it measured 700 / 400.
- `TestSelfHostSpreadCarryElems{X86_64,Arm64,Wasm}` + the sanitizer leg: a
  spread copy of an enum[] and a receiver append through the copy (twice, so
  the second append grows the first one's copy), the same append through the
  BASE, and a `.with` through a struct[] copy. All balance at live 0 on all
  three backends; the sanitizer reports nothing. The append-through-copy row
  on main's compiler: 1900 / 400.
- `TestSelfHostAliasReassignReclaimIRX86_64/spread-carry-owned` (the #8983
  program) stays 0 with the underflow check.
- `clone_override` / `clone_override_spread` in the same suite: 1000 / 1000 at
  live 0 and the interpreter's exit, against exit 99 (no spread) and 1000 / 900
  with a wrong-free-free (spread) on main.

## The string[] residue is a different gap

The same append-through-copy shape with a `names: string[]` field leaks 3
blocks a round here (900 / 600), against 900 / 300 on main. That is not the
carry: with no spread anywhere, `var s = Sc { names: [] }; s = s.bind("a");
s = s.bind("b")` leaks 2 blocks a round (500 / 300) on main and here alike,
and an explicit `Sc { names: s.names, depth: 0 }` holder measures 900 / 600
on both. The string[] FIELD's rebind release is refused by `strarrfld_scan`
(#5338's class) whatever binds the box; a spread copy now costs exactly what
the explicit holder does, and no more.

## Next lead

`return_value_is_strictfresh_struct` still declines an override whose field is
a struct[] / enum[] (its own line, older than this). With the clone secured,
that refusal is conservative for the clone-form override and lifting it is its
own measurement.
