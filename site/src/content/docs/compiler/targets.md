---
title: Targets and backends
description: Every target Fern emits for, what each one provides, the CPU baselines, and the gaps between them.
sidebar:
  order: 2
---

A target is spelled `<isa>-<environment>`, and **neither half is implied**.
The ISA picks the backend; the environment says what the host provides.
There is no bare `arm64`.

`fern -targets` prints the authoritative table for the compiler you have
installed, including each target's capability surface. What follows is the
shape of it.

## The targets

| Target | Emits | Notes |
| --- | --- | --- |
| `arm64-linux` | static ELF | The default. Assembled and linked in-process. |
| `arm64-darwin` | static Mach-O | Apple Silicon. Ad-hoc code-signed in-process; no dyld. |
| `arm64-android` | ELF `ET_DYN` PIE | Same syscall surface as `arm64-linux`. Pairs with `-shared` and `std/jni`. |
| `x86-64-linux` | static ELF | System V AMD64. Newer than the arm64 backend; a few gaps remain. |
| `wasm32-wasi` | WASI Preview 2 component | `wasmtime run`. Composed natively — no `wasm-tools` step. |
| `wasm32-wasi-http` | WASI Preview 2 component | `wasi:http/incoming-handler`, for `wasmtime serve`. |
| `arm64-freestanding` | — | Type-checks against an empty capability set. No emitter yet. |
| `x86-64-freestanding` | — | Same. |

Two knobs are orthogonal to the target rather than being targets of their
own: `-backend ssa` selects the register-allocating emitter, and
`-emit core-module` / `-emit command-module` change what the wasm backend
wraps its output in.

There is no Windows target, no Intel macOS target, and no 32-bit target.
None is planned.

## What a target provides

Each target carries a capability profile, and **anything the target cannot
provide is not expressible in a program compiled for it** — you get a build
error (`E066`), after tree-shaking, rather than a runtime surprise. There
are four profiles:

- **`hosted-native`** (the native targets) — the full surface: files,
  stdout and stdin, environment, arguments, clock, randomness, TCP,
  processes, signals, user and host identity, C ABI.
- **`wasi-cli`** (`wasm32-wasi`) — most of that, minus process control and
  file modes. `host` answers the empty string and `signal` is a no-op:
  the truth, rather than a stand-in that pretends.
- **`wasi-proxy`** (`wasm32-wasi-http`) — a deliberately tiny set: logging,
  the clock, randomness and outbound fetch. No stdout, no filesystem, no
  arguments, no environment, because a proxy component has no process
  identity. Configuration reaches it as a binding, not as `envp`.
- **`none`** (the freestanding targets) — allocation and pure computation
  and nothing else.

Note that **subprocesses are absent from every compiled target.** They are
an interpreter-only capability, so `E066` rejects them up front instead of
letting the build fail somewhere less legible.

This is a separate system from [package
capabilities](../../reference/packages/), which govern what a *dependency*
may reach. A new builtin usually needs classifying in both.

## CPU baselines

Fern emits static binaries with no runtime dispatch, so a selected
instruction is a hard requirement rather than a fast path. The baselines
are therefore project decisions, not codegen ones:

- **arm64** — plain ARMv8-A, including Advanced SIMD.
- **x86-64** — Haswell-class, 2013: SSE4.2 and BMI1, so `popcnt`, `lzcnt`
  and `tzcnt` are assumable.
- **wasm** — core WebAssembly 2.0, including fixed-width SIMD.

One trap worth knowing if you are tempted to run a binary on something
older: below the x86-64 baseline, `popcnt` faults, but `lzcnt` and `tzcnt`
fail *silently* — they share opcodes with `bsr`/`bsf` plus an `F3` prefix
the older CPU ignores, so you get wrong answers rather than a crash.

## Known gaps between targets

- `-cover` and `-sanitize` are native-only. A wasm build under `-cover`
  errors rather than quietly producing an uninstrumented binary.
- Heap exhaustion exits `125` on the native targets; on wasm the module
  traps, because a failed `memory.grow` has nowhere to go.
- Small-string optimisation stores 15 bytes inline on arm64 and 7 on
  x86-64 and wasm32.
- `wasm32-wasi-http` has no self-hosted counterpart yet, and `-emit asm`
  has no native counterpart.
- The freestanding targets have no emitter. The blocker is not codegen —
  see [Memory](../../reference/memory/) on why an interrupt handler is a
  second context racing non-atomic reference counts.

## Version support

Only the latest release of each OS is supported. Linux CI runs on
`ubuntu-latest`; macOS is pinned to `macos-15` rather than floating
`macos-latest`, so a runner-image roll cannot break the build without a
visible commit. Pinning to an older macOS to dodge a breakage is
explicitly not supported.

## ARM32 is gone

Fern shipped a 32-bit ARM backend through early 2026 — it was the original
target — and it was removed. Cross-backend parity work became untenable and
the hardware story was a poor match for where the language is going. The
backend, its tests and the cross-compilation wiring are all deleted. Do not
expect to find it, and do not add anything back for it.
