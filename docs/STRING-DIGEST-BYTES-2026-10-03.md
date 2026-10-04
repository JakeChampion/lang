# Raw input for streaming digests

The shared digest engine reads byte arrays. Its seven crypto state types
accept them through `update_array`, borrowing a view internally and
rebinding the updated state. This covers md5sum, sha1sum, sha224sum,
sha256sum, sha384sum, sha512sum, b2sum and the digest modes of cksum.
Checksum-file parsing remains separate work.

The shared GNU comparison has 192 cases: twelve digest configurations at
sixteen lengths around padding, block and 65,536-byte read boundaries.
Inputs cycle through all 256 byte values. The reproduced compiler passes
every case on native Darwin and core WebAssembly with balanced allocations.
Compiler reproduction is recorded in [the CRC report](STRING-CKSUM-BYTES-2026-10-03.md).
Darwin and Linux GNU corpora and primary-compiler parity pass for all eight
utilities. Linux primary x86-64, ARM64 and core WebAssembly checks, the
full unit suite and all lint gates pass. The digest generator emits the
new array methods and reproduces the committed Fern source exactly.

With that compiler used for both cksum versions, native code shrinks from
323,684 to 322,604 bytes and unwind data from 21,844 to 21,652 bytes.
Static data remains 14,240 bytes, and the executable stays at 381,121 bytes.
The array entry points avoid retaining unrelated view-method overloads.
No size baseline changes.

The baseline executable SHA-256 is
`d5b3dd74f1993539e07828a103d218f623a364ef5ffb15a48f40d59d090982bf`;
the candidate is
`c193766fab4aab07823a618dacf756560bd1337464d26fa68552b5ebc459b19a`.

Native arm64 Darwin benchmarks compare GNU 9.12 and uutils 0.12.0 across
nine digest modes. An 8,192-byte pilot precedes an 8,388,608-byte run,
changing only the input size. Every input cycles through bytes 0 to 255;
SHA-3 uses 512 bits explicitly. All four implementations produce identical
output and stderr and exit successfully at both scales. Two warmups precede
seven alternating samples, with compiler and container jobs idle. Peak RSS
is measured separately. All before/after timing ranges overlap, so these
measurements establish no clear throughput change. GNU and uutils remain
faster on these workloads; this slice changes ingestion, not the digest
algorithms.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| md5_pattern | text | 33.046 | 32.749-34.156 | 1,409,024 |
| md5_pattern | bytes | 33.264 | 32.851-36.273 | 1,409,024 |
| md5_pattern | gnu | 14.452 | 14.143-15.011 | 1,245,184 |
| md5_pattern | uutils | 12.517 | 12.320-12.994 | 1,949,696 |
| sha1_pattern | text | 28.611 | 28.356-28.978 | 1,392,640 |
| sha1_pattern | bytes | 28.626 | 28.376-28.862 | 1,425,408 |
| sha1_pattern | gnu | 12.338 | 12.161-12.875 | 1,261,568 |
| sha1_pattern | uutils | 5.739 | 5.531-5.851 | 1,982,464 |
| sha224_pattern | text | 62.968 | 62.327-65.699 | 1,392,640 |
| sha224_pattern | bytes | 62.629 | 62.348-64.990 | 1,392,640 |
| sha224_pattern | gnu | 21.302 | 20.957-21.892 | 1,261,568 |
| sha224_pattern | uutils | 5.962 | 5.588-6.274 | 1,949,696 |
| sha256_pattern | text | 62.559 | 62.386-65.816 | 1,425,408 |
| sha256_pattern | bytes | 63.525 | 62.118-66.046 | 1,392,640 |
| sha256_pattern | gnu | 21.265 | 21.086-22.320 | 1,261,568 |
| sha256_pattern | uutils | 5.895 | 5.556-6.773 | 1,949,696 |
| sha384_pattern | text | 30.630 | 30.025-31.757 | 1,409,024 |
| sha384_pattern | bytes | 30.664 | 29.877-31.918 | 1,409,024 |
| sha384_pattern | gnu | 15.170 | 14.687-16.138 | 1,277,952 |
| sha384_pattern | uutils | 7.363 | 7.262-7.522 | 2,015,232 |
| sha512_pattern | text | 31.474 | 29.910-31.686 | 1,409,024 |
| sha512_pattern | bytes | 31.001 | 30.128-31.503 | 1,441,792 |
| sha512_pattern | gnu | 15.206 | 14.672-16.012 | 1,327,104 |
| sha512_pattern | uutils | 7.439 | 7.113-8.610 | 2,015,232 |
| blake2b_pattern | text | 32.690 | 32.064-34.119 | 1,392,640 |
| blake2b_pattern | bytes | 32.295 | 31.883-34.486 | 1,392,640 |
| blake2b_pattern | gnu | 11.933 | 11.556-12.520 | 1,294,336 |
| blake2b_pattern | uutils | 8.601 | 8.479-8.986 | 1,982,464 |
| sm3_pattern | text | 43.303 | 42.652-46.663 | 1,441,792 |
| sm3_pattern | bytes | 42.798 | 42.665-45.519 | 1,409,024 |
| sm3_pattern | gnu | 22.490 | 22.133-23.592 | 1,261,568 |
| sm3_pattern | uutils | 20.098 | 19.925-20.804 | 1,998,848 |
| sha3_pattern | text | 74.196 | 73.694-76.574 | 1,409,024 |
| sha3_pattern | bytes | 74.466 | 73.923-76.020 | 1,409,024 |
| sha3_pattern | gnu | 40.344 | 39.424-40.909 | 1,310,720 |
| sha3_pattern | uutils | 17.675 | 17.338-18.169 | 1,998,848 |
