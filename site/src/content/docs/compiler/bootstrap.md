---
title: Self-hosting and bootstrap
description: The Fern compiler written in Fern — how it bootstraps from a pinned binary, which reproducibility gates are green, and the one that is not.
sidebar:
  order: 3
---

Fern's compiler is written in Fern. `examples/self_host/` is 222,000 lines
across 118 modules: parser, type checker, optimiser, all three backends,
the arm64 and x86-64 assemblers, and the ELF and Mach-O linkers. It
compiles itself, and it bootstraps on a machine with no Go toolchain.

This page is the honest account of what that does and does not mean.

## Building it

```sh
make bootstrap        # → bin/fern-selfhost
```

That downloads a pinned earlier compiler (**stage 0**), uses it to compile
the current sources into **stage 1**, smoke-tests the result, and installs
it. No Go, and no native backend, is needed on the machine running it.

Stage 0 is a release asset rather than a binary checked into the tree, and
`bootstrap/stage0.lock` pins it by the sha256 of the *uncompressed* binary
for each of three hosts (x86-64 Linux, arm64 Linux, arm64 macOS). You can
check the pin by hand:

```sh
curl -sL <asset-url> | gzip -dc | sha256sum
```

Or regenerate it: check out the source commit the lock names, run
`make selfhost-cli`, and the bytes will match, because a build at a given
commit is deterministic. Releases holding a pinned stage 0 are never
deleted or moved.

There is one honest caveat about that pin, and it matters more than it
looks: **the bootstrap procedure needs no Go, but the pin's provenance
still does.** See the reproducibility gate below.

## What "it compiles itself" is actually checked by

Three different gates, which prove different things.

**The per-module emit-all fixpoint.** The compiler compiles itself to
produce generation 0; generation 0 compiles the same sources to produce
generation 1; generation 1's emitted output for every module must be
byte-identical to generation 0's. This is a strong determinism gate — it
runs over the whole compiler, not a sample.

**The cross-architecture stage-2 fixpoint.** A compiler built on x86-64
emits aarch64 code for itself; that binary runs under emulation and must
emit byte-identical assembly. This proves the output does not depend on the
architecture that produced it, and it runs *through the assembler's own
output* rather than around it.

**The conformance corpus and the self-host end-to-end suite.** 578
conformance cases and a large end-to-end suite, run against the
self-hosted emitters as their own implementation.

The third one carries most of the weight, and here is why. **A fixpoint is
self-referential.** It proves the compiler reproduces itself; it is
structurally blind to a *stable* miscompile — a bug reproduced faithfully
in every generation still passes. This is not hypothetical: a change once
passed the per-module fixpoint, all the emitter fixtures and the entire
native suite while segfaulting the compiler driver. Treat the fixpoint as
secondary and the behavioural suites as primary.

## The gate that is red

`make distcheck` builds stage 2 from stage 1 and requires the two to be
byte-identical. It does not pass today, and the reason is memory rather
than correctness: the self-hosted compiler frees less than the Go one does,
so compiling the compiler with a self-hosted compiler peaks far above what
the arena allows, and the process is killed.

Measured on a four-core, 16 GB x86-64 host in September 2026: stage 1
(built by the Go stage 0) takes about 79 seconds and peaks around 4.0 GB.
Stage 2 — the same work, done by stage 1 — runs roughly 140 seconds and
peaks at 13.7 GB before being OOM-killed. A leak check on a single module
shows the shape clearly: the same number of allocations, about a third of
the frees.

That is the whole of the remaining gap, and it is why [porting reference
counting to the self-hosted compiler](../../reference/memory/) is the
project's current priority rather than one item among many.

## Speed

Compiling the entire compiler to x86-64 assembly takes the Go
implementation around 27 seconds and the self-hosted one around 68 — a
factor of about three, down from a factor of six earlier in the year. Both
produce byte-identical output.

## Why the snapshot is not WebAssembly

The obvious way to make a bootstrap binary portable is to ship it as a
`.wasm`. It does not work here, for two independent reasons. Writing an
executable file needs file permission bits, which the component model's
filesystem does not have, so the capability system refuses it. And
compiling the compiler peaks at around 4 GB, which is the entire address
space a 32-bit wasm module can have.

## The plan for the two implementations

The Go implementation is not meant to live forever alongside the Fern one.
Once the self-hosted compiler reaches parity and the freeze preconditions
are met, `internal/` accepts only bugfixes, whatever the self-hosted
sources need in order to bootstrap, and what keeps it useful as a
differential oracle. Until that happens, a new native-only feature is
treated as debt rather than as progress.
