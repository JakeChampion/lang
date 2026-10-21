# Raw byte records in tac

`tac` reads seekable windows and held pipe input into byte arrays. Literal
separators use raw reverse scans and range comparisons; regex separators
use the BRE byte-input API. Output remains bytes through the existing 8 KiB
buffer. Joining windows and accumulating short reads use byte builders.
Record processing no longer constructs unchecked text or slices partial
UTF-8 encodings into string views.

The final compiler passes 46 GNU cases on both Darwin and core WASM, with
exact output and balanced ownership. File and pipe variants cover every
byte value, NUL and Unicode separators, partial-scalar regexes, captures,
before-separator output, empty input, long records and boundaries around
the 8 KiB read window. Matching runs in the C locale, as the utility's
byte-oriented contract requires.

The same final compiler builds the text implementation with the new BRE
engine and the raw candidate. Raw reading, window builders and ownership
add 816 bytes of code and 296 bytes of unwind data; static data is unchanged.
The file grows from 199,025 to 199,041 bytes. No size baseline is changed.

Linux target and GNU parity, full unit and lint checks pass on the combined
record-processing snapshot.

Native measurements compare text and raw input handling with the same new
BRE engine. A 16-repeat pilot precedes identical workloads at 131,072
repeats. All outputs match GNU 9.12 and uutils 0.12.0 in the C locale before
timing. Two warmups precede seven alternating samples, with output directed
to the null device. Peak resident memory is measured in a separate run.
This workstream runs no other compiler or test job during measurement;
other macOS services remain active.

All before/after timing ranges overlap. Raw input uses more peak memory on
pipes and long records. The implementation uses builders to hold streams
and join backward windows; the native `buf_take_bytes` helper allocates and
copies the result while retaining the builder's capacity until `buf_free`.
That temporary overlap remains an optimization opportunity. The census
checks show balanced ownership, but they do not imply equal peak memory.

| Workload | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| plain file | Previous text | 5.673875 | 5.086625-8.068667 | 1,310,720 |
| plain file | Raw bytes | 6.379042 | 5.034042-9.866667 | 1,277,952 |
| plain file | GNU 9.12 | 6.879625 | 6.738459-7.396916 | 1,130,496 |
| plain file | uutils 0.12.0 | 5.408375 | 4.961000-11.364209 | 3,751,936 |
| plain pipe | Previous text | 12.346167 | 11.556292-12.646416 | 4,849,664 |
| plain pipe | Raw bytes | 12.588375 | 11.417000-13.747709 | 6,897,664 |
| plain pipe | GNU 9.12 | 14.494542 | 13.698709-20.682292 | 1,163,264 |
| plain pipe | uutils 0.12.0 | 12.086250 | 11.740959-14.760333 | 3,817,472 |
| long records file | Previous text | 2.185209 | 2.070416-2.660666 | 3,686,400 |
| long records file | Raw bytes | 2.312750 | 2.189583-2.380875 | 5,341,184 |
| long records file | GNU 9.12 | 3.985458 | 3.922583-4.118916 | 1,867,776 |
| long records file | uutils 0.12.0 | 2.240542 | 2.067417-2.424500 | 2,719,744 |
| long records pipe | Previous text | 4.811833 | 4.594708-5.463667 | 5,029,888 |
| long records pipe | Raw bytes | 4.977958 | 4.695958-5.511292 | 7,405,568 |
| long records pipe | GNU 9.12 | 6.584792 | 6.424292-7.424000 | 1,884,160 |
| long records pipe | uutils 0.12.0 | 5.235292 | 5.076458-5.507917 | 2,785,280 |
| NUL separator file | Previous text | 4.575584 | 4.407958-4.835500 | 1,310,720 |
| NUL separator file | Raw bytes | 4.694083 | 4.488584-4.770834 | 1,310,720 |
| NUL separator file | GNU 9.12 | 6.182834 | 5.892250-6.501000 | 1,130,496 |
| NUL separator file | uutils 0.12.0 | 4.602416 | 4.514375-4.923291 | 3,768,320 |
| NUL separator pipe | Previous text | 12.320750 | 11.479541-12.530500 | 4,849,664 |
| NUL separator pipe | Raw bytes | 12.529584 | 10.777000-12.654417 | 6,881,280 |
| NUL separator pipe | GNU 9.12 | 13.733125 | 12.594291-14.352125 | 1,163,264 |
| NUL separator pipe | uutils 0.12.0 | 12.000709 | 10.420042-12.358792 | 3,801,088 |
