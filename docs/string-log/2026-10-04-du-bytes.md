# DU file lists and exclusion patterns stay in bytes

DU reads `--files0-from` and exclusion files as byte arrays. It validates each
complete filename before converting it to text and reports invalid UTF-8 at
that boundary. Exclusion patterns remain bytes, including malformed UTF-8,
and use the shared fnmatch implementation. Stored patterns retain their file
buffer and bounds without copying each pattern or allocating a view on every
match. This removes DU's duplicate matcher.

On Darwin, a trailing-slash operand is looked up with a final `.` component.
This preserves the displayed name while requiring a directory, matching GNU
for file symlinks and symlink loops as well as directory symlinks.

## Validation

The shared corpus covers 57 cases, including file/stdin lists, malformed UTF-8,
Unicode at read boundaries, NUL and carriage-return handling, bracket classes,
collation and retained patterns. The actual reproduced primary compiler passes
all 114 native/core-WASM cases with balanced allocations. The shared fnmatch
fixture also tests stored subranges and patterns retained after the caller
replaces its original input array.

The final integrated snapshot passes the complete Linux unit suite and all
lint gates. GNU parity and primary-compiler DU tests pass on Linux x86-64,
ARM64 and WASM. Darwin's GNU DU suite, primary DU suite and shared matcher
tests pass; matcher coverage includes the interpreter and WASM component
runner. Darwin's two `/dev/full` cases retain their platform skips. The final
114 actual artifact cases pass in 2.502 seconds with balanced allocations.

## Measurements

Both versions use the reproduced primary compiler whose stages two and three
match, SHA-256
`c111e77206853b94b981796fdf3319e9cd527ed395526c73e3c4f9d1cb273ed0`.
The before DU source is from `4eb21393c`; both versions use the candidate's
same libraries. Task-owned heavy jobs were idle during timing; the desktop
was not isolated.

A 16-record pilot precedes 65,536 repetitions, changing only the scale. Name
lists repeat four names (`f`, `ab`, `é🙂`, `empty`), each NUL-terminated.
Pattern inputs repeat an unmatched ASCII or malformed UTF-8 pattern and end
with a matching pattern. GNU 9.12 supplies exact output; every timed run must
match. Two warmups precede seven samples in alternating implementation order.
Allocation counts and peak RSS use separate runs.

| Workload | Input bytes | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| Names from file | 1,179,648 | 194.511 ms | 201.896 ms | 3,408,091 | 3,932,918 |
| Names from stdin | 1,179,648 | 201.900 ms | 209.319 ms | 3,414,501 | 3,938,186 |
| ASCII patterns | 1,179,651 | 6.793 ms | 6.734 ms | 65,814 | 66,352 |
| Raw patterns | 917,508 | 9.991 ms | 9.472 ms | 70,772 | 70,572 |

The file-name workload slows down with disjoint ranges: 192.188-197.903 ms
before and 199.461-206.185 ms after. Validating and materializing complete
filenames adds two allocations per name; each scale repetition contains four
names. The stdin ranges overlap, as do ASCII-pattern ranges. Raw-pattern
ranges are disjoint: 9.740-10.118 ms before and 9.242-9.628 ms after. All
before and after Fern allocations are freed.

Native executable size grows from 299,153 to 315,681 bytes. Code grows from
231,500 to 237,740 bytes and unwind data from 28,348 to 29,860 bytes; data
remains 30,584 bytes. The implementation adds validated filename conversion
and specializes the complete shared matcher for stored byte patterns. No
binary-size baseline is changed.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`, using
the same inputs, options and sample order.

| Workload | Fern median | GNU median | uutils median | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Names from file | 201.896 ms | 228.603 ms | 173.402 ms | 18,677,760 B | 1,179,648 B | 12,845,056 B |
| Names from stdin | 209.319 ms | 230.013 ms | 173.960 ms | 18,677,760 B | 1,196,032 B | 12,861,440 B |
| ASCII patterns | 6.734 ms | 11.448 ms | 48.546 ms | 11,878,400 B | 2,424,832 B | 77,086,720 B |
| Raw patterns | 9.472 ms | 12.682 ms | Incompatible | 10,321,920 B | 2,211,840 B | Not compared |

Uutils exits 1 with `No such file or directory` and no output for the raw
pattern input, so that result is excluded from timing comparisons. All other
competitor outputs match exactly. This slice does not complete the remaining
#5714 consumers or public text-boundary contracts.
