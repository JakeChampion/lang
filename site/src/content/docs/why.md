---
title: Why Fern
description: What Fern is good at, what it gives up, and how it compares to the languages you'd otherwise reach for.
---

Fern is a small statically typed language that compiles to a standalone
binary — or to WebAssembly, from the same source. It is a good fit when
you want a program to start fast, stay small, and depend on nothing;
it is a bad fit when you need threads, Windows, or a large ecosystem.

The rest of this page is the long version, including the parts that
don't flatter it.

## Nothing between your code and the machine

There is no runtime to boot, no interpreter, no JIT, and no garbage
collector. A Fern binary is your program, statically linked, plus the
parts of the standard library it actually calls — so there is no fixed
floor to pay off before your own code earns its size:

| Language | `hello, world` | Statically linked |
| -------- | -------------- | ----------------- |
| Fern     | 4.3 kB         | yes               |
| Go       | 1.4 MB         | yes               |
| Rust     | 350 kB         | no — needs libc   |

A `grep`-style line filter — argv handling, stdin, string search, exit
codes — comes to 16 kB. Startup is the kernel's `exec` and then your
`main`; nothing initialises first, and nothing pauses you later.

Memory is reference counted, freed at the point the last use goes out of
scope rather than at a collector's convenience. There is no tuning, no
heap sizing, and no pause to plan around.

<small>Measured on x86-64 Linux, August 2026: `fern -target x86-64 -o
hello hello.fern`, `go build -ldflags="-s -w"`, `rustc -O -C
strip=symbols`. Re-run them yourself — the order of magnitude is the
point, not the digits.</small>

## One toolchain, no build system

`fern` is a single binary. It assembles and links natively in-process,
so producing an executable needs no `gcc`, no `clang`, no `ld`. There is
no build file to write and no plugin to configure: `fern -o app
app.fern` is the whole build.

The same binary type-checks (`-check`), formats (`-fmt`), runs a program
without compiling it (`-interp`), resolves dependencies, and reports what
capabilities a package uses. Editor support is `fern-lsp` plus a VS Code
extension; the test runner is `std/test`, a library rather than a
separate tool.

## The same program, native or WebAssembly

`-target wasm` emits a self-contained WASI component and `-target
wasi-http` an HTTP handler for `wasmtime serve` — from the source that
also builds a native binary. No JavaScript shim, no adapter step, no
second implementation to keep in sync.

## Types that don't lie, errors that are values

Integers carry their width and never convert silently. Enums are tagged
unions and `match` must cover every variant — miss one and the build
stops. Generics are monomorphised, so the abstraction costs nothing at
runtime.

Failure is an ordinary value: `Option` and `Result` are part of the
language, `?` passes a failure up the call stack, and `let … else`
handles the unhappy path first. There are no exceptions, so there is no
invisible second control flow to reason about.

## Packages declare what they can touch

A package's manifest grants it capabilities — `net`, `fs`, `env`,
`subprocess`, `time`, `random` — and the compiler rejects a build where a
package reaches past its grant. `fern -capabilities` prints what each
package in a program can reach, with an example call chain. A logging
library that suddenly wants the network fails the build rather than the
audit.

## What it costs

- **Pre-1.0.** Nightly builds are the release channel. Syntax still
  changes under you, and there is no compatibility promise yet.
- **The ecosystem is small.** The standard library covers strings,
  collections, iterators, JSON, I/O, HTTP, time and math. Beyond that you
  will be writing it yourself.
- **Single-threaded.** Concurrency is I/O-driven futures (`std/async`) —
  `gather`, `race`, and friends over one poll loop. There are no threads
  and no parallelism; refcounts are non-atomic by design.
- **No cycles, by construction.** Reference counting cannot collect a
  cycle, so Fern makes cycles unconstructible: the checker rejects the
  struct-field assignment that would close one. Back-pointers, doubly
  linked lists and observer graphs need a different shape — usually an
  index into an array.
- **A narrow platform set.** Linux on arm64 and x86-64, macOS on Apple
  Silicon, and WebAssembly. No Windows, no Intel Mac, no 32-bit.
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

**TypeScript** is where a lot of Fern's syntax came from, so it reads
familiar — but there is no VM to start, no `node_modules`, and the types
are compiled rather than erased. If you are writing a CLI or an edge
handler in TypeScript today and cold start is what hurts, that is the
swap Fern is built for.

## When not to use Fern

You need Windows. You need threads or parallelism. You need a library
that already exists somewhere else. You need a version you can pin and
trust for a year. Any of those, and one of the languages above is the
honest recommendation.

## When it fits

Command-line tools, edge and serverless handlers, small HTTP services,
build-time utilities — anything where you want one small artifact that
starts instantly and depends on nothing. The compiler is written in
Fern, so long-running, allocation-heavy programs work too.

Start with the [tutorial](../tutorial/install/), or read a program on the
[overview](../) first.
