---
title: Project status
description: What is solid, what is moving, what is measured, and what is not coming — the honest state of Fern.
---

Fern is pre-1.0. The rolling nightly is the release channel, there is no
compatibility promise, and syntax still changes. This page says where each
part actually is, so you can decide what to trust it with.

The short version: **the language and the native toolchain are usable
today; the memory work on the self-hosted compiler is the live front; and
threads and bare metal are not coming soon.**

## By area

| Area | State | Detail |
| --- | --- | --- |
| Language & type system | Solid | Generics, traits, `dyn`, associated types, sum types, exhaustive matching, ownership modes. Associated types are implemented in the Go compiler only so far. |
| Errors as values | Solid | `Option`, `Result`, `?`, `defer`/`errdefer`. Zero-allocation happy path — a `Result` return is a register pair. |
| arm64 targets | Solid | Linux, macOS and Android. The oldest and best-covered backend. Assembles and links in-process. |
| x86-64 Linux | Solid | Newer than arm64. A handful of representation gaps remain, listed on [Targets](../compiler/targets/). |
| WebAssembly | Solid | WASI Preview 2 components composed natively. Coverage and the sanitizer do not run here, and heap exhaustion traps rather than exiting. |
| Standard library | Solid | 77 modules, about 42,000 lines of Fern. See the [stdlib reference](../stdlib/). |
| Toolchain | Solid | Formatter, linter, coverage, sanitizer, LSP, doc generator, package manager, literate programming. |
| Reference counting | Moving | Complete in the Go compiler. Porting it to the self-hosted compiler is the current priority. |
| Self-hosting | Moving | The compiler compiles itself and bootstraps with no Go present. One reproducibility gate is red — see below. |
| Package capabilities | Moving | Grants are enforced with the offending call chain in the error, but a dependency that declares nothing is still allowed with a warning. |
| Async I/O | Partial | Real overlapping socket I/O on the native targets. On WebAssembly and in the interpreter, fd-backed futures never resolve. |
| Threads, parallelism | Not planned | Reference counts are non-atomic by design. |
| Bare metal | Not started | The freestanding targets type-check; no backend emits for them. |
| Windows, Intel macOS, 32-bit | Not planned | — |

## What is measured rather than asserted

Three things about this project are worth knowing because they change how
much a claim on this site is worth.

**Leaks are a pinned list, not a disclaimer.** Every conformance fixture is
run with allocation tracing and its unpaired-allocation count recorded in a
file under version control. As of 10 September 2026 that file records **67
of 503 fixtures leaking, 7,437 unpaired allocations in total**, each pinned
at its exact count so a regression is a diff rather than a judgement call.
The equivalent matrix for the self-hosted compiler is currently clean on
every row. The file is
[`internal/e2e/testdata/conformance-leak-census.txt`](https://github.com/JakeChampion/lang/blob/main/internal/e2e/testdata/conformance-leak-census.txt),
and it is the source of truth — prefer it to this paragraph.

**Behaviour is defined by a corpus, not by whichever implementation ran
last.** The 578 cases in
[`conformance/`](https://github.com/JakeChampion/lang/tree/main/conformance)
define observable Fern behaviour by example, and every implementation is
measured against the same corpus. Changing what a case expects is a change
to the language and is reviewed as one.

**The reproducibility gate that is red is red in public.** `make distcheck`
requires stage 2 of the bootstrap to be byte-identical to stage 1, and it
does not pass: the self-hosted compiler frees less than the Go one, so the
second stage exhausts its heap. [Self-hosting and
bootstrap](../compiler/bootstrap/) has the measurements.

## Benchmarks, including the losses

Startup-bound programs are where Fern's static, loader-free binaries win:
against GNU coreutils on arm64 Linux, `true` runs 0.15 ms against 0.19 ms
and `echo hello world` 0.15 ms against 0.20 ms, and the margin widens on
macOS where the dynamic loader costs more.

Compute-bound programs are where naive instruction selection shows: `sort`
on 200,000 lines takes about 130 ms against GNU's 22 ms, and a numeric sort
is worse. The project's own coreutils document says outright that some of
its utilities do not meet the bar it set.

Both sets are re-runnable with `scripts/coreutils-bench` and written up in
`docs/COREUTILS.md`.

## What "pre-1.0" means in practice

- **Nightly is the channel.** A build is published from `main` daily rather
  than on every push, with prebuilt binaries for x86-64 Linux, arm64 Linux
  and arm64 macOS. See [Releases](../releases/).
- **There is no deprecation window.** When a construct is replaced, the old
  one is deleted rather than kept working. That is a deliberate policy, not
  an oversight — the project treats leaving both paths alive as the more
  expensive mistake.
- **Pin what you depend on.** `fern -resolve` writes a lockfile and
  `fern -vendor` makes a build fully offline; a `url` dependency is
  identified by its content hash, so a mirror moving cannot change what you
  build.

## Where the work is going

The roadmap has one live item: bringing the self-hosted compiler's memory
management to parity with the Go compiler's — reference-count insertion,
borrow inference, drop specialisation and reuse analysis. Reuse is
substantially done; reclamation is where the remaining work is, and it is
what the red `distcheck` gate is measuring.

Behind that sits the convergence plan. Once parity lands, the Go
implementation stops being the product and becomes the bootstrap stage 0
plus a differential oracle.
