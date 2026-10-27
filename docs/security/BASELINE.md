# Native compiler security baseline

Status: audit for SEC-001, dated 2026-10-26.
Parent: [security epic #13502](https://github.com/JakeChampion/lang/issues/13502).
Source baseline: `0e2176c40c6e61e522cfa3ef204e4a4f0555933b`.

## Scope and trust boundaries

The security program covers Linux ELF64 on x86-64 and AArch64.
The input contract comes from `native_compiler_security_codex_handoff.zip`.
Its 50 tasks describe acceptance requirements, not the current implementation.

The active compiler lives in `compiler/` and uses Fern source.
`compiler/fern_main.fern` provides the CLI.
`compiler/fern.fern` provides the compiler driver.
The Go code in `internal/` provides the parser, checker, interpreter, and test oracle.
The repository retired the Go native backends.
New compiler features must start in the self-host compiler.

The source pipeline crosses these trust boundaries:

1. Source text enters module resolution, package validation, and the parser.
2. The checker constructs typed semantic information and ownership facts.
3. IR passes transform those facts into executable operations.
4. SSA and machine backends assign registers and construct instructions.
5. The assemblers and ELF writer construct the executable image.
6. Startup and runtime helpers cross the Linux syscall boundary.

Relevant source anchors include `compiler/checker.fern`, `compiler/semsource.fern`, and `compiler/ownership.fern`.
IR checks live in `compiler/irverify.fern`, `compiler/irverifyrc.fern`, and `compiler/irverifystack.fern`.
`compiler/irverifygate.fern` combines those checks.
The native emitters include `compiler/asm_ir.fern` and `compiler/asm_arm64_ir.fern`.

`compiler/x86_native.fern`, `compiler/arm64_native.fern`, and `compiler/elf.fern` construct native images.
Runtime helpers use both Fern function bodies and assembly text.
`compiler/asmcore.fern` contains shared runtime definitions.

Package archives, manifests, lockfiles, and bootstrap assets form separate input boundaries.
Current hash checks do not establish a complete signature or rollback policy.
The ELF writer does not establish a general hostile ELF object reader.
SEC-026 must first enumerate the binary inputs that the toolchain accepts.

## Threat model

The adversary can supply source, dependencies, package archives, program input, and files that tools parse.
Raw memory code, FFI code, and runtime defects can corrupt memory.
Compiler defects can invalidate source-level checks.
Optimization must preserve traps, ownership, and defined arithmetic.
Kernel compromise and physical attacks are outside this initial program.

The trusted base includes stage0, the compiler, its runtime, the kernel, and the selected CPU baseline.
Build tools and the CI environment also affect artifact integrity.
Self-host consistency cannot exclude a stable compiler defect or a compromised bootstrap.

## Current contract and limits

Fern uses Perceus reference counts and ownership modes.
This program must audit those rules against the requested safety guarantees.
It must not assume Rust ownership semantics.
The runtime uses non-atomic reference counts.
Unrestricted shared-heap threads require a separate memory model decision.

Ordinary integer operations are total and use the current width-specific rules.
Checked operators return optional results.
See [integer semantics](../INTEGER-SEMANTICS.md).
This security program preserves those rules.
A trap-on-overflow default requires a separate language proposal.

Arrays and slices carry bounds checks with defined abort behavior.
See [array bounds](../ARRAY-BOUNDS.md).
Raw memory intrinsics can bypass these protections or corrupt their metadata.
Their availability does not establish an explicit safe/unsafe boundary.
SEC-003 must document that boundary before an incompatible implementation.

The native linkers already separate executable code from writable data.
The x86 image declares a non-executable stack.
SEC-028 covers all image variants, runtime fault tests, and RELRO applicability.
Static W^X does not prove PIE or ASLR.

`-harden` emits AArch64 PAC and BTI instructions and ELF properties.
Current QEMU tests inspect those properties and exercise fault behavior.
They do not prove physical CPU support, kernel activation, or production enforcement.
Handwritten runtime frames and other image variants require further audit.
See [HARDENING.md](../HARDENING.md).

`-sanitize` provides RC over-release, quarantine-based UAF checks, and leak reports.
It does not provide complete shadow-memory, initialization, alignment, or race coverage.
Raw reads through stale pointers can escape RC-event detectors.
See [sanitizer limits](../SANITIZER.md).

Package capability checks restrict call-graph reachability.
They do not replace kernel isolation.
Current seccomp tests do not establish complete namespace, quota, or build-script isolation.

## Platform contract

| Target | Current baseline | Security evidence limit |
|---|---|---|
| Linux x86-64 | x86-64-v3 plus AES-NI | Native tests can establish process behavior on this host only |
| Linux AArch64 | ARMv8.2-A plus crypto extensions and LSE | QEMU establishes emulated behavior only |
| Other shipped targets | Darwin, WASI, Android, and freestanding variants | Preserve their current contracts outside this program |

The CPU baselines come from `CLAUDE.md`.
A change to either baseline requires a project decision.
Physical CET, PAC, BTI, and MTE verification remains unavailable in this session.
Every report must separate compiled, supported, requested, enabled, and verified states.

## Validation protocol

The pinned toolchain comes from `mise.toml` and `mise.lock`.
`scripts/toolchain-env` installs it and emits shell exports.
`make build` builds the self-host compiler and the CLI from the pinned stage0.
`make distcheck` compares later bootstrap stages.
`make test` runs the Go suites.

Targeted suites must precede broad test runs.

The current lane selection and test limits live in [TEST-GATES.md](../TEST-GATES.md).
The security harness must reuse those suites.
It must refuse missing prerequisites and empty selectors.
Negative tests require a deterministic error or the specified trap.
A skipped test supplies no acceptance evidence.

## Baseline evidence

Host: Linux `6.18.55`, x86-64, AMD EPYC 9V74.

Toolchain: Go `1.27.3` from the repository lockfile.

Stage0: `stage0-20261025-433d93a`.

Stage0 SHA-256: `efafedd32a9223a2c87b385e8425d155d5f59c10fab7e3bf9e09509254fce748`.

| Command | Result |
|---|---|
| `make build` | Passed. Stage1 compiled a program and `coreutils/tr`. Both smoke programs ran. |
| `go test ./compiler ./internal/pkg/caps ./internal/pkg/platforms ./internal/testing/sourcelint` | Passed all four packages. |

The source audit alone closes no security issue.
Each implementation PR must add its exact commands, expected results, actual results, and CI link.
Hardware claims also require physical CPU, kernel, process activation, and fault evidence.

The targeted verifier and W^X suite also passed:

```sh
go test ./internal/testing/e2ecompiler -run '^(TestSelfHostX86WXExecutable|TestSelfHostIRVerifyGateRefuses|TestSelfHostIRVerifyGateChecks)$' -count=1 -v
```

The verifier rejected malformed IR and accepted valid control cases.
The x86 suite confirmed separate code and data, NX stack, and protection fault behavior.
These results cover the source baseline, not the full 50-task contract.

## Decisions and first tasks

SEC-001 records this baseline and the complete task assessment.
SEC-002 maps current tests to the requested matrix and supplies a strict harness.
SEC-003 preserves current arithmetic and proposes an explicit raw-memory boundary.
The unsafe syntax and migration policy require review before an incompatible implementation.
Thread transfer and speculation policy also require separate design decisions.

## Task assessment

The table measures each full handoff acceptance contract.
`partial` means relevant implementation exists but acceptance evidence remains incomplete.
`absent` means this audit found no implementation of the requested facility.
`blocked` means a language or platform decision prevents implementation.
No task qualifies as fully implemented from this source audit alone.

| ID | Issue | State | Source anchors | Acceptance gap |
|---|---|---|---|---|
| SEC-001 | [#13503](https://github.com/JakeChampion/lang/issues/13503) | partial | `docs/COMPILER-ARCHITECTURE-2026.md`, `docs/TEST-GATES.md`, `CLAUDE.md` | Record the current trust boundaries and all 50 acceptance gaps. |
| SEC-002 | [#13504](https://github.com/JakeChampion/lang/issues/13504) | partial | `.github/workflows/ci.yml`, `.github/workflows/preflight.yml`, `internal/testing/e2eharness` | Map current lanes to the security matrix and fail on missing prerequisites. |
| SEC-003 | [#13505](https://github.com/JakeChampion/lang/issues/13505) | partial | `spec/semantics.md`, `docs/INTEGER-SEMANTICS.md`, `docs/MODE-LATTICE.md` | Resolve the raw-memory boundary without silently replacing current semantics. |
| SEC-004 | [#13506](https://github.com/JakeChampion/lang/issues/13506) | partial | `compiler/ownership.fern`, `compiler/checker.fern`, `compiler/rccertify.fern` | Audit Perceus and mode enforcement against the approved safety contract. |
| SEC-005 | [#13507](https://github.com/JakeChampion/lang/issues/13507) | partial | `compiler/ssabounds.fern`, `compiler/ssabytes.fern`, `docs/ARRAY-BOUNDS.md` | Audit every array and slice path plus check elimination. |
| SEC-006 | [#13508](https://github.com/JakeChampion/lang/issues/13508) | partial | `compiler/checker.fern`, `spec/semantics.md` | Document initialization and nullable-value invariants at control-flow joins. |
| SEC-007 | [#13509](https://github.com/JakeChampion/lang/issues/13509) | partial | `docs/INTEGER-SEMANTICS.md`, `compiler/irtables.fern` | Preserve total wrapping arithmetic and audit checked operator parity. |
| SEC-008 | [#13510](https://github.com/JakeChampion/lang/issues/13510) | partial | `compiler/ownership.fern`, `docs/MUST-CONSUME.md`, `internal/testing/e2e/termios_ownership_test.go` | Audit resource declarations and native handle ownership. The audit is [HANDLE-AUDIT.md](HANDLE-AUDIT.md). |
| SEC-009 | [#13511](https://github.com/JakeChampion/lang/issues/13511) | blocked | `docs/MULTICORE-RESEARCH.md`, `docs/RC-DESIGN.md` | Approve a thread-transfer model before changes to non-atomic reference counts. |
| SEC-010 | [#13512](https://github.com/JakeChampion/lang/issues/13512) | partial | `compiler/ir.fern`, `compiler/semsource.fern`, `compiler/ssasem.fern` | Map typed ownership and checked operations to explicit IR invariants. The audit is [IR-AUDIT.md](IR-AUDIT.md). |
| SEC-011 | [#13513](https://github.com/JakeChampion/lang/issues/13513) | partial | `compiler/irverify.fern`, `compiler/irverifygate.fern`, `compiler/irverifyrc.fern` | Audit verifier coverage and each pass boundary including unmodelled operations. The audit is [IR-AUDIT.md](IR-AUDIT.md). |
| SEC-012 | [#13514](https://github.com/JakeChampion/lang/issues/13514) | partial | `internal/testing/tv`, `internal/testing/fernsmith`, `compiler/irverifygate.fern` | Extend current translation validation and execution differentials to checks. The audit is [IR-AUDIT.md](IR-AUDIT.md). |
| SEC-013 | [#13515](https://github.com/JakeChampion/lang/issues/13515) | partial | `compiler/asm_ir.fern`, `compiler/x86_native.fern`, `compiler/ssa.fern` | Audit SysV frames and raw syscall ABI separately. The audit is [ABI-AUDIT.md](ABI-AUDIT.md). |
| SEC-014 | [#13516](https://github.com/JakeChampion/lang/issues/13516) | partial | `compiler/asm_arm64_ir.fern`, `compiler/arm64_native.fern`, `compiler/ssa.fern` | Audit AAPCS64 frames and baseline instruction requirements. The audit is [ABI-AUDIT.md](ABI-AUDIT.md). |
| SEC-015 | [#13517](https://github.com/JakeChampion/lang/issues/13517) | partial | `compiler/asm_ir.fern`, `compiler/asm_arm64_ir.fern`, `internal/testing/e2ecompiler/self_host_stack_overflow_test.go` | Guard and signal setup failures stop the program. Frames above one page probe every page. `-harden` adds a stack canary. The record is [STACK-GUARD.md](STACK-GUARD.md). |
| SEC-016 | [#13518](https://github.com/JakeChampion/lang/issues/13518) | partial | `compiler/asmcore.fern`, `compiler/asm_ir.fern`, `compiler/asm_arm64_ir.fern`, `internal/testing/e2ecompiler/self_host_cfi_test.go` | Under `-harden` a function value points at a stub in the text segment, and every indirect call checks for that stub's pad. Forged targets abort. No signature check. The record is [CFI.md](CFI.md). |
| SEC-017 | [#13519](https://github.com/JakeChampion/lang/issues/13519) | partial | `compiler/irtables.fern`, `compiler/ssacapacityvalue.fern`, `compiler/ssabounds.fern` | Wrapped index and slice arithmetic is pinned as `AB-10` to `AB-14`. The box allocator refuses a payload above 2^31 - 8 bytes. The audit is [ARITHMETIC-AUDIT.md](ARITHMETIC-AUDIT.md). |
| SEC-018 | [#13520](https://github.com/JakeChampion/lang/issues/13520) | partial | `compiler/asm_ir.fern`, `compiler/asm_arm64_ir.fern`, `compiler/asmcore.fern` | Audit startup stack, auxv and syscall return handling. The audit is [STARTUP-IO-AUDIT.md](STARTUP-IO-AUDIT.md). |
| SEC-019 | [#13521](https://github.com/JakeChampion/lang/issues/13521) | partial | `compiler/asmcore.fern`, `internal/stdlib/std/mitigations.fern.md`, `internal/testing/e2e/mitigations_test.go` | Entropy completion is pinned in [ENTROPY.md](ENTROPY.md). `std/mitigations` reports each mitigation's processor, kernel and process state through `prctl_query`. The record is [MITIGATION-REPORT.md](MITIGATION-REPORT.md). |
| SEC-020 | [#13522](https://github.com/JakeChampion/lang/issues/13522) | blocked | `docs/MULTICORE-RESEARCH.md`, `compiler/asmcore.fern` | Do not expose shared heap threads before the memory model decision. |
| SEC-021 | [#13523](https://github.com/JakeChampion/lang/issues/13523) | partial | `compiler/aborts.fern`, `compiler/asmcore.fern`, `docs/ARRAY-BOUNDS.md` | Audit allocation-free fatal paths and signal stack behavior. The audit is [CRASH-REPORT-AUDIT.md](CRASH-REPORT-AUDIT.md). |
| SEC-022 | [#13524](https://github.com/JakeChampion/lang/issues/13524) | partial | `compiler/asmcore.fern`, `compiler/ssacapacitymachine.fern`, `docs/RC-DESIGN.md` | Audit size classes and raw allocation arithmetic plus mapping errors. The audit is [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md). |
| SEC-023 | [#13525](https://github.com/JakeChampion/lang/issues/13525) | partial | `compiler/asmcore.fern`, `docs/RC-DESIGN.md`, `docs/SANITIZER.md` | A raw free outside the arena aborts. A typed second release is counted. The link forgery and the raw double free stay open. The record is the metadata section of [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md). |
| SEC-024 | [#13526](https://github.com/JakeChampion/lang/issues/13526) | partial | `compiler/asmcore.fern`, `docs/SANITIZER.md` | The sanitizer quarantine is unbounded by design and its growth is pinned. A bounded quarantine, randomized placement and sampled guard pages wait for an approved policy. The record is [QUARANTINE.md](QUARANTINE.md). |
| SEC-025 | [#13527](https://github.com/JakeChampion/lang/issues/13527) | partial | `compiler/asmcore.fern`, `internal/stdlib/std/crypto.fern.md`, `compiler/fern.fern` | Fresh typed boxes are zero-filled and raw blocks are not. `crypto.erase` zeroes an owned byte array before its release, and `-heap-limit` bounds the arena. The record is [ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md). |
| SEC-026 | [#13528](https://github.com/JakeChampion/lang/issues/13528) | partial | `compiler/elf.fern`, `compiler/x86_native.fern`, `compiler/arm64_native.fern` | Define the actual accepted binary-input surface before adding a parser. |
| SEC-027 | [#13529](https://github.com/JakeChampion/lang/issues/13529) | partial | `compiler/elf.fern`, `compiler/x86_native.fern`, `compiler/arm64_native.fern` | `-pie` builds a self-relocating `ET_DYN` image on both Linux targets, and the x86-64 randomization is pinned. Physical arm64 randomization is unverified. The record is [PIE.md](PIE.md). |
| SEC-028 | [#13530](https://github.com/JakeChampion/lang/issues/13530) | partial | `compiler/elf.fern`, `compiler/fern.fern`, `internal/testing/e2ecompiler/self_host_image_permissions_test.go`, `internal/testing/e2e/shared_layout_test.go` | Every executable variant and both shared objects keep code and data apart with a non-executable stack, and the shared objects carry `PT_GNU_RELRO`. The relocated words of a `-pie` executable stay writable. The record is [IMAGE-PERMISSIONS.md](IMAGE-PERMISSIONS.md). |
| SEC-029 | [#13531](https://github.com/JakeChampion/lang/issues/13531) | partial | `compiler/elf.fern`, `compiler/asm_arm64_ir.fern`, `docs/HARDENING.md` | No thread-local storage, no PLT and no veneers exist. Both targets carry unwind tables under `PT_GNU_EH_FRAME`, and the arm64 `-harden` image the BTI property. Shared and Android images carry no note. The record is [LINK-METADATA-AUDIT.md](LINK-METADATA-AUDIT.md). |
| SEC-030 | [#13532](https://github.com/JakeChampion/lang/issues/13532) | partial | `compiler/asm_ir.fern`, `internal/testing/e2ecompiler/self_host_shadow_stack_test.go` | A hardened x86-64 program asks the kernel for the shadow stack and locks it on, and continues where the kernel refuses. The fault test runs where the feature exists, and this host has none. The record is [SHADOW-STACK.md](SHADOW-STACK.md). |
| SEC-031 | [#13533](https://github.com/JakeChampion/lang/issues/13533) | partial | `compiler/asm_ir.fern`, `compiler/x86_native.fern`, `docs/security/SHADOW-STACK.md` | Every indirect-call target of a hardened image starts with `endbr64`. Linux enforces no user-space IBT, the image carries no property note, and `std/mitigations` reports the state from the kernel. |
| SEC-032 | [#13534](https://github.com/JakeChampion/lang/issues/13534) | partial | `compiler/asm_arm64_ir.fern`, `internal/testing/e2ecompiler/self_host_harden_test.go` | Every framed function signs and authenticates its return address, and the fault control passed natively on one host. The hand-written runtime frames stay unsigned. The record is [PAC-BTI.md](PAC-BTI.md). |
| SEC-033 | [#13535](https://github.com/JakeChampion/lang/issues/13535) | partial | `compiler/asm_arm64_ir.fern`, `compiler/elf.fern`, `internal/testing/e2ecompiler/self_host_harden_test.go` | The landing pads sit on the stubs of address-taken functions only, the same set the indirect-call check accepts. Physical enforcement is recorded in the issue. Shared and Android images carry no note. |
| SEC-034 | [#13536](https://github.com/JakeChampion/lang/issues/13536) | absent | `compiler/asmcore.fern`, `compiler/asm_arm64_ir.fern`, `docs/HARDENING.md` | Not implemented. The design, its size-class cost and its QEMU verification plan are [MTE-DESIGN.md](MTE-DESIGN.md), and no CI runner has the hardware. |
| SEC-035 | [#13537](https://github.com/JakeChampion/lang/issues/13537) | partial | `internal/stdlib/std/format.fern.md`, `internal/stdlib/std/json.fern.md`, `tests/stdlib/parse_limits_test.fern` | UTF-8, length, number, depth and format bounds are pinned, and a format width saturates at one million. The record is [STRING-PARSING-AUDIT.md](STRING-PARSING-AUDIT.md). |
| SEC-036 | [#13538](https://github.com/JakeChampion/lang/issues/13538) | partial | `internal/stdlib/std/io.fern.md`, `compiler/runtime/x86-64-linux/__fern_open_res.fern`, `compiler/runtime/x86-64-linux/__fern_temp_dir.fern` | Temporary names carry kernel entropy, every file open is close-on-exec, and `io.temp_file` creates a 0600 file exclusively. The record is [FILESYSTEM-AUDIT.md](FILESYSTEM-AUDIT.md). |
| SEC-037 | [#13539](https://github.com/JakeChampion/lang/issues/13539) | partial | `internal/stdlib/std/crypto`, `tests/stdlib/*_test.fern`, `internal/testing/e2ecompiler/self_host_ct_gate_test.go` | Every primitive implements a published standard with its vectors and negative cases, the random source is the kernel, verdicts are declassified under a memcheck gate, and `crypto.erase` wipes. No Wycheproof corpus. The record is [CRYPTO-AUDIT.md](CRYPTO-AUDIT.md). |
| SEC-038 | [#13540](https://github.com/JakeChampion/lang/issues/13540) | partial | `internal/pkg/caps`, `compiler/caps.fern`, `internal/testing/e2ecompiler/self_host_caps_test.go` | Compile-time per-package grants with attenuation and `E070`, no build-time code, and the seccomp sandbox at run time. The vocabulary is coarse and static. The record is [CAPABILITIES-AND-SANDBOX.md](CAPABILITIES-AND-SANDBOX.md). |
| SEC-039 | [#13541](https://github.com/JakeChampion/lang/issues/13541) | partial | `compiler/asm_ir.fern`, `internal/testing/e2e/seccomp_test.go`, `internal/testing/e2e/seccomp_nnp_test.go` | The seccomp allowlist and `no_new_privs` are pinned, including a blocked setuid escalation. Namespaces and argument filters stay open. The record is [SANDBOX-AUDIT.md](SANDBOX-AUDIT.md). |
| SEC-040 | [#13542](https://github.com/JakeChampion/lang/issues/13542) | partial | `internal/testing/fernsmith`, `internal/testing/tv`, `docs/FUZZING.md` | Extend current fuzz targets to uncovered IR and binary boundaries. |
| SEC-041 | [#13543](https://github.com/JakeChampion/lang/issues/13543) | partial | `docs/SANITIZER.md`, `internal/testing/e2e/sanitizer_test.go` | Over-release, use after free and leaks are detected on the reference-counted path with a backtrace. Raw reads after free, redzones, uninitialised raw reads and alignment have no detector. The record is [SANITIZER-AUDIT.md](SANITIZER-AUDIT.md). |
| SEC-042 | [#13544](https://github.com/JakeChampion/lang/issues/13544) | blocked | `docs/MULTICORE-RESEARCH.md`, `docs/SANITIZER.md`, `compiler/ownership.fern` | Define diagnostic coverage after handle and concurrency contracts. |
| SEC-043 | [#13545](https://github.com/JakeChampion/lang/issues/13545) | partial | `bootstrap/bootstrap.sh`, `docs/BOOTSTRAP.md`, `internal/testing/e2ecompiler/self_host_reproducible_test.go` | Plain, `-pie` and `-harden` images are byte-identical across directories and environments on both targets. A `-g` image carries the compile directory. The record is [REPRODUCIBILITY.md](REPRODUCIBILITY.md). |
| SEC-044 | [#13546](https://github.com/JakeChampion/lang/issues/13546) | partial | `internal/pkg/pkgcache`, `internal/pkg/manifest`, `docs/PACKAGES.md` | Audit current hashes and lockfiles then scope signatures and rollback policy. |
| SEC-045 | [#13547](https://github.com/JakeChampion/lang/issues/13547) | partial | `bootstrap/stage0.lock`, `bootstrap/bootstrap.sh`, `docs/BOOTSTRAP.md` | Retain stage2/stage3 comparisons and document diverse compiler feasibility. |
| SEC-046 | [#13548](https://github.com/JakeChampion/lang/issues/13548) | absent | `docs/HARDENING.md`, `docs/SANITIZER.md`, `compiler/fern_main.fern` | Unify status reporting without changing safety semantics between profiles. |
| SEC-047 | [#13549](https://github.com/JakeChampion/lang/issues/13549) | partial | `internal/testing/e2e`, `internal/testing/e2ecompiler`, `docs/TEST-GATES.md` | Index and extend current negative tests into the complete security matrix. |
| SEC-048 | [#13550](https://github.com/JakeChampion/lang/issues/13550) | partial | `scripts`, `.github/workflows/perf.yml`, `docs/BACKEND-PARITY.md` | Extend current performance records to security profile costs on both targets. |
| SEC-049 | [#13551](https://github.com/JakeChampion/lang/issues/13551) | blocked | `docs/HARDENING.md`, `docs/BACKEND-PARITY.md` | Approve a speculation threat model and hardware policy before new mitigations. |
| SEC-050 | [#13552](https://github.com/JakeChampion/lang/issues/13552) | partial | `docs/HARDENING.md`, `docs/TEST-GATES.md`, `spec/semantics.md` | Publish verified guarantees and an explicit disclosure and release policy. |
