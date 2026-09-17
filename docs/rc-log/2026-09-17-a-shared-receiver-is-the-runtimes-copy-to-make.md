# 2026-09-17 — a shared receiver is the runtime's copy to make

`append_push` un-shared the receiver itself. On the arm where
`__fern_rc_is_unique` said another holder was still reading the box, it emitted
`__fern_arr_dec` and a whole-buffer `__fern_arr_slice`, then pushed onto the
copy with the CONSUMING `__fern_arr_push_owned`. The un-share the
non-consuming `__fern_arr_push` already performs in hand-asm was therefore
dead code for every append this path lowered, and `__fern_arr_slice` is the
one array helper written in Fern: the flat stack machine compiles its
per-element body to **31 instructions per 8-byte slot**, against a memcpy's
well under one.

It also read as healthy. `__fern_arr_push`'s `.Larr_push_shared_cliff` is
where `__fern_arr_push_shared` / `__fern_arr_push_copied` are incremented, so
a copy that never reached the push never reached the counter —
`__arr_push_shared_count()` returned **0** on a program copying its whole
accumulator once per call. `util.arr_push_cliff_report`'s own doc calls that
counter "the instrument any accumulator work should be measured with".

**The change.** The unique arm is unchanged: one unit, consuming push, reclaim
on realloc. The shared arm now hands the box to `arr_push_borrowed_op`, which
frees nothing on any path, so this frame's unit is released AFTER the push and
the counted elements the fresh box aliases are retained over the receiver —
the accounting `append_borrowed` already spells out for the no-unit case.
`sole_owned_base` stays for `with_update`, where `arr_set` has no rc gate of
its own and the copy has to be made before the store.

**Measured**, retired instructions under callgrind, x86-64-linux, `-O`:

| shape | n | native | before | after |
|---|---|---|---|---|
| param-aliased accumulator, appended in a loop | 500 | 397,070 | 17,083,993 | 1,543,099 |
| | 1000 | 1,045,165 | 67,666,046 | 5,584,162 |
| | 2000 | 3,091,229 | 269,330,046 | 21,166,172 |

12.7x at n=2000. `xs = xs.append(v)` in the caller and `return acc.append(v)`
are unchanged to within 24 instructions — the unique arm is the same code.
`__arr_push_shared_count()` on the same shape goes 0 → 490 / 989 / 1988
against native's 498 / 998 / 1998.

Emitted size fell on 47 of the 84 bench-corpus rows and rose on none:
`array_append` -13.8% x86-64, -11.7% arm64, -3.9% wasm; `array_index`
-14.1% / -11.9% / -3.8%. In the compiler's own modules the slice call sites
collapse — `checker.fern` 995 → 23, `parser.fern` 485 → 3 — for 1.6% less
emitted text in each.

**Witnessed**, not contract-only: the new `shared-param-accumulator-appended-in-a-loop`
case in `self_host_arr_push_cliff_ir_test.go` pins the count at 1 on the
native oracle, the self-host x86-64 IR path and the wasm IR path, and it reads
**0 before this change** — the counter's blindness is what the case fails on.
Full `internal/e2eselfhost` green (7,467 s), all four fixpoints including
stage-2 arm64, and `TestSelfHostCoreutilsParity` green.

## The trap: this fixes no coreutils utility

All 104 `coreutils/*.fern` emit **byte-identical assembly** either side of it.
`coreutils/echo.fern`'s `append_raw` — a local bound from a plain parameter and
appended to in a loop, the shape that motivated the work — is unchanged at
every size (1.00x at 100/200/400/800/1600 operands, still quadratic), because
its appends never took this path: its emitted body has no
`__fern_rc_is_unique` and no `__fern_arr_slice`, only `arr_push_owned`.

That was a misread on the way in. An earlier callgrind profile put 56% of
echo's run inside `__fern_arr_push`, and that was taken as evidence for the
open-coded slice; it is the opposite — echo's copies were already inside the
push, already counted, and the per-byte cost there is native's constant
factor, not this. The shapes that DO reach `append_push` with a shared
receiver are in the compiler's own tree, which is where the size numbers above
come from.

**Next leads.** The per-call copy itself remains, on both compilers: a callee
that retains a plain parameter must copy once per call, so the shape is still
quadratic overall, just with a 12.7x smaller constant. Removing it is the
inference half of #9526 — that `out = f(out, …)` gives the buffer up at the
call. And `tsort` at 745x (linear under native, quadratic under the self-host)
is a third mechanism this does not touch: it survives this change unchanged at
47.7 s on a 100k-edge DAG.
