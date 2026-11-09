# ARM64 correctness qualification, 2026-11-09

The coreutils cross-runner executes the GNU oracle through its emulator prefix.
The shared `wc` byte helper previously invoked that oracle directly.
An ARM64 GNU oracle therefore caused the byte corpus to skip on x86-64.
The ordinary parity cases still ran and the Go command returned success.

The coreutils caller now passes its validated reference command into the shared corpus.
The command includes the emulator and its library prefix.
Self-hosted compiler tests retain their native GNU reference path.
The fix preserves every output check and documented filename exception.
Issue #14901 records the missing coverage.

The regression executes the corpus through a shell prefix.
The prefix records execution and starts the real GNU binary.
The native reference command and ARM64 reference command both pass.

## Executed checks

QEMU 10.0.13 executes the ARM64 binaries on the Linux x86-64 host.
GNU coreutils 9.12 comes from the same official source archive as the native oracle.
GCC 14.2.0 and Binutils 2.44 build the ARM64 oracle.
The package hashes and binary hashes are in the evidence archive.

- `wc`: 159 ordinary parity cases and 40 byte-input cases pass against the ARM64 GNU oracle.
- The 40 byte-input cases also pass against the native GNU oracle after the helper change.
- The sanitized `wc` corpus passes 40 cases on each of x86-64, ARM64, and WASI.
- `tail`: 226 ordinary parity cases and 39 byte-input cases pass against the ARM64 GNU oracle.
- The sanitized ARM64 `tail` corpus passes all 38 cases with balanced allocations.
- Packed byte-count checks pass on ARM64.
- String byte-count checks pass 5518 cases against the Go reference on ARM64.
- The test-harness race command passes.
- The source-lint suite and Go analysis pass.

The selected parity and corpus runs contain no skipped cases.
The ordinary parity cases compare stdout, stderr, and exit behavior.
The shared byte corpora retain their narrower checks and documented filename exceptions.
The earlier count of 199 `wc` cases combines 159 ordinary cases with 40 byte-input cases.
It does not mean 199 ordinary cases plus another byte corpus.

The kernel implementation source is `5ce1fff1d7877219efe86ef0932cb3f5a9f0883d`.
The separate `tail` source is `54b7117e5a31d5e6fa71719e51f36b71798cd6d2`.
The helper change affects test commands, not compiler or utility source.
The prior performance reports keep their original binary identities.

## Evidence and limits

The [evidence archive](2026-11-09-arm-qualification-data.tar.gz) contains test logs, commands, tool identities, and the helper patch.
It preserves the initial oracle-discovery failure and the incomplete `wc` run with its skip.
It also preserves the failed static GNU build and the failed dynamic configure attempt without a sysroot.
The corrected dynamic cross build and installation both pass.

These checks establish emulated correctness, not native ARM64 performance.
They do not replace the required remote CI gates.
Native Darwin execution remains unverified in this environment.
The full-catalogue speed condition remains open.
