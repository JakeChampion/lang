# Raw byte input for wc

`wc` counts raw byte chunks and reads `--files0-from` lists as bytes. A
complete filename is validated before it becomes a text path; malformed
or truncated UTF-8 names produce a diagnostic and exit status 1. Data
being counted can contain arbitrary bytes, including NUL and malformed UTF-8.
C-locale word counting follows GNU's no-break-space behavior and honors
`POSIXLY_CORRECT`, including an explicitly empty environment value.

The native environment helper now allocates exactly the copied value's
length. Empty values use the immortal empty string. This fixes the
allocation-size mismatch and empty-value leak exposed by these cases.

The pinned bootstrap reaches identical stages 2 and 3: 12,530,337 bytes,
SHA-256 `1cc7b7545ad0ade5611f4e0d06426ea818e6c06542da5a29ab2ca651c013b627`.
That compiler passes all 40 shared cases on Darwin and core WASM with
balanced allocations. Cases cover every byte value, read boundaries,
word/line/width carry, files and pipes, filename scalars spanning reads,
invalid filenames, and environment values of lengths zero, one, eight
and sixteen. Linux primary targets, GNU parity, the full unit suite and
all lint checks pass on an unchanged source snapshot.

The final compiler builds both text and byte consumers. The byte version
adds 3,140 bytes of code and 840 bytes of unwind data for raw reads and
filename validation. Static data and the 149,473-byte file size are
unchanged. The compiler itself gains 48 bytes of code and eight bytes of
unwind data relative to the previous fixed point; data and file size stay
the same. Rebuilding the parent with the final generator includes the
fixed environment runtime too, so that parent is not byte-identical to the
previous compiler. No baseline changes.

An 8 KiB pilot precedes the same native workload at 8 MiB. Input repeats
malformed UTF-8 followed by words, a tab and a newline. Both Fern versions
match GNU 9.12 before timing, with `LC_ALL=C` and `POSIXLY_CORRECT` unset.
Two warmups precede seven alternating samples. Output goes to the null
device; peak resident memory is measured separately. No compiler or test
job from this workstream runs during timing; other macOS services remain
active.

| Workload | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| File lines | Previous text | 2.432500 | 2.356792-3.160833 | 1,277,952 |
| File lines | Raw bytes | 2.539333 | 2.323542-3.268125 | 1,277,952 |
| File lines | GNU 9.12 | 5.740291 | 5.635625-5.902375 | 1,392,640 |
| File lines | uutils 0.12.0 | 2.677625 | 2.546500-3.182083 | 1,900,544 |
| File words | Previous text | 7.157750 | 7.032834-11.640792 | 1,277,952 |
| File words | Raw bytes | 7.102208 | 7.007167-7.631833 | 1,277,952 |
| File words | GNU 9.12 | 13.749584 | 13.387458-14.293625 | 1,392,640 |
| File words | uutils 0.12.0 | 20.953917 | 20.056375-23.126375 | 1,916,928 |
| File width | Previous text | 7.937292 | 7.743250-8.195667 | 1,277,952 |
| File width | Raw bytes | 8.235000 | 8.016958-8.527625 | 1,277,952 |
| File width | GNU 9.12 | 13.453208 | 12.848750-13.830791 | 1,392,640 |
| File width | uutils 0.12.0 | 20.873959 | 19.897791-45.481375 | 1,916,928 |
| File all counts | Previous text | 13.383250 | 13.033334-13.625291 | 1,277,952 |
| File all counts | Raw bytes | 13.051625 | 12.935541-13.870208 | 1,277,952 |
| File all counts | GNU 9.12 | 13.353750 | 13.090291-14.209750 | 1,392,640 |
| Pipe all counts | Previous text | 32.745375 | 32.423250-34.376125 | 1,261,568 |
| Pipe all counts | Raw bytes | 32.296750 | 31.380916-34.354000 | 1,261,568 |
| Pipe all counts | GNU 9.12 | 34.326375 | 33.852875-35.278417 | 1,196,032 |

Before/after timing ranges overlap on every workload, and the sampled
peak memory is equal for the Fern pair. These measurements establish no
general speedup. uutils 0.12.0 matches the individual line, word and width
cases. It reports 7,294,440 characters where GNU and Fern report 8,388,608
in the all-counts binary fixture, so it is excluded from those two timing
comparisons rather than treated as an equivalent implementation.
