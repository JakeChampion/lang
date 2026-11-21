# MP2 scratch allocation qualification

The same x86-64 Linux compiler and standard library decoded three protected Layer II streams. The two Fern drivers differ only in how they supply temporary storage to frame decode. Each workspace run creates a decoder, output buffer and workspace inside the measured per-stream workload, then reuses them across frames. Both drivers use the same whole-input decoder as a control. Seven trials alternate execution order and decode each stream 50 times after two warmups.

| Frame input | Allocation events per stream, per-frame / workspace | Median milliseconds per stream, per-frame / workspace | Scalar / SIMD minimp3 milliseconds |
| --- | ---: | ---: | ---: |
| 48 kHz stereo, 192 kb/s | 591 / 389 | 2.490 / 2.441 | 0.777 / 0.452 |
| 32 kHz mono, 96 kb/s | 395 / 263 | 1.457 / 1.421 | 0.475 / 0.279 |
| 16 kHz mono, 32 kb/s | 199 / 137 | 0.686 / 0.674 | 0.226 / 0.134 |

Across the three inputs, the workspace avoided 31–34% of frame allocation events. One idle decoder and its containing array advanced the allocator by 4,184 bytes, and 100 decoders by 414,960 bytes, in both builds. These counts exclude the optional workspace. Process peak RSS, cumulative fresh bytes, whole-process CPU, wall time, output errors, tool hashes, and all 168 individual trials are in the [raw results](results.json).

The complete Fern PCM exports matched scalar minimp3 byte for byte. The largest absolute difference against SIMD was below 0.00000024. Fern checks the protected-frame CRC. Minimp3 skips that check. The workspace frame path took 5.0–5.4 times the SIMD reference time on these inputs. Fern's [previous native ARM64 campaign](../audio-decode-2026-11-18/README.md) compared the combined synthesis and workspace changes against their historical baseline. Its native ARM64 time reduction of 7–12% has a different baseline from this same-library comparison.

All four shards of the 66-stream independent corpus, the eight boundary cases, and the two smoke controls passed on native x86-64 Linux, ARM64 Linux and Android under QEMU Linux user-mode, and WASI under Wasmtime. The native sanitizer run passed the same six suites. Each suite reported zero live bytes at exit. The tests check both frame APIs, caller buffer suffixes, frame progress and exact scalar PCM. The earlier [scratch profiles](../mp2-scratch-2026-11-09/README.md) and [synthesis profile](../audio-decode-2026-11-18/README.md) describe the measured causes and the changes.

Reproduce using the two minimp3 headers pinned by the benchmark script.

```sh
python3 scripts/bench-mp2 --compare-workspace --compiler build/bootstrap/stage1 \
  --reference-dir /path/to/minimp3 --output /tmp/mp2-workspace --rounds 50 --trials 7
```
