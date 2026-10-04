# Raw byte records in comm

`comm` now reads byte arrays and compares ranges with `__mismatch_bytes`.
Complete records borrow ranges of the current input block. Records spanning
blocks accumulate in a byte builder, which is released on success, EOF and
read failure. Output preserves the original bytes, including NUL and
malformed UTF-8. Column prefixes, totals and diagnostics remain text.

The late ordering-error path now returns its status through `main`, allowing
owned values in both frames to be released. The WASM runtime also frees the
24-byte `fd_flags` scratch buffer on success and failure. Diagnostics use
this operation to check stdout before flushing it.

## Validation

The pinned bootstrap reaches identical stages 2 and 3 at 12,513,569 bytes,
SHA-256
`b9991f1faf067a4de95c99a800b1c790361c7fff2fa4d44181f9b2d192fce926`.
The final stage-2 compiler passes ten GNU byte-parity cases on Darwin and
core WASM: all byte values with LF and NUL records, missing terminators,
suppressed columns, long common prefixes, shared stdin, empty inputs and
late disorder. Every case balances allocations, including the error return.
A separate core WASM probe repeats 100 successful and 100 failed descriptor
flag queries and balances all 602 allocations.

Linux checks pass on the frozen snapshot: descriptor flags, GNU parity,
primary x86/ARM/WASM byte and ownership cases, primary utility parity,
the full unit suite and all lint gates.

## Size

The same final compiler builds the parent and candidate. The WASM scratch
release adds 216 bytes of compiler code and 256 bytes of static data, with
unchanged unwind data and file size. Native `comm` adds 916 bytes of code and
328 bytes of unwind data for raw reading, builder accumulation and cleanup.
Static data is unchanged; both files are 116,353 bytes. No size baseline was
changed.

## Native measurements

An 8 KiB pilot precedes the same pipeline at 8 MiB. Each implementation's
output is checked byte-for-byte against GNU before timing. Two warmups
precede seven alternating runs with stdout directed to the null device;
peak resident memory is measured in a separate run. No other compiler or
test job from this workstream runs during measurement.

| 8 MiB workload | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| Sorted short records | Previous text input | 17.680250 | 17.138375-19.471583 | 1,736,704 |
| Sorted short records | Raw byte input | 18.133791 | 17.520875-20.405958 | 1,736,704 |
| Sorted short records | GNU 9.12 | 40.997750 | 39.049416-50.419000 | 1,212,416 |
| Sorted short records | uutils 0.12.0 | 36.922667 | 34.233125-38.225375 | 1,998,848 |
| Long common prefix | Previous text input | 16.875625 | 16.298459-19.524833 | 87,343,104 |
| Long common prefix | Raw byte input | 12.127750 | 11.648125-12.992208 | 51,724,288 |
| Long common prefix | GNU 9.12 | 24.042875 | 23.868916-25.529833 | 32,587,776 |
| Long common prefix | uutils 0.12.0 | 10.923209 | 10.458792-11.237917 | 52,510,720 |

The short-record before/after ranges overlap. Builder accumulation reduces
time and memory on this long-record fixture. Neither result establishes a
general speedup. uutils 0.12.0 produces exact GNU output for both malformed
UTF-8 fixtures; the earlier 0.0.29 incompatibility does not apply here.
