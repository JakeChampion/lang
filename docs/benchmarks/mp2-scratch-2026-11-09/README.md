# Reuse MP2 scratch within each frame

Date: 2026-11-09. Issue: #14886. Implementation: #14887, on #14884.

The decoder now reuses subband, synthesis-line, and transform arrays across the three blocks of one MP2 frame.
Seven paired trials show 8.5 to 10.7 percent less decode time.
Allocation counts fall by 30 to 43 percent.
Idle decoder state remains unchanged.
SIMD minimp3 still takes substantially less decode time.

## Change and source

The baseline is #14884 at `49b85d43b9`.
The timed candidate is `1dd46621e4`.
The final source is `6168514fcc443ec0ef54314b92ba3ef3d8fc2326`.
Both sides use the same qualified compiler from #14869.
Its SHA-256 is `a9a739de6bd908526e0820db913c028812ba5da58e4fb54ac9f65d5724814144`.

The decoder allocates its three scratch arrays after frame-header, extent, budget, allocation, and CRC checks.
Active quantizers overwrite their sample range.
Inactive subbands receive zero values before the next reuse.
The transform overwrites its temporary array.
Synthesis returns its line array and subband array for the next block.
All scratch leaves scope at frame completion or failure.

The low-level synthesis entry point requires zero line storage on its first use.
It then accepts the line storage from the previous block of that frame.
The decoder owns this initialization and reuse.
Scratch does not enter the persistent decoder structure.

Named literate chunks share the arithmetic bodies between allocation and reuse entry points.
This keeps the original MP3 allocation, call, and return path.
The final MP3 benchmark executable matches the baseline byte for byte.
The final MP2 benchmark executable matches the timed candidate byte for byte.
`executable-identity.json` retains the four executable hashes and producer hash.

## Profile evidence

The baseline CPU profile contains 965 samples with zero drops.
The 48 kHz stereo frame workload decodes 3,000 rounds.
Zero-fill accounts for 124 CPU samples, or 12.8 percent.
The per-sample checksum accounts for 134 samples.
These profiles include the full benchmark process and its final PCM export.

The allocation profile contains 1,427 samples with zero drops.
It uses 100 rounds and samples every 64 allocation events.
Most estimated bytes belong to temporary float arrays.
This fixed sampling interval can introduce systematic bias.
The exact allocation counters in the paired campaign supply the allocation result.
The folded profiles and process metrics retain the evidence.

## Paired measurements

The campaign runs seven trials with alternating execution order.
Each trial decodes one approximately one-second input 50 times after two warmups.
The timed region includes a byte checksum of every float PCM sample.
The scripts verify exact counts and consistent checksums across modes and trials.
Every Fern PCM export matches scalar minimp3 bit for bit.
The scripts also compare SIMD output and record its error.

| Input | Mode | Baseline median, 50 rounds | Candidate median | Time ratio | Allocations per round, baseline → candidate |
| --- | --- | ---: | ---: | ---: | ---: |
| 48 kHz stereo, 192 kb/s | Complete input | 165.454 ms | 151.456 ms | 0.915 | 944 → 566 |
| 48 kHz stereo, 192 kb/s | Reusable frames | 164.217 ms | 148.383 ms | 0.904 | 885 → 507 |
| 32 kHz mono, 96 kb/s | Complete input | 84.100 ms | 75.907 ms | 0.903 | 550 → 382 |
| 32 kHz mono, 96 kb/s | Reusable frames | 80.364 ms | 72.769 ms | 0.906 | 507 → 339 |
| 16 kHz mono, 32 kb/s | Complete input | 39.458 ms | 35.246 ms | 0.893 | 283 → 199 |
| 16 kHz mono, 32 kb/s | Reusable frames | 38.147 ms | 34.151 ms | 0.895 | 255 → 171 |

The candidate takes 3.08 to 3.52 times the decode time of SIMD minimp3 in this campaign.
All inputs contain protected frames.
Fern validates the CRC. Minimp3 does not.
The baseline and candidate perform the same Fern checks.
`decode-results.json` retains all 168 trials, source hashes, executable hashes, input hashes, errors, and host details.

The native bump-pointer delta remains zero after warmup.
This does not mean zero allocation activity.
Process peak RSS remains close to the baseline, within four KiB for these measured medians.
The small transform array now lives throughout the frame.
These measurements establish no reduction in temporary peak memory.

## State and qualification

The decoder retains the same 960 synthesis values and two format fields.
The state probe reports the same allocation counts and allocator advancement on both sides.
One decoder plus its array uses three allocations and advances the allocator by 8,304 bytes.
One hundred decoders plus their array use 206 allocations and advance it by 826,904 bytes.
The JSON report retains both snapshots.
Native compact numeric storage remains under #14818.

All 66 reference streams retain exact scalar PCM, frame progress, and output suffixes.
All seven boundary groups pass under native sanitizers and WASI.
The protected-frame smoke passes in both interpreters.
All eight native suites balance allocations and end with zero live bytes.
The final qualification also compiles all eight Android ARM64 shared libraries.
`qualification.json` records these 24 target builds.

The MP3 suites retain their allocation counts of 48,030 and 9,700.
The final MP3 benchmark binary is identical to the baseline, so no new MP3 time claim is necessary.
Final source lint passed in 30.750 seconds.
The complexity check passed in 6.979 seconds without a baseline change.
The logs retain these results.

## Reproduce

```sh
python3 scripts/bench-mp2 \
  --compiler /path/to/qualified/stage2 \
  --baseline-stdlib /path/to/14884/internal/stdlib \
  --reference-dir /path/to/hash-pinned/reference-headers \
  --output /tmp/mp2-scratch --rounds 50 --trials 7

FERN_PROFILE=cpu /path/to/qualified/stage2 \
  -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp2-profile bench/audio/mp2.fern
/tmp/mp2-profile /tmp/mp2-scratch/48000-2-192.mp2 frames 3000 /tmp/output.pcm

/path/to/qualified/stage2 -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp2-state docs/benchmarks/mp2-scratch-2026-11-09/state-probe.fern
/tmp/mp2-state 100
```

Full CI, native ARM64 time comparisons, and Android device measurements remain pending.
The CPU, allocation, and numeric-storage gaps remain open under #14886, #14516, and #14818.
