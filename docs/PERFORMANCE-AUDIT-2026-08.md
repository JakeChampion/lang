# Performance audit, 2026-08

Measured on a 4-core x86-64 container, commit `64213fe`, against `bin/fern`
(Go) and `bin/fern-selfhost` built by it. Every figure here is reproducible
from the commands given; nothing is an estimate. Tracking issue: #6888.

The question that started it: the per-module whole-compiler fixpoint takes
~16 minutes, and that is supposed to say something about whether Fern meets
its performance goals. It does, but not the thing it looks like — there are
**four independent multipliers**, and the largest is not the one everyone
assumes.

## 1. The gap, end to end

The same source, the same target, two compilers:

| input | `bin/fern` (Go) | `bin/fern-selfhost` (Fern) | ratio |
|---|---|---|---|
| `examples/self_host/fern.fern` → x86-64 asm | **58 s** | **6 m 05 s** | 6.3× |
| `examples/self_host/checker_run.fern` → x86-64 asm | 3.9 s | 23.3 s | 6.0× |

```
time ./bin/fern -target x86-64-linux examples/self_host/fern.fern > /tmp/n.s
time ./bin/fern-selfhost -target x86-64-linux -emit asm \
     $PWD/examples/self_host/fern.fern $PWD/internal/stdlib -o /tmp/s.s
```

Both outputs contain the same program: 4,693 functions from the Go emitter,
4,684 from the self-host one. The fixpoint pays this unit twice (gen0 in
parallel, gen1 serially), which is the ~16 minutes.

6.3× is the number to hold onto. It is not "Fern is 6× slower than Go" — it is
four things multiplying, and they are separable.

## 2. Multiplier 1 — codegen is below `gcc -O0`

Retired instructions, counted with `valgrind --tool=callgrind` (identical to
the digit across runs):

| workload | Fern | `gcc -O0` | `gcc -O2` |
|---|---|---|---|
| `fib(32)` | 239.7 M | 120.0 M (2.0×) | 66.4 M (3.6×) |
| 300 M-iteration `i64` accumulate loop | 10.20 G | 1.80 G (5.7×) | 1.50 G (6.8×) |

Wall time on the loop: Fern 1.150 s, `gcc -O0` 0.711 s, `gcc -O2` 0.096 s.

`a + b * c` in a three-parameter function is 17 instructions with four memory
round-trips:

```
mov [rbp-8], rdi        ; params spilled on entry
mov [rbp-16], rsi
mov [rbp-24], rdx
mov rax, [rbp-8]
sub rsp, 16             ; push a
mov [rsp], rax
mov rax, [rbp-16]
sub rsp, 16             ; push b
mov [rsp], rax
mov rax, [rbp-24]
mov rcx, rax
mov rax, [rsp]          ; pop b
add rsp, 16
imul rax, rcx
mov rcx, rax
mov rax, [rsp]          ; pop a
add rsp, 16
add rax, rcx
```

An allocator emits `imul esi, edx; lea eax, [rdi+rsi]`.

Measured over the whole-compiler emit (22,420,063 emitted instructions):

- **36.5% of emitted instructions are operand-stack traffic** — `sub rsp` /
  `mov [rsp], rax` / `mov REG, [rsp]` / `add rsp`. This is what a register
  allocator deletes. #4112.
- **12.1% were a push whose value is immediately discarded** — the three-line
  `sub rsp, N` / `mov [rsp], rax` / `add rsp, N` a statement-position
  expression emits for a value nobody reads, 73,977 sites in the
  `checker_run` emit alone. **Fixed**: the streaming peephole's P3 rule
  (`x86_64.go:peepholeTail` and its arm64 twin) now drops it, worth −13.0% of
  emitted instructions on that driver and −2.8% to −17.7% of retired
  instructions across `examples/bench`.

Neither native backend runs `ir.Inline` or IR dead-function elimination —
those are wasm-only, blocked on the AST↔IR parallel-index walk. #4377.

## 3. Multiplier 2 — the exit drop sweep — FIXED (#6894)

