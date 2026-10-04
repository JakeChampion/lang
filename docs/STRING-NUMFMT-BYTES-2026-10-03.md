# Raw numeric input for numfmt

`numfmt` parses input fields directly from byte arrays. Unselected and invalid
fields pass through byte-range writes; only generated numbers, options and
escaped diagnostics use text. The delimiter is a byte value, removing the
unchecked single-byte string constructor. Numeric scaling, rounding and
long-double arithmetic retain their existing implementation.

The output limit now follows the decimal-exponent check in
[GNU 9.12's `prepare_padded_number`](https://github.com/coreutils/coreutils/blob/v9.12/src/numfmt.c)
before scaling.
The old check divided by 1,000, while GNU divides repeatedly by 10. Their
rounding differs at the upper boundary on Darwin: 33 nines incorrectly
printed `1.0(error)` instead of returning the size error. The decimal check
also applies to IEC output. Values below 10^32 bypass the division loop.

Whole records are borrowed ranges within each input chunk. A record crossing
read boundaries uses one growing builder. This preserves the final partial
record on EOF or read failure without repeatedly copying its accumulated
prefix. NUL searches stop at the current record's boundary.

Conversion failures return through normal cleanup before main flushes and
returns their status. This releases input, options and diagnostic allocations
even in abort mode. Headers now match GNU when an embedded NUL truncates
them: their discarded suffix includes the newline. Zero-terminated headers
still omit their NUL terminator.

The new differential corpus contains 71 cases: arbitrary bytes in headers,
unselected fields and diagnostics; all invalid modes; LF/NUL termination;
embedded NUL truncation; split digits and raw records across 64 KiB boundaries;
long records; partial final records; conversion failures after output; and
overflow under each invalid-input mode and output scale. The existing GNU
corpus also gains negative, SI and IEC decimal-boundary cases.
The reproduced compiler from [the CRC report](STRING-CKSUM-BYTES-2026-10-03.md)
passes every native Darwin case with balanced allocations. Core WebAssembly
passes 70 cases with balanced allocations; Wasmtime rejects the malformed
UTF-8 argv fixture before the guest starts, so that case remains native-only.
Malformed input bytes are covered through stdin on every target.

Darwin primary byte cases, the full GNU numfmt corpus and primary/bootstrap
utility parity pass (34.987, 11.553 and 14.267 seconds). The reproduced
compiler's native/core byte cases pass in 2.778 seconds. Linux GNU numfmt,
primary x86/ARM/WebAssembly byte cases and primary/bootstrap parity pass
(82.793, 36.577 and 13.132 seconds). The full unit suite and `make lint-all`
pass on the same frozen source; all 5,964 Go/Fern files match the checkout.

## Size

The same reproduced compiler builds parent `0f841e824` and this version.
Both executables are 215,873 bytes. Code grows from 170,156 to 170,244 bytes;
unwind data grows from 21,244 to 21,516 bytes, and static data stays at
12,472 bytes. The byte migration alone reduced code by 344 bytes; the
decimal-limit correction adds 432 bytes, leaving 88 bytes of net growth.
No size baseline changes.

Parent SHA-256:
`b5967e2cf42b121633a1d2dfa43fb9aa1f6facc94b5e6126eff45a4906963a92`.
Candidate SHA-256:
`e7de6a122ff2bae135c01a78e92909591fc30c9f0cf23b407caf7fdb5a630414`.

## Native measurements

An 8,192-byte pilot precedes an 8,388,608-byte workload parameter, changing
only that parameter. Repeated-record workloads use the largest whole number
of records fitting that size; long-record workloads add their delimiter and
numeric field. Input is a regular file connected to stdin. Exact stdout,
stderr and status are checked before timing. Two warmups precede seven
alternating samples, with output sent to `/dev/null`. Peak RSS is measured
separately. This task's compiler and container jobs are idle during timing;
other desktop activity is not isolated.

| Workload | Parent median ms | Byte version median ms | GNU 9.12 ms | uutils 0.12 ms |
| --- | ---: | ---: | ---: | ---: |
| Short numeric records | 357.332 | 354.752 | 666.741 | 540.252 |
| Short records with raw unselected fields | 208.156 | 205.714 | 457.598 | incompatible |
| Long raw header | 55.609 | 10.495 | 13.623 | 6.112 |
| Long raw first field, convert second | 60.175 | 17.020 | 14.031 | 27.802 |

The short-record timing ranges overlap. Long-header ranges are
54.133-57.867 ms before and 9.939-11.698 ms after. Long-field ranges are
59.845-60.685 ms before and 16.615-17.788 ms after. These support improvements
for the long-record workloads, without a general throughput claim.

Long-header peak RSS falls from 122,732,544 to 26,525,696 bytes; long-field
peak RSS falls from 131,219,456 to 43,335,680 bytes. Short numeric peak RSS
is 1,425,408 bytes before and 1,458,176 bytes after.

GNU matches every workload. uutils matches three, but treats the raw-field
record as an invalid number and exits with status 2. That refusal is reported
as incompatible and excluded from timing. An earlier implementation used
owned byte records for every line; its short numeric workload rose from
358.339 to 409.231 ms. Borrowing chunk ranges removes that extra per-line
allocation and copy while retaining the long-record improvement.
