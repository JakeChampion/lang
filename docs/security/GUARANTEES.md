# Verified guarantees

Status: [reference] the guarantees the compiler makes, for [SEC-050](https://github.com/JakeChampion/lang/issues/13552).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Gate: `TestSecurityClaimsNameLiveGates` fails when a test, a case or a lane this file names does not exist.

## How to read a row

A row is a claim, the test that proves it, and the hosts the test ran on.
A claim without a test is not on this list.
`x86` is a native x86-64 Linux host, and `arm64` is QEMU user mode unless the row says native. `darwin` is the macOS runner, and `wasm` is wasmtime.
QEMU establishes emulated behaviour only.

A hardware mitigation row says where the fault test ran. A host without the feature reports the claim as unverified, never as passed.

## Language and checker

| Claim | Test or case | Hosts |
|---|---|---|
| An out-of-range index, slice or write aborts the program with exit 134 on every backend | `conformance/cases/oob_index_wrapped` and the bounds rows of `ADVERSARIAL-SUITE.md`, `TestArrayBoundsCheck` | interp, x86, arm64, wasm |
| Integer arithmetic wraps and never traps, with the same result on every backend | `TestIntOverflowingX86_64`, `TestIntOverflowingArm64`, `TestIntOverflowingWasm`, `TestIntOverflowingInterp` | x86, arm64, wasm, interp |
| An unsafe function is callable only inside an unsafe block, cannot become a value, and cannot implement a safe trait method | `conformance/cases/diag_e095`, `conformance/cases/diag_e095_unsafe_function_as_a_value`, `conformance/cases/diag_e095_safe_trait_method_unsafe_impl` | both checkers |
| A raw builtin that forges an address, changes a count or a length, or calls foreign code is callable only inside an unsafe block | `conformance/cases/diag_e095_raw_builtin_outside_unsafe`, with the control `conformance/cases/unsafe_block_accepts_raw_builtins`, `TestUnsafeIntrinsicsMatchSelfHost` | both checkers |
| A consumed owned value cannot be used again, through a closure or a handle | `conformance/cases/diag_e050_closure_consumes_captured_owned`, `conformance/cases/diag_e051_handle_closed_twice` | both checkers |
| A deferred consuming close is a handle's last use, and a field moves out of a local or an `own` parameter only at the path's last use | `conformance/cases/handle_defer_closes_at_exit`, `conformance/cases/diag_e051_handle_closed_after_defer`, `conformance/cases/handle_field_closes_from_local`, `conformance/cases/diag_e051_handle_field_closed_twice` | both checkers |
| A consuming close inside a loop is a handle's last use only when every path from it leaves the loop | `conformance/cases/handle_closes_on_loop_exit`, `conformance/cases/diag_e051_handle_closed_before_continue`, `conformance/cases/diag_e051_handle_closed_each_round`, `conformance/cases/diag_e051_handle_closed_in_loop_head` | both checkers |
| A match or a for-each takes a payload only from an owned scrutinee: a local at its last use, an `own` parameter or a fresh call | `conformance/cases/handle_payload_closes_from_local_match`, `conformance/cases/handle_payload_field_closes`, `conformance/cases/handle_elements_close_in_for_each`, `conformance/cases/diag_e051_handle_matched_after_payload_closed`, `conformance/cases/diag_e051_handle_payload_field_of_borrowed`, `conformance/cases/diag_e051_handle_elements_closed_from_borrowed_array` | both checkers |
| Every compile verifies its IR unless `FERN_IR_VERIFY=0` | `TestSelfHostIRVerifyGateRefuses`, `TestVerifyGateRunsOnNativeCompile` | x86 |

## Memory

| Claim | Test or case | Hosts |
|---|---|---|
| An array box never exceeds 2^28 - 1 words, so no payload reaches 2^31 bytes | `TestSelfHostArrayCeiling` | x86, arm64 |
| An allocation whose size wraps the heap pointer aborts | `TestSelfHostAllocRefusesWrappedBump` | x86, arm64 |
| A raw free of an address outside the arena aborts | `TestSelfHostRawFreeOutsideHeap` | x86, arm64 |
| Under `-sanitize`, a use after free, a double free and a leak are reported | `TestX86_64SanitizeUseAfterFreeReported`, `TestX86_64SanitizeDoubleFreeReported`, `TestX86_64SanitizeLeakVerdict` and their arm64 rows | x86, arm64 |
| `crypto.erase` zeroes a secret before its block is reused | `TestSelfHostEraseZeroesStorage` | x86, arm64 |
| `-heap-limit` bounds the arena, and a program past it exits 125 | `TestSelfHostHeapLimit` | x86, arm64 |

## Stack, control flow and the image

| Claim | Test or case | Hosts |
|---|---|---|
| Unbounded recursion and a frame larger than the guard report a stack overflow | `TestSelfHostStackOverflow`, `TestSelfHostFrameProbes` | x86, arm64 |
| A startup call the kernel refuses stops the program before `main` with its cause | `TestSelfHostStackGuardDenial`, `TestSelfHostSignalStackMapDenial`, `TestSelfHostSignalStackDenial`, `TestSelfHostFaultHandlerDenial` | x86, arm64 |
| Under `-harden`, a write over a frame's saved registers reports a canary mismatch | `TestSelfHostStackCanary` | x86, arm64 |
| Under `-harden`, an indirect call to an address that is not an address-taken function aborts | `TestSelfHostForwardEdgeCFI` | x86, arm64 |
| Under `-harden` on x86-64, the program asks the kernel for the shadow stack and locks it | `TestSelfHostShadowStack` | x86; the fault is unverified on a host without the feature |
| Under `-harden` on arm64, every function value's stub is a BTI landing pad and every framed function signs its return address | `TestSelfHostHardenArm64` | arm64 emulated; native unverified |
| No load segment is writable and executable, the stack is not executable, and a shared object's tables are read-only after relocation | `TestSelfHostImagePermissions`, `TestSharedObjectLayout` | x86, arm64 |
| A `-pie` image loads at a base the kernel chooses | `TestSelfHostPIE` | x86, arm64 |
| A build is reproducible across source roots, directories, locales, time zones and environments. A `-g` build is reproducible with `-remap-path-prefix` | `TestSelfHostReproducibleAcrossPaths`, `TestSelfHostRemapPathPrefix` | x86, arm64, Darwin and Android images, WASI component |
| Every native executable carries a build manifest that `fern -manifest` reads back | `TestSelfHostProfiles` | x86, arm64 |

## Runtime, standard library and isolation

| Claim | Test or case | Hosts |
|---|---|---|
| An interrupted or short transfer keeps the bytes already moved | `TestSelfHostWriteAllContinuesAfterInterrupt`, `TestSelfHostReadAllContinuesAfterInterrupt` | x86, arm64 |
| A temporary name comes from kernel entropy, and no descriptor leaks to a child | `TestSelfHostTempFiles` | x86, arm64 |
| A JSON document deeper than 128 levels, an invalid UTF-8 sequence and a format width past one million are refused or saturated | `tests/stdlib/parse_limits_test.fern` | x86, arm64, oracle |
| A sandboxed program cannot gain privilege through a setuid helper, and a system call outside the allowlist is `SIGSYS` | `TestSeccompNoNewPrivsBlocksSetuidEscalation`, `TestSeccompFilterDenies` | x86 |
| `std/mitigations` reports hardware mitigations from the kernel's records only | `TestSelfHostMitigationsReport` | x86, arm64 |

## Toolchain and packages

| Claim | Test or case | Hosts |
|---|---|---|
| Hostile source, a hostile op stream, hostile assembly text and a hostile image never crash or hang the toolchain | `FuzzSelfHostFrontEnd`, `FuzzSelfHostIRVerify`, `FuzzSelfHostX86Assembler`, `FuzzSelfHostArm64Assembler`, `FuzzSelfHostManifest` | x86 |
| An archive whose bytes do not match the declared hash, or whose entries escape the root, is refused before anything is unpacked | `TestShippedFernFetchRefusals`, `TestFetchRejectsHashMismatch`, `TestFetchRejectsEscapingEntries` | x86, host |
| A lock below the manifest's demand is refused | `TestSelfHostStaleLockRefused`, `TestLoadStaleLockErrors` | x86, host |
| An index whose signature does not verify against the manifest's `index_key` is refused before the index is read | `TestSelfHostSignedIndexResolve`, `TestResolveSignedIndex` | x86, host |
| The pinned stage0 is refused when its bytes do not match the lock, and the self-built compiler reproduces itself byte for byte | `TestPinnedStage0WithWrongHashIsRejected`, `.github/workflows/bootstrap.yml` | host, three hosts in CI |

## Policy

The owner decided these on 2026-10-29. Each one bounds what a row above can claim.

- **Speculation.** Speculation attacks are outside the threat model. A Fern process runs only the code it was compiled from. No boundary inside it separates two trust domains. No speculation barrier is emitted, and no row claims one. The policy changes when a Fern program loads code it did not compile, such as a plugin loader or a sandbox for untrusted code. That change is an owner decision, with its cost measured in `PROFILE-COSTS.md` first.
- **Hardware claims.** A hardware claim needs a physical run. A hardware mitigation row (PAC, BTI, MTE, the user shadow stack) stays emulated or unverified until its fault test runs on a physical host. The host's kernel must enable the feature. PAC needs a core with ARMv8.3 (`paca` in the processor flags), and BTI and MTE need ARMv8.5 (`bti`, `mte`). The user shadow stack needs CET (`user_shstk`) and a kernel of 6.6 or newer. A row moves to native when the test log says its control passed natively, and the mitigation's record names the run. The native ARM shard gives that record for PAC today.
- **Threads.** Share-nothing workers are the thread model. A thread never shares a heap. A worker owns its heap, and a value crosses a worker boundary by copy at spawn, send and join. Reference counts stay non-atomic. The design is `docs/MULTICORE-RESEARCH.md`, design (a). Until workers ship, a thread that shares the heap is not supported.

## Not guaranteed

- Raw builtins (`__alloc`, `__store_*`, `__load_*`, `__free`, `__memcpy`, `__c_call*`, `__arr_set_len`) are callable from every source until the stage0 pin learns unsafe blocks (`UNSAFE-INVENTORY.md`).
- A thread that shares the heap is not supported. Reference counts are not atomic.
- No signature covers a package archive. An index file is verified only when the manifest names `index_key` (`PACKAGE-INTEGRITY.md`).
- An index rollback is refused only after a lock records the index serial. Nothing refuses a frozen index.
- The fault tests ran on a physical host for PAC only. BTI, MTE and the user shadow stack carry emulated evidence or none.
- Nothing mitigates a speculation attack.
- The seccomp sandbox exists on x86-64 only.