This was the large one, it was not on anyone's list, and the framing in the
original audit was wrong in a way worth recording.

The audit measured **589,045 `call __fern_rc_dec` in `irlower__lower_call_named`**
and read that as duplication across exits, which implies sharing one epilogue
between them. A per-sweep census said otherwise:

```
funcs=4286  sweeps=16362  ops=7,056,385
  irlower__lower_call_named    sweeps=259  ops=3,574,571  avg=13,801
  irlower__lower_call_method   sweeps=177  ops=1,291,650  avg= 7,297
  irlower__lower_expr_dispatch sweeps=133  ops=  752,430  avg= 5,657
```

259 sweeps is simply that function's return count. The pathology was
**13,801 IR ops per sweep** — ~91 per rc-tracked local, because `emitDec`
inlined a full deep-free for every local at every exit. Three functions held
79% of all sweep code.

So the fix was to outline the per-local drop, not to share the sweep. Both
struct arms now call a generated drop fn. The arm that dominates is the
NOT-free-eligible one (the self-host `LowerState` locals are not eligible),
whose semantics differ — flat one-level field decs, box dec'd and never
freed — so it needed its own generator rather than a flag on the existing one.

| | before | after |
|---|---|---|
| whole-compiler emitted asm | 18,601,039 lines | 5,343,328 (−71.3%) |
| `irlower__lower_call_named` | 6,415,067 | 298,499 (−95.3%) |
| `irlower__lower_call_method` | 2,289,116 | 297,783 (−87.0%) |
| compiling the compiler | 39.4 s | 12.4 s (3.2×) |
| self-host binary (`fern.fern`) | 155,973,500 B | 89,738,492 B (−42.5%) |

**The trade:** outlining costs a call per drop — measured at +0.78% retired
instructions for −80% static code on `examples/bench/struct_drop.fern`. Code
size dominates this workload, so it is the right trade, but it is a real cost
on drop-heavy inner loops.

**Two things remain.** Sharing one sweep per function (259 → 1) still composes
with this for a further ~259× on that function, but needs care: `OpBr` lowers
to a bare `jmp` with no stack-pointer reconciliation on the natives, where the
operand stack IS the machine stack, and `verifystack` is wasm-polymorphic and
would not catch the misalignment. A shared epilogue must be reached with an
empty operand stack.

And the self-host mirror — `examples/self_host/irlower.fern`'s
`emit_dec_sweep_except` — still inlines. Nothing compares the two emitters'
bytes or shape, so it was a safe follow-up, but the self-host stays ~4× slower
than the Go compiler until it lands, and the fixpoint is structurally blind to
that divergence.

## 4. Multiplier 3 — the self-host compiler's symbol tables are linear

> **These shares are the ORIGINAL profile and three of the four are now wrong.**
> They were sampled before #6894 (−71.3% whole-compiler emit) and #6899 (~16%
> off the checker) landed, which moved the denominator and the mix underneath
> them. §4b re-measures against current `main`; read it before choosing work.
> The description of the *shapes* below is still accurate — only the shares moved.

200-sample profile of `bin/fern-selfhost` compiling `checker_run.fern`
(built with `-g`, sampled with `gdb -batch -ex "bt 40"`):

| leaf frame | share |
|---|---|
| `__fern_strcmp` | 20.5% |
| `Scope.lookup_sig` / `array_method_ret_type` / `array_recv_method` / `lookup_method` / `lookup` / `lookup_struct` | 17.0% |
| `irlower.param_is_borrowable` | 10.5% |
| allocator / rc / memcpy helpers | 7.5% |

The top three are one problem. `checker.Scope` holds flat arrays and scans
them:

```fern
function (s: Scope) lookup_sig(name: string): FuncSig {
    var i: i32 = 0;
    while (i < s.sigs.len()) {
        if (s.sigs[i].name == name) { return s.sigs[i]; }
        i = i + 1;
    }
    ...
}
```

`s.sigs` is module-wide — ~4,600 entries when compiling the whole compiler —
and this runs per identifier. **The Go checker does the same job with a
chained hash map** (`checker.go:7833`, `cur.names[name]`). So this is not
"the same compiler in a slower language"; the two implementations have
different asymptotics for the same operation.

