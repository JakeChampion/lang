# Counted append capacity comparison

The compiler now reserves scalar array capacity for exact counted append loops.
The proof requires a zero start, a unit step, an invariant i32 bound, and one unconditional append per iteration.
The pass skips early exits, mutable bounds, observed prefixes, unknown calls, and effects.
Zero and negative bounds retain the empty array without an allocation.
The native array ABI and allocation checks remain unchanged.

The compiler change starts at commit `e073da87472783724120dfddd7fe9f5c4effd46f`.
Commit `c63b8da8e803cc437ebd470154c48dda23ad9fd6` removes redundant closure analysis after capacity reservations.
The final compiler core SHA-256 is `e45db7860ca628d5b0b36a6102c7685028cdc3d9b44776322640c5572d4c8b71`.
The prior compiler core SHA-256 is `b888ea5fe864d17c077e02bc10efe2c3c76dde47d88814c2951b936098a99e3e`.
Both codec campaigns use the same frozen library and drivers from commit `503bebe1526a9952d8f0c48acedcc30a074afe4b`.
The reports retain source, compiler, library, and reference hashes.

The codec campaigns use the initial candidate core, with SHA-256 `1b84c81ebc8c3a7404b98d2a1978a842cb2a40c625e877d97968a46c26992fe3`.
The final compiler emits byte-identical native executables for both codec drivers.
`output-equivalence.json` retains the commands and executable hashes for this check.
The final compiler also passes fresh codec qualification and the reduced input probe.

## Input construction

The reduced probe constructs 96,000 i32 samples from little-endian PCM bytes.
The prior compiler uses 16 allocations and 2,621,040 fresh runtime heap bytes.
The candidate uses one allocation and 786,432 fresh runtime heap bytes, a 70.0% reduction.
Both outputs contain 60,458 encoded bytes.
This count covers input construction separately from the encoder.

Native i32 arrays still use eight-byte slots.
The 96,000 samples require 768,000 payload bytes before allocator size classes.
This change removes repeated capacity growth.
Compact 32-bit array storage remains separate work in #14818.

## LPC campaign

Each campaign uses seven alternating-order trials on one AMD EPYC 9V74 host.
The process affinity is CPU 0, with a four-core quota.
The local qualification jobs completed before the timed campaigns.
Each input contains 48,000 frames.
The LPC campaign measures four complete encodes after two warmup rounds.
Both compilers use maximum LPC order 32 and maximum partition order six.

| Input | Candidate / prior time | Allocations, candidate / prior | Peak RSS KiB, candidate / prior |
| --- | ---: | ---: | ---: |
| Stereo music, 24-bit | 0.999 | 136,332 / 164,872 | 2,016 / 2,908 |
| Eight-channel music, 24-bit | 1.004 | 133,920 / 161,936 | 7,372 / 10,652 |
| Stereo noise, 16-bit | 1.003 | 123,356 / 141,404 | 2,292 / 2,880 |
| Stereo silence, 24-bit | 1.006 | 1,044 / 1,044 | 1,724 / 2,724 |
| Stereo music, 32-bit | 1.002 | 137,320 / 167,044 | 2,304 / 2,920 |
| Stereo noise, 32-bit | 0.995 | 84,188 / 102,236 | 2,628 / 2,832 |

Time uses the median paired ratio.
Allocation counts and RSS use the median measurement for each compiler.
Non-silent inputs use 12.8% through 17.8% fewer allocation events.
Process peak RSS falls 7.2% through 36.7% across these cases.
The time results do not establish a consistent codec CPU improvement.

RSS uses Linux VmHWM and includes input construction, executable code, warmup, and output storage.
The timed region excludes input construction and warmup.
Fresh runtime heap bytes during the timed region remain zero because the allocator reuses heap blocks.
This figure does not mean zero allocation traffic.
These measurements describe one native x86-64 host.

## Fixed-only campaign

The fixed-only control measures 16 complete encodes per trial.
It uses the same inputs, library, target, and seven alternating-order trials.

