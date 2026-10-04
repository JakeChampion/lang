# Raw byte records and delimiters in paste

Serial and parallel `paste` now keep input chunks, the shared stdin cursor
and pending empty-column delimiters as bytes. Delimiter options cycle over
individual bytes, including each byte of a UTF-8 argument. NUL delimiter
entries advance that cycle without producing output. Filenames, options
and diagnostics remain text.

The final stage-2 compiler passes 12 GNU cases on Darwin and core WASM with
exact output and balanced ownership. They cover every byte value, serial
and parallel LF/NUL records, UTF-8 and suppressed delimiters, long split
records, shared stdin and empty columns.

The same final compiler builds the parent and candidate. Raw input and byte
delimiter handling add 416 bytes of code and 272 bytes of unwind data.
Static data and the 116,273-byte file size are unchanged. No size baseline
is changed.

Linux target and GNU parity, full unit and lint checks pass on the combined
record-processing snapshot.

An 8 KiB pilot precedes the same native pipeline at 8 MiB per file. All
outputs match GNU 9.12 and uutils 0.12.0 before timing. Two warmups precede
seven alternating samples with output directed to the null device; peak
resident memory is measured separately. This workstream runs no other
compiler or test job during measurement, although other macOS services
remain active. Every before/after sample range overlaps. The raw change
does not establish a speedup or a substantial memory reduction.

| Workload | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| parallel lines | Previous text | 20.934750 | 20.009667-21.856417 | 1,507,328 |
| parallel lines | Raw bytes | 21.501583 | 20.150833-22.055208 | 1,507,328 |
| parallel lines | GNU 9.12 | 63.674708 | 63.024958-65.858583 | 1,196,032 |
| parallel lines | uutils 0.12.0 | 252.088791 | 249.583500-255.875125 | 1,769,472 |
| serial lines | Previous text | 16.366666 | 15.531250-16.626459 | 1,425,408 |
| serial lines | Raw bytes | 16.711334 | 16.281459-17.058875 | 1,425,408 |
| serial lines | GNU 9.12 | 59.816583 | 59.479875-60.426625 | 1,196,032 |
| serial lines | uutils 0.12.0 | 26.622625 | 25.961875-27.577250 | 16,531,456 |
| parallel long | Previous text | 11.687333 | 11.432042-12.656750 | 68,534,272 |
| parallel long | Raw bytes | 11.590291 | 11.182500-12.605000 | 68,501,504 |
| parallel long | GNU 9.12 | 47.988708 | 47.777666-49.321541 | 1,245,184 |
| parallel long | uutils 0.12.0 | 9.045875 | 8.835708-9.910334 | 27,082,752 |
| serial long | Previous text | 5.470875 | 5.270833-5.936417 | 1,540,096 |
| serial long | Raw bytes | 6.018917 | 5.080791-6.573625 | 1,523,712 |
| serial long | GNU 9.12 | 39.474083 | 38.750333-40.654625 | 1,196,032 |
| serial long | uutils 0.12.0 | 8.689416 | 8.216083-9.158417 | 18,694,144 |
