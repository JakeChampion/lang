# Constant-data protection without static startup cost

Fixed-address Linux images map constant boxes and address tables through a separate read-only load segment.
The kernel applies this permission before the program starts.
A virtual gap separates constant data from writable data. The file omits this gap.
PIE and Android images retain the relocation and startup protection path.
Byte-array literals, scalar-array literals and the shared empty allocation stay writable pending issue #14013.

This repair preserves the security features and owner commits in PR #14019 through `34e87d553b`.
It removes redundant ARM section switches and pins the retained-body proof to the exact empty-pool output.
The proof rejects other pool text. It does not normalize arbitrary section lines.
The decoder returns an empty array before a zero-capacity length write.
ELF fixtures retain their assertions with the measured new header and data offsets.

## Source and producer

`fixed-point.json` records every compiler, standard-library and embedded-source hash.
All four compiler stages have SHA-256 `b8a3e526eb1e03fd42f573c6c9b046950f484da497317b165694f5530d027ee8` and size 24,015,313 bytes.
The parent comparison producer is `87b2adf5a96b6f4c335138d2faee88576005e56f43fb82335cf319829b70e53e`.
Main `c52ff47d` has the same compiler and standard library as that qualified parent.

`manifest.json` records each artifact's source path, hash and size.
Absolute command paths describe the measurement host. They are not portable install paths.

## Results

| Control | Result | Scope |
|---|---|---|
| Image permissions | 14 variants pass | Seven flag combinations on native ARM64 Linux and QEMU x86-64 |
| Page permissions | 12 probes pass | QEMU ARM64 with 4, 16 and 64 KiB pages; writable data succeeds and prohibited operations fault |
| Retained-body proofs | Nine runs pass | Three drivers on ARM64 Linux, QEMU x86-64 and Wasm; nonzero balanced allocations and zero live bytes |
| Decoder regression | 22 tests pass per target | Darwin, ARM64 Linux, QEMU x86-64 and Wasm; zero live bytes |
| Go controls | Ten roots pass | One root and one subcase skip; see limits below |
| Source gate | Five runtime targets pass | 87 compiler drivers type-check |
| Lint | Pass | `make lint-fast` |
| Standard benchmarks | 39 benchmarks pass | 78 ARM strict metrics, 39 x86 static strict metrics and 117 advisory metrics |
| Static profile costs | 144 metrics pass | Two targets and four profiles; native x86 dynamic costs require CI |
| Image library | Full script passes | Native ARM adapter; 117 compiler invocations, 56 program builds, three expected diagnostic refusals |
| Symbols | Pass | GNU `nm` finds both symbols; QEMU x86 exits with status 7 |
| Android positive control | Pass | QEMU ARM64 prints `ok42` |

The standard benchmark campaign compares all 39 ARM observable outputs with the parent. All match.
The native and self-host baseline files are byte-identical to main `c52ff47d`.

This repair lowers profile limits from the published owner values and adds no increases.
Some owner profile limits exceed preceding main because PIE and hardened images retain explicit protection code and new image layout costs.
The profile records preserve the measured values, accepted limits and comparison output.
These records make no native latency claim and no dynamic x86 performance claim.

The compiler remains 24,015,313 bytes, equal to the earlier startup-protection prototype.
`compiler-size.log.gz` records Mach-O sections for that prototype, the parent and the final producer.
The parent compiler is 23,948,993 bytes. The final compiler includes the new security features.
No compiler-size baseline changes form part of this repair.

## Limits

The local Go selection skips `TestSelfHostArm64NativeLinuxElfRuns`, which requires a native x86 host.
It also skips the ARM `close-on-exec` subcase under QEMU.
Native x86 CI must supply those controls before merge.
An earlier Go campaign reached its 20-minute limit while QEMU compiled the last retained-body case.
The nine native-primary-compiler runs cover those driver inputs separately. They do not turn that timeout into a harness pass.

The full image script uses an explicit adapter from requested x86 output to native ARM Linux output.
The adapter runs the resulting images in the pinned Linux container.
The three refusals are the immutable-array, immutable-field and checked-`usize` negative controls.
CI must also pass the unmodified native x86 script.

The page probes preserve the primary image headers and replace only the entry stub with GNU-assembled probe instructions.
They test writable data, code writes, constant writes and execution from data without a `/proc/self/maps` filename lookup.
QEMU results establish emulated correctness, not native ARM page-size or timing claims.
PIE and Android still have the larger-page protection limit in `docs/security/IMAGE-PERMISSIONS.md`.
These changes do not complete all acceptance items of SEC-028.