| Input | Candidate / prior time | Allocations, candidate / prior | Peak RSS KiB, candidate / prior |
| --- | ---: | ---: | ---: |
| Stereo music, 24-bit | 0.998 | 79,312 / 120,528 | 1,932 / 2,856 |
| Eight-channel music, 24-bit | 1.014 | 69,728 / 109,056 | 7,288 / 10,600 |
| Stereo noise, 16-bit | 1.003 | 28,016 / 28,016 | 2,212 / 2,832 |
| Stereo silence, 24-bit | 1.010 | 4,176 / 4,176 | 1,716 / 2,716 |
| Stereo music, 32-bit | 1.006 | 83,040 / 129,008 | 2,208 / 2,856 |
| Stereo noise, 32-bit | 0.997 | 13,616 / 13,616 | 2,544 / 2,780 |

The music cases use 34.2% through 36.1% fewer allocation events.
The other cases retain their allocation counts during the timed region.
Peak RSS falls 8.5% through 36.8% across these cases.
Eight-channel music takes 1.4% more time, and silence takes 1.0% more time in this campaign.
The report retains both time regressions.
Every complete encoded stream matches the prior compiler byte for byte.

## Compiler costs

Both compilers compile the same checker source and standard library for the comparison.
The allocation and append-copy campaigns disable ownership certification, as the same CI lanes do.

| Metric | Candidate | Prior | Change |
| --- | ---: | ---: | ---: |
| Compiler allocations and frees | 41,722,599 | 41,857,882 | -0.3% |
| Bytes copied at shared appends | 40,126,728 | 40,124,160 | +0.006% |
| Shared append count | 397,570 | 397,567 | +0.001% |

Both allocation campaigns report zero live bytes.
The append-copy count measures one source of compiler copies, not total memory traffic.

The initial candidate performs two redundant closure-inline passes after capacity reservations.
That version uses 5.1% more allocations and 33.3% more shared-append copy bytes.
Its checker compilation takes 16.6% more time with a cold proof cache and 19.5% more time with a warm cache.
Capacity reservations introduce no callbacks, so the repair retains the prior signal for closure analysis.
The initial reports remain in `compiler-comparison-initial.json` and `compiler-native-initial.json`.

The final compiler comparison uses seven alternating-order pairs on CPU 0.
The median candidate/prior time ratio is 0.988 with a cold proof cache and 0.994 with a warm cache.
These trials retain ownership certification and use a separate proof cache for each compiler.
An initial execution initializes each measurement mode before the paired trials.
X25519 and calendar controls retain their output and give median time ratios of 1.001 and 0.999.
The report does not measure complete compiler self-compilation time.

The four scalar append profile cases execute 10.2% through 26.1% fewer instructions under Callgrind.
Their instruction counts repeat within 0.001% in a separate campaign.
The deflate diagnostic and hardened cases also execute fewer instructions.
The other repeated counts differ by less than 0.5%.
These cases contain the randomized map cases.
The portable Callgrind build is Debian Valgrind 3.24.0-3.

All 180 profile metrics complete, with 36 dynamic instruction counts.
Six emitted-size rows grow 1.0% through 2.4% because the reservation adds control flow.
All 14 compiler driver images remain within the five-percent size budget.
The reports retain the raw measurements.

## Qualification and limits

All 81 fixed and 70 LPC reference streams retain their exact bytes.
The independent FLAC decoder and FFmpeg verify PCM and MD5 for each benchmark output.
The exhaustive cost controls retain predictor, Rice, escape, partition, and wasted-bit decisions.

All six FLAC suites pass native sanitizers and WASI execution.
All six also compile as Android ARM64 shared libraries.
The WavPack predictor bank and boundary suites pass the same three target checks.
The selected MP3 and Opus input and file smoke suites also pass.
Every native leak census reports zero live bytes.

The registered compiler controls cover scalar values, aliases, observed prefixes, zero bounds, negative bounds, early exits, mutable bounds, closures, and effects.
The enabled construction control requires exactly one allocation.
The disabled control requires repeated growth.
The prior fusion, scan, report, and counted-unroll controls pass on the selected native and WASI targets.
Source lint and complexity gates pass without larger limits.

This change does not establish the fastest codec performance.
The reports retain the matched libFLAC 1.5.0 results and the remaining CPU gap.
Native ARM64 CI and the full CI verdict remain required before merge.
Physical Android power, latency, thermal behavior, and underruns remain unmeasured.

Issues: #14817, #14818, #14815, #14516, #14508.
