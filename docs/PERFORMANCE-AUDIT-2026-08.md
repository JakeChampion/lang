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

So a flat cliff line means "no append regressed", never "nothing is copying",
and the converse holds too: a large cliff number is not by itself evidence of
where the *time* goes.

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

**8 has paid once already and is not finished.** #6911 gave the x86 in-process
assembler the `own` treatment `arm64_native.fern` got in #6011: a `checker.fern`
self-host compile went 97 s → 19 s and the append cliff 21.4 GB → 3.07 GB, all
from one file. The remaining 3.07 GB still copies ~2,170× the emitted binary, so
the next increment is in the same place — see §4c for how to find it.

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
