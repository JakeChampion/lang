# Retiring the native backends: the `internal/e2e` slice

The measured state of `internal/e2e` against the self-host compiler, the
disposition of every test that reaches a Go backend, and the order the
deletion PRs go in. `docs/NATIVE-CONVERGENCE.md §3a` is the policy; this is
the work list under it. Refs #4451.

## What was measured (2026-09-30)

`internal/e2e` reaches the Go backends two ways: the three run helpers in
`internal/e2eharness` (`CompileX86_64Bin`, `CompileArm64Bin`,
`CompileAndRunWasmbinMain`; 1,800 call sites in 900 files), and direct calls
into `x86_64.Emit` (38 sites, 29 files) and `wasmbin.Build*` (191 sites, 86
files).

The three helpers were re-pointed, in a scratch tree, at the current
`fern.fern` built by the pinned stage0, the same subject `internal/e2eselfhost`
tests, and the whole package was run once. Two things about the method
matter for whoever repeats it:

- **The subject is the current compiler built by the pin, not the pin.** The
  pin is a day or more older than the stdlib it would compile: on 2026-09-29
  it refused every program importing `std/http` (`HttpRequest has no field
  "trailers"`) and several map shapes the current compiler handles. Building
  `fern.fern` with `CachedDriverBinFor` costs one driver build per process,
  cached like every other driver.
- **The cost is nothing.** The self-host compiles one of these programs in
  0.2 s at 22 MB, so the package's runtime does not move; the Go pipeline it
  replaces was in-process but also linked through gcc.

The result, on the `#10729` head (main at f8f0bc6dc):

| | tests |
|---|---|
| in the package | 4,125 |
| fail through the self-host | 137 |
| skip, as before | 43 |
| pass | the rest |

Of the 137, 108 are self-host gaps (the table below sums to 108) and 29 are
tests of native-only instrumentation. The 108 are the retirement's remaining engineering: each is
a program native compiles and runs to the answer the test pins, and the
self-host does not. They are filed by cause below. Two failures the sweep saw
were already fixed on main the same day (#10728 closed
`TestX86_64BoxedDynCaptureRebind` and its arm64 twin), which is the rate the
list moves at.

## The 108 self-host gaps, by cause

Each issue names its tests and the diagnostic; the test's own program is the
reproducer. The count is tests, not programs (most run on two or three
targets).

| issue | cause | tests |
|---|---|---|
| #10005 (closed by #10780 after this measurement) | typed lowering: a map key wider than 4 bytes (`Map[i64, i32]`, `Map[i64, i64]`, `Map[i64, f64]`) has no column | 17 |
| #10767 (closed by #10821 after this measurement) | checker: six rejections native accepts (E009, E015, E038 x2, E042, E064) | 12 |
| #10756 (the mechanics landed in #10821; the finalizer timing is open) | typed lowering: `impl mem.Drop` finalizers never run | 8 |
| #10760 (closed by #10821 after this measurement) | typed lowering: a declared `str[]` holds a `string[]` (`graphemes`, `words`) | 6 |
| #10766 (closed by #10821 after this measurement) | parser: `@` binding with a braceless arm, braceless `if let`, `let … else` in a `var` | 6 |
| #10763 (closed by #10828 after this measurement) | typed lowering: empty array literal at a view parameter, `?` on a unit-payload success, a value block with no live edge, a `use` callback wrapper | 6 |
| #10757 (closed by #10835 after this measurement; the checker typed a suffixed float literal f64) | typed lowering: f32 values typed f64 | 5 |
| #10768 (closed by #10775 after this measurement) | wasm: the core module exports only `_start`, so a main result of 126 or more is unreadable | 5 |
| #10771 (the negative `__alloc_u8` and `repeat` aborts landed after this measurement; the array-grow test pins native's 32-bit request, and the `__memcpy` test waits on #8799) | runtime: allocation-size overflow does not abort with 134; `__memcpy` size classes copy the wrong count | 5 |
| #10737 (closed by #10741 after this measurement; re-measure before planning it) | typed lowering: `usize / usize`, `f64 as usize` | 4 |
| #10759 (closed by #10835 after this measurement) | typed lowering: for-each pattern bindings are not semantic values | 4 |
| #10816 (split from #10767; closed by #10844 after this measurement) | checker: an impl record names its trait without the trait's module, so same-named traits collide (E021) and `dyn cmp.Display` matches no impl (E034) | 4 |
| #10764 (the member-name call fixed after this measurement; the generic form is open) | union type aliases: members have no semantic contract; the generic form does not parse | 4 |
| #10765 (the `async` half closed by #10804) | typed lowering: `async` functions; a `dyn` std Error's `message` | 4 |
| #10762 (closed by #10807 after this measurement) | typed lowering: a generic enum's struct payload; the rc-correctness corpus probe | 4 |
| #10770 (three rows were the measuring harness failing on stderr; the split fixed after this measurement) | wasm: four programs answer wrongly (payloadless Result box, `std/platform`, scratch slots, split on `""`) | 4 |
| #10761 | typed lowering: reading a `str` map value | 3 |
| #10769 (the selection and the missing helper fixed after this measurement; `TestBytesFloorWasm` then waits on #8799, the self-host's word-per-element `u8[]`) | wasm: no instruction selection for `raw_store8` and `write_some`; one module fails wasmtime's compile | 3 |
| #10773 | arm64: the termios round trip fails at step 21 | 2 |
| #10758 (closed by #10828 after this measurement; the cause was the AST folder, not the typed lowering) | typed lowering: unary minus on u32 | 1 |
| #10772 | x86-64: the in-process assembler cannot encode an instruction in a string-payload `match`, and names nothing | 1 |

