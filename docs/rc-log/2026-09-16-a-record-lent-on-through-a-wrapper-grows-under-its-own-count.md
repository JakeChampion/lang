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

## The field moved into a counted slot

The byte sample on `asm_modload_run` (the watchpoint reading the copied
length at each hit) put 5.3 GB of the 5.8 GB on one shape:

```fern
function step(own e: Emit, op: ir.Op): Emit {
    return Emit { ...e, r: emit(e.r, op) };
}
```

`emit` takes its accumulator by `own`. The record `e` is owned here, and
`e.r` is a projection of it, so the supply for the call was RETAIN: the
callee got a unit of the field and the record kept its own, `emit`'s
append found the buffer under two counts, and copied it. Every op the
ssarc chain emits through an `Emit` record paid the whole `ops` buffer.

`ssaunits` has a third supply mode beside retain and move now:
`steal_unit`. A retained projection whose root the frame owns, and whose
value the instruction names once, is stolen: `ssarc.steal` tests the
record's count with `__fern_rc_is_unique`, clears the field in place when
the record is the only holder — the callee then owns the unit the record
held — and retains as before when it is not, because a second holder of
the record still reads the field. The projection stays a plain read; what
changes is which unit the callee is handed.

Measured, produced compiler, semantic build:

| input | compiler | shared appends | bytes copied | `__heap_bump_bytes` |
|---|---|---|---|---|
| `checker.fern` | native-built | 809,126 | 251 MB | 1,007 MB |
| `checker.fern` | produced, accumulator `own` | 376,181 | 950 MB | 976 MB |
| `checker.fern` | produced, field stolen | 263,057 | 76 MB | 976 MB |
| `asm_modload_run` | native-built | 3,974,650 | 2.45 GB | 6.69 GB |
| `asm_modload_run` | produced, accumulator `own` | 1,705,652 | 5.80 GB | 3.76 GB |
| `asm_modload_run` | produced, field stolen | 1,162,833 | 577 MB | 3.76 GB |

The output is byte-identical to the AST build on both inputs. The RC
fixture's `thread_run` threads a record through such a call seven times
and reads the shared-append counter: 210 with the steal, 214 without;
`thread_shared` keeps a second holder of the record and reads both
holders' fields after the call.

The fixpoint is not reached by it: the produced compiler rebuilding itself
still exhausts the arena, 10m25s in at 13.5 GB RSS (from 11m22s). The
high-water mark did not move on `asm_modload_run` either — 3.76 GB with
the copies and without them — because a copied buffer goes back to the
freelist when its shared count drops, and the exact-size freelist hands it
out again. What is left in the produced compiler's arena on the whole tree
is not the copies.

## What it was

A `FERN_LEAKCHECK` build of the produced compiler, with the flag on over
`checker.fern`, frees everything it allocates (8 bytes live at exit, from
77 million allocations), and per input the produced compiler is leaner
than the native-built one at every size measured (checker_run 1,198
declarations: 1.02 GB against 1.07; irlower_run 4,014: 3.25 against 4.42;
asm_load_run 5,205: 4.00 against 6.95). Only the whole tree (8,322)
exhausted the arena, and the phase readout `FERN_CLIFF_REPORT` prints at
each step of the substitution said where: the AST lowering, which
`ircore.lower_gated` ran over every declaration for the eligibility
verdict the substitution then replaced. Inside the produced compiler that
lowering's working set for 8,322 declarations, beside the semantic
lowering's own, was the arena. With the gate reading the produced body in
the declaration's place, the produced compiler compiles the whole tree
through the semantic path in 4.4 GB (arena 4.69 GB, against the
native-built compiler's 7.21 GB) and its assembly is byte-identical to the
native-built compiler's: the fixpoint holds.

## The assembler's code buffer

Binary output still exhausted the arena where the assembly held: the
produced compiler's `-o fern.fern` reached 13.4 GB before the host killed
it, and `-o checker.fern` passed 10 GB in six minutes. The phase readout
at `x86:assembled` on `lexer.fern` said which step: 34,967 shared appends
copying 12.3 GB, the arena growing from 17 MB to 6.85 GB across the
assembler alone, against the native-built compiler's 24,004 appends
copying 4.4 MB. The watchpoint sampler put the copies under `x86_le32`,
`x86_rex_r` and `x86_rex_rr`, the byte emitters at the bottom of
`x86_native`, reached through `x86_emit_mem`, `x86_gas_mem_op` and
`x86_gas_emit`.

Every one of those emitters took the code buffer as a borrowed `buf:
i32[]` and returned it grown, and every caller held the buffer inside an
owned `X86Asm` record, writing `a = X86Asm { ...a, code: x86_le32(a.code,
0) }`. The field read lends the buffer while the record still counts it,
so the append inside the callee finds a shared buffer and copies it, once
per byte emitted. The 88 buffer-threading emitters now take `own buf`:
the field of an owned record is a legal own argument, the steal supply
moves it out of the record for the call, and the append grows the buffer
in place. The native-built compiler and the produced compiler still emit
byte-identical binaries for `lexer.fern` and `checker.fern` on both
lowerings.
With the buffers owned, the produced compiler's `-o lexer.fern` shows 8,743
appends copying 2.3 MB, the assembler adding 155 MB to the arena, 1.5 s and
138 MB resident.

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
