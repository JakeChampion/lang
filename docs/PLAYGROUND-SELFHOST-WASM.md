# Can the self-host compiler power the browser playground?

Measured 2026-09-01, on x86-64 Linux (4 cores, 15 GiB), wasmtime 46.0.1.
Answers `NATIVE-CONVERGENCE.md §3a` precondition 4 (#6643): the playground is
built on native codegen, and retiring the native backends needs it rebuilt on
the self-host compiler instead.

## Headline

**The precondition is not bounded by module size or by memory.** Both come in
an order of magnitude *better* than the bundle shipping today. It is bounded by
one correctness bug and two missing-builtin gaps, all three of them named below
and none of them open-ended.

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
  recorded number in the tree is a prose estimate in `web/.gitignore:1-5`
  ("~5 MB per version of the toolchain"), and it is stale by 5.7x — see below.
  `scripts/ci-check-driver-sizes` sounds like the gate and is not: it covers
  self-host *driver* binaries, not this.
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
compiles already declare `MemoryMin = 1, MemoryMax = -1`
(`internal/codegen/wasmbin/wasmbin.go:630-631`) — unbounded growth — and both
JS shims are already written to survive linear memory detaching under them
(`web/wasi-shim.js:38-41`, `web/wasi-http-shim.js:21-24`).

The whole 19-module compiler also links and runs as one wasm module today —
`TestSelfHostWasmWholeCompilerShardedLink`, measured here at 270 s producing
15,679,514 bytes of WAT, validated, then run to compile a 2-module program.
That path is sharded per 150-function window across processes because the
*host-side* emit peaks ~10 GB, not because the result is too big.

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

## The three real blockers

### 1. The wasm-hosted compiler miscompiles nested arithmetic (the blocker)

Same driver source, same input, same compiler, only the target differs — and
they disagree:

```fern
function f(a: i32): i32 { var x: i32 = a + a * a; var y: i32 = a + a * a; return x; }
function main(): i32 { return f(1); }
```

```
$ ./wasm_ir_run.native < repro.fern            # exit 0, valid WAT
$ wasmtime run --env FERN_STRICT_IR=1 wasm_ir_run.wasm < repro.fern
FERN_STRICT_IR: f (did not lower: unknown expression)   # exit 3
```

Both binaries are `examples/self_host/wasm_ir_run.fern` built by the same
`bin/fern-selfhost`; only `-target` differs. So the **self-host wasm backend
miscompiles the compiler itself**. Deterministic — 5/5 identical runs.

The trigger is two or more right-nested (depth >= 2) binary initializers with a
non-constant operand. Narrowed by bisection:

| program shape | native | wasm-hosted |
|---|---|---|
| `var x = a + a * a; var y = a + a * a; return x;` | ok | **bails** |
| `var v0 = 1 + a * 1; var v1 = 1 + a * 1; return v0 + v1;` | ok | **bails** |
| one such statement only — `var x = a + a * a; return x;` | ok | ok |
| one nested + one flat — `var x = 1 + a * 1; var y = 2;` | ok | ok |
| all-constant nesting — `var x = 1 + 2 * 3; var y = 1 + 2 * 3;` | ok | ok |
| left-nested via parens — `var x = (1 + a) * 1; var y = (1 + a) * 1;` | ok | ok |
| flat binaries — `var v0 = 0 + a; var v1 = 1 + a;` | ok | ok |

"unknown expression" is the compiler failing to recognise one of its own AST
expression nodes, so the discriminant it reads back is wrong. That the *second*
identical nested initializer is what trips it points at reuse or reference
counting rather than a missing case.

**Nothing gates this.** The one test that runs a wasm-hosted self-host compiler
(`runShardedCompiler`, `self_host_wasm_wholecompiler_test.go:233`) compiles a
two-module program whose whole body is `leaf.leaf_tag().len() + 7` — no nested
arithmetic, so the bug is invisible to it. That is the coverage gap this
measurement found, and it is the one worth closing first: a wasm-hosted
compiler is only interesting if it agrees with the native one, and today
nothing asks.

### 2. Native `wasmbin` cannot compile the self-host compiler at all (#7947)

```
$ ./bin/fern -target wasm32-wasi -emit core-module -o out.wasm examples/self_host/fern.fern
wasmbin: asm_arm64_ir__peephole_push_pop_arm64: op[0] call: call: unknown callee "strbuf_reset"
```

`strbuf_reset` / `strbuf_append` / `strbuf_take` appear nowhere in
`internal/codegen/wasmbin`. They are **core** builtins
(`internal/platforms/enforce.go:151-153`), so E066 never refuses them and they
reach codegen on every target. Every other native backend implements them —
`arm64.go:5552`, `x86_64.go:8637`, `arm64ssa/gas.go:5902` — and the self-host
wasm backend implements them too (`examples/self_host/wasm_ir.fern:6120`).
`wasmbin` is alone in not.

Same file, three more latent gaps reachable on `wasm32-wasi`: `sleep_ms` and
`timer_fd` (cap `now`, granted), `write_file_exec` (cap `fs`, granted), plus
the `__c_call0..4` FFI family, which the checker registers
(`checker.go:2225`) but `gatedBuiltins` does not cover.

The consequence for this precondition: the playground's wasm artifact can only
be produced *by the self-host compiler compiling itself*, never by the native
toolchain — which also means there is no independent second witness to
cross-check blocker 1 against.

`TestProvidedSigsAgreeWithWasmRuntime`
(`internal/codegen/wasmbin/verifier_sigs_test.go:21`) looks like the gate for
this and is not: it checks arity only for helpers wasmbin already has, and
`continue`s past names it does not know. A "wasmbin implements every provided
callee" test does not exist.

The hole runs the other way too: `udp_send` is implemented only in `wasmbin`
(`wasi_udp.go`) and is missing from `internal/codegen/arm64` and
`internal/codegen/x86_64`, though `hosted-native` grants `tcp`, which gates it.

### 3. The CLI driver is the wrong entry point

```
$ ./bin/fern-selfhost -target wasm32-wasi -emit core-module -o out.wasm examples/self_host/fern.fern
error: write_file_exec is not supported on the wasm target
```

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
The real filesystem constraint is blocker 3, and it is about the driver's
shape, not about these tests.

**`wasm-bin` is not a target.** It was replaced by an `-emit` axis:
`-target wasm32-wasi -emit core-module`. Pinned by
`cmd/fern/targetflags_test.go:161`. The self-host driver spells it the same way
since #6635.

## What it would actually take

In dependency order, and the estimate the issue asked for — **this is a
weekend, not a quarter**, with the caveat that the weekend is blocker 1's and
its size is unknown until it is root-caused:

1. **Root-cause blocker 1** and pin it with a differential that runs the
   *wasm-hosted* compiler against the native one on more than a trivial
   program. Unknown size; everything else is small and none of it matters until
   this is done, because a compiler that miscompiles nested arithmetic cannot
   ship to a playground.
2. **strbuf in `wasmbin`** (blocker 2, #7947). Contained: three scratch slots appended
   to the `memlayout.go` chain and three `runtimeHelperSpecs` entries keyed by
   the source names, the way `poll` and `isatty` already are
   (`runtime.go:1698,1713`). The self-host wasm implementation
   (`wasm_ir.fern:6120`) is the reference, with one adaptation — it uses
   one-word strings where wasmbin uses the two-word SSO ABI, so `strbuf_append`
   takes `(data, len)` and must read through the tag like `buildStrConcatBody`
   does. Add the missing completeness test while there.
3. **A playground driver** — `wasm_ir_run.fern`'s shape plus in-memory stdlib
   resolution and the interpret/check entry points, exporting to JS rather than
   reading stdin.
4. **The LSP** is the long pole and is not costed here; it has no self-host
   counterpart at all.

Precondition 3 of §3a — whether the native backends should be deleted at all,
given `BOOTSTRAP-RESEARCH.md §1` recommends two-implementations-forever so the
fuzz-diff oracle keeps two witnesses — is untouched by any of this and still
needs its own call. Blocker 2 sharpens it: today there is no second wasm
witness, because only one of the two compilers can emit this artifact.
