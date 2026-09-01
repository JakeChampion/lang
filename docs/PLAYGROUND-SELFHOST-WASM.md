# Can the self-host compiler power the browser playground?

Measured 2026-09-01, on x86-64 Linux (4 cores, 15 GiB), wasmtime 46.0.1.
Answers `NATIVE-CONVERGENCE.md §3a` precondition 4 (#6643): the playground is
built on native codegen, and retiring the native backends needs it rebuilt on
the self-host compiler instead.

## Headline

**The precondition is not bounded by module size or by memory.** Both come in
an order of magnitude *better* than the bundle shipping today. It is bounded by
a set of missing wasmbin builtins and the shape of the compiler's entry point,
both named below and neither open-ended. The correctness bug that used to head
this list (#7948) is fixed, and so is the strbuf third of the builtin gap. Of
what the builtin gap turned out to be, only `sleep_ms` is still a missing
lowering: `timer_fd`, `write_file_exec` and the `__c_call*` family are things
wasm cannot express, and are now refused by E066 at check time rather than by
the emitter mid-build.

The self-host compiler compiled to wasm **already runs in wasm today**. A
stdin-driven wasm-emitting driver, compiled to a core module by the self-host
compiler itself, reads a Fern program on stdin under wasmtime and emits a
module that runs and returns the right answer:

```
$ echo 'function main(): i32 { return 6 * 7; }' | wasmtime run wasm_ir_run.wasm > out.wat
$ wasm-tools parse out.wat -o out.wasm && wasmtime run out.wasm; echo $?
42
```

2.34 MB of module, 1.9 s, 104 MiB peak. The playground's current bundle is
28.5 MB.

## What the playground actually is

`web/fern.wasm` is **not** the compiler — it is `cmd/fern-wasm` (490 lines,
`//go:build js && wasm`), the whole Go toolchain compiled by *Go* with
`GOOS=js GOARCH=wasm`, exposing six functions to the page (`main.go:370-467`):
`fernInterpret`, `fernLsp`, `fernCompile` (native asm text),
`fernCompileComponent`, `fernCompileCoreWasm`, `fernCompileHttpHandlerCore`.
`internal/wasm/playground` (563 lines over 4 files) is the library half and
depends on `internal/codegen/wasmbin` only; `cmd/fern-wasm` pulls in
`codegen/arm64` and `codegen/x86_64` on top for the assembly pane, plus
`internal/interp` and `internal/lsp`.

So a self-host replacement is not one artifact. It is *at least* compile,
interpret and check; the LSP is a fourth consumer and the furthest from having
a self-host counterpart.

Two facts worth having before costing anything:

- **Nothing gates the playground's size or memory.** No `-ldflags` beyond
  `-s -w`, no size assertion, no heap or arena limit, no CI check. The only
  recorded number in the tree is a prose estimate in `web/.gitignore:1-5`,
  which said "~5 MB per version of the toolchain" and was stale by 5.7x until
  this measurement corrected it. `scripts/ci-check-driver-sizes` sounds like the
  gate and is not: it covers self-host *driver* binaries, not this.
- **Nothing builds `cmd/fern-wasm` under CI's normal test lanes.** No `go vet`
  or `go build` under `GOOS=js` anywhere; it is compile-checked only by the
  three path-filtered workflows that publish the site (`pages.yml`,
  `docs-build.yml`, `playground-e2e.yml`).

## The measurement

Sizes, gzip at -9 because that is what a static host serves:

| artifact | built by | raw | gzipped |
|---|---|---|---|
| `web/fern.wasm` — the playground today | Go, `GOOS=js GOARCH=wasm`, `-s -w` | **28,521,433** | **6,334,648** |
| `wasm_ir_run` — source → wasm core module | self-host, `-target wasm32-wasi -emit core-module` | **2,344,167** | **613,735** |
| the whole 19-module compiler, sharded-linked | self-host, per-function-window emit | **2,258,458** | — |
| `interp_run` — source → interpreted result | same | 787,729 | 221,321 |
| `checker_run` — type-check only | same | 945,931 | 269,807 |
| `asm_run` — source → x86-64 asm text | same | 2,100,728 | 570,241 |

The self-host wasm compiler is **12x smaller raw and 10x smaller gzipped** than
the bundle it would replace. The comparison is not quite fair — `fern.wasm`
carries compile, interpret, check and the LSP in one module where each row
above is one driver — so take the sum of all four as a deliberate
over-estimate: **6,178,555 raw / 1,675,104 gzipped**, still 4.6x and 3.8x
under the single bundle shipping today. It is an over-estimate because the
drivers overlap heavily (each carries lexer, parser and IR lowering), so a
merged bundle lands well below their sum, not above it.

Run cost, compiling `function main(): i32 { return 6 * 7; }` under wasmtime:

| | wall | peak RSS |
|---|---|---|
| `wasm_ir_run.wasm` under wasmtime | 1.9 s | 104 MiB |

104 MiB is not a browser problem. For scale, the guest modules the playground
compiles declare `MemoryMax = -1` — unbounded growth — with the initial size
derived from the static data (`memoryMinPages`, `memlayout.go`), and both JS
shims are already written to survive linear memory detaching under them
(`web/wasi-shim.js:38-41`, `web/wasi-http-shim.js:21-24`).

The whole 19-module compiler also links and runs as one wasm module today —
`TestSelfHostWasmWholeCompilerShardedLink`, measured here at 241 s over 41
units, validated, then run to compile a 2-module program. It comes to
**2,258,458 bytes** as a core module, which is the number to quote: the same
module is 15,679,514 bytes as WAT text, so the text length that test used to
report on its own overstates the artifact by 6.9x. Even the entire compiler is
12.6x under the playground bundle.

That path is sharded per 150-function window across processes because the
*host-side* emit peaks ~10 GB, not because the result is too big. Nothing about
the shipped artifact needs sharding.

### Reproducing

```
make build && make selfhost-cli
./bin/fern-selfhost -target wasm32-wasi -emit core-module \
    -o wasm_ir_run.wasm examples/self_host/wasm_ir_run.fern internal/stdlib
echo 'function main(): i32 { return 6 * 7; }' | wasmtime run wasm_ir_run.wasm
```

The trailing `internal/stdlib` is the stdlib root: the self-host driver takes it
as a second positional argument, where native serves stdlib from `go:embed`
(`internal/stdlib/stdlib.go:37`). Building that driver to wasm costs 82.7 s and
3.19 GB peak on the host — a build-time cost, not a ship-time one.

## The two real blockers

The blocker this measurement led with — **the wasm-hosted compiler
miscompiling nested arithmetic**, #7948 — is gone, so it is not one of the two
and is recorded here only because everything below was written around it: the
same `wasm_ir_run.fern`, compiled by the same self-host CLI with
only `-target` differing, refused as `unknown expression` a program the native
build compiled fine. The cause was a use-after-free in the
compiler's own gate passes, not a lowering divergence: a struct-ARRAY snapshot
parameter was routed through `__field_reclaim_<ElementType>`, a helper written
against a struct BOX, so its field offsets walked off the end of a short buffer
and released what followed. Both backends emitted the call; only wasm's reclaim
body walks enough field slots to dereference garbage. Root cause, the
measurement that found it, and the dead ends worth not repeating:
`rc-log/2026-09-01-field-reclaim-on-struct-array-param.md`.

The coverage gap it exposed is closed too, and it is the part that generalises.
The one test that ran a wasm-hosted self-host compiler (`runShardedCompiler`,
`self_host_wasm_wholecompiler_test.go:233`) compiled a two-module program whose
whole body is `leaf.leaf_tag().len() + 7` — no nested arithmetic, so the bug was
invisible to it. `TestSelfHostWasmHostedCompilerMatchesNativeOnNestedArith` now
runs the wasm-hosted driver against the native-hosted one on nested arithmetic
and asserts identical exit code AND identical WAT: a wasm-hosted compiler is
only interesting if it agrees with the native one, and until then nothing asked.

### 1. The native toolchain cannot compile `fern.fern` for wasm (#7947)

```
$ ./bin/fern -target wasm32-wasi -emit core-module -o out.wasm examples/self_host/fern.fern
error[E066]: target "wasm32-wasi" does not provide `fsmode`, required by
`write_file_exec` (reached via "write_output" from module ".../fern.fern");
targets providing it: [arm64-android arm64-darwin arm64-linux x86-64-linux]
```

The `strbuf_*` third of this is **fixed**: `strbuf_reset` / `strbuf_append` /
`strbuf_take` are core builtins (`internal/platforms/enforce.go:151-153`), so
E066 never refuses them and they reach codegen on every target, and wasmbin was
the one backend with no lowering for them at all. It has one now — a growable
heap buffer over three scratch words, rather than the natives' fixed 64 MiB
`.bss` reservation, which in linear memory would push `stringStart` and the
whole heap up by that much.

Three of the rest were never lowerings wasmbin was missing — they are things
wasm cannot express, and they now say so at check time. `timer_fd` is gated on
`pollfd` (wasm has no file descriptors to poll; `wasm_timer_pollable` is the
analog), `write_file_exec` on `fsmode` (`path_open` has no mode argument and
the component-model filesystem has no permission bits, #6133), and the
`__c_call0..4` family on `cabi` (no C ABI on any wasm path) — the same
refusals the self-host has made since #4317/#4375, moved into
`internal/platforms` where a target property belongs.

`sleep_ms` is the one genuinely missing lowering left. It stays gated on `now`,
which wasm grants, because wasm CAN block: subscribe-duration on
`wasi:clocks/monotonic-clock` plus a wait is the sleep. It is listed alone in
`providedMissingLowering`.

The consequence for this precondition is unchanged while any of them is open:
the playground's wasm artifact can only be produced *by the self-host compiler
compiling itself*, never by the native toolchain — so there is no independent
second witness to cross-check a wasm miscompile against, which is why #7948 had
to be diagnosed by diffing the two TARGETS of one compiler instead.

The IR driver is the exception, and it is a usable partial witness: the native
toolchain compiles `examples/self_host/wasm_ir_run.fern` to a core module that
now instantiates and compiles a program handed to it on stdin. It could always
be *built*; it could not be *started* until the emitted memory was sized from
the static data (the literals of a whole compiler run well past 64 KiB, and data
segments are written at instantiation). Invoke it as `wasmtime run --invoke main`
— a wasmbin core module carries no `_start`, so a plain `wasmtime run` exits 0
without calling anything.

The gate that should have caught the whole class exists now.
`TestProvidedSigsAgreeWithWasmRuntime`
(`internal/codegen/wasmbin/verifier_sigs_test.go:21`) looked like it and was
not: it checks arity only for helpers wasmbin already has, and `continue`s past
names it does not know, so a builtin with no lowering at all passed it silently.
`TestEveryProvidedCalleeHasAWasmLowering` asserts every callee in `providedSigs`
has somewhere to land, and its known-missing list — `sleep_ms` alone now — is
exact in both directions, so a fix cannot leave the table stale. Its sibling
`TestPlatformExemptionsAreReallyRefused` re-derives the refused list from
`internal/platforms` rather than trusting it, so moving a name there has to be
a real refusal.

The hole runs the other way too: `udp_send` is implemented only in `wasmbin`
(`wasi_udp.go`) and is missing from `internal/codegen/arm64` and
`internal/codegen/x86_64`, though `hosted-native` grants `tcp`, which gates it.

### 2. The CLI driver is the wrong entry point

Both compilers refuse `fern.fern` for wasm on `write_file_exec` — the
self-host through its own `capability_violations` gate, native through
`internal/platforms`, and now with the same E066 text on both sides.

`examples/self_host/fern.fern` is a *CLI*: it takes argv paths, reads files,
and writes executables. None of that is what the playground wants, and
`write_file_exec` has no WASI preview-1 form (`path_open` has no mode —
`wasm_ir.fern:1526`, #6133).

This is not a blocker so much as a signpost: the playground needs a driver
shaped like `wasm_ir_run.fern` (stdin in, module out) — which is exactly what
was measured above — not the CLI. A playground driver would additionally need
in-memory stdlib resolution, since the self-host resolves `std/…` by host path
(`modloader.fern:87`) where native serves it from `go:embed`.

## Two corrections to how this was framed

**The `argv paths` skips are not evidence of a wasm problem.** #6643 cites the
`t.Skip("file-loading driver test runs only natively (argv paths)")` family
(25 sites, plus ~15 siblings) as the shape of the work. Every one of them is
guarded by `if len(runner) != 0`, where `runner` is empty on a native
`linux/amd64` host and `[]string{qemu-x86_64}` otherwise
(`internal/e2eharness/x86_64.go:39-58`). They fire under a **qemu
cross-runner**, never because of wasm, and none of them fires on an amd64 box.
The real filesystem constraint is blocker 2, and it is about the driver's
shape, not about these tests.

**`wasm-bin` is not a target.** It was replaced by an `-emit` axis:
`-target wasm32-wasi -emit core-module`. Pinned by
`cmd/fern/targetflags_test.go:161`. The self-host driver spells it the same way
since #6635.

## What it would actually take

In dependency order, and the estimate the issue asked for — **this is a
weekend, not a quarter**. The unknown that qualified that estimate was #7948's
size before it was root-caused; it is closed, and what remains is scoped:

1. **strbuf in `wasmbin`** (blocker 1, #7947). Contained: three scratch slots appended
   to the `memlayout.go` chain and three `runtimeHelperSpecs` entries keyed by
   the source names, the way `poll` and `isatty` already are
   (`runtime.go:1698,1713`). The self-host wasm implementation
   (`wasm_ir.fern:6120`) is the reference, with one adaptation — it uses
   one-word strings where wasmbin uses the two-word SSO ABI, so `strbuf_append`
   takes `(data, len)` and must read through the tag like `buildStrConcatBody`
   does. Add the missing completeness test while there.
2. **A playground driver** — `wasm_ir_run.fern`'s shape plus in-memory stdlib
   resolution and the interpret/check entry points, exporting to JS rather than
   reading stdin.
3. **The LSP** is the long pole and is not costed here; it has no self-host
   counterpart at all.

Precondition 3 of §3a — whether the native backends should be deleted at all,
given `BOOTSTRAP-RESEARCH.md §1` recommends two-implementations-forever so the
fuzz-diff oracle keeps two witnesses — is untouched by any of this and still
needs its own call. Blocker 1 sharpens it: today there is no second wasm
witness, because only one of the two compilers can emit this artifact.
