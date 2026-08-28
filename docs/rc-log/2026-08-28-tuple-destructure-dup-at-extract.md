# A tuple destructure retains the element it extracts

Closes #7682: `var (a, b) = p` over a tuple with a scalar-array element
**over-released** — rc underflow and a sanitizer-confirmed use-after-free,
where both oracles are correct. It is the first defect in this log whose
census is *perfectly balanced in the failing case*, so it is worth reading for
the measurement discipline as much as the fix.

## The defect

`lower_stmt_var_destructure` bound each element with `op_tuple_get` →
`op_store_local` → `mark_arr` / `mark_str` / … and **no retain anywhere in the
function**. `tuple_get` yields the element POINTER; the marks then make the
slot one `emit_dec_sweep_except_list` releases (it iterates slots and decs
every `is_arr_slot`, no name credit required). So the binding gave back a
reference it never took. Where the source tuple also carried a reclaim credit,
the same buffer was decremented twice: the first dec frees it, the second
underflows into the quarantined block.

`FERN_RC_TRACE=1` on a one-round run shows both blocks allocated once and
freed once BY POINTER — the defect is a refcount driven below zero, not a
block freed twice, which is exactly why `allocs == frees` at `live_bytes 0`
reports it as clean.

## The polarity flip that confirmed the mechanism

| variant | before |
|---|---|
| `var p: (i32, i32[]) = (i, [i, i+1]); var (a, b) = p;` | underflow + UAF |
| bare-ident element (`(i, xs)`) — no `TUPRC:` literal credit | underflow + UAF |
| **no annotation** (`var p = (i, [..])`) — no `TUPRCS:` sweep credit | a plain LEAK, answer correct |
| read `base.0` / `base.1` instead of destructuring | clean |

Removing the source's credit converts the double-dec into a single unmatched
dec. That is what identified the second releaser as the tuple's own reclaim
rather than anything about the param or the producer — neither is involved,
and the shape reproduces on a single round.

## The fix, and why it is scoped where it is

Dup-at-extract: retain the element as it is bound, so the pair balances —
rc 1 →inc 2 →this binding's sweep 1 →the tuple's drop 0. This is native's own
idiom (the leak matrix's note for the analogous element-return shape reads
"native is clean via dup-at-extract") and it is the cause-level fix: the
invariant broken was *a slot the sweep will release must hold a counted
reference*.

Guarded by **`is_leaksafe_array_field`** — an existing predicate, not a new
one, and it is exactly right twice over: it is the set the sweep releases with
a shallow `__fern_rc_dec`, and it is the set measured to over-release
(`i32[]`, `boolean[]`, `u32[]`, `u8[]`, `f64[]`/`f32[]`, `i64[]`).

The deeper-release kinds are deliberately excluded, and that is a measurement
rather than a guess:

| destructured element | before | after |
|---|---|---|
| `string[]` (`__fern_str_arr_free`) | leak 600/100 | unchanged |
| struct / enum array (element walk) | leak 400/100 | unchanged |
| `Option[i32]` | leak 200/0 | unchanged |
| `string` | clean | unchanged |

Retaining one of those would deepen a leak instead of closing a double-dec.
Each is its own measured increment.

## Probes

`self_host_tuple_destructure_retain_test.go`. Every row gates on the **exit
code** — the `__rc_underflow()` guard — because the census is balanced in the
failing case; each re-runs under `FERN_SANITIZE=1`, where the parent reports
`use-after-free (touched a quarantined block)` and exits 124.

| case | exit | was |
|---|---|---|
| `i32[]` bind + read (the repro) | 9 | 99 |
| `f64[]` bind + read | 9 | 99 |
| `i64[]` bind + read | 9 | 99 |
| bare-ident element source | 9 | 99 |
| moved out by `return b` | 6 | 99 |
| passed to a borrowing callee | 8 | 99 |
| `string[]` element | 9, frees pinned 100 | unchanged leak |

The two disposal rows matter beyond their verdict: `return b` elides the slot
from the sweep, so an unmatched retain would LEAK there rather than balance —
it balancing is what shows the reference is handed to the caller rather than
merely cancelling a dec.

## Why the whole RC layer missed it

`irlower.fern` has no destructure awareness in its escape analysis:
`is_destr_marker`, `name_has_comma` and `split_pattern_names` appear nowhere
in it, though the checker has all three. The parser encodes `var (a, b) = p`
as a `StmtVar` whose name is the comma-joined `"a,b"` with a bare
`ExprIdent(p)` init, so `rctuple_esc_stmt_alias` reads it as an ordinary
single binding and applies the #7282 alias forgiveness — *"a plain
`var v = name` bind retained the box"* — which a destructure precisely does
not do. The tuple therefore kept a credit that `keep = t.1` would have sunk.

Fixing the extraction side makes that moot for the measured kinds, but the
blind spot is still there: **an escape scan that cannot see a destructure will
mis-read the next shape too.** Teaching `rctuple_payload_escapes*` the
comma-named form is the follow-up, and it is what the deeper-release kinds
above will need before their retains can be widened.
