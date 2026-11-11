# MP4 composition-run memory comparison

This comparison measures native MP4 indexing with explicit composition-time runs.
The earlier reader expands each run into a temporary 64-bit offset for every packet.
The revised indexer validates complete run coverage and reads the original rows through a scalar cursor.
The public composition reader still returns one offset per sample.

Positive run counts must cover exactly the sample count before packet construction starts.
When the row count equals the sample count, every count must be one.
The cursor uses that proof to read only the offset at later run boundaries.
Signed version-one and unsigned version-zero offsets retain their existing interpretation.

## Method

Both versions use one frozen nonembedded compiler from main `4e38d84c7fe34200e300ad55c05ed92d8b67c6b7`.
Its SHA256 is `21b5152830af8304308e722e4ac9be637d880c62cf04e5d689c49bdb3b604749`.
Explicit library paths select the source before and after the change.
The reports retain compiler, consumer, generator, sampler, input, and changed-module hashes.

Ten workloads cover 65,536 and 262,144 packets, with five composition patterns at each size.
They include absent tables, one constant run, signed eleven-packet runs, and signed or unsigned one-packet runs.
The unsigned case reaches `UINT32_MAX`. Signed cases include negative offsets and both signed 32-bit limits.
Every child process must return the independent packet count and checksum over all retained packet fields.

The synthetic inputs contain one-byte packet payloads. These bytes are not decodable AVC pictures.
Fresh FFprobe and FFmpeg interoperability tests qualify real AVC, HEVC, and QTRLE inputs separately.
The benchmark reuses the earlier metadata generator and the existing native process sampler.
Python does not supply the child's peak-memory measurement.

Seven groups alternate the before/after order.
The report retains raw peak RSS, allocation counts, and elapsed samples.
Actual Valgrind 3.24 guest instruction counts cover all five 262,144-packet controls.
These are executed instruction counts, rather than static text sizes.

## Results

At 262,144 packets, each explicit-table workload removes 2 MiB from median peak RSS and sixteen heap allocations.
At 65,536 packets, it removes 0.5 MiB and fourteen allocations.
The retained packet records remain unchanged.
Peak RSS for the absent-table control stays unchanged.
This control adds one fixed allocation for the validated table view.

| 262,144 packets | Before peak RSS, MiB | After peak RSS, MiB | Before instructions | After instructions | Difference |
| --- | ---: | ---: | ---: | ---: | ---: |
| Absent | 46.25 | 46.25 | 95,002,974 | 96,051,621 | +1.10% |
| Constant | 48.25 | 46.25 | 99,725,077 | 96,052,671 | -3.68% |
| Eleven-packet runs | 48.875 | 46.875 | 102,632,445 | 100,199,251 | -2.37% |
| Signed one-packet runs | 50.5 | 48.5 | 131,706,518 | 131,441,971 | -0.20% |
| Unsigned one-packet runs | 50.5 | 48.5 | 131,706,601 | 131,442,054 | -0.20% |

The absent-table instruction cost is a measured tradeoff, without a memory gain for that control.
Elapsed samples include startup and shared-host contention. They do not establish a general elapsed-time improvement.
These results establish no native ARM speed, player throughput, physical-device power, or lip-sync accuracy.
The input limits still do not define an exact process-memory bound.

## Reproduction

Use the frozen compiler and the two explicit source trees:

```sh
python3 docs/benchmarks/video-mp4-composition-view-2026-11-11/run.py \
  --compiler /path/to/frozen/compiler \
  --before-stdlib /path/to/main-4e38d84c/internal/stdlib \
  --after-stdlib internal/stdlib \
  --output /tmp/composition-report.json \
  --instruction-output /tmp/composition-instructions.json
```

The instruction option requires Valgrind on `PATH`.
Normal execution needs a native C compiler for the existing sampler, without a runtime FFmpeg dependency.
The checked-in `report.json` and `instructions.json` contain the measurements above.
