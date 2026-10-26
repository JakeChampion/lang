# Native entropy completion and failure

Status: partial implementation for [SEC-019](https://github.com/JakeChampion/lang/issues/13521).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

## Contract

`random_bytes(n)` must complete every requested byte before it returns data.
`random_i32()` must complete all four bytes before it returns a value.
The native runtime uses the kernel entropy source without a deterministic fallback.
An empty byte request requires no entropy syscall.
The separate seeded PCG32 API retains its reproducible sequence contract.

The old native helpers ignored errors or advanced past short reads.
They could return zero or incomplete data after an entropy failure.
The helpers now share a completion loop and process each result explicitly.

| Platform | Entropy syscall | Successful result |
|---|---|---|
| Linux x86-64 | `getrandom`, number 318 | Positive byte count, at most the request size |
| Linux AArch64 | `getrandom`, number 278 | Positive byte count, at most the request size |
| Darwin AArch64 | `getentropy`, number 500 | Zero, which means a complete request |

Each request contains at most 256 bytes.
Linux short reads advance by the actual byte count.
An EINTR result retries the remaining request.
A zero-byte Linux result or another error prevents a return to the caller.
An invalid result above the request size also prevents a return.

## Failure policy

The current value-return APIs have no error result.
A fatal entropy failure releases the owned buffer and exits with status 134.
The runtime prints this fixed diagnostic to stderr:

```text
fern: entropy unavailable
```

The runtime does not return a partially filled buffer or a scratch value after failure.
Stack scratch requires no heap release.
The failure path uses a static message and raw write and exit operations.

## Verification

The regression suite executes the current compiler output on native x86-64 and QEMU AArch64.
It uses real kernel seccomp denials for EIO and ENOSYS.
The old compiler returns normally under both errors.
The fixed compiler stops before it returns either random value.
Empty requests succeed under the same denial.

Test-only assembly substitutes controlled entropy results without a substitute for the other kernel syscalls.
The controls cover full fills, one-byte short reads, one EINTR, and a zero-byte result.
Each successful control must produce all 257 expected bytes and the expected four-byte integer.
The unchanged baseline passes the full-fill control and fails the other three controls on both targets.
The fixed compiler passes all eight target-mode pairs.

The x86 sandbox test contains no user output operation.
Its failure diagnostic therefore requires the runtime's own write dependency in the syscall filter.
The test passes with the new failure path.
The one-megabyte fill, seeded sequences, and WASI random tests also pass locally.
The RC verifier remains active for every test.

Run the focused failure and completion tests with:

```sh
go test ./internal/testing/e2e -run '^TestSelfHost(EntropyFailure|EntropyReadCompletion|SandboxEntropyFailure)$' -count=1 -v -timeout 12m
```

## Remaining limits

This change does not complete SEC-019.
CPU and kernel mitigation reports still require a separate implementation.
The tests establish completion and failure behavior, not a statistical proof of entropy quality.
The kernel and host entropy services remain trusted.

Native Darwin CI must verify its zero-success syscall convention.
`TestArm64DarwinEntropyCompletion` covers zero, one, 256, 257, and 1024 bytes and an integer draw.
Local QEMU results do not replace that native CI evidence.

WASI continues to use the host random interface.
The Go oracle continues to use `crypto/rand`.
This change preserves their current entropy providers.
