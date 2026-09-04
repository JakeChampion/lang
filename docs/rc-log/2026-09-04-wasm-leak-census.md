# wasm gets a leak census, and the rc corpus a third leak leg

#7912's wasm bullet: "no leak detector at all". Not a leak entry — this adds
the instrument, and then reports what the instrument immediately found.

## Two of the issue's three gaps were already closed

Checked against the code before writing anything, per the tracker-lag rule:

- **arm64 quarantine.** Present and complete. `internal/codegen/arm64/arm64.go`
  carries `quarantine` (5 poison sites + the declined freelist push at
  `emitFreeRuntime`) and `rcPoisonCheck` at three readers — `__fern_rc_inc`,
  `__fern_rc_dec`, and `__fern_str_inc`, which inlines its bump and so needs
  its own check. `internal/e2e/sanitizer_test.go` has the arm64 legs for all
  four properties. Nothing to do.
- **self-host x86-64.** Also complete, including the use-after-free quarantine
  the issue lists as missing — `docs/rc-log/2026-08-23-uaf-quarantine-port.md`
  landed it, gated by `internal/e2eselfhost/self_host_uaf_quarantine_test.go`.
  `docs/SANITIZER.md` still described the old subset; corrected here.

So the only live gap was wasm.

## The census

Two counters in two helpers. This runtime has exactly two chokepoints —
`__fern_alloc` (the box / rc1 / u8-array wrappers all forward to it) and
`__free` (the freelist push lives *inside* it, so there is no second writer of
a released block) — which is why wasm needs no per-site list where the natives
have one. Both count at `(size+15)&-16`, the natives' rounding, so alloc and
free cancel exactly and the large tier's internal waste does not drift
`live_bytes`. The alloc-side bump is placed AHEAD of the freelist pop's early
return: the census is of blocks handed out, not of memory bought.

`__fern_lc_report` formats the line into static scratch and writes it to
stderr — `fd_write` on preview 1, `wasi:cli/stderr` on preview 2 — with
`__fern_lc_wrnum` as the itoa (the language's i64-to-string is Fern-level and
may not be in the module). Text byte-identical to both natives':

```
leakcheck: allocs=<N> frees=<M> live_bytes=<K>
fern-sanitizer: leak <K> bytes in <N> blocks     # FERN_SANITIZE only, K > 0
```

Two wasm-specific pieces:

- **The report latches.** Wasm's exit seams NEST: the synthesised `_start` /
  `_lang_run` wrapper calls `__fern_exit`, which is also where the `exit()`
  builtin lands. Native's seams are exclusive and need no latch; here without
  one a program leaving through both prints the census twice.
- **The call sits immediately after `call main`,** before the wrapper's own
  epilogue. `PrintMainResult` stringifies and prints main's result, which
  allocates — a census taken after that charges the harness's bytes to the
  program it is measuring. (Measured: 16 B of harness, every time.)

Counters and buffers are scratch slots chained off the end of the reserved
window, like `rcUnderflowAddr` and the append-cliff pair already there.
Unconditional, as those are: nothing writes them flag-off, but a flag-off wasm
module is not byte-identical to one from a compiler predating the census, which
the natives' guarantee does promise. Recorded in `docs/SANITIZER.md` rather
than quietly.

## What it found on day one

`__fern_print` copies its argument into a fresh `__fern_alloc` buffer and never
freed it — one leak per call, sized by the string. Preview 2 leaked a second
16-byte block per call (the `blocking-write-and-flush` result area), and
`__fern_putchar` leaked both. Every write is synchronous, so both blocks are
dead when the call returns; they are released there now. Witnessed:
`TestCommandModuleLeakCensusReportsLeak` read `live_bytes=144` for a program
whose only leak is two 64-byte blocks, and reads 128 with the fix.

## The gate

`TestWASMRcCorpusLeakGate` — the third leg of #7790's rc-corpus leak gate, over
the same 267 cases, same pinned-baseline discipline (a case absent from the
table must read 0; leaking more fails; leaking LESS fails asking to be banked).
Verified byte-identical across repeat runs. 42 cases leak, against 40 on x86-64
and 47 on arm64, and the differences are findings:

- `cell_string_read_aliased` and `copying_builtin_own_param_not_double_freed`
  leak on x86-64 and reclaim here — both are single-word-string shapes, an ABI
  this backend does not carry.
- Four map cases with string KEYS or VALUES leak here and are clean on BOTH
  natives: `map_string_keys_churn_free` (3200 B),
  `map_string_values_churn_free` (3200), `map_keys_values_header_churn_free`
  (16000), `map_string_value_overwrite_pre_drop_churn` (16000). **That is the
  next lead** — the string buffers a map column owns are not reclaimed on the
  wasm backend, and it took a detector on this backend to see it.

## Still not on wasm

The two fatal checks. Both want a poisoned rc word and an abort path, and
neither is built here — so on wasm a silent run means "did not leak", not
"clean". The over-release COUNTER is still there (`__rc_underflow_count()`); it
is just not a report. A `wasm32-wasi-http` handler has no exit seam at all, so
its counters tick and nothing prints — the same rule as a native server that
never returns from `main`, and what `-sanitize` now says for that target.
