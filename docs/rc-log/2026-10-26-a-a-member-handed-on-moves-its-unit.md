# 2026-10-26 — a member handed on moves its unit

`ssaunits.identity_roots` / `supplied_unit`, `seminline.scrutinee_reads`
and the held seed in `ssaunits.payload_takes`. Fixes #13415, found by
the self-host interpreter's quadratic `append` (#13394).

## The shapes

A frame takes a struct-union value out of an array slot (read it, then
`with` at the same index) and grows an array field of its member. Written
inline, the append grew in place. Three spellings of the same step copied
the buffer on every append:

- **Handed to a callee.** `grow(cur, x)`, where `grow` rebuilds the member
  with `{ ...av, items: av.items.append(x) }`. `cur` is `variant_as VA v`,
  an identity of the taken slot `v`, and is not a unit of its own, so the
  supply was a retain and `v` was dropped after the call. The callee found
  the box shared.
- **Spliced from a callee.** The same callee, small enough to splice. The
  spliced field reads were `record_get` of the narrowed value, which no take
  reads through; the source lowering spells a member binding's field read
  as a `variant_get` of the scrutinee, and a splice did not.
- **The slot emptied by a method.** `taken = env.values[1]` then
  `env.update("xs", …)`, a call that writes the slot. The read is held,
  because `env` goes to the callee while the read is in hand, and a payload
  take out of a held value was refused twice over: `takes_past_holds`
  accepted only owned sources, and a held struct-union was not likely
  unique.

## What changed

A supply whose value is a `variant_up` or `variant_as` chain moves the unit
of the value at the bottom of the chain when that value dies at the step
and nothing lent at the step reads it. The replay maps the move the same
way. This also drops the retain and release that a `variant_up` of a
fresh record cost wherever it was passed to a counted parameter, returned
or stored.

After splicing, a `record_get` of a member-narrowing `variant_as` becomes
a `variant_get` of the union, with the member's name and the same field
index.

A held value seeds `likely_unique` for the payload takes asked past the
holds, and those takes may read out of a held source.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostMemberTakeGrowsInPlace`, 20,000 appends, `__arr_push_shared_count()`, each of call / spliced / method, x86-64 / arm64 / wasm | 19,986 | 0 |
| main's compiler, compiled by main's compiler vs by this one: binary size | 21,775,728 | 21,693,808 |
| that binary compiling main's compiler, three interleaved rounds, x86-64, s | 73.2 / 73.8 / 74.0 | 69.2 / 73.7 / 73.4 |

The two binaries' outputs for that compile are byte-identical.

## Traps

The held seed is the speculation `f904ee67e` measured at 2.5% of a compile
when it covered every owned struct-union value. A held value is far rarer;
the compile above did not slow.

`variant_up` and `variant_as` are the only identity ops the move reads
through. `str_as` and `array_lend` are views, which are never counted, and
`dyn_up` / `dyn_as` were left out until a shape needs them.
