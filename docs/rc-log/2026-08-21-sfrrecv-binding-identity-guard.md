# A borrowing string method's result is releasable at a BINDING too

`var v: str = base.drop(2)` leaked on both backends measured, while the same
call in receiver position (`base.drop(2).len()`) had been flat since #6544's
first half. Per round, one shape per binary, `__heap_bump_bytes()` at 100 and 200 rounds:

| shape | x86-64 | wasm |
| --- | --- | --- |
| `var v: str = base.drop2(2)` (local decl) | 24 → **0** | 120 → **0** |
| `var v: string = base.pad2(4)` (owned box) | 184 → **0** | — |
| `var v: str = base.drop(2)` (std/string) | 24 → **0** | — |
| `var v: str = base.drop2(0)` (identity path) | 0 → 0 | 0 → 0 |

24 bytes is exactly one view box on the register backends; wasm's slice copies,
so its 120 is box + data.

## Why the receiver position could do this and the binding could not

`str_fresh_ret_fns_of` already admitted these methods under `SFRRECV:<Recv>.<m>`
— every return fresh, the bare receiver, or a direct slice of the receiver —
and `sfrrecv_chain_root_slot` already resolved the root a chain hangs off. What
the binding lacked was the credit, and `irlower.fern` said so in as many words:
a binding "has no such discriminator". It has exactly the same one. The
guarded release `emit_str_slot_release` grew for `replace` needed no change at
all: point its recorded source slot at the chain root instead of the replace
receiver and the identity path stops freeing.

The new machinery is therefore one syntactic classifier and a promotion at the
binding site; the release path itself is unchanged. The shape of the family is
the `replace` entry's: *what the receiver is* is a compile-time question,
*whether the result is the receiver* is not.

## The credit had to be a CANDIDATE, not a credit

`reclaimable_names_of` is an AST scan. It can resolve a receiver's DECLARED type
(`sfrrecv_str_init` is the fifth consumer of the declared-name/type pair, after
`tostr_scalar_init`, `join_strarr_init`, `trim_str_init` and `replace_str_init`),
but it cannot resolve a SLOT — and the guard is a slot load. A credit whose guard
slot failed to resolve would emit an UNGUARDED free, which on the identity path
frees a box the receiver still owns.

That is not hypothetical: `var v: str = h.name.drop(2)` is a field receiver, so
the slot walk answers −1 while a name-keyed credit fires happily. So the collector
emits `SFRCAND:<name>` and `bind_var_slot` promotes it to `STR:<name>` only when
`sfrrecv_chain_root_slot` agrees — the `DYNCAND:` → `DYN:` pattern already in the
file. Credit without guard is then unrepresentable rather than merely unlikely,
and the field-receiver case is pinned as REFUSED by a liveness test rather than
by argument.

## Release helper

`str_free_helper` picks `__fern_str_view_free` for these slots, via the same
`str_view_local` flag `trim` sets. It is load-bearing: a link may return
`s[a:b]`, whose box carries the immortal `rc = -1` the `str_slice` op stamps, and
the ordinary free would walk that rc to free the source's bytes. The helper takes
`__fern_str_free`'s path on any other rc, so one release is right for both a view
box and a link that really allocated.

## Witnessed, in both directions

- **The release**: seven cases exit 98 on the parent commit, x86-64 legs — four
  single-file (`tail(4)`, `pad2(4)`, the param root, the two-link chain) and
  three cross-module (`drop`, `pad_start`, `remove_prefix` through the real
  `std/string`). The wasm half of the witness is the table above: 120 → 0.
- **The guard**: a compiler with the credit KEPT and the pointer compare REMOVED
  exits **99 (rc underflow) on x86-64 and on wasm** for `base.drop2(0)`, against
  0 for both a clean main and the fix. The byte gate cannot see this — a missing
  guard scores as an improvement there — which is why the two witnesses are
  separate runs and fail in opposite directions.

## What this reaches, and what it does not

A sweep of 29 `std/string` helpers that can return their receiver, one binary
each, x86-64 bytes/round before → after:

| flat now (24) | still leaking (5) |
| --- | --- |
| take 24→0, drop 24→0, remove_prefix 24→0, remove_suffix 24→0, trim_start_matches 24→0, trim_end_matches 24→0, before 24→0, after 24→0, replace_at 136→0, pad_start 224→0, pad_end 224→0, zfill 224→0, center 224→0, pad_start_str 224→0, pad_end_str 224→0, replace_n 136→0, replacen 136→0, without_chars 128→0, without_byte 128→0, replace_byte 136→0, shift_byte 136→0, to_ascii_capitalize 136→0, to_ascii_title_case 136→0, rstrip_newline 0→0 | trim_chars 96→72, trim_start_chars 72→48, trim_end_chars 72→48, truncate 56→56, ellipsis 56→56 |

(`rstrip_newline` was already 0: the argument used takes its identity path, so
nothing was allocated to leak.)

The two remainders are different gaps, and neither is this one:

- **trim_chars and its two siblings** improve by exactly one box and keep the
  rest. What is left is the INTERMEDIATE link of a chain, which nothing releases:
  freeing one needs the outer callee proven both borrowing and never-a-view, the
  same slice the receiver-position half left open. Do not reach for
  `is_fresh_str_temp` to close it — #6590 records that widening breaking two
  over-release contracts, and #6544's own receiver half re-recorded it.
- **truncate and ellipsis** do not move at all: they never enter the registry,
  because one of their returns is `s[0:n].to_owned()` and
  `body_has_nonfreshrecv_str_return_reg` admits a slice of the receiver only as a
  DIRECT return expression, not as another call's receiver. That is an admission
  widening, and it would change the receiver-position half too — its own
  increment, with its own witnesses.

## Renamed

`LocalInfo.str_replace_src` is now `str_identity_src`: it carries the chain root
as well as the replace receiver, and the question it answers — which slot might
this box already be? — was never replace-specific. The 2026-08-20 entry naming
the old field is write-once and stays as it is.

## Stale comments this retires

`irlower.fern` carried the claim that IR string boxes are "header-less, so an
rc-dec would corrupt the adjacent block" in two places. #2649 made every string
box rc-headered and `asm_ir.fern` says so explicitly at both
`__fn___fern_str_free` and the `str_slice` op; the surviving fact is that nothing
INCS a string on alias, so a dec would be unpaired. Several more copies of the
same dead sentence remain elsewhere in the file, in code this change does not
touch.