The three fuzz differentials (`TestDifferential_LangsmithMain`,
`TestDifferential_PrintableStdout`, `TestDropGuidedDifferential`,
`TestNumericProperty_Differential`) are counted once each above, under the
gap their seeds hit (#10768, #10757, #10767). Their native legs are not the
gate for anything the self-host legs in `diff_oracle_selfhost_test.go` and
its arm64 and wasm siblings do not already hold, and go with the backends;
the seeds the self-host declines by design are that suite's documented
floor, not a gap here.

## The 29 native-instrumentation tests

These assert how the native RC implementation behaves, through probes the
self-host either does not have or answers with its own figures. Every
property they hold has a self-host gate in `internal/e2eselfhost`; the tests
go with the backends, in the re-point PR, each with the gate that covers it
named in the commit.

| tests | probe | self-host gate |
|---|---|---|
| `TestX86_64Rc*` / `TestArm64Rc*` (18: `RcBuiltins`, `RcAliasInc*`, `RcClosureCaptureInc`, `RcDecOnOverwrite`, `RcDecAtExit`, `RcDropArrayElements`, `RcDropStructFields`) | `__rc_get`, a native builtin the self-host does not declare (`call target has no semantic contract: __rc_get`); the self-host's counterpart is `__rc` | the `FERN_LEAKCHECK` census legs (245 files) and the sanitize legs (109 files): the same retain and release shapes, judged by what is live at exit rather than by a counter read mid-program |
| `Test*RcUnderflowDetector` (2) | `__rc_underflow_count` answers 2 on the self-host where native answers 1 for the same double release | the self-host's own underflow rows (345 files read the counter); whether 1 or 2 is right for that program is #10771's neighbour and is decided there, not by keeping a native test |
| `Test*ArrayPushInPlaceFastPath`, `Test*ArrayIndexSetInPlaceFastPath`, `TestX86_64StructFieldWithInPlaceFastPath` (5) | native's in-place fast path when `rc == 1` | the six `*InPlace*` tests in `internal/e2eselfhost` and the allocation gate's cliff column |
| `TestArm64ArrPushCliffCounter`, `TestArm64CallResultMaterialiseCliff`, `TestArm64ArrPushCliffBytes`, `TestArm64DeadAliasAppendNoCopy` (4) | `__arr_push_shared_count` at native's figures | `TestSelfHostArrPushCliffIR*` and the alloc gate, which hold the self-host to its recorded figures (7 files) |

## The direct Go-emitter callers

Not in the sweep, because they do not go through the three helpers. Each
group is one PR, after the re-point.

| files | what the Go call does | disposition |
|---|---|---|
| `fixture_test.go` (`TestFernFixtures`, four native legs plus the `FERN_NATIVE_ASM` leg in `test-e2e-x86_64.yml`) | runs the 335-fixture corpus through each backend | delete: `fixture_selfhost_test.go` runs the same corpus through the self-host on all three targets |
| `diff_oracle_test.go` (`x86_64.Emit` leg) | the fuzz differential's native leg | delete: the self-host legs are the gate |
| `rctrace`, `leakcheck`, `sanitizer`, `heap_alloc_count`, `iter_adapter_leak`, `conformance_leak_census`, `seccomp`, `rc_freelist`, `rc_heap_benchmark` | `x86_64.Emit` with the native instrumentation options (the heap tracer, leak census, sanitizer, seccomp filter) | per test: the self-host has `FERN_LEAKCHECK` and `-sanitize`; the conformance leak census and the seccomp corpus need a self-host run of the same corpus or a decision that the property is native-only, made in that PR |
| `wit_*` (60 files) and `wasm_p3_*` (15) | `wasmbin.BuildWithOptions` (`Preview2WASI`, `SynthCliRun`, `PrintMainResult`) and `component.Compose*` | a second measurement: the same programs through `fern -target wasm32-wasi` (a component) and `wasm32-wasi-http`, which is the playground's path since #6636. Preview-3 async and streams are a question that measurement answers |
| `arm64_ssa_differential`, `x86_64_ssa_differential`, `x86_64ssa_*`, `arm64_ssa_*`, `crc32_cksum_ssa`, `ssa_coreutils_coverage`, `f64_ulp` (the SSA legs) | the Go SSA backends | delete with `internal/codegen/{x86_64ssa,arm64ssa}` |
| `cover`, `treeshake_backend_dce`, `target_os_fold`, `pub_package`, `pub_use`, `cross_module_variant*`, `trait_default_module`, `shared_variant_name_determinism`, `runtime_helper_closure`, `read_file_utf8_differential`, `supervised_serve`, `signal_disposition`, `arena_exit_code`, `x86_64_remove_dir_all`, `selfhost_coverage_fuzz`, `x86_64_test.go` | `x86_64.Emit` on a multi-file or modload-shaped program, or an assertion on the emitted text | per test: a program moves to the self-host CLI (it takes a project directory); an assertion on Go assembly text goes |

## The order

1. **#10768 first.** Exporting `main` from the mode-0 core module is one
   line in `wasm_ir.fern`, lets `CompileAndRunWasmbinMain` keep
   `wasmtime run --invoke main`, and unblocks the wasm differential's
   uncomparable seeds.
2. **The gap issues, largest first**, each its own PR with its fix in
   `examples/self_host` and its test: the `internal/e2e` test that found it
   is the gate once step 3 lands, and a row in `internal/e2eselfhost` holds
   it until then. Nothing in this list is a tracking entry: the re-point
   cannot land with a red test in it, and `CLAUDE.md` forbids an allowlist
   to make it land.
3. **The re-point PR.** The three helpers compile with the current self-host
   (the shape is in this doc's history: a `compileSelfHostProgram` that runs
   `fern -target <t> -o <bin> main.fern <stdlib>` with the CLI from
   `CachedDriverBinFor(dir, "fern.fern", TargetX86_64Linux)`;
   `FERN_HIGH_HEAP=1` in the compiler's environment for the high-heap gate).
   The 29 native-instrumentation tests go in the same PR. The sweep is green
   before it opens.
4. **The direct-caller PRs**, one per row of the table above, the wit and
   preview-3 measurement first since it is the one with an open question.
5. **The deletions.** `internal/codegen/{x86_64,arm64,wasmbin,x86_64ssa,arm64ssa}`,
   `internal/native/{x86_64,arm64}` (the assembler the tests link with;
   `internal/native/elf` and the Mach-O writer stay if `cmd/fern` keeps a
   Go link path), `cmd/dump_arm64`, the `FERN_NATIVE_ASM` leg, and
   `internal/sourcelint`'s codegen-boundary population. `docs/TEST-GATES.md`
   loses its native rows and `docs/BACKEND-PARITY.md` its per-backend table.
6. **What `cmd/fern` becomes** is the decision #4451 still owes: a Go front
   end (`-check`, `-interp`, `-fmt`, the LSP) that hands `-target` to the
   self-host binary, or a thin launcher for it. It does not gate steps 1 to
   4.

CI lanes keep their names: `test-e2e-x86_64`, `test-e2e-arm64` and
`test-e2e-wasm` select by target prefix, which stays the right split when the
target is compiled by the self-host. The "native test runners" wording, the
`FERN_NATIVE_ASM` fixture leg go in step 5. The `drop-selfhost-sources` step
and the lane table's `examples/self_host/**` ignore go in step 3: from then
on every e2e lane builds `fern.fern` once from the driver cache, and a
self-host change runs them.
