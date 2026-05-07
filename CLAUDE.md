# Project notes for Claude Code

## Language direction

This language started TypeScript-flavoured (the syntax for functions, `var`,
`if/else`, struct literals, etc. all came from TS) but **that's no longer the
target**. It's evolving into its own thing. When designing new features —
syntax, type system, error handling, stdlib shape — feel free to look beyond
TS for inspiration. The stated use cases are small fast-startup CLI tools and
short-lived edge-function-style HTTP servers, so cribbing from Roc, MoonBit,
Rust, Zig, Go is more productive than reaching for TS conventions when they
don't fit.

Concretely: don't justify a design choice with "this is how TS does it" if a
better shape exists. Treat the existing TS-shaped surface as historical, not
as a constraint to preserve.

## Targets

- ARM32 (qemu under test, real hardware as a follow-up)
- WASI / WebAssembly (currently exercised via wasmtime)
- x86-64 is on the roadmap

The IR layer is target-agnostic; new optimisations should live in `internal/ir`
so both backends benefit.

## Working with PRs

When you open a PR, subscribe to its activity (`subscribe_pr_activity`)
without being asked. The user prefers to be alerted via the subscription
flow rather than driving manual CI checks after the fact.
