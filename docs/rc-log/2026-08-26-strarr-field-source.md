# A `string[]` in a struct field is only accidentally reclaimed

`var src: string[] = mkv(i); if (..) { var p: P = P { f: src, n: i }; .. }` freed
450 of 650 boxes over 100 rounds. Native freed all 450 of its own.

## Not #7557 one type over

That is the first thing worth recording, because it is what two wrong guesses
cost. For `string` the discriminator was the MOVE site: the unconditional store
leaked, the moved one was already correct. Here the move axis is **irrelevant**.
Measured on a compiler built from main, 100 rounds, x86:

| shape | self-host | |
|---|---|---|
| unconditional, source not read after (a move) | 700 / 700 | clean |
| unconditional, source read after | 700 / 700 | clean |
| unconditional nested block `{ }` | 700 / 700 | clean |
| `if (i >= 0)` — always entered | 700 / 700 | clean |
| `while (k < 1)` — entered once | 700 / 700 | clean |
| `if (i % 2 == 0)` — entered half the time | 650 / 450 | **leaks 200** |

The decisive pair is `if (i >= 0)` against `if (i % 2 == 0)`. `round`'s emitted
call profile is IDENTICAL between them — the same counts of `__fern_rc_inc`,
`__struct_drop_P`, `__field_reclaim_P` and `__fern_arr_dec`, and no
`__fern_str_arr_free` in either. Same code, different path coverage. So it is
neither the move, nor the statement kind, nor block scope; each of those is ruled
out by a row above rather than by argument.

## What was actually wrong: the program was correct by accident

The retain already fired — the struct-literal ARRAY arm's `is_array_type_name(cfft)`
covers `string[]` — and `__struct_drop_P` walked the elements. What was missing is
the SOURCE's own DEEP release. `src` never earned `"SARR:"`, so its exit sweep was
a shallow `__fern_arr_dec`: buffer only, no element walk. There is no
`__fern_str_arr_free` for it anywhere in the function.

The arithmetic that made this look fine:

- the exit sweep's ARRAY loop runs BEFORE its struct loop;
- `src` is rc 1, the store incs it to 2;
- the array loop decs 2 → 1 and, being rc>1, frees nothing;
- the struct loop then finds rc 1 and performs the full walk.

Balanced — but only on paths where the holder is constructed. Skip it and `src`
sits at rc 1, the shallow dec frees the buffer, and the elements leak: 200 over 50
skipped rounds, exactly the two element strings at box + buffer each.

## The fix, and why granting both owners the walk is safe

`src` keeps the DEEP `"SARR:"` class rather than delegating its walk to a holder
that may never exist. The forgiveness enters `strarr_unsafe_for_alias` through a
new `sfld_ok` channel, on the same three conditions #7557 established: the holder
must have earned its own struct credit, its type must route field reclaim, and the
store must retain rather than move.

Soundness rests on a mechanism the `"SARR:"` block already states for the alias
bind: `__fern_str_arr_free` is rc-gated, so at rc>1 it decs and leaves the elements
to the other owner, and only the LAST owner at rc 1 walks them. The walk cannot run
twice. That is what lets the string[] family share the DEEP class where a struct
alias must take box-only `"NODEEP:"`.

`#7557`'s `struct_lit_str_field_retained` / `str_counted_field_sites` are now
`struct_lit_rc_field_retained` / `rc_counted_field_sites`, taking the field type as
a parameter. One helper for both families rather than a near-duplicate: the two
differ in WHAT the source keeps, not in when it may keep it.

## Measured, x86, 100 rounds, against native

| shape | before | after | native |
|---|---|---|---|
| conditional holder | 650 / 450 | **650 / 650** | 450 / 450 |
| conditional holder, source read after | 650 / 450 | **650 / 650** | 450 / 450 |
| elements read back after churn | 1850 / 1650 | **1850 / 1850** | 1250 / 1250 |
| unconditional holder (control) | 700 / 700 | 700 / 700 | 500 / 500 |
| always-entered `if` (control) | 700 / 700 | 700 / 700 | 500 / 500 |
| escaping holder (control) | 700 / 700 | 700 / 700 | 500 / 500 |

Answers unchanged and matching native on every row, on x86, arm64 and wasm alike.
`__rc_underflow_count()` is 0 throughout, and `FERN_SANITIZE=1` reports neither a
use-after-free nor a double free on any of them.

**The churn row carries the soundness, and the risk it guards is not a leak.** The
failure mode for this change is a DOUBLE element walk — the holder's field drop and
the source's sweep both walking. So that row reads the source's ELEMENTS back after
the holder has died, with two fresh string arrays allocated in between: a box freed
twice is reused before the read and the answer stops matching native's.

The self-host still compiles itself under `FERN_STRICT_IR=1`, 26,801,393 bytes, no
bails.

## Converged with native

`selfhost-construction-retain-matrix.txt` moves exactly one cell, `str_arr__local`,
from `leak` to `clean`. Its `local` row is COMPOUND, and the matrix header warns
against attributing such a row to one floor — correctly, in this case: both halves
of that cell measure clean in isolation and only the compound shape leaked, because
the leak lives in the conditional half alone.

`str_arr__param` and `str_arr__fieldread` do not move with this and each needs its
own measurement.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_strarr_field_source_test.go`.
