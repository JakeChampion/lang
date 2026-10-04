# Byte input for expand and unexpand

Both utilities keep input chunks and complete lines as byte arrays. A line
crossing a read boundary accumulates in a builder, allocated only when needed
and freed after extraction. A partial line survives operand boundaries and
failed opens or reads. Tab-stop options, generated padding and diagnostics
remain text. This removes the use of unchecked strings for arbitrary input.

The first validation on main found a 24-byte WASM leak on each file-error
case. The descriptor-flags runtime allocated a scratch record without freeing
it. The repair frees that record after reading its fields and before returning
an error. A regression repeatedly queries a valid and then closed descriptor.

Exact diagnostic comparison also exposed WASI returning EBADF when reading
a directory. Both converters now inspect the open descriptor after a failed
read and report GNU's directory diagnostic when appropriate. Successful reads
perform no additional stat. The permanent native/WASM corpus checks stderr
as well as stdout, exit status and allocation balance.

## Validation

The frozen snapshot contains 7,897 files. All 5,586 Go, Fern and golden files
match the tested snapshot. Linux GNU parity passes in 1.684 seconds, primary
Linux x86/ARM and core WebAssembly byte cases plus the descriptor regression
in 25.863 seconds, and primary utility parity in 13.299 seconds. The full
Linux unit suite and `make lint-all` pass.

The compiler reaches stage2 = stage3 from pin `stage0-20261001-c891ebc` on
ARM64 Linux and Darwin. Stage1 differs because the pin predates code changes.
Darwin's initial bootstrap covered the same compiler source; the final run
reused that stage1 and rebuilt stages 2 and 3 after the utility-only change.

| Host | Fixed-point bytes | SHA-256 |
| --- | ---: | --- |
| ARM64 Linux | 12,988,352 | `1d64671f1134e42c877e5a0b1557c6cae1125023f24fae862f3f886282047039` |
| ARM64 Darwin | 13,194,769 | `612bf23db61963b6c0acb362445593305f36333711502bcc9288b0a07b98deea` |

Darwin GNU parity passes in 3.857 seconds and primary utility parity in
15.727 seconds. The reproduced compiler passes all 28 direct artifact cases:
seven per utility on native Darwin and core WebAssembly. Each has exact GNU
stdout, normalized stderr and exit status, plus exactly one balanced census.
Cases cover all byte values, NULs, malformed UTF-8, long lines, unterminated
tails, cross-file continuation, empty input and open/read errors while
retaining a partial line. No test exclusions or gate baselines changed.

## Measurement method

Both versions use the same reproduced compiler and current stdlib. The
baseline restores only the two utility sources from main `886ee8807`.
Compiler size is measured separately by restoring only its WASM emitter.
Each utility passes an 8,192-byte pilot before an 8,388,608-byte workload,
with only the size parameter changing. Both inputs repeat tabs, malformed
bytes, spaces, backspace and NUL; one includes newlines and the other does not.

Exact GNU stdout, stderr and status are checked before timing. Each version
also runs with allocation checks. Two warmups precede seven alternating
samples using regular-file stdin and `/dev/null` output; peak RSS is measured
separately. Incompatible uutils outputs are recorded and excluded from timing.
No task-owned compiler or container runs alongside measurements. Desktop
activity is not isolated. All samples, including outliers, remain in the
reported ranges.

| Utility / workload | Parent median [range] ms | Byte version median [range] ms | GNU 9.12 median ms | uutils 0.12 median ms |
| --- | --- | --- | ---: | --- |
| expand / lines | 22.534 [21.989, 23.264] | 24.277 [22.673, 25.502] | 193.929 | incompatible |
| expand / long line | 26.907 [26.615, 29.121] | 23.671 [22.920, 24.483] | 198.050 | incompatible |
| unexpand / lines | 10.453 [9.546, 10.826] | 10.083 [9.950, 11.039] | 191.504 | 42.736 |
| unexpand / long line | 11.044 [10.871, 11.373] | 7.721 [7.438, 7.962] | 190.991 | 40.951 |

Both long-line workloads have disjoint faster ranges. The short-line ranges
overlap; expand's median rises. GNU matches all outputs. uutils matches both
unexpand workloads, but its expand outputs contain 13,323,085 instead of
13,816,532 bytes for lines, and 13,631,488 instead of 14,155,776 bytes for
the long line. Those successful-but-different outputs are excluded from
timing. GNU's expand long-line range includes a 306.688 ms outlier.

| Utility / workload | Parent allocations | Byte allocations | Parent RSS bytes | Byte RSS bytes |
| --- | ---: | ---: | ---: | ---: |
| expand / lines | 2,244 | 2,486 | 1,327,104 | 1,343,488 |
| expand / long line | 353 | 339 | 91,652,096 | 54,837,248 |
| unexpand / lines | 1,586 | 1,827 | 1,327,104 | 1,376,256 |
| unexpand / long line | 348 | 333 | 70,483,968 | 34,881,536 |

Every census balances with zero live bytes. Short-line allocations and RSS
increase; long-line allocations and RSS fall. This change does not improve
every workload. Retaining a complete line still uses more memory than GNU's
streaming path.

## Size

The native expand executable remains 116,321 bytes and unexpand remains
116,337 bytes. Static data stays at 7,240 bytes each.

| Utility | Parent code | Byte code | Parent unwind | Byte unwind |
| --- | ---: | ---: | ---: | ---: |
| expand | 80,684 | 82,868 | 10,628 | 11,100 |
| unexpand | 78,988 | 81,180 | 10,580 | 11,052 |

Instruction, literal and alignment accounting exactly reproduces both code
sizes. Each gains 832 bytes for raw read/short-read helpers, 664 for descriptor
stat and 896 for result ownership. The expand loop shrinks 204 bytes and its
alignment shrinks 4; the unexpand loop shrinks 200. These account for the full
2,184-byte and 2,192-byte growth. The additional code provides byte ownership
and correct directory diagnostics; no size baseline was changed.

The compiler's native executable grows from 13,194,753 to 13,194,769 bytes.
Code grows 216 bytes, all in `wasm_ir.fd_flags_func`; unwind stays 638,756
bytes and static data stays 1,060,272 bytes. The two emitted scratch-release
calls account for all added code. The measured candidate compiler hash is
identical to the Darwin bootstrap fixed point above.

| Artifact | Parent SHA-256 | Candidate SHA-256 |
| --- | --- | --- |
| expand | `8dfe3e173179103db49e7853301e34038b9240cf63d2c28cd1ccf9e7229bb82f` | `f34d67774ae7bd629c7fce5bd2a8f42980083dc3cf58746d1c6ccb9a2aafd229` |
| unexpand | `acf90c779c6fd599d40b11d24d98953ec232ccaaa69b2ca97211dbb0c17de27b` | `263310122a0aff5d50bbe6bd697342e6b4cace615a9853ac30ef30fa9fcaecc6` |
| compiler | `aa6a57efe5c3c789b68fd00092ce526407a43cb170c1cc47951e45a01d27b5f0` | `612bf23db61963b6c0acb362445593305f36333711502bcc9288b0a07b98deea` |
