# Project notes for Claude Code

## Name

The language is named **Fern**. Source files use the `.fern`
extension; the CLI is `fern` (built from `cmd/fern`), the LSP is
`fern-lsp` (`cmd/fern-lsp`), the wasm playground bundle is
`cmd/fern-wasm`, and the stdlib doc generator is `cmd/ferndoc`.
The rename also covers: internal packages `internal/fernsmith` /
`internal/fernstring`, the emitted runtime symbols `__fern_*`,
the `FERN_WASI_ADAPTER` build/test env var, the wasm JS-interop
globals (`fernCompile` / `fernInterpret` / `fernLsp`) and their
`"fern:theme"` postMessage protocol, and the WIT `fern` world
(`cmd/fern/wit/fern.wit`, `componenttype` `fern.bin`).

The **only** things still on the old `lang` name — deferred
because they cross the GitHub-repo boundary — are the Go module
path `github.com/jakechampion/lang` and the GitHub repo + Pages
URLs (`jakechampion.github.io/lang`). Both should follow a rename
of the GitHub repo itself, otherwise `go install` and the Pages
site break.

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

- ARM64 / aarch64 Linux (the **default** target; qemu-aarch64
  under test; real hardware: AWS Graviton, Raspberry Pi 4+ in
  64-bit mode, Android, Apple Silicon Macs via Linux containers)
