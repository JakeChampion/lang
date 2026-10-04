# DD records stay in bytes

DD reads and carries records as byte arrays through translation, swab,
padding, block/unblock conversion, output assembly and writes. Invalid UTF-8
and split codepoints never become strings. Argument and diagnostic text keep
their existing contracts.

The persistent output and conversion builders use `io_buffered.BufBlock`
owners. Temporary builders are freed after extraction, and the normal main
path returns its status so local owners are released. This also removes the
old implementation's measured builder leaks.

## Validation

The shared GNU 9.12 corpus has 138 cases: all byte values, malformed UTF-8,
empty input, file/stdin input, translation and record conversion combinations,
and 4096/65536-byte boundaries. The Go bootstrap and source-built primary
compiler pass their Linux target tests. Primary coverage includes x86-64,
ARM64 and WASM, with balanced allocation counts.

The integrated snapshot, including Stdio's borrowed direct-write repair,
passes the full Linux unit suite and all lint gates. Darwin's complete GNU
DD group and primary DD tests pass. The reproduced primary compiler passes
276 actual native/core-WASM DD cases in 3.390 seconds, and all 366 Stdio
cases in 2.372 seconds. Both artifact suites require balanced allocation
counts and zero live bytes.

## Measurements

The before DD source is from `4eb21393c`; both versions use the candidate's
same coreutils library and standard library. Both use the reproduced compiler
whose stages two and three match, SHA-256
`c111e77206853b94b981796fdf3319e9cd527ed395526c73e3c4f9d1cb273ed0`.
Task-owned heavy jobs were idle during timing; the desktop was not isolated.

An 8192-byte pilot precedes an 8388608-byte input, changing only the input
size. Raw workloads repeat bytes 0 through 255. Block conversion repeats
` a\xff b\x00c\x80 \n\nlast\n`. Every run reads and writes temporary files with
`status=none`. Exact file output is compared with GNU before timing and after
every sample. Two warmups precede seven samples in alternating implementation
order. Allocation counts and peak RSS use separate runs.

| Workload | Options | Before median | After median | Before allocations | After allocations |
| --- | --- | ---: | ---: | ---: | ---: |
| Copy | `bs=65536` | 6.525 ms | 5.070 ms | 700 | 702 |
| Assemble | `ibs=4093 obs=8191 iflag=fullblock` | 12.577 ms | 12.976 ms | 14,437 | 13,413 |
| Uppercase | `bs=65536 conv=ucase` | 7.324 ms | 9.024 ms | 1,219 | 1,093 |
| Swab | `ibs=65535 obs=65536 iflag=fullblock conv=swab` | 13.012 ms | 13.076 ms | 1,771 | 1,515 |
| Block | `ibs=65536 obs=65536 cbs=16 conv=block` | 74.164 ms | 70.335 ms | 4,197,351 | 1,575,401 |

Only block conversion has disjoint before/after timing ranges: 73.366-88.752
ms before and 69.333-71.083 ms after. The other ranges overlap. All candidate
allocations are freed. The old implementation retains 131,152, 20,592,
131,152, 196,720 and 33,698,960 bytes respectively; these are baseline leaks,
not passing allocation checks. Block conversion emits 25,165,824 bytes;
the other workloads each emit 8,388,608 bytes.

Both native executables occupy 166,385 bytes. Code grows from 124,364 to
126,344 bytes and unwind data from 14,820 to 15,236 bytes; data remains
14,168 bytes. The byte paths and owned builder cleanup replace the text
paths. No binary-size baseline is changed.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`, using
the same inputs, options and sample order. All outputs match exactly.

| Workload | Fern median | GNU median | uutils median | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Copy | 5.070 ms | 6.067 ms | 4.678 ms | 1,294,336 B | 1,245,184 B | 2,326,528 B |
| Assemble | 12.976 ms | 14.542 ms | 15.455 ms | 1,245,184 B | 1,212,416 B | 44,187,648 B |
| Uppercase | 9.024 ms | 9.680 ms | 6.826 ms | 1,441,792 B | 1,310,720 B | 2,981,888 B |
| Swab | 13.076 ms | 7.409 ms | 7.937 ms | 1,523,712 B | 1,359,872 B | 19,038,208 B |
| Block | 70.335 ms | 29.290 ms | 118.799 ms | 2,064,384 B | 1,310,720 B | 4,227,072 B |

Fern is slower than both competitors on swab, and between GNU and uutils on
block conversion, with disjoint timing ranges. The other comparisons overlap.
This change completes DD's raw-record migration; it does not complete #5714's
remaining consumers or text-boundary contracts.