Three specific shapes:

- **Miss paths allocated** — **fixed in #6899**. `lookup_sig` built a `FuncSig`
  plus two empty arrays plus a concatenated `t_unknown("undefined function:" +
  name)` on every miss, and its callers used it as a predicate; `lookup_method`
  and `array_recv_method` did the same. The four predicate call sites now use
  `has_sig` / `has_method` / `has_array_recv_method`, and `lookup_method` tests
  the name before computing the receiver match (which parsed a type per
  candidate). ~16% off the checker driver, with **byte-identical emitted asm**
  either side of the change. The linear scan itself is untouched — that is the
  hashing half, still open.
- **Association lists encoded in strings.** The borrow registry is a
  `string[]` of `"callee|1011"` entries, linear-scanned by
  `param_is_borrowable` per call-arg, per body walk, per iteration of a
  whole-program greatest fixpoint. 10.5% of runtime, on its own.
- **Symbols are compared, not identified.** No interning: every symbol
  comparison is a byte compare and every string-keyed map lookup re-hashes
  the whole key. The interning design already exists — #4394 lever 1,
  `docs/SELFHOST-SYMBOL-INTERNING.md` — and is unblocked.

## 4b. Re-profiled against current `main` — the ranking inverted

Same method and workload as §4, run after #6894, #6899, #6907 and #6909. Two
runs, because one would have misled: leaf-frame shares are **not** as
reproducible as the corpus's instruction counts, and §4's single run was
presented as though they were.

| leaf frame | run 1 (200 samples) | run 2 (150) | §4 said |
|---|---|---|---|
| `__fern_strcmp` | 18.0% | 18.0% | 20.5% |
| `__fern_memcpy` | 27.0% | 15.3% | *unlisted* |
| `__fern_rc_inc` | 6.0% | 3.3% | — |
| `__str_slice` | 5.0% | 3.3% | — |
| `Scope.*` cluster | 2.5% | 6.0% | **17.0%** |
| `irlower.param_is_borrowable` | absent | absent | **10.5%** |

**What is safe to act on** — the claims that hold in both runs:

- **`param_is_borrowable` is gone.** #6909 bucketed its registry and measured
  −0.18%, i.e. nothing; the profile agrees by not containing it. The fix was
  right, the target had already evaporated.
- **The `Scope` cluster is 2.5–6%, not 17%.** #6899 took most of it. The
  ~4,600-entry linear scan described above is real, and it is no longer where
  the time goes.
