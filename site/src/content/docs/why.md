---
title: Why Fern
description: What Fern is good at, what it gives up, and how it compares to the languages you'd otherwise reach for.
---

Fern is a statically typed, general-purpose language that compiles to a
standalone binary — or to WebAssembly, from the same source. It is a good
fit when you want a program that starts instantly, stays small and depends
on nothing at all. It is a bad fit when you need threads, Windows, or a
library somebody else has already written.

The rest of this page is the long version, including the parts that don't
flatter it.

## Nothing between your code and the machine

There is no runtime to boot, no interpreter, no JIT, and no garbage
collector. A Fern binary is your program, statically linked, plus the parts
of the standard library it actually calls — so there is no fixed floor to
pay off before your own code earns its size:

| Language | `hello, world` | Needs libc at runtime |
| -------- | -------------- | --------------------- |
| Fern     | 4.3 kB         | no                    |
| C, dynamic | 16 kB        | yes                   |
| Rust     | 350 kB         | yes                   |
| C, static | 785 kB         | no                    |
| Go       | 1.6 MB         | no                    |

The measure that matters more is what runs before your code does. Counting
retired instructions between `_start` and `exit` for an empty `main`, Fern
executes **7**. Statically linked C executes 228,519; Go, 291,170; Rust,
375,878. There is no dynamic loader, no libc initialiser, no runtime and no
collector to bring up — so cold start is `exec` and then you.

A `grep`-style line filter — argument handling, stdin, string search, exit
codes — comes to 23 kB.

<small>Binary sizes measured on x86-64 Linux, 10 September 2026:
`fern -O -target x86-64-linux`, `gcc -O2` (and `-static`), `rustc -O -C
strip=symbols`, `go build -ldflags="-s -w"`. Instruction counts are from
`docs/CODEGEN-QUALITY-AUDIT-2026-09.md`, measured under callgrind. Re-run
them yourself — the order of magnitude is the point, not the digits.</small>

## Memory that is freed, not collected

Memory is reference counted. The compiler inserts the counting, then
removes most of it again at compile time; what is left frees a value at the
point its last use goes out of scope. There is no heap to size, no pause to
plan around and no lifetime annotation to satisfy.

The usual objection to reference counting is cycles, and Fern answers it by
making them unconstructible rather than by shipping a collector to clean up
after them. That has a cost — back-pointers and doubly linked lists need a
different shape — and [Memory](../reference/memory/) is the full account.

## One toolchain, no build system

`fern` is a single binary that assembles **and links** in-process, so
producing an executable needs no `gcc`, no `clang`, no `ld` and no
`wasm-tools`. There is no build file to write and no plugin to configure:
`fern -o app app.fern` is the whole build.

The same binary type-checks (`-check`), formats (`-fmt`), lints
(`-lint`), interprets (`-interp`), gives you a REPL (`-repl`), measures
coverage (`-cover`), hunts memory bugs (`-sanitize`), embeds assets
(`-embed`), resolves and vendors dependencies, explains any diagnostic by
code (`-explain`), and tangles literate Markdown into source (`-tangle`).
Editor support is `fern-lsp` plus a VS Code extension; the test runner is
`std/test`, a library rather than a separate tool.

## The same program, native or WebAssembly

`-target wasm32-wasi` emits a self-contained WASI Preview 2 component
and `-target wasm32-wasi-http` an HTTP handler for `wasmtime serve` —
from the source that also builds a native binary. The component is
composed inside the compiler, so there is no `wasm-tools` step, no
JavaScript shim, and no second implementation to keep in sync.

## Types that don't lie, errors that are values

Integers carry their width and never convert silently. Enums are tagged
unions and `match` must cover every variant — miss one and the build
stops. Generics are monomorphised, so the abstraction costs nothing at
runtime; traits give you shared behaviour, and `dyn Trait` gives you
runtime dispatch when you ask for it by name.

Failure is an ordinary value: `Option` and `Result` are part of the
language, `?` passes a failure up the call stack, and `let … else`
handles the unhappy path first. There are no exceptions, so there is no
invisible second control flow to reason about.

## Packages declare what they can touch

