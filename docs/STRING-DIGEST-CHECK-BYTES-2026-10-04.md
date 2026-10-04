# Checksum files stay in bytes

The shared checksum reader parses byte records and validates complete digest
fields and unescaped filenames before converting them to text. Malformed
UTF-8 in comments or rejected grammar never becomes a string. A malformed
filename produces an explicit read failure, including with `--ignore-missing`;
`--status` retains its normal output and summary suppression.

Tagged records reject embedded NUL bytes, including bytes after an otherwise
valid digest. Untagged filenames retain GNU's first-NUL termination rule.
Algorithm detection still carries a recognized tag into later diagnostics.

## Validation

The shared corpus covers md5sum, sha1sum, sha224sum, sha256sum, sha384sum,
sha512sum, b2sum and cksum. It includes file/stdin input, escaped and tagged
records, malformed bytes, invalid filenames, all check modes, NUL placement,
Unicode across 4096/65536-byte boundaries and algorithm detection state.
Invalid UTF-8 filenames exercise Fern's D10 refusal explicitly; raw command
line argument tests retain their existing platform-boundary exception.

GNU 9.12 parity, primary x86-64/ARM64/WASM coverage, the full Linux unit suite
and all lint gates pass. Darwin's GNU suite passes in 28.831 seconds and its
source-built primary suite in 55.745 seconds. The reproduced primary compiler
passes all 2,632 native/core-WASM artifact cases in 39.549 seconds, with
balanced allocations and zero live bytes.

## Measurements

Before and after use the same candidate libraries and standard library; the
before digest implementation comes from `b9503762d`. Both use the reproduced
compiler whose stages two and three match, SHA-256
`c111e77206853b94b981796fdf3319e9cd527ed395526c73e3c4f9d1cb273ed0`.
Task-owned heavy jobs were idle during timing; the desktop was not isolated.

A 16-record pilot precedes 65,536 records, changing only the repetition count.
Each checksum names an empty file. GNU generates the MD5 check records for
`f`, `é🙂` and a filename containing a newline. The raw-comment workload
inserts malformed UTF-8 comments before ordinary records. Every implementation
must match normal-mode output exactly before timing. Timed runs use `--status`
and must succeed without output. Two warmups precede seven samples in
alternating implementation order. Census and peak RSS use separate runs.

| Workload | Input bytes | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| Plain | 2,359,296 | 1177.071 ms | 1180.293 ms | 1,704,171 | 2,359,596 |
| Tagged Unicode | 3,145,728 | 1117.719 ms | 1114.287 ms | 1,835,267 | 2,490,692 |
| Escaped filename | 2,949,120 | 1041.592 ms | 1054.182 ms | 2,031,877 | 2,621,794 |
| Raw comments | 2,752,512 | 1156.457 ms | 1168.270 ms | 2,031,863 | 2,818,374 |

All before/after timing ranges overlap; no speedup is claimed. Byte record
and field materialization plus validating conversions increase allocation
counts. Every allocation is freed in both versions. Native executable size
remains 348,113 bytes. Code grows from 297,392 to 300,568 bytes and unwind
data from 21,948 to 22,444 bytes; data remains 14,152 bytes. No size or
performance baseline is changed.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`,
using the same inputs, options and sample order.

| Workload | Fern median | GNU median | uutils median | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Plain | 1180.293 ms | 754.576 ms | 1203.563 ms | 1,507,328 B | 1,572,864 B | 2,195,456 B |
| Tagged Unicode | 1114.287 ms | 761.309 ms | 1147.925 ms | 1,507,328 B | 1,179,648 B | 2,228,224 B |
| Escaped filename | 1054.182 ms | 760.934 ms | Incompatible | 1,507,328 B | 1,458,176 B | Not compared |
| Raw comments | 1168.270 ms | 756.996 ms | 1171.565 ms | 1,507,328 B | 1,179,648 B | 2,228,224 B |

GNU is faster on all four workloads, with disjoint ranges. Fern/uutils ranges
overlap where output agrees. Uutils rejects the escaped-filename records as
improperly formatted, so that result is excluded from timing comparisons.
Normal-mode output is 393,216, 2,228,224, 1,376,256 and 393,216 bytes
respectively. This slice does not complete #5714's remaining consumers or
public text-boundary contracts.
