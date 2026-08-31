# The heap tracer's site named where code ended up, not who wrote it

*2026-08-31* — native x86-64, diagnostic mode only.

`FERN_RC_TRACE` reported one frame: `site`, the return address of
whoever called the allocator. That is enough when the producer is small
and wrong whenever it is not, because **`ir.Inline` runs twice in every
backend battery** and an inlined callee's allocation is attributed to
the function it was spliced into.

Two measurements over the self-host driver, both of which had been read
as facts about producers:

| | |
| --- | --- |
| `lexer__tokenize_impl` | 133 allocations attributed |
| `Lex { … }` constructions in its 1043 lines | **one**, running once per call |
| `__fern_alloc_rc1` | 1689 blocks, the largest bucket, naming the shared allocator |

So the biggest row in the attribution table was the allocator itself,
and the second biggest was an aggregation point.

## The fix is two instructions

At both hook positions the helper's own `rbp` is either already popped
(the alloc hook sits after the epilogue) or not yet pushed (the free
hook sits at the leaf entry). In both cases `rbp` is the **caller's**
frame base, so `[rbp+8]` is that caller's return address — one frame
further out than `site`, for free.

```
mov r8, [rbp + 8]     ; at the hook, after the argument registers are set
mov r15, r8           ; in __fern_rct_ev, parked beside ptr/size/site
```

Read last at the hook site, because `ptrReg` or `sizeReg` may itself be
`r8`. `r15` needs saving, and one more push flips the stack alignment
every `call` in the writer depends on, so `rbp` is saved beside it —
an even count, and the honest partner since the hook reads through it.

Format goes from four fields to five:

```
rctrace <a|f> <ptr> <size> <site> <caller>
```

Both regexes in `internal/e2e/rctrace_test.go` are anchored, so they
failed loudly rather than silently mis-parsing.

## What it bought, immediately

Re-running the attribution that motivated it:

```
--- site -> caller, top pairs (6169 leaked blocks) ---
  1172  __fern_alloc_rc1                 <- __str_slice
   256  __fn_checker__t_i32              <- __fn_checker__check_expr
   172  __fern_alloc_rc1                 <- __fn_parser__rl_expr_kids
   163  __fern_alloc_rc1                 <- __fern_strcat
   155  __fn_parser__dl_expr_kids        <- __fn_parser__map_expr_kids
   132  __fn_lexer__tokenize_impl        <- __fn_lexer__tokenize
```

**`__str_slice` alone is 1172 blocks — 19% of everything the driver
leaks, and it was invisible.** It sat inside the `__fern_alloc_rc1`
bucket, which the old instrument could only report as "the allocator".
The next pair is 4.5x smaller.

It also ends a hunt that had failed seven times. `checker__t_i32`'s
consumer is `check_expr`; I had written seven probes guessing at that
shape from reading the source and every one came back clean.

## The limit, stated because it will bite

Two frames is not a backtrace. Where the producer is *itself* a runtime
helper — `__str_slice`, `__fern_strcat` — the user code that called it
is one frame further out still. The rule that falls out:

> `site` is trustworthy for a small producer and close to meaningless
> for a large one. Read `caller` whenever `site` names a runtime helper
> or a function big enough to have been inlined into. When `caller`
> names a helper too, neither frame reaches user code.

And it is read through the frame pointer, so a caller that kept none
yields an address resolving to no symbol. A consumer must treat an
unresolvable `caller` as absent rather than trusting it — the same
fail-soft stance the rest of this area takes.

## The self-host tracer still emits four fields

Deliberately, not by oversight. `asm_ir.fern`'s `__fern_hev_a` takes
`site` as an **explicit stack argument** and carries a
`__fern_hev_site` shim that lets a box shim override it, so the frame
relationship native relies on does not hold there and the port is not a
transcription. Its own comment states the hazard: passing the wrong
offset "does not fail loudly; it silently names the wrong code".

Until it lands, pair a native trace against a self-host one on `site`
only. Filed rather than guessed at.

## Nothing outside the diagnostic mode moves

`ast.RcTrace` gates every line of this, and a trace-off build is
byte-identical — verified with `cmp`, not argued.
