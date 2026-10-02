# Component output writes

The primary compiler's WASI component output shim passed an entire iovec
to `blocking-write-and-flush`. The host rejects writes larger than 4096
bytes. The shim also ignored the returned stream error and never released
its return-area allocation.

The shim now writes every iovec in chunks of at most 4096 bytes. It returns
the completed byte count, reports an I/O error when a host call fails,
drops an owned error handle, and releases its aligned scratch area on
both success and failure. An empty iovec still calls the host so a closed
stream remains observable. Invalid output descriptors return `BADF`.

The component framing includes the error-resource destructor. Existing
standalone framing helpers retain their default import contract; the
compiler opts into the destructor when framing the generated output shim.

## Validation

Actual primary-compiled components write 8193 bytes through both `write`
and `write_some`, on stdout and stderr, with both lowering modes. A
deterministic host exercises the emitted shim for empty writes, no
iovecs, chunk boundaries, multiple iovecs, closed streams, owned errors,
failure after one successful chunk, and invalid descriptors. It checks
counts, error handles, alignment and scratch allocation balance.

Darwin component and host-outcome tests passed. The full unit suite and
`make lint-all` passed. The Linux component suite initially found stale
expected import lists; the focused rerun after updating them passed.
No compiler source changed after the full gate began.

The pinned stage0 builds the compiler and passes its smoke tests.
`make distcheck` reaches identical stage2 and stage3 binaries:
`6fbbdbe0cc862e879740731536ac72896687aa995876eb59675c8f597efa5c02`.

This fixes component output. Primary component stdin remains a separate
target-support gap. This change makes no throughput claim.
