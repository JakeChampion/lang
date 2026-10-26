# Security validation matrix

Status: [reference] implementation for [SEC-002](https://github.com/JakeChampion/lang/issues/13504).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

## Smoke harness

`scripts/security-smoke` executes ten normative conformance cases on each Linux target.
It requires native x86-64 Linux, the current Fern CLI, QEMU AArch64, and `readelf`.
It rejects missing tools and never reports a skipped target as a pass.
Each compiler or program invocation has a timeout.

```sh
make build
python3 -B -m unittest discover -s scripts -p test_security_smoke.py
scripts/security-smoke
```

Use `--compiler`, `--qemu`, and `--report` to select explicit paths.
The default report is `build/security-smoke.json`.
The report records the source commit, compiler hash, host, tool versions, commands, output, and results.
An error preserves the partial report and returns a nonzero exit status.

| Area | Normative cases | Required observation |
|---|---|---|
| Defined integers | `int_wrap`, `int_checked_ops` | Exact corpus output and successful exit |
| Valid slices | `slice_at_length` | Exact corpus output and successful exit |
| Ownership | `diag_e050`, `diag_e051` | Compile failure with the expected diagnostic |
| Local view lifetime | `diag_e063` | Compile failure with the expected diagnostic |
| Array bounds | `oob_index_read`, `oob_index_negative`, `oob_index_write` | Exit 134 and the array cause |
| Slice bounds | `oob_slice_range` | Exit 134 and the slice cause |
| ELF output | Each executable case | Correct machine, complete headers, W^X load pages, and NX stack declaration |

The harness reuses the normative corpus without new expected-result files.
It does not accept a generic compiler failure as an expected diagnostic.
Its own tests reject malformed ELF, W+X pages, executable stacks, missing tools, and false compile-fail results.

The `security-smoke` job in `test-e2e-x86_64.yml` builds the current compiler.
It installs QEMU through the shared cross-toolchain action.
It executes both architectures and uploads the JSON evidence.
The full suite calls this lane for pull requests and main.

## Deeper validation

The smoke harness does not replace the larger suites in [TEST-GATES.md](TEST-GATES.md).

| Area | Current suite or facility | Limit |
|---|---|---|
| Native W^X faults | `TestSelfHostX86WXExecutable` | Native x86 process behavior |
| AArch64 ELF and stack faults | `TestSelfHostArm64NativeLinuxElfRuns` | QEMU behavior on an x86 host |
| PAC and BTI faults | `TestSelfHostHardenArm64` | Native ARM or QEMU process behavior on the test host |
| IR rejection | `TestSelfHostIRVerifyGateRefuses`, `TestSelfHostIRVerifyGateChecks` | Verifier coverage remains subject to SEC-011 |
| Bounds and sanitizer regression | `internal/testing/e2e` | Diagnostic coverage does not prove complete memory safety |
| Generated programs | `internal/testing/fernsmith` | Corpus and time budgets bound coverage |
| Translation validation | `internal/testing/tv` | The model limits the proof |
| Bootstrap consistency | `make distcheck` | Stable miscompiles can reproduce themselves |

Later SEC tasks must add missing hostile-input, handle, concurrency, CFI, and profile tests.
SEC-026 must enumerate accepted binary inputs before a general hostile ELF reader test exists.
Performance and compatibility evidence must use both target baselines.

## Hardware status

The smoke report marks CET, PAC, BTI, and MTE as `unverified`.
Instruction emission and ELF properties cannot change that state.
Physical verification requires CPU support, kernel support, process activation, and an adversarial fault test.
The report does not infer any of these facts from QEMU.

`TestSelfHostHardenArm64` uses a compiler for the native host architecture.
The test runs on the native ARM self-host shards without an x86 compiler.
It also runs with QEMU on the x86 self-host shards.
The log identifies native or QEMU execution before any fault test.
The native test selects each fault test from its CPU feature flag.
PAC coverage does not require BTI support.

Unsupported features remain unverified.

Each enabled BTI test requires SIGILL after removal of the BTI instructions.
The PAC test substitutes `bti c` for `paciasp` to preserve valid BTI targets.
It requires SIGSEGV or SIGILL after authentication of the unsigned return address.
The architecture can fault during authentication or after the subsequent return.
The same control must run correctly without authentication.

An arbitrary nonzero exit does not satisfy either fault test.
Positive controls must execute before the fault tests.
These observations apply to the tested process and image variants only.
They do not certify handwritten runtime frames or every production host.

## Local evidence

The baseline compiler failed the matrix because ordinary AArch64 output omitted `PT_GNU_STACK`.
The SEC-028 fix supplies that declaration and updates image offsets.
The fixed compiler passed all 20 case-target pairs with QEMU 10.0.13.
The harness unit tests passed.
The workflow syntax and source-lint checks passed.
These results establish smoke coverage only.
