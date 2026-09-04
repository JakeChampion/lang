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
this list (#7948) is fixed, and so is the builtin gap in full: strbuf shipped in
#7951 and `sleep_ms` in #7947, leaving no missing lowering at all. `timer_fd`,
`write_file_exec` and the `__c_call*` family are things wasm cannot express, and
are refused by E066 at check time rather than by the emitter mid-build.

The self-host compiler compiled to wasm **already runs in wasm today**. A
stdin-driven wasm-emitting driver, compiled to a core module by the self-host
compiler itself, reads a Fern program on stdin under wasmtime and emits a
module that runs and returns the right answer:

```
$ echo 'function main(): i32 { return 6 * 7; }' | wasmtime run wasm_ir_run.wasm > out.wat
$ wasm-tools parse out.wat -o out.wasm && wasmtime run out.wasm; echo $?
42
```

2.13 MB of module, 1.9 s, 104 MiB peak. The playground's current bundle is
28.7 MB.

The driver a PAGE would fetch goes further: `playground_run.fern`, which
compiles, checks and interprets off one embedded stdlib, builds to a
**4,126,904-byte** module (1,110,860 gzipped) that runs all three modes under
wasmtime — `-check` silent on a good program and `1:24: error[E002]: …` on a
bad one, `-interp` printing the program's output and exiting with its code,
and the default writing WAT. Getting there needed two self-host codegen fixes,
both in "The measurement" below.

**That run used to write an internal error to stderr, which this page did not
say.** The command redirects stdout and never looked at the other stream, which
carried:

```
wasm_ir_run: internal error: rc over-release detected (1 dec(s) on an already-released block)
```

The emitted module was correct either way — that was #7969, a refcount defect
in the compiler's own execution on the wasm target rather than a miscompile of
its output — but "already runs in wasm today" is a claim about a run that
reported an internal error, and it should have said so.

Fixed. The trigger was `args()`: `__fern_args` handed Fern an array with a bare
4-byte length prefix where a 16-byte cap / rc / len header belongs, so the
caller's scope-exit dec read a refcount word that was whatever preceded the
allocation. Writing `rc = 1` there is also wrong — the array is a cached
process-lifetime singleton, so no one reference can be the last, and x86-64
segfaulted on a second `for a in args()` — which is why the header is the
static sentinel. Seven wasmbin helpers carried the same bare-prefix defect. The
emitted WAT is byte-identical across the fix on all three backends; stderr is
now empty. Note the invocation: a wasmbin core module carries no `_start`, so a
plain `wasmtime run` exits 0 having called nothing and looks like an empty
answer. Use `--invoke main`, which appends main's return value to stdout.

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

Re-measured 2026-09-02 on x86-64 Linux. Every module below was put through
`wasm-tools validate`; gzip at -9, because that is what a static host serves.

`playground_run` is the row that answers the question — it is the one driver
that does all three panes, and it carries the stdlib as `-embed` assets, so it
is the artifact a page would actually fetch:

| artifact | built by | raw | gzipped |
|---|---|---|---|
| `web/fern.wasm` — the playground today | Go, `GOOS=js GOARCH=wasm`, `-s -w` | **28,661,200** | **6,396,834** |
| `playground_run` — compile + check + interpret, stdlib embedded | self-host | **4,126,904** | **1,110,860** |
| `wasm_ir_run` — source → wasm core module | self-host | 2,129,864 | 540,544 |
| `asm_run` — source → x86-64 asm text | self-host | 2,000,276 | 521,911 |
| `checker_run` — type-check only | self-host | 470,388 | 132,368 |
| `interp_run` — source → interpreted result | self-host | 357,111 | 92,960 |
| the whole 19-module compiler, sharded-linked | self-host | 2,474,201 | — |

**6.9x smaller raw and 5.8x smaller gzipped** than the bundle it replaces, for
a module that compiles, checks and interprets. An earlier revision of this
table quoted 12x from `wasm_ir_run` alone and then added the drivers up as a
deliberate over-estimate (6,178,555 / 1,675,104), reasoning that a merged
bundle would land below their sum because the drivers overlap. It does:
`playground_run` is that merged bundle, and it comes in a third under the
guess.

### The compiler doing the emitting is worth 5x

The same sources through the NATIVE compiler, same flags, also validated:

| artifact | native raw | self-host raw | native ÷ self-host |
|---|---|---|---|
| `playground_run` | 13,217,895 | 4,126,904 | 3.2x |
| `wasm_ir_run` | 10,774,121 | 2,129,864 | 5.1x |
| `asm_run` | 10,723,290 | 2,000,276 | 5.4x |
| `checker_run` | 1,705,076 | 470,388 | 3.6x |
| `interp_run` | 1,370,831 | 357,111 | 3.8x |

So "how big is the compiler as wasm" has no single answer: a native-built
`playground_run` is 13 MB, which is not a page's artifact, and the self-host's
4 MB is. Whatever ships has to be self-host-built.

The gap is **not** monomorphisation, which is the first guess and is wrong. The
two modules hold the same functions:

| `wasm_ir_run` | functions | code section | bytes per function |
|---|---|---|---|
| native | 3,944 | 10,442,809 | 2,647 |
| self-host | 3,854 | 1,860,754 | 482 |

Within 2% on the count, 5.5x on the bytes each one costs — and that average
hides the shape. The median function is only 1.5x larger; twenty functions
carry 55.6% of native's code section, and `irlower__lower_call_named` alone is
**1,866,949 bytes**, 18% of the module, where the self-host's LARGEST function
is 29,509. Those are the long dispatch chains, so whatever native emits per
branch is paid thousands of times in one body. On a small array-building
program with reference counting in it native emits the smaller module (1,253
bytes against 2,144), so this is superlinear in something these bodies do a
lot of rather than a constant overhead. #8121 has the distribution and the
named functions; what native spends the bytes on is still unopened.

(One incidental find from the same measurement: the self-host emits a distinct
function type per function — 3,865 types for 3,854 functions, where native
dedups to 33. It costs almost nothing in bytes and is not the gap.)

Two bugs stood between this table and a running module, both found by building
it: a `Cell[f64]` reached through a struct field indexed 4-byte (which made
`interp_run` fail to validate outright), and the declared wasm memory being a
hardcoded 16 pages (which made the `-embed` build validate and then trap at
instantiation). Both are fixed; the `interp_run` row above is the first one
that validates.

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
`TestSelfHostWasmWholeCompilerShardedLink`, measured here at 304 s over 41
units, validated, then run to compile a 2-module program. It comes to
**2,474,201 bytes** as a core module, which is the number to quote: the same
module is 17,493,609 bytes as WAT text, so the text length that test used to
report on its own overstates the artifact by 7.1x. Even the entire compiler is
11.6x under the playground bundle.

That path is sharded per 150-function window across processes because the
*host-side* emit peaks ~10 GB, not because the result is too big. Nothing about
the shipped artifact needs sharding.

### Reproducing

```
make build && make selfhost-cli
./bin/fern-selfhost -target wasm32-wasi -emit core-module \
    -o wasm_ir_run.wasm examples/self_host/wasm_ir_run.fern internal/stdlib
echo 'function main(): i32 { return 6 * 7; }' | wasmtime run wasm_ir_run.wasm

# The page's artifact. -embed names the stdlib it CARRIES; the trailing
# positional is the one resolving this driver's own imports at build time.
./bin/fern-selfhost -target wasm32-wasi -emit core-module -embed internal/stdlib \
    -o playground_run.wasm examples/self_host/playground_run.fern internal/stdlib
echo 'function main(): i32 { print("hi"); return 42; }' \
    | wasmtime run playground_run.wasm -interp    # prints hi, exits 42
```

Swap `./bin/fern-selfhost` for `./bin/fern` to reproduce the native column;
`-emit command-module` there gives the `_start` shape a browser shim runs (the
self-host's `-emit core-module` already emits one).

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

`sleep_ms` was the last one, and #7947 landed it: wasm CAN block, so preview-1
takes `poll_oneoff` with a single monotonic-clock subscription and preview-2
takes subscribe-duration plus a wait. `providedMissingLowering` is now empty.

That removes the builtin half of this precondition, and the rest of it stands
on a different footing than it did. The playground's wasm artifact is still
produced *by the self-host compiler compiling itself* rather than by the native
toolchain — but what refuses the native route now is `write_file_exec` needing
`fsmode`, listed among the E066 refusals above, not a lowering anyone can add.
So there is still no independent second witness to cross-check a wasm
miscompile against — which is why #7948 had to be diagnosed by diffing the two
TARGETS of one compiler instead — and it is no longer a witness to wait for.

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
has somewhere to land, and its known-missing list — empty since #7947 — is
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
the stdlib inside the module, since the self-host resolves `std/…` by host path
(`modloader.fern:87`) where native serves it from `go:embed`. The mechanism for
that exists now on both compilers — see "What it would actually take" below —
so what remains is the driver wiring it up.

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
size before it was root-caused; it is closed, and what remains is scoped.

**strbuf in `wasmbin`** headed this list and shipped in #7951: three scratch
slots on the `memlayout.go` chain plus `runtimeHelperSpecs` entries keyed by the
source names, with `strbuf_append` reading through the two-word SSO tag rather
than the self-host's one-word strings. `sleep_ms`, the remainder of that
blocker, shipped the same way in #7947.

**An embedded stdlib for the self-host compiler** was the second, and it has
shipped too. A wasm-hosted compiler has no host filesystem to read
`internal/stdlib` from, and the self-host CLI had no other way to find it:
`fern.fern` takes the stdlib root as its second positional argument where native
serves it from `go:embed` (`internal/stdlib/stdlib.go:37`). Native's mechanism —
`-embed DIR` plus `__fern_asset("name")` / `__fern_assets()`, folded into
ordinary string literals (`docs/EMBED.md`) — is now on both compilers:
`examples/self_host/embed.fern`, gated against native case for case by
`internal/e2eselfhost/self_host_embed_test.go`. The self-host compiler embeds
its own stdlib and finds `std/io.fern` in the resulting binary by name.

The stdlib itself as a wasm core module is **1,610,275 bytes raw / 458,170
gzipped**, built in under a second, all 73 files enumerable at runtime under
wasmtime. The pass costs nothing measurable: compiling `checker_run.fern` takes
11.0 s with it and 11.1 s without, over three runs each.

**A compiling, checking and interpreting playground driver** was the third, and it runs.
`examples/self_host/playground_run.fern` reads a program on stdin, resolves its
`std/…` imports out of an embedded bundle handed to the module loader as a
sealed overlay (`modloader.Overlay`), and writes a wasm module. Compiled to
wasm itself and run under `wasmtime` with **no `--dir` at all** — so no preopen
exists and `path_open` cannot succeed — it compiles a program importing
`std/i64` and emits a module that runs and returns the right answer, byte-for-
byte identical to what the natively-hosted driver emits for the same input.

The projection above it was wrong and is corrected here. This page previously
estimated "roughly 3.95 MB raw / 1.07 MB gzipped" by adding the measured
`wasm_ir_run` driver to the stdlib. The real driver is **15,008,071 bytes raw /
1,508,333 gzipped**: it carries the module loader, the flattener, the checker,
constfold and treeshake on top of what `wasm_ir_run` links, and none of that was
in the sum. Against the bundle shipping today it is **1.9x smaller raw and 4.2x
smaller gzipped** — still a win, and still the wrong shape to quote as "an order
of magnitude", which the raw figure no longer is.

What is left:

1. **The other entry points — done.** The driver compiles, checks and
   interprets, each selected from argv. `-check` reports
   `line:col: error[E0XX]: message` and emits nothing, hosted in wasm as well
   as natively. Its verdict is the diagnostics rather than
   `ModuleTypes.all_well_typed`, which is false for any program the partial
   checker (#4346) merely cannot model — `import "std/string"` is enough — and
   would fail almost everything with nothing printed. So check under-reports
   where the port is incomplete rather than rejecting valid programs.

   Interpret now writes: `interp.fern` implements `print` / `write` / `eprint`,
   compared against the native interpreter on all three channels by
   `internal/e2eselfhost/self_host_interp_io_test.go`. It had **no I/O builtins
   at all** before — no `print` anywhere in its 4,138 lines — so
   `interp_run.fern` reported an exit code and nothing else, and the exit-code
   driver test next door passed either way.

   `putchar` landed with it, and settling its parity question found a native
   bug rather than a self-host one. Measured across all four engines, the three
   compiled backends wrote the low byte (`putchar(233)` → `e9`) and
   `internal/interp` wrote `%c` of the argument as a rune (`c3 a9`); an
   argument outside a rune's range gave U+FFFD instead of wrapping. The
   backends match `docs/FEATURE-AUDIT.md` ("`putchar` (byte)",
   `write(1, &byte, 1)`) and `TestX86_64NativePutchar`, so the interpreter was
   the outlier and now writes one byte too.

   What the interpreter still lacks is the reader/writer handles and anything
   touching the filesystem or the clock. (An earlier revision of this list also
   named `print_int` / `eprint_int`; they are not user-callable — native
   answers E001 on both. `__fern_print_int` is a runtime helper the backends
   emit for `i32.to_string`, and a program prints a number by importing
   `std/i32` and concatenating, which the string arm already covers.)

   The sharper limit was the DRIVER, not the evaluator: `interp_run.fern` has
   no module loader, so an interpreted program could not resolve `std/i32` and
   therefore could not turn a number into a string at all — it warns and exits
   254. `playground_run.fern` already carried the sealed stdlib overlay that
   answers this, so `-interp` lives there: it evaluates the same bundle the
   compile path emits, prints the program's own output, and exits with the
   program's own code. All three panes now have a self-host counterpart, and
   the LSP is the only missing consumer left.
2. **Getting the driver into a page.** Two routes, and the stdin/stdout one is
   now the shorter.

   **As a command.** `-emit command-module` writes a WASI preview-1 command:
   the core module plus a `_start` that runs main and exits with its value.
   That is the shape `web/wasi-shim.js` already runs, so the driver goes into a
   page as-is — source on stdin, artifact on stdout, mode in argv. Verified
   end-to-end under wasmtime on a 13 MB `playground_run.wasm`: `-check` exits 0
   silent on a good program and 1 with `1:24: error[E002]: …` on a bad one,
   `-interp` prints the program's output and exits with the program's own code
   (42, not the 0/1 a component reports), and the compile path writes WAT.

   The exit code is why the form had to exist. The default `-target
   wasm32-wasi` composes a wasi:cli/run component, whose `run: func() -> result`
   carries ok or err and nothing wider, and `-emit core-module` has no `_start`
   at all — `wasmtime run` on one calls nothing and exits 0, which reads exactly
   like a program that ran and succeeded. Both were dead ends for a driver whose
   whole verdict is its exit code.

   The JS half is in place too: `web/wasi-shim.js` implements `fd_read` over a
   caller-supplied stdin and answers `args_sizes_get` / `args_get` from a
   caller-supplied argv, so `runCoreWasm(bytes, { stdin, args })` supplies both
   the source and the mode. It had neither — no `fd_read` at all, `argc = 0` —
   which is why the driver could not be hosted in a page however it was built.
   `web/test/shim/` pins the contract by compiling real programs and running
   them through the real shim.

   What remains is wiring, not capability. Both halves exist and have been run
   against each other's contract: a 4.1 MB self-host-built `playground_run.wasm`
   that answers all three modes, and a shim that can hand it a source and a
   mode. `web/index.html` still loads `web/fern.wasm`, the Go compiler under
   `GOOS=js`, and would have to fetch and drive the driver instead — a page
   change, not a compiler one.

   **As exports.** `@export("iface", "name")` emits canonical-ABI wrappers into
   a `-emit core-module` build in both compilers, and a module whose exports
   take a string or a list also exports `cabi_realloc`, the canonical ABI's
   guest allocator. A page calls `cabi_realloc(0, 0, align, n)`, writes `n`
   bytes at the returned pointer and passes `(ptr, n)`; the allocator forwards
   to `__fern_alloc`, which grows memory, so the argument size is bounded by the
   host's memory limit rather than by the module's initial page. A string result
   comes back as a pointer to a `[ptr, len]` pair the page reads directly. That
   is about twenty lines of page-side JS, and a cleaner API than argv and pipes
   — but it needs export entry points written into the driver first, where the
   command route needs nothing from the driver at all. The two ABIs share no
   code: this one is the component model's, the other the shim contract's.
3. **The CLI's own stdlib.** `fern.fern`'s `load_bundle` resolves imports with
   its own worklist and only falls back to `modloader.resolve_module`, so the
   overlay does not give `fern -embed` an embedded stdlib. That is its own
   piece of work.
4. **The LSP** is the long pole and is not costed here; it has no self-host
   counterpart at all.

Precondition 3 of §3a — whether the native backends should be deleted at all,
given `BOOTSTRAP-RESEARCH.md §1` recommends two-implementations-forever so the
fuzz-diff oracle keeps two witnesses — is untouched by any of this and still
needs its own call. Blocker 1 sharpens it: today there is no second wasm
witness, because only one of the two compilers can emit this artifact.