A package's manifest grants it capabilities — `net`, `fs`, `env`,
`subprocess`, `time`, `random` — and the compiler rejects a build where a
package reaches past its grant, with the offending call chain in the error.
`fern -capabilities` prints what each package in a program can reach. A
logging library that suddenly wants the network fails the build rather than
the audit.

One caveat while this is still being finished: a dependency that declares
*no* capabilities is currently allowed with a warning rather than denied.
Treat it as a strong signal, not yet as a sandbox.

## The compiler is written in Fern

222,000 lines of it — parser, checker, optimiser, all three backends, the
assemblers and the linkers. It compiles itself, and `make bootstrap` builds
it from a pinned earlier binary on a machine with no Go toolchain present.

That matters beyond the novelty: it is the proof that Fern is not only for
short-lived programs. A compiler is a long-running, allocation-heavy
workload, and making it work is what drove the language's memory model.
[Self-hosting and bootstrap](../compiler/bootstrap/) has the detail,
including the one reproducibility gate that is still red.

## What it costs

- **Pre-1.0.** Nightly builds are the release channel. Syntax still
  changes under you, and there is no compatibility promise yet.
- **The ecosystem is small.** The standard library is not: 77 modules
  covering strings and Unicode, regex and PEG parsing, JSON and CSV,
  crypto hashes, HTTP and TCP, time, arbitrary-precision integers, and
  persistent maps, sets and vectors. But beyond it there is very little,
  and no registry.
- **Single-threaded.** Concurrency is I/O-driven futures (`std/async`) —
  `gather`, `race`, and friends over one poll loop. There are no threads
  and no parallelism; refcounts are non-atomic by design.
- **Inner loops are not optimised hard.** Instruction selection is naive:
  Fern's advantage is startup and size, not throughput. A comparison sort
  against GNU `sort` is roughly six times slower.
- **No cycles, by construction.** See [Memory](../reference/memory/) —
  cheap for most code, a rewrite for anything shaped like a graph.
- **A narrow platform set.** Linux on arm64 and x86-64, macOS on Apple
  Silicon, Android on arm64, and WebAssembly. No Windows, no Intel Mac, no
  32-bit.
- **C interop is native-only.** The wasm target rejects it at build
  time rather than failing at runtime, but it is still a gap.

## How it compares

**Go** is the closest neighbour, and for most production work today it
is the better answer: a mature ecosystem, goroutines, and a garbage
collector good enough that you rarely think about it. Fern trades all of
that for output measured in kilobytes, no collector at all, and
WebAssembly as a first-class target rather than a port.

**Rust** shares the shape of the type system — exhaustive matching,
errors as values, no null. It buys memory safety with a borrow checker
you have to satisfy. Fern buys it with reference counting: much less to
learn, and much less control when you need it. Reach for Rust when the
performance ceiling or the safety guarantee is the requirement.

**Zig** is the other small-and-toolchain-light option. Zig hands you
manual allocation and `comptime`; Fern hands you automatic memory and a
stricter, more opinionated type system. Zig's cross-compilation story is
a superset of Fern's.

**Koka and Roc** are where Fern's memory model comes from — Perceus
reference counting, in-place reuse, and functions that promise not to
allocate. Fern is the less academic member of that family: imperative
syntax, four production targets, and its own assembler.

**TypeScript** is where a lot of Fern's syntax originally came from, so it
reads familiar — but there is no VM to start, no `node_modules`, and the
types are compiled rather than erased. If you are writing a CLI or an edge
handler in TypeScript today and cold start is what hurts, that is the swap
Fern is built for.

## When not to use Fern

You need Windows. You need threads or parallelism. You need a library that
already exists somewhere else. You need throughput in a tight numeric loop.
You need a version you can pin and trust for a year. Any of those, and one
of the languages above is the honest recommendation.

## When it fits

Command-line tools, edge and serverless handlers, small HTTP services,
build-time utilities — anything where you want one small artifact that
starts instantly and depends on nothing. But the boundary has moved: the
compiler is written in Fern, so long-running, allocation-heavy programs are
in scope too, and 57 GNU coreutils are reimplemented in it at byte-for-byte
parity.

Start with the [tutorial](../tutorial/install/), or read the current
[project status](../status/) first.