- **`__fern_strcmp` is 18.0% in both runs, to the decimal.** Symbol interning
  (#4394) is the only item from §7 still near its attributed size, and now the
  best-evidenced one.
- **Copying dominates and was never on the list.** Summing memcpy + rc + slice
  + alloc gives **24%–51%** depending on the run, against the 7.5% §4 records.
  The magnitude is genuinely unpinned — 200 gdb samples cannot settle it — but
  every run puts it first.

Direct callers of `__fern_memcpy` (run 2): `__fern_arr_cow_inplace` 6,
`__str_slice` 5, `__fern_arr_push_grow` 5, `__fern_arr_push_grow_ptr` 4,
`__fern_strcat` 2, `__fern_arr_push_grow_move_ptr` 1.

Two of those contradict §6. `__fern_arr_cow_inplace` firing at all means arrays
are being copied *because they are shared* — the rc==1 cliff §6 reports as not
firing, which is true of `examples/bench/array_append.fern` and evidently false
of the compiler. And `__str_slice` reaching `memcpy` sits badly with "string
slicing does not allocate". Both were measured on benchmarks; neither
generalised to a 46k-line module.

**`arr_cow_inplace` is now accounted for: it was `x86_patch_rel32`.** Every
branch fixup in the in-process x86 assembler ran four `.with`s over a borrowed
`a.code`, cloning the whole .text per patched branch. Lifting the buffer out of
the struct for the two patch loops (#6911, the idiom `arm64_native.fern` already
used from #6011) took a `checker.fern` compile from 97 s to **19 s** with
byte-identical output. It is the largest single win this audit has produced, and
the cliff counters could not see it at all — see §4c.

**Instrument limit.** Frame #2 resolves to `??` above these helpers, so the
Fern-level caller that chose to copy is not recoverable this way. Attributing
copies to source sites needs `__heap_bump_bytes()` probes or the existing
`FERN_CLIFF_REPORT` diagnostic, not gdb leaf sampling.

**Lesson for this document.** A profile is a snapshot against one build. Three
of §4's four rows decayed within days of being written, and two of the items
ranked off them were aimed at costs that had already been removed. Re-measure
before picking, and record the run count.

## 4c. Copying: what the cliff counters can and cannot see

`FERN_CLIFF_REPORT=1` / `__arr_push_shared_bytes()` are bumped by
`__fern_arr_push`'s un-share path only. A quadratic `.with` — copy-on-write
through `__fern_arr_cow_inplace` — moves neither number. #6911's two halves
separate cleanly on this:

| change | cliff bytes | wall clock |
|---|---|---|
| `own` on the 31 `X86Asm` state params | 21.4 GB → 3.07 GB | 97 s → 97 s |
| buffer lifted out of the struct for the `.with` patch loops | 3.07 GB → 3.07 GB | 97 s → **19 s** |
| `bindingHoldsContainer` in `fieldPlaceAppendCopies` | 3.07 GB → **988 B** | 19 s → **9.3 s** |
| bucket-indexing the assembler's label table | 988 B (flat) | 9.8 s → **5.9 s** |

And on the whole-compiler self-compile, the subject that actually ranks item 3:

| change | wall clock |
|---|---|
| name-indexing `Scope.sigs` | 2 m 13 s → **1 m 44 s** |
| suffix-indexing the array-method lookups | 1 m 50 s → **1 m 31 s** |

So a flat cliff line means "no append regressed", never "nothing is copying",
and the converse holds too: a large cliff number is not by itself evidence of
where the *time* goes.

**Nor is the byte count a cost.** The 3.07 GB left after the `own` conversion
was all `x86_fixup_or_patch`'s two queue appends, one per forward branch — and
one of those queues is a `string[]`. A crossing there memcpys the buffer AND
inc's every element, and the discarded copy dec's every element back. 3 GB of
memcpy, but a leaf profile put `__fern_rc_inc` (100% of it under
`__fern_arr_push_grow_ptr`) plus `__fern_str_dec` (95% under
`__fern_drop_arr_str`, all of it under `x86_fixup_or_patch`) at **33% of the
whole compile**, stable across two runs. Weigh the ELEMENT TYPE, not just the
weight.

**The cause was `fieldPlaceAppendCopies` (#6665), and it is fixed.** That
analysis forces a field-receiver append to copy when the container can still be
read through afterwards. Its `capturing` set marked any bare read of the root
under a `var` initialiser — without checking that the binding could hold the
container. `var target: i32 = x86_label_off(a, name)` hands `a` to a call that
gives back an i32, and an i32 names nothing; the append two lines later cloned
the queue anyway. Minimal repro, two fields:

```fern
function m(own a: T, v: i32): T {
    var t: i32 = borrow(a);              // borrow(a: T): i32
    a = T { ...a, xs: a.xs.append(v) };  // cloned xs, once per call
    return a;
}
```

`bindingHoldsContainer` now gates the mark on the binding's type — a whitelist
of the scalars, because a `Map` handle carries a container while
`ast.IsPointerType` says it does not. On `checker.fern` that took the cliff from
3.07 GB to **988 bytes** (221 crossings — the healthy 2026-08-04 figure) and the
compile from 19 s to **9.3 s**, with byte-identical output. It is a native
Perceus fix, so it pays for every Fern program, not just the self-host.

Attribution: frame #2 resolves to `??` above the runtime helpers (hand-written
asm, no frame pointers), so gdb cannot name the Fern caller — §4b's instrument
limit. Source instrumentation can: bracket a region with
`__arr_push_shared_bytes()` reads and print only non-zero deltas, tagged with
what was being processed. That is how the 21.4 GB was localised to
`x86_gas_assemble` in three ~2-minute iterations. It has an observer effect —
an added statement changes liveness and so can change which appends reuse in
place — so confirm by fixing the site and re-measuring an unprobed build.

## 5. Multiplier 4 — repeated whole-program walks

Not separately quantified, but visible in the profile's any-frame column:
`irlower` runs interprocedural fixpoints (`borrowable_params_interproc`, the
`lift_lambdas` / `lift_inline_closures` views, `expr_unsafe_for` /
`stmt_unsafe_for` / `body_unsafe_for`) that re-walk every function body
several times each, on top of ~15 `module_uses_*` inspection passes. Each
walk is cheap; there are a lot of them, and each one pays §4's lookup costs.

## 6. What is *not* wrong

Recorded so the next audit does not re-derive it, and because two of these
contradict comments in the tree:

- **String slicing does not allocate.** `str` views are real and they work:
  `var part: str = src[i:i+16]` measures **0 bytes** per iteration via
  `__heap_bump_bytes()`, and so does the compiler's own
  `nm[nm.len()-tn : nm.len()] == target` shape, including when `nm` comes
  from a struct field. The comment at `irlower.fern:21187` claiming that
  slice churn was "~800 MB of the whole-program emit floor" describes a
  fixed problem, and the hand-rolled byte loop it justifies is now
  unnecessary. (`ast.go:96` also offers `str.to_owned()`, which does not
  exist — `str` has `as_bytes` and `len`.)
- **Array append is good.** Build-and-sum of 5 M `i32`: Fern 0.128 s, Go
  0.300 s. The bump allocator plus in-place-when-unique beats a GC here, and
  the rc==1 append cliff does not fire.
- **The map is a reasonable IndexMap.** Open addressing, power-of-two
  buckets, FNV-1a over 4-byte blocks with an fmix32 tail, insertion-ordered
  entries. String-keyed lookup is 4.3× Go's (0.480 s vs 0.111 s on 100 k
  inserts + 1 M lookups) — that gap is §2 and the absent cached hash, not
  the data structure.
- **The bounds-check elision works** on the `i < xs.len()` idiom: zero trap
  sites emitted. A loop bounded by a separate variable keeps the check and
  reloads the length each iteration.

## 7. Ranked by leverage

| # | Fix | Where | Evidence |
|---|---|---|---|
| 1 | ~~Share drop code between exits~~ — **done as outlining**, #6894 | `internal/ir/rc_insert.go` | §3 — −71.3% whole-compiler emit, self-host binary −42.5% |
| 2 | ~~Peephole the push-then-discard triple~~ — **done**, P3 | `x86_64.go:peepholeTail`, arm64 twin | §2 — was 12.1% of emitted instructions; measured −13.0% on the checker driver |
| 3 | Hash the self-host `Scope` tables — miss-allocation half landed in #6899 | `examples/self_host/checker.fern` | **§4b — 2.5–6%, not §4's 17%** |
| 4 | Register allocation (#4112) | `internal/ssa` → new native emit | §2 — 36.5% of emitted instructions |
| 5 | Symbol interning (#4394 lever 1) | `lexer.fern`, `flatten.fern` | §4b — **18.0% in `strcmp`, in both runs** |
| 6 | `ir.Inline` + IR dead-funcs on the natives (#4377) | `internal/codegen/{x86_64,arm64}` | measured −9% when trialled |
| 7 | ~~Index the string-encoded borrow registry~~ — **done**, #6909 | `irlower.fern` | **measured −0.18%: the cost was already gone** |
| 8 | **Cut the copying** — `arr_push_grow*`, `str_slice`, `strcat`; `arr_cow_inplace` done for x86 in #6911 | runtime + whoever calls them | §4b — 24–51%, the largest cost and previously unlisted |

**The ordering to trust is 8, then 5, then 4** — not the numbering, which is
historical. 8 is where the time is; 5 is the only pre-existing item still
measuring near its original attribution; 4 is the multi-PR track.

**8 is done for this workload — and it was worth 16x end to end.** #6911 landed in two
parts: the x86 in-process assembler got the `own` treatment `arm64_native.fern`
got in #6011 (97 s → 19 s), and `fieldPlaceAppendCopies` stopped treating a
container handed to a scalar-returning call as captured (19 s → 9.3 s). The
append cliff on `checker.fern` went 21.4 GB → 988 bytes, output byte-identical
throughout. §4c has the mechanism and the minimal repro.

**Then the assembler's label table, which the same profiles surfaced.** With
the rc traffic gone, a 200-sample run of the 9.3 s build put
`x86_native__x86_label_idx` at 21.0% directly plus 32 of `__fern_strcmp`'s 56
samples — **37% of the compile in one linear scan**, ~14k names walked per
branch fixup and again per resolve. Bucket-indexing it by name took 9.8 s to
**5.9 s**, byte-identical (#6911). It is item 3's shape (a flat table scanned
per lookup) in a different file, and it was the larger of the two.

**Item 3 was downgraded on the wrong workload.** §4b measured the `Scope`
cluster at 2.5–6% and concluded #6899 had taken most of it — but that was the
self-host compiler compiling ONE module (`checker.fern`), where `sigs` is small.
Profiled against the whole compiler compiling ITSELF — the fixpoint workload,
where `sigs` reaches ~4,600 entries — `Scope.lookup_sig` is **62% / 64%** of the
run across two 400-sample runs, every direct sample of it under one caller
(`check_call_expr`). Name-indexing the table took that self-compile from
2 m 13 s to **1 m 44 s** (user 99.5 s → 70.5 s), byte-identical.

**Pick the subject as carefully as the metric.** A per-module compile and a
whole-compiler self-compile rank these items differently, and the audit had been
reading the first while the roadmap cares about the second. `lookup_sig` was
0.5% of the `checker.fern` run and 62% of the self-compile.

`Scope.array_method_ret_type` was next, at **35%** of the 1 m 44 s build (52
direct + 89 of `__fern_strcmp`'s 120): the same table, scanned by SUFFIX
(`nm` ends with `__method_Array_<field>`), which the exact-name index cannot
answer. A second index over the same array, keyed on the substring from the
LAST `__method_Array_` occurrence, took the self-compile to **1 m 31 s**
(user 74.4 s → 57.9 s).

## 4d. The kernel third: zeroing, not page faults

User time is now 57.9 s of a 91 s wall — **36% of the run is SYSTEM time**, and
it did not move once across this whole sequence (35.6 s → 33.3 s while user time
nearly halved). It is now the largest single block, and no leaf profile of user
code will show it.

It is not syscalls: `strace -c` over a whole self-compile counts **29 syscalls**,
0.9 ms, one of them the arena's `mmap`. It is the arena's first-touch faults —
1,414,760 minor, 0 major, against a resident set that climbs past 6.5 GB.

**Huge pages are not the fix, and the experiment is worth not repeating.** THP
is `[madvise]` on typical hosts, so the arena gets 4 KiB pages; adding
`madvise(base, len, MADV_HUGEPAGE)` after the mmap works exactly as advertised —
`AnonHugePages` goes 0 → 6.7 GB and the fault count **1,414,760 → 3,562, a 397x
cut**. System time moved **0.2 s**. Wall clock improved ~2.5 s (2.7%, consistent
over two A/B rounds) and peak RSS rose 5.68 GB → 6.72 GB from 2 MiB rounding.

So the kernel time is not fault *count*, it is the **zeroing** the kernel owes
for every fresh page — identical work whether it arrives as 1.4 M 4-KiB pages or
3.5 k 2-MiB ones. ~6.7 GB at this container's memory bandwidth is the whole
34 s. The lever is allocating less, not mapping differently. (The 2.7% is real
but it buys a global +18% RSS on every binary including the small CLI tools, so
it is a project trade rather than a codegen tweak — not taken here.)

**Where the allocation is.** `__heap_bump_bytes()` bracketed around the driver's
phases, whole compiler compiling itself:

| phase | cumulative | of which |
|---|---|---|
| lex + parse + flatten (`load_bundle`) | 383 MB | 378 MB |
| type check | 1.470 GB | 1.087 GB |
| `annotate_module` | 1.520 GB | 50 MB |
| `module_with_builtins` | 1.534 GB | 14 MB |
| **`asm_ir.emit_module_or_error`** | **9.628 GB** | **8.094 GB** |
| in-process assemble + link | 10.116 GB | 488 MB |

**80% of every byte the compiler bumps is the IR lowering + x86 emit.** It is
not the output text: the emitted assembly is ~19 MB, so even a doubling string
builder accounts for under 40 MB of it.

One level in, with the same probe inside `emit_module_ir_gated`:

| step | of the emit's 8.094 GB |
|---|---|
| `script_normalized` | 264 MB |
| `infer_ret_types_module` | ~0 |
| side tables (`array_ret_fns_of`, `borrowable_params_interproc`, `fn_sigs*`, `consume_safe_params`) | 147 MB |
| **the `irlower.lower_func` loop** | **6.550 GB** |
| `emit_module_ir_unit` (IR → asm text) | 1.132 GB |

So **65% of the whole compiler's allocation is one loop**: lowering each
function's AST to IR ops. The side tables — the interprocedural fixpoints §5
flags as repeated whole-program walks — are 147 MB between them, i.e. not where
the memory goes either.

That is roadmap goal 2's territory (the self-host's memory management) rather
than another index.

### 4d.1 Lowering is super-quadratic in `else if` depth — and OOMs

Measuring allocation per lowered function, as the line above says to, points at
one shape. Of the 6.55 GB across 4,649 functions (mean 1.38 MB each), the top
three are **2.28 GB** — `asm_ir__emit_function_via_ir_pre` (785 MB),
`wasm_ir__emit_function_ir` (774 MB) and
`asm_arm64_ir__emit_function_via_ir` (719 MB). All three are the emit
dispatchers: barely twenty statements apiece, each one an enormous `else if`
chain.

`scripts/depth-repro` isolates it — an N-arm chain and nothing else:

| arms | native peak RSS | self-host peak RSS |
|---|---|---|
| 100 | 16 MB | 56 MB |
| 200 | 18 MB | 278 MB |
| 400 | 20 MB | 1.83 GB |
| 800 | 23 MB | 13.4 GB |
| 1600 | 31 MB | **OOM-killed** |

Native is linear. The self-host multiplies by 4.5–7.3x per doubling — worse than
quadratic — is 580x native's footprint at 800 arms, and **cannot compile 1,600
arms at all**. So this is a bug with a memory cliff, not only a slow path.

**It is not the rc==1 append cliff.** `FERN_CLIFF_REPORT=1` over a whole-compiler
self-compile reports 1,056 crossings / 4,792 bytes; the appends grow in place.

**Two hypotheses are already ruled out by experiment**, so do not re-spend them:

- *`irlower.body_assign_targets`.* It appears ~4x per stack in a gdb sample of
  the 400-arm case, which is recursion DEPTH, not hotness — an easy misread. Its
  recursion did copy each child's whole result via `append_all`, so threading one
  `own` accumulator through it looked obvious; that change made the 400-arm case
  **worse** (1.83 GB → 2.53 GB) and left 800 arms OOMing. Reverted.
- *Huge pages* — §4d above.

**It is also not the whole-body pre-walks — §5's hypothesis is ruled out.**
`lower_func` opens with fourteen of them (`reclaimable_names_of`,
`snapshot_param_names_of`, `aliased_array_names_of`, `precise_drop_names`,
`consumed_scalar_enum_frees`, `trmc_eligible`, …), each handed the whole body,
and they are the obvious suspects for a depth-quadratic walk. Probed one by one
with `__heap_bump_bytes()` on the 400-arm function, **all fourteen together
allocate about 300 bytes**. The function's 458 MB lands entirely AFTER the last
of them — in the statement lowering itself.

**It IS `lower_block`'s per-block pre-scans.** The same fourteen-pass shape, one
level down and without the "once per function" that made the fn-level copy
harmless. `lower_block` opens by running six whole-subtree analyses over the
statement list it was handed — `self_overwrite_reuse_sites`, `cross_reuse_sites`,
`cross_tuple_reuse_sites`, `consumed_rcpayload_option_frees`,
`consumed_scalar_enum_frees`, `consumed_rcpayload_enum_frees`. An `else if`
chain is RIGHT-NESTED, so `lower_block(iff.else_body, …)` at level i is handed
the entire remaining chain, and every one of the six walks all of it — again at
level i+1, and again at i+2.

Probed on the reproducer:

| chain | `lower_block` calls | bumped by the six pre-scans |
|---|---|---|
| 200 arms | 400 | 36.2 MB |
| 400 arms | 800 | **265.4 MB** |

The call count doubles — the recursion itself is linear — while the bytes go
**7.3x**, which is the whole compile's scaling signature. At 400 arms these six
scans are 265 MB of the function's 458 MB.

**The fix direction the code itself suggests.** Each of those analyses is
documented as applying to "THIS block's own statement list", and `lower_block`
already runs for every nested body — so a walk that recurses into nested bodies
is both redundant with the nested call and the source of the quadratic. Either
stop them at the block's own statements, or hoist them to one per-function pass
keyed by statement position, the way the fn-level family already works. Check
each of the six against its own gates before changing it: several of them exist
precisely because a fn-level-only scan MISSED nested blocks (#4353 item 2,
#6127), so "scan only this list" has to keep meaning what those fixes needed.

Item 5 (symbol interning, #4394) should be re-measured on this subject before
scoping: a third of what `strcmp` was has already gone with the two indexes.

**3 is much smaller than its rank suggests.** It measured 17% in §4 and 2.5–6%
in §4b, because #6899 removed most of it. The linear scan is still real and
still worth hashing eventually; it is no longer a headline.

**7 is done and is the cautionary tale.** #6909 replaced the O(N) probe with a
251-bucket index and measured −0.18% — nothing. Two facts had to be checked
against the code first, and both held: the registry is not recomputed per
function (every emit-path caller already hoists `borrowable_params_of` to module
scope, so there was no O(F²) to delete), and first-byte bucketing would have
been a poor index because the names are dominated by `set_*` / `emit_*` / `is_*`
/ `lower_*` / `collect_*` prefixes — the worst first-byte bucket holds 159 of
the 1,359. All correct, all irrelevant: the 10.5% it targeted had already been
removed by #6894 and #6899. What that PR did land was a fix to
`consume_safe_params_interproc`'s convergence test, which was comparing entry
*counts* and stopping early; making it exact recovered a reclaim in
`checker__e060_collect_dyn_locals`.

**Do not mirror 1 into `irlower.fern`.** It reads like the obvious next step
and it is not one: the self-host emitter has no drop-function machinery to
outline *into*, and its emit is already 3.1× denser than the Go path's, so the
duplication 1 deleted is not there to delete. Bringing the self-host to
native's memory-management parity is roadmap goal 2, tracked in
`docs/RC-PERCEUS-SELF-HOST-PORT.md`, not a performance item.

## 8. Reproducing any of this

The corpus and the gate landed with this audit:

```
scripts/perf-bench /tmp/report.txt          # ~15 s, all 15 benchmarks
scripts/ci-check-perf .github/perf-baseline.txt /tmp/report.txt
```

The determinism the gate rests on is checked, not assumed: two full runs on
one commit agreed on **29 of the 30 x86_64 metrics to the digit**, and the
single exception was `map_string.ir` — the one metric the baseline flags as
seed-dependent — 0.43% apart, inside its declared 2% tolerance.

`examples/bench/README.md` covers what belongs in the corpus. For the
compiler itself, `make selfhost-cli` then the commands in §1; for a profile,
build with `-g` (which emits `.symtab`) and sample with gdb — there is no
profiler yet (#5547).
