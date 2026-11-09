# Matched QTRLE decode checkpoint

Date: 2026-11-08. Target: x86-64 Linux.

This checkpoint compares the public Fern and FFmpeg QTRLE decode APIs on identical packets.
The compiler hash, source hashes, corpus hashes, tool versions, and every trial are in [the JSON report](x86-64-linux.json).
The codec source tree is `440ca369671635e454ad5e1b65b98e5ddb442240`.
The Fern compiler SHA-256 is `33ff1c9c434698775b673c378c2de4c39aabfc7feba8a60487f5f22a74a3e18a`.

The host reports `INTEL(R) XEON(R) PLATINUM 8573C`.
This is a shared host. The measurement does not control clock frequency or background load.

Each decoder uses one thread and retains only the current frame.
Each process completes one corpus cycle before the timed loop.
The loop includes public decode calls and one sampled ARGB pixel per frame.
Setup, complete-frame verification, and output writes remain outside that loop.
Seven pairs alternate their execution order.

The timed executable contains no allocation counter. Assembly checks enforce this condition.
A separate instrumented executable measures Fern allocations on the same packets and frame count.
That executable must produce the same checksums. Its time and RSS do not enter the comparison.

| Workload | Frames per trial | Fern median ms | FFmpeg median ms | Fern peak RSS MiB | FFmpeg peak RSS MiB | Fern probe allocations per call |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rgb24-runs | 3392 | 189.641 | 84.687 | 3.12 | 10.50 | 436.50 |
| argb32-literals | 3392 | 730.786 | 94.686 | 3.75 | 11.25 | 436.50 |
| argb32-hd-sparse | 244 | 705.746 | 141.691 | 47.75 | 25.88 | 4596.75 |
| rgb24-hd-dense | 244 | 1823.722 | 405.908 | 51.75 | 33.88 | 13772.00 |

Fern takes 2.2 to 7.7 times the FFmpeg loop time on these four workloads.
Whole-process RSS is lower for Fern on the small images and higher on the 1080p images.
These results identify decode time, transient allocations, and large-frame memory as optimization targets.

RSS includes the runtime, shared libraries, preloaded packets, and frame storage.
It is not a decoder-only memory measurement. FFmpeg heap allocation counts remain unmeasured.
The native sampler excludes the inherited RSS floor of the Python process.
Complete-process user and system CPU times include setup and warmup.

Every decoded canonical ARGB byte matches the source in Fern, the native sanitizer build, FFmpeg, and WASI.
The cases repeat four packets with one unchanged frame. They do not represent general video playback.
The results do not establish Android throughput, physical-device power, or lip-sync accuracy.

## Reproduce

Install FFmpeg, `libavcodec-dev`, `libavutil-dev`, `pkg-config`, and a C compiler.
Use the pinned Fern toolchain from the repository.

```sh
scripts/compare-video-qtrle --wasi --output build/video/qtrle-comparison.json
```

This run used FFmpeg headers and shared libraries from Debian 7.1.5.
The JSON records the exact compiler and linker flags.
`FFMPEG_CFLAGS` and `FFMPEG_LIBS` can select a specific header and library installation.
`FERN` can select a compiler binary. An unset `FERN` rebuilds the local self-hosted compiler.
Archive the new report when a compiler, codec, corpus, or host changes.
