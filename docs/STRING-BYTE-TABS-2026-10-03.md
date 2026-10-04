# Raw byte input for expand and unexpand

Both utilities keep input chunks and complete lines as byte arrays. A line
crossing a read boundary accumulates in a byte builder, allocated only when
needed and released after extraction. This also preserves a partial line
across operands and input errors. Diagnostics and tab-stop options remain
text.

The accompanying WASM runtime repair releases temporary paths for
payloadless I/O errors and releases symlink-read scratch storage on both
success and failure. Existing borrowed-path retains remain in place.

## Validation

The pinned bootstrap reaches identical stages 2 and 3 at 12,513,601 bytes,
SHA-256
`229bf8df28e6223e68b6edd3be78762aca1d2946a5838e90010b5e4ee631d8f3`.
The final compiler passes seven cases per utility on both Darwin and core
WASM, with exact GNU output and balanced allocations. These cover all byte
values, long lines and unterminated tails, continuation across operands,
empty input and open/read errors while a partial line is retained.

Repeated core WASM probes cover descriptor flags, payloadless errors and
17 filesystem cases. They balance ownership while checking that a caller's
heap-backed path remains valid after its error is released. Three additional
component cases verify read-text, read-bytes and write behavior. Components
do not emit an exit-time allocation census, so no whole-component allocation
balance is claimed.

Linux target and GNU parity checks, the full unit suite and all lint gates
pass on the frozen source snapshot. The changed implementation and test
files match that snapshot byte-for-byte.

## Size

The same final compiler builds the parent and candidate. Runtime cleanup
adds 648 bytes of compiler code and 256 bytes of static data; unwind data is
unchanged and the file grows 32 bytes. `expand` adds 1,124 bytes of code and
`unexpand` adds 1,140 bytes for raw reading, builder accumulation and array
ownership. Each adds 320 bytes of unwind data with unchanged static data.
All four utility files remain 116,321 bytes. No size baseline was changed.

## Native measurements

Each utility first passes an 8 KiB pilot, then repeats the same pipeline at
8 MiB. Exact output is checked against GNU before timing. Two warmups
precede seven alternating runs with output directed to the null device;
peak resident memory is measured separately. No other compiler or test job
from this workstream runs during measurement.

| Utility and input | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| expand, short lines | Previous text | 32.631500 | 32.094375-33.514042 | 1,376,256 |
| expand, short lines | Raw bytes | 32.426958 | 32.037042-33.825625 | 1,376,256 |
| expand, short lines | GNU 9.12 | 196.040000 | 195.283667-202.478834 | 1,228,800 |
| expand, long line | Previous text | 59.839500 | 58.423959-64.470417 | 93,208,576 |
| expand, long line | Raw bytes | 55.461541 | 54.871250-91.626709 | 54,886,400 |
| expand, long line | GNU 9.12 | 203.874667 | 197.950166-205.431041 | 1,228,800 |
| unexpand, short lines | Previous text | 34.110583 | 33.175750-35.811042 | 1,294,336 |
| unexpand, short lines | Raw bytes | 34.407209 | 33.726833-35.892958 | 1,294,336 |
| unexpand, short lines | GNU 9.12 | 193.672959 | 192.688167-203.721084 | 1,294,336 |
| unexpand, short lines | uutils 0.12.0 | 48.720000 | 47.594000-51.440875 | 1,916,928 |
| unexpand, long line | Previous text | 41.686125 | 41.443250-44.529584 | 72,712,192 |
| unexpand, long line | Raw bytes | 39.066416 | 38.209250-39.469833 | 34,897,920 |
| unexpand, long line | GNU 9.12 | 194.950708 | 191.097417-227.711333 | 1,245,184 |
| unexpand, long line | uutils 0.12.0 | 47.326875 | 46.539792-47.674166 | 1,916,928 |

Short-line before/after ranges overlap. Both raw implementations use less
memory on the long-line fixture. `unexpand` is faster there; `expand` has a
lower median but overlapping ranges, including the retained 91.626709 ms
sample. These measurements do not establish a general speedup.

uutils 0.12.0 `expand` differs from GNU on both fixtures and is excluded from
timings. A reduced input of NUL, tab and newline produces NUL followed by
eight spaces with GNU 9.12, but seven spaces with uutils 0.12.0. Both Fern
implementations match GNU. uutils `unexpand` matches the measured fixtures.
