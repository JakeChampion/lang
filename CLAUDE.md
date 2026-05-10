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

- ARM32 (qemu-arm under test; real hardware: Raspberry Pi 2/3
  in 32-bit mode, embedded Linux)
- ARM64 / aarch64 Linux (qemu-aarch64 under test; real hardware:
  AWS Graviton, Raspberry Pi 4+ in 64-bit mode, Android,
  Apple Silicon Macs via Linux containers)
- ARM64 / aarch64 Darwin — Mach-O for native Apple Silicon Macs
  (`-target arm64-darwin`). No Linux container needed; clang +
  ld64 (native) or clang + lld (cross from Linux) link directly.
  Verified end-to-end on `macos-14` CI runner. **Known gap**:
  the Map runtime's `__store_i32` / `__load_i32` truncates heap
  pointers when macOS returns addresses > 4 GiB. Plain string
  concat / array push / TCP all work; Map and Map-dependent
  programs (HTTP routers, etc.) need the prelude widened to i64
  pointer storage. Excluded from the macos-14 matrix until
  fixed; tracked separately.
- WASI / WebAssembly (currently exercised via wasmtime)
- x86-64 is on the roadmap

The IR layer is target-agnostic; new optimisations should live in `internal/ir`
so all backends benefit.

## Working with PRs

When you open a PR, subscribe to its activity (`subscribe_pr_activity`)
without being asked. The user prefers to be alerted via the subscription
flow rather than driving manual CI checks after the fact.

## Engineering bar (non-negotiable)

- **Confirm passing tests before opening a PR.** Run the full relevant
  suite locally (including the WASM e2e tests — `wasmtime` lives at
  `/tmp/wt/wasmtime-v34.0.1-x86_64-linux/`, `wasm-tools` at
  `/tmp/wt/wasm-tools-1.225.0-x86_64-linux/`, adapter at
  `/tmp/wt/adapter.wasm`; export the binaries onto `PATH` and set
  `LANG_WASI_ADAPTER` so the e2e tests don't SKIP). If a test SKIPs,
  treat that as a missing dependency to fix, not a green light.
- **Every new feature ships with tests.** Parser-time desugar →
  parser test. Checker rule → checker test. Runtime behaviour →
  e2e test. No "the next PR will add coverage."
- **Never regress.** Re-run the full suite after every change, not
  just the targeted test for the new code.
- **Fix bugs you find on the way.** If exploring for one feature
  surfaces a separate bug (e.g. the f-string `__str_concat`
  helper-emission gap, the `for x in m { ... }` struct-lit clash),
  fix it in the same PR with its own test rather than leaving it
  for later.
