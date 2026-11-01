# Adversarial security regression suite

Status: [reference] index for [SEC-047](https://github.com/JakeChampion/lang/issues/13549).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Gate: `TestAdversarialSuiteIndexNamesLiveTests` fails when a test, a fuzz target or a case this file names does not exist.

## Rule

Every row pairs an attack-shaped input with the outcome the program must have.
The outcome is a refusal with a documented diagnostic, an abort with a documented cause, or a signal the test expects.
Every negative test has a control that passes the same path with a valid input.
A test that cannot run on a host logs the reason and reports the claim as unverified.
It never reports a pass for a check it did not run.

The "Hosts" column names where the test executes.
`x86` is the native x86-64 host, `arm64` is QEMU user mode, and `wasm` is wasmtime.
QEMU establishes emulated behaviour only.

## Bounds and arithmetic

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| Index past the length, below zero, or made negative by wrapped arithmetic | `conformance/cases/oob_index_wrapped`, `conformance/cases/oob_index_below_guard`, `conformance/cases/oob_index_wrapped_guard`, `conformance/cases/oob_write_wrapped_index` | abort, exit 134 | interp, x86, arm64 |
| Slice bounds past the view, a negative start, or a wrapped end | `conformance/cases/oob_slice_negative_start`, `conformance/cases/oob_slice_wrapped_end`, `conformance/cases/oob_view_index_past_view` | abort, exit 134 | interp, x86, arm64 |
| A loop guard that proves nothing about the index | `conformance/cases/oob_loop_guard_past_length` | abort, exit 134 | interp, x86, arm64 |
| Index arithmetic that a wrapped product makes in range | `conformance/cases/index_wrapped_product_rejected` | the check reads the value | every engine |
| Bounds elision that must not remove a needed check | `TestSelfHostBoundsElideIRX86_64`, `TestSelfHostBoundsElideIRArm64`, `TestSelfHostBoundsElideIRWasm`, `TestX86_64BoundsElisionCorrect`, `TestArm64BoundsElisionCorrect`, `TestWASMBoundsElisionCorrect` | the kept check aborts | x86, arm64, wasm |
| A capacity above 2^28 words or a length past the string ceiling | `TestSelfHostArrayCeiling`, `TestSelfHostConcatPastLengthCeilingAborts`, `TestRepeatPastLengthCeilingAborts`, `TestAllocSizeOverflowAborts`, `TestArrayGrowSizeOverflowAborts` | abort with the allocation size cause | x86, arm64 |
| An allocation whose size wraps the heap pointer | `TestSelfHostAllocRefusesWrappedBump` | abort with the allocation size cause | x86, arm64 |
| A product of shape dimensions that overflows | `TestSelfHostOuterMulOverflow`, `TestOuterMulRejectsOverflowBeforeAllocation`, `TestSelfHostNdarrayProductShapeOverflow` | refusal before the allocation | x86, arm64, wasm |

## Memory lifetime

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| A read through a released box | `TestX86_64SanitizeUseAfterFreeReported`, `TestArm64SanitizeUseAfterFreeReported`, `TestX86_64SanitizeMapHandleUseAfterFree`, `TestArm64SanitizeMapHandleUseAfterFree` | `-sanitize` report, exit 134 | x86, arm64 |
| A second release of one box | `TestX86_64SanitizeDoubleFreeReported`, `TestArm64SanitizeDoubleFreeReported`, `TestSelfHostDoubleFreeSilentWithoutSanitizeX86_64` | `-sanitize` report; the release build counts and continues | x86, arm64 |
| A released block reused before the sanitizer can see it | `TestX86_64SanitizeQuarantinesFreedBlocks`, `TestArm64SanitizeQuarantinesFreedBlocks`, `TestSelfHostSanitizeBoxFreeIsQuarantined`, `TestSelfHostUafQuarantineAsmContractX86_64`, `TestSelfHostUafQuarantineAsmContractArm64`, `TestSelfHostSanitizeQuarantineGrowth` | the block stays quarantined; the growth is pinned | x86, arm64 |
| A raw free of an address outside the arena | `TestSelfHostRawFreeOutsideHeap` | abort with the out-of-heap cause | x86, arm64 |
| A secret left in a released block | `TestSelfHostEraseZeroesStorage` | `crypto.erase` zeroes it; the control reads it back | x86, arm64 |
| A leak at exit | `TestX86_64SanitizeLeakVerdict`, `TestArm64SanitizeLeakVerdict`, `TestSelfHostWasmSanitizeLeakVerdict` | `-sanitize` verdict | x86, arm64, wasm |
| A handle closed twice or used after its close or transfer | `conformance/cases/diag_e051_handle_closed_twice`, `conformance/cases/diag_e051_handle_read_after_close`, `conformance/cases/diag_e051_handle_used_after_transfer` | `E051` | both checkers |
| A closure that consumes a captured owned value the caller uses later | `conformance/cases/diag_e050_closure_consumes_captured_owned` | `E050` | both checkers |

## Stack and control flow

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| Unbounded recursion into the guard | `TestSelfHostStackOverflow`, `TestArm64DarwinStackOverflow` | `fern: stack overflow`, exit 134 | x86, arm64, darwin |
| A frame larger than the guard | `TestSelfHostFrameProbes` | the probe loop reaches the guard; overflow report | x86, arm64 |
| A write past a frame box into the saved registers | `TestSelfHostStackCanary` | `fern: stack canary mismatch`, exit 134 under `-harden` | x86, arm64 |
| A forged function pointer | `TestSelfHostForwardEdgeCFI` | `fern: indirect call to an address that is not a function`, exit 134 under `-harden` | x86, arm64 |
| A branch to the middle of a function on a BTI core | `TestSelfHostHardenArm64` | `SIGILL`; unverified on a host without BTI | arm64 |
| An overwritten return address | `TestSelfHostShadowStack`, `TestSelfHostHardenArm64` | `SIGSEGV` where the shadow stack or PAC is active; the test logs unverified elsewhere | x86, arm64 |
| A corrupted frame pointer during a report | `TestSelfHostAbortBacktraceCorruptFrame` | the walk stops; exit 134 with the cause | x86, arm64 |
| A startup call the kernel refuses | `TestSelfHostStackGuardDenial`, `TestSelfHostSignalStackMapDenial`, `TestSelfHostSignalStackDenial`, `TestSelfHostFaultHandlerDenial` | the cause and exit 134 before `main` | x86, arm64 |

## Image and loader

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| A store into the program's own code | `TestSelfHostImagePermissions`, `TestSelfHostX86WXExecutable` | `SIGSEGV` on every variant | x86, arm64 |
| An entry point moved into the data segment | `TestSelfHostImagePermissions` | `SIGSEGV` | x86, arm64 |
| A shared object with a writable code segment or writable dynamic tables | `TestSharedObjectLayout` | the writer emits none; `PT_GNU_RELRO` covers the tables | x86, arm64 |
| An image at a fixed address | `TestSelfHostPIE` | `-pie` images load at a random base | x86, arm64 |
| A malformed image handed to the harness inspector | `scripts/test_security_smoke.py` | the inspector refuses before it reports a property | host |
| A malformed image handed to `fern -manifest` | `FuzzSelfHostManifest` | the fields, or the refusal naming the file | x86 |

## Hostile input to the toolchain

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| Hostile source text | `FuzzParse`, `FuzzCheck`, `FuzzSelfHostFrontEnd` | a diagnostic, never a signal or a hang | host, x86 |
| A hostile op stream | `FuzzSelfHostIRVerify` | a problem list from every verifier | x86 |
| Hostile assembly text | `FuzzSelfHostX86Assembler`, `FuzzSelfHostArm64Assembler` | a summary or an abort with a cause | x86 |
| An ELF file supplied as source | the probes in `ELF-INPUTS.md` | `P001` | x86 |
| A malformed attribute in a format directive | `TestSelfHostFmtWriteRefusesMalformedAttribute` | refusal | x86 |
| An unsafe function reached without an unsafe block | `conformance/cases/diag_e095`, `conformance/cases/diag_e095_unsafe_function_as_a_value`, `conformance/cases/diag_e095_import_conceals_unsafe_function`, `conformance/cases/diag_e095_safe_trait_method_unsafe_impl` | `E095` | both checkers |
| A raw builtin called without an unsafe block, in a safe function and after an unsafe block that ends | `conformance/cases/diag_e095_raw_builtin_outside_unsafe`, control `conformance/cases/unsafe_block_accepts_raw_builtins` | `E095` | both checkers |

## Hostile input to the standard library

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| Deep JSON, invalid UTF-8, out-of-range numbers, and a format width that asks for gigabytes | `tests/stdlib/parse_limits_test.fern` | the limit answers; the width saturates | x86, arm64, oracle |
| Oversized headers, bodies and frames at the server | `TestSelfHostServeLimits`, `TestServeH3PeerLimitX86_64` | refusal at the limit | x86 |
| A predictable temporary name, a symlink in its place, or an inherited descriptor | `TestSelfHostTempFiles` | random names, `NOFOLLOW`, `O_CLOEXEC` | x86, arm64 |

## Packages and bootstrap

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| An archive whose bytes do not match the declared hash | `TestFetchRejectsHashMismatch` | refusal, nothing unpacked | host |
| An archive entry that escapes the package root | `TestFetchRejectsEscapingEntries` | refusal | host |
| A hash spelled to reach a path outside the store | `TestDirRejectsTraversalAndMalformedHex`, `TestFetchRejectsMalformedHash`, `TestReadLockRejectsMalformedHash`, `TestParseIndexRejectsMalformedHash`, `TestLockHashOutsideStoreFailsBuild` | refusal at the parse boundary | host, x86 |
| A pinned stage0 whose bytes do not match the lock | `TestPinnedStage0WithWrongHashIsRejected` | the bootstrap refuses | host |

## Isolation

| Attack | Test or case | Outcome | Hosts |
|---|---|---|---|
| A system call outside the allowlist | `TestSeccompFilterDenies`, `TestSeccompFilterIsLoadedAtRuntime` | `SIGSYS` | x86, native arm64 |
| A setuid helper run from a sandboxed process | `TestSeccompNoNewPrivsBlocksSetuidEscalation` | the helper keeps the caller's user | x86, native arm64 |
| A program that allocates past its quota | `TestSelfHostHeapLimit` | exit 125 with the heap exhausted cause | x86, arm64 |

## Claims that must not be false

| Claim | Test or case | How the test keeps it honest | Hosts |
|---|---|---|---|
| The processor, the kernel and the process state of PAC, BTI, MTE, the shadow stack and IBT | `TestSelfHostMitigationsReport`, `tests/stdlib/mitigations_test.fern` | every cell comes from the kernel's records; the Go test reads the same files | x86, arm64 |
| The compiled, requested and enabled state of each mitigation in a built program | `TestSelfHostProfiles` | the manifest is read from the image, the rows from the process | x86, arm64 |
| A hardware fault test | `TestSelfHostHardenArm64`, `TestSelfHostShadowStack` | the log names native or emulated execution; an unsupported feature stays unverified | x86, arm64 |
| A reproducible image | `TestSelfHostReproducibleAcrossPaths`, `TestSelfHostRemapPathPrefix` | two source roots, directories, locales and environments, byte compared. A `-g` image must differ without a remap. A malformed remap is refused | x86, arm64 |

## Gaps

- The fault tests did not run on a native host with PAC, BTI, MTE or the user shadow stack. The arm64 rows are QEMU rows.
- The seccomp filter exists on Linux only. QEMU user mode does not emulate it, so the arm64 tests run on the native arm64 lane.
- The fuzz targets that drive a self-host binary get no coverage feedback. Go's instrumentation does not see into a Fern process.
- Package archives carry a hash and no signature ([PACKAGE-INTEGRITY.md](PACKAGE-INTEGRITY.md)).
