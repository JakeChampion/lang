---
title: How the compiler works
description: The pipeline from .fern to executable, the shared IR, and the in-process assembler and linker that mean no external toolchain is involved.
sidebar:
  order: 1
---

`fern -o app app.fern` is the whole build. There is no object file, no
`ld`, no `gcc`, and no build system — the compiler goes from source to a
finished executable inside one process.

This section is for people who want to know how, or who want to work on
it. Nothing here is needed to write Fern.

## The pipeline

```
source.fern
  ↓ lexer
  ↓ recursive-descent parser              → AST
  ↓ type checker                          → aggregated errors, did-you-mean
  ↓ monomorphisation                      → generics become concrete
  ↓ closure conversion                    → lambdas become explicit
  ↓ IR lowering                           → a flat, value-based instruction stream
  ↓ IR optimisation                       → constant folding, RC insertion + elision, TRMC
  ↓ backend                               → arm64 / x86-64 / wasm
  ↓ assembler + linker                    → ELF, Mach-O, or a wasm component
```

Two things about the middle of that list are worth calling out.

**The IR is target-agnostic, and that is where optimisation belongs.** All
three backends consume the same instruction stream, so an optimisation
written once benefits every target. It is a flat `[]Op` value stream rather
than a graph of instruction objects, with rarely used fields moved to a
side table to keep each op small.

**Reference counting is an IR pass, not a runtime.** Increments and
decrements are inserted here, then the elision analysis removes the pairs
it can prove redundant, and the reuse analysis rewrites allocating
constructions into in-place writes. [Memory](../../reference/memory/) covers
what that means for the language.

## No external toolchain

The native backends do not stop at assembly text. `internal/native` holds a
pure-Go assembler for arm64 and for x86-64, an ELF writer, a Mach-O writer,
a DWARF emitter and an ad-hoc Mach-O code signer, and the compiler drives
all of them itself.

That is unusual enough to be worth spelling out what follows from it:

- **Nothing else has to be installed.** Not a C compiler, not binutils, not
  `wasm-tools`. The one binary is the toolchain.
- **The assembly text never exists.** For a build the size of the compiler
  itself, that is hundreds of megabytes that are never formatted, never
  written to disk and never re-parsed — which is most of why the in-process
  path is both faster and lighter than shelling out to `as` and `ld`.
- **The whole program is visible at once.** There is no `.o` round trip to
  lose information across, so what other toolchains call link-time
  optimisation is just where the compiler already is.

`-cc CC` opts out to an external assembler and linker, and exists mostly as
an escape hatch. It has earned its keep once, though, in the other
direction: the native assembler refuses to emit code it cannot encode
correctly rather than emitting something wrong, and three assembler bugs
were found precisely because the fallback had been masking them as codegen
bugs.

## Determinism

The same input produces byte-identical output, and the gate on that is
stronger than most compilers carry: the compiler compiles itself, the
result compiles itself again, and the two must be identical byte for byte.
A cross-architecture variant runs the second stage under emulation on a
different ISA, which proves the emitted code does not depend on the machine
that emitted it.

See [Self-hosting and bootstrap](../bootstrap/) for what those gates do and
do not prove — the answer is more interesting than it sounds.

## Two implementations

There are two compilers for Fern, on purpose:

- **`internal/`** — the Go implementation. This is what `go install` gives
  you, and what compiles the pinned bootstrap binary.
- **`examples/self_host/`** — the Fern implementation. 222,000 lines across
  118 modules, covering the parser, the checker, the optimiser, all three
  backends, the assemblers and the linkers.

They are not a permanent pair. The project's convergence policy says that
once the Fern implementation reaches parity, the Go one accepts only
bugfixes, whatever the self-hosted sources need in order to bootstrap, and
what is required to keep it useful as a differential oracle. Until then,
every native-only feature is counted as debt rather than as a free win.

Having two implementations of the same language specification is also what
makes the [conformance corpus](https://github.com/JakeChampion/lang/tree/main/conformance)
meaningful: 578 cases that define observable behaviour by example, run
against the interpreter, all three backends, and the self-hosted compiler's
own emitters. Changing what a case expects is a change to the language.

## What is not good yet

Instruction selection is naive. Across the millions of arm64 instructions
the compiler emits for its own sources, fused multiply-add appears zero
times, conditional select barely at all, and the great majority of the
available SIMD instructions are unreachable. Fern's advantage is at
startup and in output size, not in the quality of the inner loop, and a
comparison sort against GNU `sort` is where that shows most plainly.

The other live front is architectural. The type checker computes a type for
every expression, uses it, and throws it away, because AST nodes carry no
type field — so IR lowering re-derives all of it. That single decision is
why the lowering module is by far the largest file in the codebase, and
introducing a typed IR level is the work in progress that removes it.
