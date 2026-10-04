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
| #10756 (closed after this measurement: the mechanics landed in #10821, and the tests check the documented contract, each finalizer once at the value's death, rather than native's release timing) | typed lowering: `impl mem.Drop` finalizers never run | 8 |
| #10760 (closed by #10821 after this measurement) | typed lowering: a declared `str[]` holds a `string[]` (`graphemes`, `words`) | 6 |
| #10766 (closed by #10821 after this measurement) | parser: `@` binding with a braceless arm, braceless `if let`, `let … else` in a `let` | 6 |
| #10763 (closed by #10828 after this measurement) | typed lowering: empty array literal at a view parameter, `?` on a unit-payload success, a value block with no live edge, a `use` callback wrapper | 6 |
| #10757 (closed by #10835 after this measurement; the checker typed a suffixed float literal f64) | typed lowering: f32 values typed f64 | 5 |
| #10768 (closed by #10775 after this measurement) | wasm: the core module exports only `_start`, so a main result of 126 or more is unreadable | 5 |
| #10771 (the negative `__alloc_u8` and `repeat` aborts landed after this measurement; the array-grow test pins native's 32-bit request, and the `__memcpy` test is native-only by decision on #8799: it goes with the backends) | runtime: allocation-size overflow does not abort with 134; `__memcpy` size classes copy the wrong count | 5 |
| #10737 (closed by #10741 after this measurement; re-measured: `f64 as usize` was still refused, and the SSA backends had no pointer-width conversion, fixed on #10874) | typed lowering: `usize / usize`, `f64 as usize` | 4 |
| #10759 (closed by #10835 after this measurement) | typed lowering: for-each pattern bindings are not semantic values | 4 |
| #10816 (split from #10767; closed by #10844 after this measurement) | checker: an impl record names its trait without the trait's module, so same-named traits collide (E021) and `dyn cmp.Display` matches no impl (E034) | 4 |
| #10764 (fixed after this measurement: the member-name call in #10852; the generic form desugars into the generic enum the Go checker desugars every union into) | union type aliases: members have no semantic contract; the generic form does not parse | 4 |
| #10765 (the `async` half closed by #10804; the `message` half by #10844, pinned after this measurement) | typed lowering: `async` functions; a `dyn` std Error's `message` | 4 |
| #10762 (closed by #10807 after this measurement) | typed lowering: a generic enum's struct payload; the rc-correctness corpus probe | 4 |
| #10770 (three rows were the measuring harness failing on stderr; the split fixed after this measurement) | wasm: four programs answer wrongly (payloadless Result box, `std/platform`, scratch slots, split on `""`) | 4 |
| #10761 (fixed after this measurement, with #10701: a read of a view map value takes a fresh box) | typed lowering: reading a `str` map value | 3 |
| #10769 (the selection and the missing helper fixed after this measurement; `TestBytesFloorWasm` then waits on #8799, the self-host's word-per-element `u8[]`) | wasm: no instruction selection for `raw_store8` and `write_some`; one module fails wasmtime's compile | 3 |
| #10773 (closed: qemu-user drops the two unnamed `c_cc` slots for native's binaries too; the arm64 lane runs on real hardware) | arm64: the termios round trip fails at step 21 | 2 |
| #10758 (closed by #10828 after this measurement; the cause was the AST folder, not the typed lowering) | typed lowering: unary minus on u32 | 1 |
| #10772 (fixed after this measurement: the tree shaker dropped core/map behind a builtin enum's variant; the assembler names the symbol) | x86-64: the in-process assembler cannot encode an instruction in a string-payload `match`, and names nothing | 1 |
| #10927 (found by the 2026-10-01 re-measurement on main 7ae1854f4 with #10915; closed after it by #10928, #10933 and #10936, one shape each) | lambda lift: an if-expression as a call argument with an arm whose value is a generic passthrough call carrying lambdas is left unboxed (`function address is not a closure value`, `unsupported expression`); the fuzz seeds also reach a hoisted arm wrapping a capturing lambda in `id`, and arms naming a local fn value | 2 |
| #10926 (found by the 2026-10-01 re-measurement; fixed after it: the view stays in the type spelling, which the checker resolves and the lowering folds to the array it borrows from) | parser and checker: a `[u8]` view inside a struct field, tuple or array element type is erased to `u8[]`, and `[u8][]` does not parse (`TestX86_64RcCorrectnessCorpus/slice_header_in_containers_churn_free` and its arm64 twin) | 2 |

