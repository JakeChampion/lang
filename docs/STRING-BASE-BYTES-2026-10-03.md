# Raw buffers for base encodings

The shared base64, base32 and basenc implementation reads byte arrays.
Short reads accumulate in a builder until the codec's block is full or
input ends. Decoding writes byte ranges, preserving arbitrary output
without constructing invalid strings. Base58's whole-input accumulator
also uses a byte builder. Its generated ASCII alphabet remains text.

Successful runs now release the codec and decoder lookup tables. The
unchanged parent leaves 1,096 bytes allocated after empty base64 encoding
and 263,304 after empty decoding on both Darwin and core WebAssembly.

The tests also exposed two existing GNU differences. Incomplete base32
padding now withholds that final group's bytes, and z85 retains NUL under
`--ignore-garbage` so the decoder rejects it. Both differences were
reproduced against the unchanged parent before correction.

The shared GNU comparison exercises all nine basenc codecs, both
standalone utilities, every byte, empty input, block boundaries, malformed
input, ignored garbage and fragmented pipes. Base58 covers every byte
on a small input and zero bytes across the read boundary; its existing
quadratic conversion algorithm is outside this change.

The reproduced compiler passes the basenc corpus on native Darwin and
core WebAssembly with balanced allocations on successful executions. Error
exits retain allocations through the existing immediate-exit paths; the
allocation claim does not cover those paths. Compiler reproduction is in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

All three utilities add 1,244 code bytes and 432 unwind bytes; static data
is unchanged. The raw reader, short-read builder, byte output and table
cleanup account for the added paths. Base64 and base32 grow from 116,305
to 132,817 file bytes: crossing a text-segment boundary adds 16,384 bytes
and the code signature adds 128. Basenc stays at 132,929 file bytes.
No size baseline changes.

Native arm64 Darwin benchmarks use GNU 9.12 and uutils 0.12.0. The
8,192-byte pilot precedes 8,388,608 source bytes, changing only that scale.
Decoded inputs are the corresponding encoded streams. Base58 uses zero
source bytes, so these measurements do not characterize its quadratic
general conversion. All four implementations match GNU status, output
and stderr at both scales. Two warmups precede seven alternating samples;
RSS is measured separately, with compiler and container jobs idle.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| base64_encode | text | 35.022 | 34.380-36.079 | 1,327,104 |
| base64_encode | bytes | 31.327 | 30.553-34.543 | 1,277,952 |
| base64_encode | gnu | 7.185 | 6.508-7.700 | 1,294,336 |
| base64_encode | uutils | 5.709 | 5.472-6.332 | 1,884,160 |
| base64_decode | text | 27.562 | 27.195-28.416 | 1,572,864 |
| base64_decode | bytes | 24.106 | 23.643-26.167 | 1,605,632 |
| base64_decode | gnu | 14.082 | 13.566-18.087 | 1,212,416 |
| base64_decode | uutils | 16.987 | 16.754-17.997 | 1,884,160 |
| base32_encode | text | 35.304 | 34.951-37.355 | 1,343,488 |
| base32_encode | bytes | 33.099 | 31.581-33.563 | 1,294,336 |
| base32_encode | gnu | 9.207 | 8.391-9.589 | 1,294,336 |
| base32_encode | uutils | 7.778 | 7.241-8.537 | 1,900,544 |
| base32_decode | text | 28.001 | 27.837-28.912 | 1,605,632 |
| base32_decode | bytes | 23.983 | 23.558-24.445 | 1,638,400 |
| base32_decode | gnu | 12.976 | 12.118-13.286 | 1,228,800 |
| base32_decode | uutils | 58.521 | 58.351-59.439 | 1,900,544 |
| base16_encode | text | 55.058 | 54.478-55.364 | 1,376,256 |
| base16_encode | bytes | 52.036 | 51.023-53.100 | 1,310,720 |
| base16_encode | gnu | 10.521 | 9.456-10.742 | 1,359,872 |
| base16_encode | uutils | 10.188 | 9.748-10.404 | 1,916,928 |
| base16_decode | text | 33.973 | 33.262-34.245 | 1,572,864 |
| base16_decode | bytes | 33.081 | 32.671-36.236 | 1,589,248 |
| base16_decode | gnu | 20.239 | 19.556-22.089 | 1,310,720 |
| base16_decode | uutils | 24.021 | 22.750-25.256 | 1,982,464 |
| z85_encode | text | 40.983 | 40.022-45.694 | 1,310,720 |
| z85_encode | bytes | 37.833 | 37.474-39.365 | 1,392,640 |
| z85_encode | gnu | 16.240 | 15.143-16.621 | 1,343,488 |
| z85_encode | uutils | 11.820 | 11.133-11.971 | 1,916,928 |
| z85_decode | text | 54.497 | 53.945-55.597 | 1,327,104 |
| z85_decode | bytes | 51.697 | 50.824-52.934 | 1,359,872 |
| z85_decode | gnu | 32.659 | 31.641-33.578 | 1,277,952 |
| z85_decode | uutils | 18.692 | 18.049-19.847 | 1,949,696 |
| base58_encode | text | 30.500 | 30.111-32.149 | 80,674,816 |
| base58_encode | bytes | 25.254 | 23.071-26.205 | 50,888,704 |
| base58_encode | gnu | 10.402 | 9.350-10.795 | 26,132,480 |
| base58_encode | uutils | 11.854 | 11.520-12.914 | 27,131,904 |
| base58_decode | text | 44.402 | 43.962-54.095 | 80,674,816 |
| base58_decode | bytes | 44.939 | 43.174-47.208 | 50,888,704 |
| base58_decode | gnu | 26.739 | 25.039-29.056 | 25,706,496 |
| base58_decode | uutils | 17.065 | 15.728-17.796 | 10,321,920 |

Seven workloads improve with disjoint timing ranges. Base64 encoding,
base16 decoding and base58 decoding have overlapping ranges. Sampled RSS
falls for encoding in base64, base32, base16 and base58; other workloads
vary. The raw buffers do not make Fern uniformly faster than GNU or uutils.

Linux raw-byte and primary target checks, GNU and primary corpora, the
full unit suite and all lint checks pass. The Darwin raw-byte, GNU and
primary corpora also pass. No Darwin exceptions were added.