- ARM64 / aarch64 Darwin — Mach-O for native Apple Silicon Macs
  (`-target arm64-darwin`). No Linux container needed; clang +
  ld64 (native) or clang + lld (cross from Linux) link directly.
  Verified end-to-end on the `macos-latest` CI runner (Apple
  Silicon arm64; tracks Apple's current macOS release). Policy:
  only the latest macOS is supported — see
  `docs/BACKEND-PARITY.md` for the version-support stance and
  known limitations. All pointer-
  shaped values (string / array / struct / enum / slice /
  tuple) round-trip through 8-byte slots on arm64-darwin's
  high heap. The IR has a `WidthPtr` sentinel on
  `Op.Width` that each backend resolves to its heap-pointer
  width (4 on wasm32, 8 on arm64); `ast.IsPointerType` is
  the type-side classifier that drives stride / offset /
  store-width selection in `payloadSlotSize`,
  `structFieldLayout`, `tupleElemLayout`, `arrayElemStoreOp`,
  and `ast.ElemSizeBytesFor`. Map operations
  (`set`/`get_or`/`has`/`delete`/`iter`/`len`/`keys`/`values`)
  cover all combinations of i32 / string K/V. Closures with
  captures are lowered on every backend
  (`OpMakeClosure` / `OpMakeEnv` / `OpCallClosureDirect` —
  see `arm64.go:emitMakeClosureOrEnv` and the x86-64 mirror);
  the `ptrW`-aware capture layout from `closureconv` lines
  up with the load side (`payloadLoadOp` on CaptureRef emits
  `WidthPtr`). Test coverage: 8 `TestArm64Closure*` cases,
  matching counts on x86-64 + wasm.
- WASI / WebAssembly (currently exercised via wasmtime)
- x86-64 / amd64 Linux ELF — System V AMD64 ABI, native
  exec on x86_64 hosts + `qemu-x86_64` on non-x86 hosts.
  Six PR shape (#269–#274) covers everything from exit
  codes through arithmetic, control flow, strings + alloc,
  composite types + floats, TCP + HTTP, and the
  `ir.TailCallOptimize` pass (which is now also wired in
  arm64 + wasm — same one-line backport, every backend
  gets O(1) stack depth on self-tail recursion). End-to-
  end parity with arm64 for the edge-handler use case
  (`function handle(req): resp` → serving HTTP/1.1). No
  Darwin x86-64; Apple Silicon is the active macOS path.

The IR layer is target-agnostic; new optimisations should live in `internal/ir`
so all backends benefit.

**ARM32 was retired.** The codebase shipped an arm32 (Linux ELF)
backend through early 2026 — it was the original target and the
default — but parity work between backends became untenable and
the arm32 hardware story (Raspberry Pi 2/3 embedded) was poorly
matched to the language's stated edge-function focus. The backend,
its e2e tests, and the cross-compiler / qemu wiring were all
removed. **Do not add arm32-specific code back.** If a comment in
the codebase still says "on arm32" or "same as arm32", treat it
as a TODO to clean up.

## Working with PRs

**Always open a PR for completed work — no exceptions, never ask
first.** Once a change is committed and pushed to its feature
branch, open a PR for it immediately. This includes small
follow-ups, doc-only fixes, and comment corrections — every push
of completed work gets a PR. Do NOT pause to ask "want me to open
a PR?" or "should I open a PR for this?"; opening it IS the
expected action, so just do it. The default flow is always:
branch → commit → push → PR → subscribe. Don't stop at "pushed to
the branch" — finish the loop every time.

When you open a PR, subscribe to its activity (`subscribe_pr_activity`)
without being asked. The user prefers to be alerted via the subscription
flow rather than driving manual CI checks after the fact.

## Engineering bar (non-negotiable)

- **Confirm passing tests before opening a PR.** Run the full relevant
  suite locally (including the WASM e2e tests — `wasmtime` lives at
  `/tmp/wt/wasmtime-v34.0.1-x86_64-linux/`, `wasm-tools` at
  `/tmp/wt/wasm-tools-1.225.0-x86_64-linux/`, adapter at
  `/tmp/wt/adapter.wasm`; export the binaries onto `PATH` and set
  `FERN_WASI_ADAPTER` so the e2e tests don't SKIP). If a test SKIPs,
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

## Test runner

`internal/stdlib/std/test.fern` is the pure-Fern test runner —
the shape the project plans to migrate to once the compiler is
self-hosted and Go-side `*_test.go` files retire. With the
auto-prelude gone (Phase 5), test programs `import "std/test";`
and call its functions qualified (`test.test_new`,
`test.assert_eq_i32`, `test.fail`, …) with the runner type
written `test.TestRunner`; receiver methods (`.it`, `.finish`)
stay bare. Output is
TAP-13. Examples under `examples/tests/` (`arithmetic_test.fern`,
`strings_test.fern`, `runner_self_test.fern`) — the self-test
walks every assertion helper on both pass + fail paths. The
Go-side gate that keeps the runner from regressing lives at
`internal/e2e/test_runner_test.go`.

When adding NEW assertion helpers or extending the runner,
add a corresponding case to `runner_self_test.fern` covering
both the passing and failing path — the failure-reporting
contract (predicate name in the message, actual + expected
both quoted) is the runner's most regression-prone surface.

Module loading: there is no prelude injector anymore — a program
sees only what it `import`s. `modload` loads each imported
`std/`/`core/` module (and its transitive imports), mangles
non-entry decls to `<mod>__name`, and rewrites qualified call
sites; `ast.Program.LoadedStdlibPaths` records what was loaded so
a module pulled in twice (directly + transitively) dedupes rather
than redeclaring methods. That dedupe also closed an older bug
where an explicit `import "std/foo";` of a module that
transitively imports another (e.g. `std/json` → `core/int`) sent
bare-name method dispatch (`(n).to_string()`) through the mangled
`int__int_to_string` name and crashed the interpreter with "cast
from interp.Array to i32 not supported". It's fixed and guarded by
`TestInterpScriptInteropIntToStringViaMangling`
(`internal/e2e/interp_script_test.go`), which exercises the
explicit-import, transitive-import, and qualified-call shapes —
extend it if you touch the mangling / alias path.

In-memory source (stdin, REPL, the wasm playground bundle) loads
through `modload.LoadSource`, not bare `parser.Parse`, so those
paths resolve stdlib imports the same way the file-based driver
does.
