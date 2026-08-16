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

## 3. Multiplier 2 — drop code is duplicated per exit

This is the large one, and it was not on anyone's list.

`irlower__lower_call_named` is 1,432 lines of Fern. The Go emitter turns it
into **8,214,279 lines of assembly**, of which **589,045 are
`call __fern_rc_dec`** — 411 decrements per source line. Ten functions are
75.9% of the entire 390 MB emit, all the same shape.

The same function through the self-host emitter: **13,017 lines, zero
`rc_dec`**. A 631× difference on identical source, because the self-host's
Perceus port is incomplete (it leaks instead — roadmap goal 2).

The mechanism is that every scope exit emits its own full drop sweep over
every live local, rather than branching to shared drop code. Reproduced
synthetically with `bin/fern` — decrement count is linear in each dimension
independently, so it grows as their product:

| | decrements |
|---|---|
| 16 locals, 4 → 64 exits | 112 → 176 → 304 → 560 → 1,072 |
| 4 → 64 locals, 16 exits | 76 → 152 → 304 → 608 → 1,216 |

`lower_call_named` has 152 `var` declarations, 268 `return`s and 290 `if`
blocks, each block being a scope with its own sweep.

Across the whole emit: 917,977 rc/drop call sites, 4.1% of emitted
instructions as bare `call`s and roughly 16% once each one's surrounding
load/spill sequence is counted.

**This is why the self-host binary is 160 MB** where the Go compiler is
23 MB — and why `.text` + `.rodata` is 93.3 MB against Go's 16.4 MB.

**The forward-looking part matters more than the current cost.** The
self-host compiler is fast *relative to what it is about to become*: finishing
the Perceus port (goal 2) imports this blowup into the self-host unless drop
sharing lands first. A 631× per-function code increase arriving on the
compiler's hottest functions is not a regression anyone will be able to
bisect after the fact.

## 4. Multiplier 3 — the self-host compiler's symbol tables are linear

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

- **Miss paths allocate.** `lookup_sig` builds a `FuncSig` plus two empty
  arrays plus a concatenated `t_unknown("undefined function:" + name)` on
  every miss — and callers use it as a predicate
  (`s.lookup_sig(name).name.len() > 0`, `checker.fern:734`, `:759`).
  `lookup_struct` and `array_recv_method` do the same.
- **Association lists encoded in strings.** The borrow registry is a
  `string[]` of `"callee|1011"` entries, linear-scanned by
  `param_is_borrowable` per call-arg, per body walk, per iteration of a
  whole-program greatest fixpoint. 10.5% of runtime, on its own.
- **Symbols are compared, not identified.** No interning: every symbol
  comparison is a byte compare and every string-keyed map lookup re-hashes
  the whole key. The interning design already exists — #4394 lever 1,
  `docs/SELFHOST-SYMBOL-INTERNING.md` — and is unblocked.

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
| 1 | Share drop code between exits instead of duplicating the sweep | `internal/ir/rc_insert.go` | §3 — 589 k decrements in one function; 631× vs the self-host emitter |
| 2 | ~~Peephole the push-then-discard triple~~ — **done**, P3 | `x86_64.go:peepholeTail`, arm64 twin | §2 — was 12.1% of emitted instructions; measured −13.0% on the checker driver |
| 3 | Hash the self-host `Scope` tables; stop allocating on miss | `examples/self_host/checker.fern` | §4 — 17% self time, native already does this |
| 4 | Register allocation (#4112) | `internal/ssa` → new native emit | §2 — 36.5% of emitted instructions |
| 5 | Symbol interning (#4394 lever 1) | `lexer.fern`, `flatten.fern` | §4 — 20.5% in `strcmp` |
| 6 | `ir.Inline` + IR dead-funcs on the natives (#4377) | `internal/codegen/{x86_64,arm64}` | measured −9% when trialled |
| 7 | Replace the string-encoded borrow registry with a map | `irlower.fern` | §4 — 10.5% self time |

1, 3 and 7 are each a bounded change with a measurable outcome and no
architectural prerequisite — 2 was, and landing it took an afternoon. 4 is the
multi-PR track.

## 8. Reproducing any of this

The corpus and the gate landed with this audit:

```
scripts/perf-bench /tmp/report.txt          # ~8 s, whole corpus
scripts/ci-check-perf .github/perf-baseline.txt /tmp/report.txt
```

`examples/bench/README.md` covers what belongs in the corpus. For the
compiler itself, `make selfhost-cli` then the commands in §1; for a profile,
build with `-g` (which emits `.symtab`) and sample with gdb — there is no
profiler yet (#5547).