The fuzz differentials (`TestDifferential_LangsmithMain`,
`TestDifferential_PrintableStdout`, `TestDropGuidedDifferential`,
`TestNumericProperty_Differential`) are counted once each above, under the
gap their seeds hit (#10768, #10757, #10767; on the 2026-10-01 re-measurement,
#10927). Their native legs were not the
gate for anything the self-host legs in `diff_oracle_selfhost_test.go` and
its arm64 and wasm siblings do not already hold: `TestDifferential_LangsmithMain`
went in step 4, `TestDropGuidedDifferential` with the drop-guided flag it
toggled (the self-host has no such flag, so both its legs were one build),
and the other two run every compiled leg through the self-host. The seeds the self-host declines by design are that suite's
documented floor, not a gap here.

## The native-instrumentation tests

These asserted how the native RC implementation behaves, through probes the
self-host either does not have or answers with its own figures. Every
property they held has a self-host gate in `internal/e2eselfhost`. The 29
register-backend tests were deleted in the re-point (step 3), and 23 of
their wasm twins with the `buildComponent` move. The x86-64 twins of the four
cliff tests compile with the self-host since the `FreeOn` helpers moved, and
are held at the self-host's figures.

| tests | probe | self-host gate |
|---|---|---|
| `TestX86_64Rc*` / `TestArm64Rc*` (18: `RcBuiltins`, `RcAliasInc*`, `RcClosureCaptureInc`, `RcDecOnOverwrite`, `RcDecAtExit`, `RcDropArrayElements`, `RcDropStructFields`) | `__rc_get`, a native builtin the self-host does not declare (`call target has no semantic contract: __rc_get`), and it has no counter read in its place | the `FERN_LEAKCHECK` census legs (245 files) and the sanitize legs (109 files): the same retain and release shapes, judged by what is live at exit rather than by a counter read mid-program |
| `Test*RcUnderflowDetector` (2) | `__rc_underflow_count` answers 2 on the self-host where native answers 1 for the same double release | the self-host's own underflow rows (345 files read the counter); whether 1 or 2 is right for that program is #10771's neighbour and is decided there, not by keeping a native test |
| `Test*ArrayPushInPlaceFastPath`, `Test*ArrayIndexSetInPlaceFastPath`, `TestX86_64StructFieldWithInPlaceFastPath` (5) | native's in-place fast path when `rc == 1` | the six `*InPlace*` tests in `internal/e2eselfhost` and the allocation gate's cliff column |
| `TestArm64ArrPushCliffCounter`, `TestArm64CallResultMaterialiseCliff`, `TestArm64ArrPushCliffBytes`, `TestArm64DeadAliasAppendNoCopy` (4) | `__arr_push_shared_count` at native's figures | `TestSelfHostArrPushCliffIR*` and the alloc gate, which hold the self-host to its recorded figures (7 files) |
| the wasm twins of the four rows above (15: `TestWASMRc*`, `TestWASMRcUnderflowDetector`, the two `TestWASM*InPlaceFastPath`, the four `TestWASM*Cliff*` / `TestWASMDeadAliasAppendNoCopy`) | the same probes | the same gates |
| `TestWASMAllocReuse`, `TestWasmHeapBumpIsI64`, `TestWASMTupleHeapBumpBounded`, `TestWASMPreciseDrops`, `TestWASMControlFlowDrop`, `TestWASMGeneralReuse`, `TestWASMTrmcConsumePeakHalved`, `TestWASMVariantPayloadArgTempBounded` (8) | `__alloc_reuse` and bounds on `__heap_bump_bytes`, which on the self-host's wasm core is the native probe's figure for a different allocator | the `FERN_LEAKCHECK` census on each program (balanced on the self-host) and the reuse and heap-class tests: `TestSelfHostHeapClassReuse*`, `TestSelfHostLoopReuse*`, `TestSelfHostPreciseDrop*`, `TestSelfHostArm64DarwinTrmcConsume`, `TestSelfHostClosureVariantPayload*` |

## The direct Go-emitter callers

Not in the sweep, because they do not go through the three helpers. Each
group is one PR, after the re-point.

| files | what the Go call does | disposition |
|---|---|---|
| `fixture_test.go` (`TestFernFixtures`'s three compiled legs, the `FERN_NATIVE_ASM` leg in `test-e2e-x86_64.yml`, and `runFixtureX86_64` / `runFixtureArm64`, which 42 other test files call) | runs the 335-fixture corpus through each backend | DONE: the compiled legs and `FERN_NATIVE_ASM` went, since `fixture_selfhost_test.go` runs the same corpus through the self-host on all three targets; the interpreter leg stays as the oracle. The two runners compile with the self-host (`e2eharness.CompileSelfHostFile`). `runFixtureWasm` and `runFixture{X86_64,Arm64}Native` stay native for the rc flag differentials, which compare two native builds and belong to the instrumentation row |
| `diff_oracle_test.go` (`x86_64.Emit` leg) | the fuzz differential's native leg | DONE: `TestDifferential_LangsmithMain` went with its `x86_64.Emit` and `arm64.Emit` legs and the leg-requirement check (`FERN_REQUIRE_DIFF_BACKENDS`); `TestDifferential_SelfHost{X86_64,Arm64,Wasm}` in the `diff-selfhost` job are the exit-byte gate. `FuzzGenerate_ExecutionAgrees` loses its wasmbin component leg, and the numeric and printable-stdout sweeps build their wasm leg with the self-host. |
| `wasm_e2e_test.go`'s `buildComponent` / `buildComponentMulti`, behind `runWasm`, `invokeWasmtime` and their multi-file forms (287 files) | `wasmbin.BuildWithOptions` with `PrintMainResult`, so stdout ends with main's result | DONE: the self-host core run with `--invoke main`, which prints the same line. A test of preview-2 host behaviour builds a component with the self-host CLI (`buildCLIComponent`, `runCLIComponent`); 23 native-probe twins went, as in the instrumentation table, and the `__memcpy` / `__memset` tests now copy between raw `__alloc` blocks rather than `u8[]` addresses |
| the async programs: `async_wasm_e2e`, `async_wasm_fetch_e2e`, `wasm_reactor`, the `sim_driver` / `sim_fault` / `sim_net` / `sim_fetch` / `sim_platform` wasm legs and `TestSimProperty`'s wasm leg (13 tests and three legs) | `wasmbin.BuildWithOptions`, through the one native builder they share (`buildNativeComponent`, `runWasmNative`) | stays native until the wasm async decision on #4451: `std/async` and `std/sim` drive the poll host, which the self-host's wasm component does not import |
| `leakcheck`, `sanitizer`, `heap_alloc_count`: the run helpers | `x86_64.Emit` with the leak census or the sanitizer on | DONE: `runLeakCheck{X86_64,Arm64}` and `runSanitize{X86_64,Arm64}`, behind every census and sanitize leg in the package, compile with the self-host (`e2eharness.CompileSelfHostSource` with `FERN_LEAKCHECK=1` / `FERN_SANITIZE=1`), and `heap_alloc_count` reads the self-host's emitted assembly. Moving them found three self-host gaps, each fixed in its own PR first: an in-place string growth counted as a free plus an alloc, a string field append that never grew in place, and a box freed through `__fern_box_free` that the quarantine missed; and the self-host sanitizer gained native's backtrace. The rest of the difference was native's figures: the leak gate's x86-64 and arm64 tables are empty (no corpus case leaks on the self-host), the size-class counts are re-banked at the self-host's 8-byte classes, and fixtures whose only array was a constant literal build it with `append`, since the self-host emits a constant literal as a static aggregate the census cannot see. `TestArm64StrcatShortResultIsInline` went: it asserted native arm64's inline short strings, which the self-host does not have |
| `leakcheck`, `sanitizer`: the emit-text tests (`emitLeakCheck`, `emitSanitize`) | `x86_64.Emit` / `arm64.Emit` to read the emitted text: flag-off byte identity, the quarantine's bump growth, the poison check in the rc helpers, the silent unsanitized run | DONE: the helpers went. The flag-off, poison-wiring and silent-run tests went too, each with a self-host twin in `internal/e2eselfhost`: `TestSelfHostHeapEventFlagOffX86_64` and `TestSelfHostArm64LeakcheckOffEmitsNothing`, `TestSelfHostSanitizeOffEmitsNoSymbolsX86_64` and `TestSelfHostOverReleaseReportArm64` (whose flag-off half now checks every `.Lsan_` label and the `fern-sanitizer` text, as native's arm64 leg did), `TestSelfHostUafQuarantineAsmContract{X86_64,Arm64}`, `TestSelfHostDoubleFreeSilentWithoutSanitizeX86_64` and `TestSelfHostUafSilentWithoutFlag{X86_64,Arm64}`. `TestX86_64IoErrorBoxesAreHeadered` went: it pinned native's hand-written `__fern_io_error`, where the self-host's is a Fern function whose `IoError` box the typed lowering builds like any other. The quarantine pair, the read-file census, the uncounted map over-release and `TestMapOwnParamResult`'s arm64 leg build with the self-host |
| `rctrace`, `iter_adapter_leak`, `conformance_leak_census`, `rc_heap_benchmark`, and through their shared helper `tuple_projection_leak`, `match_binding_rebind`, `examples_crash_census` and `string_transform_leak` | `x86_64.Emit` with the heap tracer on | DONE: the tracer helpers compile with the self-host and `FERN_RC_TRACE=1`, which writes native's `rctrace` format. The conformance census is now the self-host's: its pin file was re-banked at the self-host's figures, and the examples crash floor runs the examples corpus through the self-host. Fixtures whose only array was a constant literal embed a runtime value, since a static aggregate is never allocated, and `match_binding_rebind` judges by the census alone: an extra retain is a block still held at exit. Of the eight `rctrace` tests, the one checking that a traced program keeps its stdout and exit status stays. Five went, each with a twin in `internal/e2eselfhost/self_host_rctrace_test.go`. The two that read native's `i`/`d` retain and release events went too: the self-host does not emit those events, and nothing reads them. `rc_heap_benchmark` went: it compared native's freelist switched on and off, and the self-host has no switch to turn freeing off. `TestX86_64CertifyAgreesWithTheLeakCensus` went with the native census: it measured `ssa.Certify`'s walk over the native IR lowering against the clean fixtures in the census pin, and that pin now records the self-host's runtime, which leaks less. `ssa.Certify` keeps its unit tests |
| `compileAndRun{X86_64,Arm64}FreeOn` and `compile{X86_64,Arm64}FreeOn` in `rc_freelist`, behind 132 files | `x86_64.Emit` / `arm64.Emit` with the freelist on | DONE: they compile with the self-host. 13 of the 508 tests in those files answered differently. The four cliff tests are re-banked at the self-host's figures: the call-result shapes native copied 49 times copy nothing, and a pointer or string accumulator behind a live second box crosses 45 times, against native's 49, because five appends find the buffer full under the 8-byte classes. Four tests read `__alloc_reuse` or heap-bump bounds the self-host does not keep, the consume-peak test compared native's TRMC consume on and off, and the drop-guided traffic test native's drop-guided flag on and off, so those went. The two deep-stack tests keep their TRMC-on leg, and the TRMC on/off pairs check against the interpreter instead. Native flag toggles around these compiles no longer reached any compiler, so they went: 180 `ast.RcFreeEnabled = true` assignments (already the default), the `OwnedByDefault`, `BorrowInferEnabled`, `RcReuseEnabled` and `RcReuseDropGuided` wrappers, three tests that became exact duplicates of their plain twins, and `TestDropGuidedDifferential`, whose remaining leg is `TestDifferential_SelfHostX86_64` |
| `seccomp` | `x86_64.Emit` with the seccomp filter | DONE: the self-host has the sandbox. `FERN_SANDBOX=1` records every syscall number its x86-64 output issues, installs the filter first thing in `_start`, and refuses a raw syscall whose number is a run-time value, and a per-module unit, which cannot see its siblings' syscalls. The four tests build with the self-host, the fixture corpus included; `TestSelfHostSandbox*` in `internal/e2eselfhost` pin the filter's shape and the refusals, as `internal/codegen/x86_64/seccomp_test.go` does for native |
| the free and reuse on/off differentials in `rc_freelist`, and the owned-by-default and borrow-inference differentials beside them | native builds with `ast.RcFreeEnabled`, `ast.RcReuseEnabled`, `ast.OwnedByDefault` or `ast.BorrowInferEnabled` flipped | DONE: `Test{X86_64,Arm64,WASM}FixturesFreeMatchesNoFree`, `…ReuseMatchesNoReuse`, `…OwnedByDefaultMatchesBorrow` and `…BorrowInferMatchesOwned` compared two Go builds, and went with the runners that built them (`runFixture{X86_64,Arm64}Native` and the Go `runFixtureWasm`). The self-host has no such switches; the self-host fixture legs, the quarantine leg (`FERN_RC_FREE_DEBUG=1`), the leak census and `self_host_reuse_differential_test.go` hold the property. `TestX86_64OwnedByDefaultSound` stays, through the self-host `FreeOn` helper. The last non-async users of the Go wasm builder went in the same change: `TestGenericFnValue` and `TestTraitParams` run their wasm leg through the self-host component (`runFixtureWasm`), and the preview-2 host-behaviour legs of `wasm_fs_handle_parity` and `wasm_stdio_stream` (`runParityPreview2`, the `wasm preview 2` stdio leg, the native half of `TestWASMFailedPrint`) went, each with its `TestSelfHostWasm*` twin already beside it |
| `wit_*` (65 files, 77 tests) | the self-host's core or component, composed by `component.Compose*` against the embedded `fern` or `proxy` world, a user-supplied world, or a provider | DONE, measured: every program compiles with the self-host (`SelfHostCompileCmd`, or `SelfHostReactorCore` for an export or a handler), and no file reaches a package step 5 deletes. The composition stays in Go, in `internal/wasm/component`, because bring-your-own worlds and providers have no `fern` flag; the self-host composer's own output runs in the `self_host_extern_*` and `self_host_export_*` twins in `internal/e2eselfhost` |
| `wasm_p3_*` (21 files, 39 tests) | `wasmbin.BuildWithOptions` with `AsyncExportName` / `AsyncSourceFunc` (23 tests), the native CLI's `-emit core-module` with `async function` or `-async-export` (the three `TestCmdLangAsync*`), and hand-built components from `component.Build*` (13, which compile no Fern) | stays native until the wasm async decision on #4451, with the async programs above. Measured on the self-host CLI: it parses `async` and nothing reads it, so `async function run(): i32` becomes a plain function in a `wasi:cli/run` component with no async lift; an async `@import` of an interface the `fern` world does not declare is refused (`the fern world cannot take this program`); and `stream[u8]` is an unknown type (E064). Preview-3 async and streams are a native-only surface: the 26 tests that compile Fern have nothing to move to until the self-host gains that surface or #4451 drops it. The 13 hand-built tests exercise `internal/wasm/component` alone and stay with it |
| `arm64_ssa_differential`, `x86_64_ssa_differential`, `x86_64ssa_*`, `arm64_ssa_*`, `crc32_cksum_ssa`, `ssa_coreutils_coverage`, `f64_ulp` (the SSA legs) | the Go SSA backends | DONE: deleted with `internal/codegen/{x86_64ssa,arm64ssa}`, and with them `internal/ssa` and `internal/semir`, whose only consumers they were, `cmd/fern`'s `-backend ssa` and `typed-ssa`, the SSA legs of every mixed test, and the `FERN_REQUIRE_ARM64_SSA_DIFF` lane setting. `-backend` on `cmd/fern` accepts `flat` or nothing. The self-host lifetime solver's oracle was `ssa.ComputeLivenessWithDependencies`; its answers for the five fixture graphs are recorded in `internal/e2eselfhost/testdata/lifetime` |
| `cover`, `treeshake_backend_dce`, `target_os_fold`, `pub_package`, `pub_use`, `cross_module_variant*`, `trait_default_module`, `shared_variant_name_determinism`, `runtime_helper_closure`, `read_file_utf8_differential`, `supervised_serve`, `signal_disposition`, `arena_exit_code`, `x86_64_remove_dir_all`, `selfhost_coverage_fuzz`, `x86_64_test.go` | `x86_64.Emit` on a multi-file or modload-shaped program, or an assertion on the emitted text | DONE: the programs compile with the self-host, through `runFixture*`, `compileX86_64Bin` or `CompileSelfHostFile`, and the tree-shake and target-fold assertions read the self-host's own assembly and core module. Moving them found two self-host gaps, fixed in the same change: `pub(package)` was refused from a sibling module, and a trait's default methods were not inherited by an impl in another module. The two determinism tests compare the self-host's own emission across compiles instead of native's. `buildSupervisedServeBin` was the native builder for every compiled serve test in the package; each of those had a self-host twin in `internal/e2eselfhost` over the same scenario, so they went and the interpreter legs stay, as did the native signal-disposition legs beside `self_host_signal_ir_test.go`. `cover` and `selfhost_coverage_fuzz` compile with the self-host's own `-cover` (#11140), and the nightly fuzz lane's instrumented compiler is the self-host compiler instrumenting itself |
| `wasm_leakcheck`'s `buildLeakCheckComponent`, behind the wasm census legs of 47 files (`runLeakCheckWasm`, the socket, poll, clock and http censuses, `writer_bytes`) | `wasmbin.BuildWithOptions` with the leak census or the sanitizer on | DONE: the census core compiles with the self-host (`CompileSelfHostSource` with `FERN_LEAKCHECK=1` or `FERN_SANITIZE=1`). Moving it found two self-host gaps in `wasm_ir.fern`, fixed first: under `FERN_SANITIZE` the wasm emitter printed no `fern-sanitizer: leak` verdict, and a core run with `--invoke main` never reached the reporting `$proc_exit`, so the exported `main` now reports on its return (`TestSelfHostWasmSanitizeLeakVerdict`, `TestSelfHostWasmLeakcheckReportsOnInvoke`). A program whose host surface is preview 2 (sockets, clocks, http) becomes a wasi:cli/run component through the preview-1 adapter, `e2eharness.AdaptPreview1Component`, the route the `TestSelfHostWasm*Census` twins already took and now share. `TestWASMStrAppendRangeAllocsCollapse` followed once the self-host fused `a + slice_unchecked(s, lo, hi)` into `__fern_str_grow_range` (#11327) |
| `wasm_cow`, `wasm_heap_exhaustion`, `identity_environ`, `fsmeta_primitives`'s `buildPreview1Module` (behind 12 files), `TestWASMComponentGoEncoderRunsLangCore` | `wasmbin.BuildWithOptions` for a bare cli/run component or a preview-1 core | DONE: the programs compile with the self-host (`runWasm`, `buildCLIComponent`, `CompileSelfHostSource` for the preview-1 core). The CoW dec case read `__rc_get`, a native probe, and is a census assertion now. `TestWASMComponentGoEncoderRunsLangCore` went: the self-host core of a trivial program imports `proc_exit`, so nothing is left for an import-free lift, and the Go encoder lifting a self-host core runs in the `wit_export_*` tests. `TestAllocGrowFailureTrapsSelfHostWasm`'s source scan went too: the runtime test it stood in for, `TestWASMHeapExhaustionTrapsInTheAllocator`, runs the self-host |
| `ExitSanitizer` / `ExitArenaExhausted` in `arena_exit_code`, `sanitizer`, `map_drop_sanitize`, `arm64_darwin_sanitizer`, `x86_64_ssa_handles`, and `internal/e2eselfhost`'s duplicate; `e2eharness/arm64.go`'s emit options | the Go backends' constants and `arm64codegen.Options` | DONE: `e2eharness.ExitSanitizer` and `e2eharness.ExitArenaExhausted` are the one copy the tests read; the lockstep test scans the self-host emitters against them, and the rows checking native's constants went. The high-heap seam is a flag on the compiler's environment |
| `native_wx`, `native_pie`, `arm64_native`, `arm64_sp_adjust_native`, `crc32_cksum_abi`, `TestArm64HighHeapProbeRaisesTheHint`, and the seven direct `arm64codegen.Emit` sites in `arm64_test.go` (`TestArm64Args`, `TestArm64HttpHandler`, `TestArm64TailCall`, `TestArm64ReadLine`, `TestArm64ReadLineBuiltin`, `compileArm64InDir`, `TestArm64DarwinBuilds`) | the Go arm64 and x86-64 emitters' text, or the Go assemblers' two ELF layouts (`AssembleProgramWX` against `AssembleProgram`, `AssembleProgramPIE` with its relocations) | DONE. The runtime tests compile with the self-host through `compileArm64Bin`: `args()`, the HTTP handler, the deep tail call, both `read_line` paths and the seeded-directory file tests. The assertions on the Go emitters' and assemblers' own output went, each already pinned on the self-host's: the W^X two-segment image is the only layout the self-host emits and every arm64-linux run exercises it; the PIE is `-target arm64-android` in `TestSelfHostConstAggregateArm64PIE` (a `dyn` vtable case added, the relocated constant the native self-reloc test covered) and `TestSelfHostCLIX86_64/emit-target-arm64-android`; the register-form SP adjust is `TestArm64LargeFrame` on the self-host and the encoding row in `TestSelfHostArm64FormDiff`; the hint knob is `TestSelfHostArm64HighHeapProbeRaisesTheHint`; `TestSelfHostArm64Crc32CksumKeepsCalleeSavedRegisters` scans the self-host's `__fern_crc32_cksum` for x18 and x19..x28 the way the native test scanned the Go emitter's; the Mach-O build is `TestSelfHostArm64DarwinBuilds`; `TestSelfHostTcoIR` gained the arm64 leg the deleted `bl sum_to` count stood for. Re-pointing `TestArm64HttpHandler` found #11377, fixed in the same change: both self-host assemblers reserved one `.bss` slot for a `.quad` value list, so the task runtime's four-word state block had the heap allocator's globals on top of it and every self-host-built arm64 server faulted on its first request; the self-host HTTP handler test gained the arm64 leg that would have caught it. Behind it was #11386: the leak census counted the task runtime's retained record table and records, so every native HTTP census had been red since #11370; the native emitters now take those runtime-owned blocks back out of the census as they do the string builder's buffer. `x86_64.AssembleProgramPIE` and `elf.StaticPieExecutableX86` went with their only callers; the x86-64 `Options.PIE` prologue has no `-target` and goes with the emitter in step 5 |
| `x86_64_native_test.go`'s `compileAndRunX86Native` (20 files), `compileToX86Asm` behind the x86-64 emitted-text assertions in `bounds_elision`, `optimize_cleanup_native`, `rc_str_append_range`, and the native twin `dyn_enum_dispatch_native` | `x86_64.Emit` assembled and linked in-process, or its text read | DONE: the 20 files' programs compile with the self-host through `compileAndRunX86_64` (all 33 tests green on the first measurement); the helper and the nine `TestX86_64Native*` programs the self-host corpus already covers went, and the two map-field rebind regressions (#2763, #4871) stay as `TestX86_64MapFieldStructRebind*`. The emitted-text assertions went to the self-host's own: the bounds-check elision is `TestSelfHostBoundsElideIRX86_64`'s `__fern_oob_abort` differential, which gained the `for x in xs` shape; constant folding is `TestSelfHostConstExprFoldShape`; the fused-append guard in `TestStrAppendRangeTrap` restated what `internal/ir/rc_str_append_range_test.go` pins. `dyn_enum_dispatch_native` was the native leg of `TestSelfHostDynEnumIR*`'s cases. `x86NativeRunner` stays in `x86_64_runner_test.go`: it is how a test execs an x86-64 binary off amd64, not a Go-emitter caller |
| `shared_lib` (18 tests) and its `compileToX86AsmExports` | `x86_64.Emit` with `Options.Exports`, `x86_64.AssembleProgramShared`, `elf.SharedLibraryX86` | open until the self-host has `-shared` or #4451 drops it: the self-host's four targets are executables |
| `internal/e2eharness`'s link path for self-host-emitted asm: `nativeLinkX86` / `nativeLinkArm64` in `CachedLink` and `BuildBinArm64`, `NativeLinkArm64` behind `TestSelfHostArm64ModloadNativeBuild`'s execution check, and their gcc-equivalence gate `self_host_nativelink_test.go` | `x86_64.AssembleProgram` / `arm64.AssembleProgramWXEntry` and `elf.StaticExecutableData*` to assemble and link a listing in-process, under `withEmitMemLimit` | DONE: every link is gcc. The x86 attempt had failed on every self-host listing since the self-host settled on AT&T syntax, so gcc was already the only x86 link; the arm64 attempt carried the stage-2 self-compile, whose listing is now 102 MB and which GNU as assembles in 9 s at a 590 MB peak (the x86-64 listing: 100 MB, 5 s, 392 MB), against 723 MB and 1.3 GB for the self-host's own assemblers. The reservation (`gccBigLinkWeightMB`) is sized to those figures. The modload test's execution check now wants the aarch64 cross toolchain, which every lane running it installs. `withEmitMemLimit` stays for the asm benches' in-process emit until step 5 takes them. |
| 69 files (200 tests) that run `cmd/fern` through `buildFernCLI` (`fip_*`, `bench_module`, `array_pipeline_baseline`, `module_name_mangle`, the `abort_*`, `debug_symtab`, `dwarf`, `native_socket_*`, `native_tcp_census`, `reactor_socket`, `sim_driver`, `stat_fields`, the `arm64_darwin_*`, the `*Wasm` legs of the socket and reactor files, …) | the Go CLI's native compile: `x86_64.Emit` / `arm64.Emit` / `wasmbin.Build*` plus the in-process assembler, behind the `-target` flag; `-interp` and `-check` in the same files are the oracle and stay Go | measured 2026-10-04 with a shim that sent every `-target` compile to the current `fern.fern` (built by the pin) with the stdlib root appended, and `-interp` / `-check` / the literate modes to the Go CLI: **137 pass, 34 fail, 29 skip** (the Darwin execution lane, as before). The 34 by cause, each a self-host gap with its issue unless marked: silent bounds aborts, no backtrace, no `-backtrace` flag (6 tests, #11405, fixed: the self-host reports every fatal abort as native does); no DWARF under `-g` (8, #11410: the compilation unit and its subprogram DIEs landed; the line table, the variables and the types are open); `reactor_new`, `sleep_ms`, `udp_sendto_bytes` and `poll` refused by the component framing (6, #11411, fixed: the timer and pollable builtins compose against the fern world like the sockets, and a component's sleep is a timer pollable held to completion); `-shared` (4, the `shared_lib` row above); the Mach-O `-g` symbol table (2, #11409, fixed); a flag after the entry file accepted (1, #11406, fixed: driver flags end at the entry file, and a later one is refused in native's words); E066 without the file path (1, #11407, fixed: every diagnostic in the entry module names the file the command line did); `-emit command-module` unknown (1, #11408, fixed: the spelling names the self-host's core module, which is already the preview-1 command); no `FERN_IR_VERIFY` coverage line (1, #11412, fixed); `-sanitize` as a flag rather than `FERN_SANITIZE=1` (1, #7976's driver half, fixed: the CLI takes the flag and prints native's coverage note). Three go with the backends or re-bank: `TestBytesFloor` and its Darwin twin read `b as usize` as the data pointer, which #8799 decided against (with `TestBytesFloorWasm`, step 3); `TestArm64AndroidCLI` wants a `PT_DYNAMIC` the self-host's PIE has no use for, since it self-relocates at `_start` (#7112) — the W^X, ET_DYN and runs-under-qemu checks hold; `TestArrayPipelineOwnMapReusesTheDonor`'s premise row toggles native's in-place pass (`FERN_NO_ARRAY_INPLACE`), which the self-host does not have because its `own xs` through `xs.map(f)` already allocates nothing — the "what it bought" row holds. The re-point itself is step 6: these tests call the Go CLI as a user would, and the launcher that execs the self-host for `-target` is what makes them self-host tests, so they wait for it rather than each growing a stdlib-root argument. The gap PRs do not. |

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
3. **DONE: the re-point.** The three helpers compile with the current
   self-host: `compileSelfHostProgram` runs
   `fern -target <t> -o <bin> main.fern <stdlib>` with `fern.fern` built by
   the pin for the host's own target, and `FERN_HIGH_HEAP=1` in the
   compiler's environment for the high-heap gate. The 29
   native-instrumentation tests went with it, and so did three that pin
   native's reading of `b as usize` on a `u8[]` as the address of its first
   byte, which #8799 leaves open: `TestBytesFloorWasm` and the x86-64 and
   arm64 legs of the `__memcpy` size-class test. `TestArrayGrowSizeOverflowAborts`
   keeps only its wasm leg: the Linux legs pinned native's refusal of a
   request past 32 bits, where the self-host sizes it in 64 bits and runs
   the program to its answer.
4. **The direct-caller PRs**, one per row of the table above.
5. **The deletions.** `internal/codegen/{x86_64,arm64,wasmbin}` (the SSA pair
   is gone, with `internal/ssa` and `internal/semir`),
   `internal/native/{x86_64,arm64}` (the assembler the tests link with;
   `internal/native/elf` and the Mach-O writer stay if `cmd/fern` keeps a
   Go link path), `cmd/dump_arm64`, and
   `internal/sourcelint`'s codegen-boundary population. `docs/TEST-GATES.md`
   loses its native rows and `docs/BACKEND-PARITY.md` its per-backend table.
6. **What `cmd/fern` becomes** is decided on #4451: as thin as possible. Go
   keeps the parser, checker and interpreter, which the oracle needs;
   `-fmt`, the LSP and every `-target` compile are the self-host's, reached
   through the launcher `go install` builds. It does not gate steps 1 to 4.
   Two things the launcher has to supply that the 2026-10-04 measurement
   above made concrete: the self-host binary itself (where it is found or
   built), and the stdlib root, which the self-host CLI takes as a positional
   after the entry file and which the Go CLI's users never pass because Go
   embeds the stdlib. The 69 `buildFernCLI` files are the gate for the
   launcher: 137 of their tests pass through the self-host today, and the
   rest wait on the gap issues the row names.

CI lanes keep their names: `test-e2e-x86_64`, `test-e2e-arm64` and
`test-e2e-wasm` select by target prefix, which stays the right split when the
target is compiled by the self-host. The "native test runners" wording goes
in step 5. Since step 3 every `internal/e2e`
lane builds `fern.fern` once from the driver cache, and a self-host change
runs them; `test-fernsmith` and `examples` never read the self-host and keep
skipping a change to it.
