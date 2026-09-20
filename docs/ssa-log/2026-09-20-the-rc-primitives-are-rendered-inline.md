# 2026-09-20 — the rc primitives are rendered at their call sites

`record_update` was the worst non-map gap on the bench table at 2.22x the
native register backend. Symbolized and run under callgrind, a fifth of it was
one function:

| function | instructions | share |
|---|---|---|
| `Blk__put` (the program's own method) | 78,643,200 | 20.6% |
| `__fern_rc_is_unique` | 73,789,455 | **19.3%** |
| `main` | 67,263,490 | 17.6% |
| `__fern_arr_dec` | 40,960,042 | 10.7% |
| `__fern_rc_inc` | 18,022,400 | 4.7% |

Native's IR has dedicated `OpRcInc` / `OpRcDec` / `OpRcIsUnique` kinds and
renders all three at their call sites
(`internal/codegen/x86_64ssa/rcinline.go`, #9618). The self-host spells the
same three as plain direct calls, so every backend emitted a real call to a
runtime stub. `internal/ssa/ownership.go` already documented the two
spellings side by side; #9873 has the full measurement.

## What changed

Both register-path emitters render a one-argument call to
`__fern_rc_is_unique` or `__fern_rc_inc` inline, in the instructions the stub
uses, so the in-process assemblers need nothing new. `__fern_rc_dec` is NOT
one of them: in the self-host it maps to `__fn___fern_arr_dec`, which frees.
The stub bodies stay for the drop walkers that call them, exactly as native
keeps its own.

Both forms skip a tagged pointer and anything below the heap floor, which is
also every null. is_unique needs no test for a static sentinel's negative
count, since a negative count never compares equal to one; rc_inc needs one,
because it would otherwise increment it. The count is read into a register
that is never a home, and the destination is written after the last read of
the operand, so a destination that IS the operand's register is safe — the
usual case since the coalescing entry.

## Measured

x86-64, retired instructions under callgrind, before and after, native's
register backend for scale. Every exit status is unchanged.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `array_with` | 78,968,257 | 65,828,289 | **−16.6%** | 0.89x |
| `sort_inplace` | 88,989,651 | 75,826,363 | −14.8% | 1.04x |
| `record_update` | 382,072,781 | 332,888,005 | −12.9% | 1.93x |
| `ordmap_insert` | 355,361,981 | 320,027,237 | −9.9% | 0.74x |
| `pvec_with` | 807,827,988 | 739,051,050 | −8.5% | 4.00x |
| `enum_match` | 159,075,079 | 151,875,079 | −4.5% | 1.61x |
| `struct_drop` | 519,740,195 | 503,180,195 | −3.2% | 0.83x |
| `string_build` | 229,880,016 | 226,622,016 | −1.4% | 1.85x |
| `tokenize` | 128,700,088 | 128,700,088 | +0.0% | 1.44x |
| `closure_call` | 135,534,082 | 135,534,082 | +0.0% | 0.72x |

Nothing regressed. The two flat rows emit no rc primitive in a hot loop.

## The half that did not pay

Native's own comment on `rcinline.go` names the cost as the caller-saves the
allocator plants around a call whose callee it knows nothing about, rather
than the call and return. So the obvious companion change was to stop
counting these positions as calls in `regalloc_linear`, letting a value stay
in a caller-saved register across one.

Measured, it does not pay here: `record_update` came out 0.5% WORSE, and
every other bench in the sweep moved by less than 0.05% in either direction.
The self-host's caller pool is six registers; removing the call boundaries
hands more values to those six at once, and the allocator's choices get worse
about as often as the saved spills help. The change is not in the diff, and
`ssa.rc_inlined` says so where a reader would otherwise try it again.

That is the difference between the two compilers on this point: native's
allocator has the registers for the saving to show up, and this one does not,
yet.
