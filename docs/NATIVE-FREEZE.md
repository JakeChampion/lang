# Native freeze — 2026-09-28

The native compiler (`internal/`) stopped being where language features land
on 2026-09-28, when the last of `NATIVE-CONVERGENCE.md`'s four preconditions
went green. That doc holds the policy and the definitions; this one records the
date, the gate state on that date, and what changed hands.

## The gate on the day

`make freeze` (`tools/freeze_gate.sh`) at the commit that wired `make
distcheck` into CI:

```
1. Roadmap goal 2 — Perceus port at parity in the self-host compiler
  GREEN        constructor-reuse admission present (struct_fields_reusable_cross)
  GREEN        make distcheck runs in CI — the self-host compiler reproduces itself

2. #3451 / #3457 — per-module compilation, ending with the AST emitters deleted
  GREEN        asm.fern / asm_arm64.fern / wasm.fern all deleted
  GREEN        asm_ir.fern routes IR-or-error (emit_module_or_error)
  GREEN        asm_arm64_ir.fern routes IR-or-error (emit_module_or_error)
  GREEN        wasm_ir.fern routes IR-or-error (emit_module_mode_or_error)

3. Checker-codes differential compares unfiltered code sets
  GREEN        no selfHostImplementedCodes filter in code (comments referencing it are fine)

4. SH-057-class semantics (mutable scalar capture) closed in every engine
  GREEN        self-host interpreter has by-reference scalar capture (VCellI/VCellF)
  GREEN        capture cells are wired (cellify_env)

summary: 9 derived green, 0 pending measurement
```

Preconditions 2, 3 and 4 went green on 2026-08-02, 2026-07-12 and 2026-08-02.
Precondition 1's criterion is `make distcheck` green: the self-built compiler
recompiling the compiler, and the result doing it once more, byte-identically.
Measured on a 4-core 16 GB x86-64 host (`BOOTSTRAP.md` has the table): stage2
and stage3 are identical, stage3 built in 137 s at 5.7 GB peak RSS. On
2026-09-02 the same step was OOM-killed at 13.9 GB. What closed the gap was
the reclaim work logged in `rc-log/` and the typed semantic lowering
(`SELFHOST-SEMANTIC-SOURCE.md`) becoming the default: the compiler it builds
runs the whole tree in a twelfth of the AST lowering's memory.

`distcheck` had been comparing stage1 with stage2, which cannot match once a
code generator change lands after the pin's commit: stage1 carries the pin's
codegen, stage2 the source's. The Go-style comparison — stage2 against a
stage3 that stage2 built — is what the script checks now, and the measurement
above is the first run of it.

## What changes

- `internal/` (`internal/ir`, `internal/interp`, `internal/codegen/*`, the
  native front end) accepts **bugfixes, oracle needs, and what the self-host
  sources require to bootstrap** — the "Go 1.4 rule" of
  `NATIVE-CONVERGENCE.md §1`. A new language feature lands in
  `examples/self_host/` first, gated by the fixpoints and the differential
  suites; native gets it only if the self-host sources come to use it.
- A native-only feature that still lands is an exception argued on #4451, so
  the debt stays visible in one place, rather than the default.
- The bootstrap pin on every host is the self-built fixed point
  (`bootstrap.yml`'s publish job uploads `build/bootstrap/stage2`). Using it
  needs no native binary; producing the next one still seeds a native-built
  candidate on every publish, so each pin has one native-built generation in
  its ancestry (`BOOTSTRAP.md`).

## What does not change

- `internal/interp` stays the semantics reference every differential test
  anchors on, and keeps receiving bugfixes (`NATIVE-CONVERGENCE.md §3`).
- The differential suites keep running on every push; the freeze is a policy
  on where features land, not a relaxation of any gate.
- The native backends are **not** deleted. Retiring them has its own
  prerequisites (`NATIVE-CONVERGENCE.md §3a`), and the freeze is the first of
  the two events, not the second.

## What still stands between the freeze and retiring the native backends

`NATIVE-CONVERGENCE.md §3a`, read against the tree on the freeze date:

1. **Bootstrap without native** — closed on x86-64 Linux and on arm64
   Linux. On the arm64 runner the current compiler's chain is a fixed point
   (`candidate-arm64-linux`: stage2 in 82 s, stage3 in 79 s, identical); the
   2026-09-25 pin's stage1 looped compiling the compiler there and under
   qemu (#10448), so the pin was refreshed to a self-built stage2 from the
   PR that wired the lane. Closed on arm64-darwin on 2026-09-29: the arena
   exhaustion its stage2 hit compiling the compiler (#8479) stopped
   reproducing, the `verify-arm64-darwin` lane runs `make distcheck` there
   too, and its pin is a self-built stage2 like the Linux ones.
2. **Every target self-contained on the self-host side** — closed.
3. **The oracle decision** — made on 2026-09-28: the native backends are
   not witnesses, and they go with the next step after the freeze.
   `internal/interp` is the oracle the differentials anchor on, and §3 keeps
   it. Gates that compare self-host codegen against native codegen go with
   the backends or are re-anchored on the interpreter, in the deletion PRs.
4. **The non-compiler consumers** — the playground runs on the self-host
   compiler since 2026-09-28 (`PLAYGROUND-SELFHOST-WASM.md`, top), the
   wasi:http world included: the self-host compiler has had
   `wasm32-wasi-http` since the same day (#6636: `std/wasi_http` is the
   entry, `wit_compose.fern` the framing, and the native component is the
   differential's other side), and the page's http panes run on it. What
   `cmd/fern-wasm` still carries is the language server (#6641), the Go
   front end the freeze keeps, which does not touch the backends;
   `cmd/fern-lsp` is the same front end.
