# Self-host symbol interning (#4394 lever 1) — measured and retired

Status: **closed, negative result.** Interning the self-host compiler's
identifier space was specified, built, and measured; it moves nothing worth the
code, because the churn it targets is not there. This note records what the
premise was, what the measurement said, and the one real allocation source the
work did find — so nobody re-derives it. The harness that made it decidable is
`scripts/selfhost-alloc-bench`.

Numbers below are the self-host x86-64 compiler compiling
`examples/self_host/checker.fern` against `internal/stdlib`, measured
2026-08-26 at `fce242c`.

## What the premise was

`docs/IR-SELFCOMPILE-OOM-FINDINGS.md` pins the self-compile's binding constraint
on allocation *performed*, and #4394 lever 1 named two sites:

- **`lexer.fern` `scan_ident`** — "allocates a fresh heap substring
  `l.src[begin:l.i]` per identifier *occurrence*", so every `x` in a program is
  a distinct heap string that every later pass compares by content.
- **`flatten.fern` `mangle_bare`** — allocates `prefix + "__" + name` per
  reference rewrite, and those mangled names ride `Op.str` into codegen.

The fix was to be a `SymTab` (name → i32 id, names stored once) owned by the
lexer, so identifiers are ids from birth.

## What the measurement says

Three findings, each of which independently removes a piece of the premise.

### 1. Short identifiers have no heap body at all — SSO

Native strings of **7 bytes or fewer pack their bytes inline** (the SSO
inline-tag paths, `internal/codegen/arm64/arm64.go:2180` and the x86-64
equivalent), so they never touch the heap. Lexing 20,000 occurrences of five
distinct names costs, per occurrence:

| name length | heap allocations per occurrence |
|---|---|
| 5, 7 | 0 |
| 9 and up | 1 |

Across `checker.fern`, `parser.fern`, `lexer.fern`, `util.fern` and `ir.fern`,
**8.7% of identifier occurrences exceed 7 bytes** (14,835 of 170,769). The
"fresh heap substring per identifier occurrence" the lever was built to remove
applies to under a tenth of them.

### 2. Interning what remains is worth 0.02%

A lexer-owned open-addressed intern table, keyed on the source byte range so a
repeat occurrence allocates nothing (the design this note used to specify), was
built and measured against the same compile:

| variant | allocations | Δ vs the step above | peak RSS |
|---|---|---|---|
| baseline | 20,802,163 | — | 900 MB |
| `scan_ident`'s `NumResult` box removed | 20,653,542 | −148,621 (−0.71%) | 900 MB |
| + identifiers and keywords interned | 20,641,909 | −4,535 (−0.02%) | 900 MB |
| + punctuator spellings interned | 20,634,710 | −7,199 (−0.03%) | 900 MB |

Interning is 11,734 of the 160,355 allocations removed — **7% of the win, for
all of the machinery**: a hash-indexed table, a rehash path, and a probe in the
hot lexer loop. Live bytes at exit moved 0.05%; peak RSS did not move at all.
The emitted asm was byte-identical at every step. So the table was not kept —
`examples/self_host/symtab.fern` and its driver are deleted, not parked.

Wall clock is not evidence either way here: three interleaved rounds of the
same pair disagreed on the sign, exactly as `docs/LOCAL-DEV-LOOP.md` warns.

### 3. The one real per-identifier allocation was a struct box

93% of the win above is a **`NumResult`**: `scan_ident` returned
`{lex, tok}`, and boxing that struct cost one heap allocation per identifier
*token* — 156k of them on this compile, against ~15k for every identifier string
body put together. Splitting it into `ident_end` (the scan) plus
`ident_or_keyword` (the classification) lets the caller thread `Lex` itself and
build the token directly. That change shipped; it is byte-identical.

The lesson generalises past this lever: in a compiler whose short strings are
free, **per-token struct boxing is the allocation, not the text**. The other
`scan_*` helpers still return `NumResult`, and `advance()` still allocates a
`Lex` per byte on the paths `advance_to` does not cover — neither is #4394's
scope, and both are larger than anything interning was going to reach.

## The `Op.str` half, which was already dead

The other target — putting `Op.str` (mangled callee names) on ids — was
retired earlier, on its own evidence, and this note kept the reasoning:

- **Runtime-helper callee names** (`__fern_str_dec`, `__fern_rc_inc`, …) are
  Fern **string literals**, `.rodata`-backed by the backends' own
  `str_lit_label` paths. No heap body.
- **Mangled user-callee names** are deduped since #4612: `mangle_bare`'s
  own-decl branch resolves to a precomputed shared instance from
  `RewriteCtx.declnames_mangled`, so N references to a decl share one body.
  irlower passes the AST identifier straight into `op_call_direct` rather than
  re-concatenating.
- So the residual is not string bodies but the **`Op.str` pointer slot × N ops**,
  and `str` cannot leave `Op`: it is double-duty, carrying `const_str`'s literal
  *value* as well as call names, and Fern structs have no optional fields.
  Adding a `sym: i32` beside it *grows* every op box — a memory regression with
  no offsetting body removal.

Reclaiming that slot is an **IR-representation** change (a constant pool for all
string operands, or an op-variant split so ops that carry no string do not pay
for one), scoped against a measured baseline. It is not an interning slice, and
nothing in it needs a name→id table.

## Measuring this: use `scripts/selfhost-alloc-bench`

The reason this lever sat unmeasured for a year is that both obvious
instruments lie, and the script exists so the next attempt starts from a
working one:

- **`getrusage` / `ru_maxrss` is useless here.** The runtime pre-reserves a
  large fixed arena, and `ru_maxrss` comes back at ~the arena size *independent
  of the workload* — 100×20 and 1000×20 report the same number. It measures the
  reservation, not touched pages.
- **Sampled `/proc/<pid>/status:VmHWM` under-reports.** The compile is
  sub-second to seconds, so even a tight poll misses the transient peak; a
  bigger workload can sample *lower* than a smaller one.
- **A scratch cgroup is the right tool**, and is available wherever cgroup
  delegation is (`memory.peak` on v2, `memory.max_usage_in_bytes` on v1). Two
  details decide whether the number is steady or drifts: the cgroup must be
  **fresh per run** — a v1 memory cgroup charges page cache to whoever faulted
  it in, so a reused one climbs across runs — and the emitted asm must go to
  `/dev/null`, or 8 MB of it is charged as cache. With both, the reading
  repeats to the megabyte.
- **`FERN_LEAKCHECK=1` is the churn number**, and it is exact rather than
  sampled. It is read at *emit* time, so the compiler being measured has to be
  built with it set; the script builds its own instrumented binary rather than
  touching `bin/fern-selfhost`. It repeats to the digit — every run of one
  binary on one subject reports the same count.
- **Hold the SUBJECT fixed across an A/B.** `checker.fern` imports `lexer.fern`
  transitively, so editing the lexer changes what the measurement compiles as
  well as what compiles it, and the two moves land in one number. The readings
  above are all taken with the *shipped* tree on disk, varying only which
  compiler binary runs; an earlier round that let the subject drift read 250k
  allocations higher for the same baseline binary. A stray `.fern` file left in
  `examples/self_host/` moves it too.

## Relationship to the native side

Per `docs/NATIVE-CONVERGENCE.md`, none of this was native-only surface, so
nothing here is freeze-relevant debt.
