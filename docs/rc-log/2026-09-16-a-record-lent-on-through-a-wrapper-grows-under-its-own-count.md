# A record lent on through a wrapper grows under its own count

#9415, the fixpoint gate. The compiler built through the semantic lowering
compiles the whole tree byte-identically (#9416) and then, rebuilding itself
through the same path, exhausts the 16 GiB arena 19 minutes in (exit 125),
where the native-built compiler does the identical job in 8.4 GB.

## What the counter said

`FERN_CLIFF_REPORT=1` on the semantic build of `checker.fern`: the produced
compiler copied **2.46 GB across 1,057,899 shared appends** where the
native-built one copied 816 MB across 897,676. A hardware watchpoint on the
counter word (`__fern_arr_push_shared`, the `incq` inside `__fern_arr_push`)
with a Python stop handler recording six frames per hit put over half a
million of them in one chain:

```
__fern_arr_push < ssarc.emit < ssarc.helper < ssarc.drop_value < ssarc.drop_record_fields < ssarc.one_helper
```

`emit` is the field append #9365 admitted — `LowerResult { ...r, ops:
r.ops.append(op) }` on a borrowed record — and it grows in place when its
caller passes a record it owns. The drop-helper generation reaches it through
wrappers: `helper(r, …)` lends its own parameter `r` on to `emit` three
times. Reduced to eight lines (`fa3` in the session): a loop calling `emit`
directly copies nothing; the same appends through `helper` copy once per
call, 168 ms against 5 ms for 20,000 pushes; through a recursive
`drop_value` shape, 14.2 s against 24 ms.

## Why the wrapper copied

`ssaunits.hands` names the buffers a call is handed without a bracket: an
array parameter this frame reads no further, or a field projection whose
record it reads no further. A whole RECORD parameter was neither, so
`helper`'s call to `emit` bracketed the field `grow_rows` says `emit` may
grow — `__fern_arr_share_inc` on `r.ops` across the call — and `emit`'s push
read a count of 2 on a buffer only the record holds. Every first append in
every wrapper copied the whole buffer, and the helpers grow to the size of the
program.

The bracket was sound for the wrong reason: `helper` itself had no grow row,
so a caller of `helper` that still read its record after the call would not
have bracketed either, and in-place growth would have nulled a field that
caller still read. The row was missing because `hands` never handed the
record on.

## What landed

`hands` hands a whole borrowed record parameter on at its last use (not an
environment record, named once among the operands, read no further), with
`hand_fields` at -1 as for an array parameter. `grow_rows` then propagates
every field the callee may grow at that position to the caller's parameter,
so `helper` carries `(helper, 0, ops)` and its own callers bracket or hand on
exactly as they do for `emit`. At the call, `ssarc.bracketed` GATES such a
field rather than bracketing it: the buffer is captured in a scratch slot,
`__fern_rc_is_unique` on the record decides, and a shared record holds the
second count across the call as `append_field` does around its own push
(`gated_records` / `gated_fields`, two scratch slots each).

Measured, produced compiler on the semantic build of `checker.fern`: shared
appends 1,057,899 → 579,811, bytes copied 2.46 GB → 1.49 GB; the wrapper
reproducer 168 ms → 8 ms, the recursive one 14.2 s → 2.0 s. The production
suite's `record-handed-through-wrapper` reads `__arr_push_shared_count()`
itself and answers 200 and up on a copy, 4 without one.

## The accumulator's own edge

The recursive reproducer's remaining 2.0 s, and the `drop_elements` /
`drop_variant_fields` rows that did not move, are one shape:

```fern
function dv(r: R, k: i32, arr: boolean): R {
    if (arr) { r = emit(r, k); }
    return two(r, k);
}
```

After the branch `r` is a phi of the borrowed parameter and an owned local.
A phi is a fresh unit, so the parameter's edge RETAINS the record, the
record's count is 2 at the call to `two`, and the gate inside `two` copies —
on that edge only: 148 copies in 300 iterations, half of them taking the
branch. The count is honest (the caller's unit and the phi's both name the
box), and only the caller moving its unit in changes it, which is what
`own` says: the accumulator shape the language spells that way, and what
borrow inference would infer. The semantic path does not infer modes, by
decision, while any caller may be AST-lowered.

So the ssarc emit chain's accumulator is declared `own` through its 53
functions, the way the compiler's other accumulators are written. The move
checker shaped the rest: a nested `emit(emit(r, a), b)` becomes two steps,
a probe chain that may hand the record back continues from `done.r`, and
the Emit record's chain rebuilds `e` at each step rather than moving `e.r`
out. `depth` only reads the labels and stays borrowed.

Measured, produced compiler on the semantic build of `checker.fern`, with
the bump high-water mark the cliff report now prints beside the counters:

| compiler | shared appends | bytes copied | `__heap_bump_bytes` |
|---|---|---|---|
| native-built, before | 897,676 | 816 MB | — |
| produced, before | 1,057,899 | 2.46 GB | — |
| produced, record handed on | 579,811 | 1.49 GB | — |
| produced, accumulator `own` | 376,181 | 950 MB | 976 MB |
| native-built, accumulator `own` | 809,126 | 251 MB | 1,007 MB |

The produced compiler is now the leaner of the two on `lexer.fern`,
`parser.fern`, `checker.fern`, `checker_run`, `irlower_run`, `asm_ir_run`
and `ssa_emit_run` by arena high-water mark. It is not on the whole tree:
rebuilding itself still exhausts the arena, 11m22s in rather than 19m, and
on `asm_modload_run` (4,908 declarations) its shared appends copy 5.8 GB
against the native-built compiler's 2.45 GB at a lower high-water mark
(3.76 GB against 6.69 GB). So the copies that remain scale with the size
of the MODULE, not the function — a whole-module structure appended to
under a count of 2 — and the next sample is on that input.

## Traps

**The watchpoint's ignore count is not honoured from a Python `stop`.** The
handler returned `False` and set `ignore_count`; every hit was still
delivered. The run took five minutes instead of thirty seconds and the
counts came out exact rather than sampled, which was the better outcome.

**`-g` on the self-host driver writes a symbol table, not debug info.** gdb
resolves frames through it but `watch __fern_arr_push_shared` says no symbol
table is loaded; the counter's address is the `incq` operand in
`disassemble __fern_arr_push`, and `watch *(long*)ADDR` works. The
native-built binary has the same word behind `__fern_arr_push_shared_count`'s
first `mov`.

**Interrupt sampling under gdb did not deliver.** Neither a Python thread
(gdb holds the GIL across `execute`) nor a `kill -USR1` loop from a
subprocess stopped the inferior; `handle SIGUSR1 stop` in batch mode never
fired. callgrind on the 30 s run (20 minutes) was the profile that worked.
