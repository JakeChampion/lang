# Raw file data in the shared copy engine

The engine used by `cp`, `install` and cross-device `mv` reads owned byte
arrays. Sparse detection compares each filesystem block against one reused
zero block, using the existing bounded vector mismatch operation.
Whole input chunks go directly to the byte writer; partial nonzero runs
use a raw builder range. The writer retries short writes against the same
array, and trailing holes still extend the destination with truncate.

Direct Darwin checks with the reproduced builder compiler pass 36 `cp`
cases and 12 `install` cases with balanced allocation censuses. They cover
every byte value, read boundaries, zero tails and middles, allocated zeros
and real sparse holes. The Linux `mv` fixture checks device IDs before
running, so rename cannot bypass the copy fallback. Compiler reproduction
is recorded in [the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The same compiler builds parent and candidate binaries:

| Utility | Code delta | Unwind delta | Static data delta | File bytes, before | File bytes, after |
| --- | ---: | ---: | ---: | ---: | ---: |
| cp | +1,648 | +264 | 0 | 232,593 | 232,593 |
| install | +192 | -56 | 0 | 282,081 | 282,081 |
| mv | +1,648 | +264 | 0 | 232,433 | 248,945 |

The shared engine replaces string slices and repeated partial-write copies
with raw reads, bounded vector comparisons and byte writes. `mv` crosses a Mach-O text
page boundary: its text segment grows from 212,992 to 229,376 bytes and
its signature from 1,937 to 2,065 bytes. Those 16,384 and 128 bytes account
for the 16,512-byte file growth. No size baseline changes.

Native arm64 Darwin measurements use an 8,192-byte pilot followed by
8,388,608 bytes, changing only the input size. File contents, stdout,
stderr and status are checked before timing. Two warmups precede seven
alternating samples, with RSS measured separately and other compiler
and container jobs idle. GNU 9.12 is the oracle; uutils is 0.12.0.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes | Output blocks (512 bytes) |
| --- | --- | ---: | ---: | ---: | ---: |
| dense | text | 4.245 | 3.993-4.725 | 1,605,632 | 16,384 |
| dense | bytes | 4.023 | 3.681-6.484 | 1,458,176 | 16,384 |
| dense | gnu | 5.388 | 5.276-6.573 | 1,474,560 | 16,384 |
| zeros | text | 3.513 | 3.266-8.354 | 1,474,560 | 0 |
| zeros | bytes | 3.143 | 2.950-5.841 | 1,490,944 | 0 |
| zeros | gnu | 4.718 | 4.504-8.251 | 1,474,560 | 0 |
| mixed | text | 6.897 | 4.411-9.781 | 1,474,560 | 16,384 |
| mixed | bytes | 4.593 | 4.222-6.619 | 1,474,560 | 16,384 |
| mixed | gnu | 5.859 | 4.921-8.515 | 1,474,560 | 16,384 |
| sparse_auto | text | 4.362 | 4.143-10.869 | 1,605,632 | 16,384 |
| sparse_auto | bytes | 3.930 | 3.854-5.862 | 1,458,176 | 16,384 |
| sparse_auto | gnu | 5.456 | 5.243-27.693 | 1,474,560 | 16,384 |
| sparse_auto | uutils | 7.733 | 7.546-8.366 | 2,244,608 | 16,384 |

All parent/candidate timing ranges overlap in the final run. The dense
and auto cases use less sampled peak RSS; this is not a blanket speed
improvement claim. Full input allocations are 16,384 blocks for every
workload on this host. Even the seek-seeded mixed source is fully
allocated: a separate probe reports zero blocks for an 8 MiB file after
truncate alone, but 16,384 after writing its first byte. Thus the auto
measurement does not establish performance on a genuinely sparse
mixed source. The all-zero output is sparse, with zero allocated blocks.

uutils matches the auto case. It rejects explicit `--sparse=never` and
`--sparse=always` on Darwin with exit 1 and no output file, so those
cases are excluded from its timings. Both Fern versions match GNU in
all four workloads at both scales.

An initial scalar scan regressed the 8 MiB all-zero workload from
3.236 to 7.375 ms with disjoint ranges. The final implementation uses
the existing vector mismatch kernel and one zero block per sparse
copy; it does not allocate a fresh block for every comparison.

Linux passes the new byte cases, both primary native targets, the existing
GNU and primary corpora for all three utilities, the full unit suite and
all lint checks. Cross-device move tests prove different source and
destination device IDs before running. All successful target cases have
balanced allocation censuses. Source files match the frozen snapshot.

Darwin's new byte cases and existing install corpus pass. The broader cp
and mv corpora retain the same failures as the unchanged parent: backup
suffixes, reflink requests, copy traversal order and trailing-slash handling.
Their existing CI exceptions are unchanged. This migration does not claim
to resolve those platform differences.
