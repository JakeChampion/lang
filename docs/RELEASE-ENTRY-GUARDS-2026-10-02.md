# Share the release fast path at the helper entry

PR #11134 exposed a stage-2 compiler size failure inherited from main:
11,456,752 bytes against the existing 10,738,872-byte x86 baseline. The
recent nominal-release optimization repeated its reference-count guard
at every call site. Its profile showed useful savings from avoiding the
destructor's frame, but the repeated code made the compiler larger.

The native emitters now put that guard before the release helper's frame.
A shared count is decremented and returns immediately. Null and static
values return without changing their count. Unique and zero counts enter
the existing body, preserving destruction and underflow handling. Both the
stack and register entries reach the guard. Sanitizer/debug builds retain
the ordinary body.

The optimization checks the generated body's contract as well as its name:
it must release its sole argument through `__fern_rc_dec` and return zero.
Borrowed string descriptors and special map or closure releases do not
qualify merely because their names start with `__sem_release_`.

## Size and performance

Two native arm64 Darwin compilers, the published DNS head and the candidate
stage 2, compiled the same candidate source to x86-64 Linux:

| Generated compiler | Call-site guards | Shared entry guards |
|---|---:|---:|
| File bytes | 11,454,928 | 10,906,616 |
| Loaded memory bytes | 12,544,824 | 11,996,512 |

The file shrinks by 548,312 bytes. The result is 1.56% above the existing
baseline, within its unchanged 5% threshold. These binaries omit section
headers, so the measurement reports load segments rather than inventing a
section breakdown. The bytes outside the file in the load segment are
unchanged. No size baseline was raised.

The same compilers built `checker_modload_run.fern` on native arm64 Darwin.
After a one-run pilot, five runs alternated compiler order. The workstream
ran no other heavy job during measurement:

| Seconds per compile | Call-site guards | Shared entry guards |
|---|---:|---:|
| Median | 3.394 | 3.296 |
| Observed range | 3.378-3.445 | 3.257-3.354 |

This measures that compile workload, not a general runtime speedup.

## Validation

The pinned Darwin bootstrap reaches identical stage-2 and stage-3 binaries:
12,296,625 bytes, SHA-256
`bdd20b690a2d2b9e5c6c724138db0ebdf4832337cf674de6fb64f065bc0018ad`.
The seed remains `stage0-20261001-c891ebc`.

The native probe retains a record through a returned alias, releases that
alias while its source remains live, and repeats the operation on a literal
record. Nested arrays and enum payloads are read after releases. Both
compilers finish with 182 allocations and 182 frees, zero live bytes. The
candidate emits three shared entry guards and no call-site guards.

The two-native-target normal/debug regression passes, as do the runtime-helper
refusal, allocation-count and module-cache checks. The broader ownership group,
native backend differential tests, DNS/socket checks, full unit suite and all
lint gates pass in an immutable Linux snapshot. Actual stage-2 DNS fault,
UDP-to-TCP retry and silent-peer deadline tests also pass on the targets listed
in the [DNS report](STRING-DNS-TCP-BYTES-2026-10-02.md).

Remote checks remain a merge gate.
