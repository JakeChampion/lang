# Native MPEG Layer II decode

Date: 2026-11-09. Related issue: #14879. Parent: #14689 and #14508.

Fern now decodes indexed MPEG-1 and MPEG-2 Layer II frames.
The independent corpus and boundary tests pass on native Linux and WASI.
Android ARM64 shared libraries compile.
Native ARM64 CI and Android device execution remain pending.

Fern takes 3.40 to 3.87 times the time of SIMD minimp3 in this campaign.
This implementation does not establish a performance lead.
It matches scalar minimp3 PCM bit for bit on every reference stream.

## Producer and references

The qualified self-hosted compiler comes from #14869 at `921a5e3daf81e0bce5e3127f605d026416607000`.
Its stage-two executable matches stage three.
The executable SHA-256 is `a9a739de6bd908526e0820db913c028812ba5da58e4fb54ac9f65d5724814144`.
The measured audio code comes from `1e785c7a2f48e2545712cb261fb23182b1945b3a`.
The MP3 baseline replaces only `std/audio/mp3/synthesis` with its version from main at `aafef0e89a`.
Both sides use the same compiler and all other library files.

The C decoder is minimp3 at `ea99364f61c14656440e8d77e9c233ccf3124633` under CC0.
The scripts verify both reference headers by SHA-256.
The C builds use `-O3 -ffp-contract=off`.
The scalar build disables SIMD.
The SIMD build uses the reference defaults.
The JSON reports record the host, tool versions, input hashes, library hashes, and executable hashes.

## Correctness and boundaries

The corpus contains 66 streams from FFmpeg and TwoLAME.
It covers six rates, four channel modes, tones, transients, deterministic noise, and silence.
Twenty-four streams contain protected frames.
The tests compare every PCM sample with the scalar C decoder.
They also compare complete-input decode with repeated frame decode into reusable output storage.
Suffix sentinels verify the unused output region.

The generator checks every reference against the FFmpeg float decoder with CRC checks enabled.
The largest absolute difference between the independent decoders is less than 0.00000194.
FFmpeg receives the unchanged MPEG payload through a WAVE tag `0x50` wrapper.
This avoids a raw-demux restriction on short streams with channel-mode changes.
The wrapper changes no MPEG header or payload byte.

Seven boundary groups check incomplete prefixes, reserved headers, budgets, CRC failures, storage retries, state resets, and payload mutations.
Native tests enable strict IR checks, sanitizers, and leak checks.
All eight native suites end with balanced allocations and zero live bytes.
WASI executes the same eight suites.
All eight Android ARM64 shared libraries compile.
`qualification.json` records the 24 final executable hashes and results.

Both interpreters pass the thin protected-frame smoke, CRC failure, and PCM budget controls.
The Go interpreter also passes all seven boundary groups.
The self-hosted interpreter passes six boundary groups before the mutation campaign exceeds the 120-second limit.
Routine interpreter CI keeps the thin smoke case.
Compiled corpus and boundary gates retain complete coverage.

Full source lint passed in 34.756 seconds.
The complexity check passed in 9.631 seconds without a baseline change.
The logs retain these results.

## Decode time and allocation counts

The campaign uses seven trials with alternating execution order.
Each trial decodes the same approximately one-second input 50 times after two warmups.
Every timed decode includes a byte checksum of every PCM sample.
The drivers export PCM after the timed region for an independent comparison.
The scripts check exact sample counts and consistent checksums across modes and trials.
No other build or test runs during the final MP2 campaign.

The inputs use TwoLAME, deterministic noise, and protected frames.
Fern validates the CRC. Minimp3 does not validate the CRC.
The comparison measures the same encoded input and output extent with this feature difference.
The frame mode reuses the caller output buffer.
The complete-input mode also constructs the full PCM result.

| Input | Mode | Fern median, 50 rounds | SIMD C median | Fern/C | Fern allocations per round |
| --- | --- | ---: | ---: | ---: | ---: |
| 48 kHz stereo, 192 kb/s | Complete input | 165.827 ms | 48.800 ms | 3.40 | 944 |
| 48 kHz stereo, 192 kb/s | Reusable frames | 157.753 ms | 43.474 ms | 3.63 | 885 |
| 32 kHz mono, 96 kb/s | Complete input | 80.480 ms | 22.382 ms | 3.60 | 550 |
| 32 kHz mono, 96 kb/s | Reusable frames | 78.451 ms | 20.580 ms | 3.81 | 507 |
| 16 kHz mono, 32 kb/s | Complete input | 38.768 ms | 10.024 ms | 3.87 | 283 |
| 16 kHz mono, 32 kb/s | Reusable frames | 37.822 ms | 9.962 ms | 3.80 | 255 |

Fern matches scalar minimp3 bit for bit in every benchmark mode.
SIMD output differs slightly because the reference uses a different arithmetic order.
`decode-results.json` retains every trial and the exact output errors.

The raw `fresh_bytes` counter measures advancement of the native heap bump pointer.
Its zero delta after warmup does not mean zero allocation activity.
The nonzero allocation counts above remain the relevant allocation result.
Process peak RSS includes executable and runtime costs.
It is not a decoder-state measurement.

## Decoder state

The decoder stores 960 synthesis values and two format fields.
It retains no frame scratch, encoded input, or PCM output.
The logical float payload occupies 3,840 bytes.
Native Fern arrays currently use eight-byte slots and capacity rounding for these values.
Issue #14818 tracks compact native numeric storage.

The explicit state probe retains all decoder values until after its memory snapshot.
One decoder plus its containing array advances the allocator by 8,304 bytes in three allocations.
One hundred decoders plus their containing array advance it by 826,904 bytes in 206 allocations.
These counts include array growth and allocation overhead.
They do not measure only the logical decoder fields.
The sanitized 100-decoder probe ends with zero live bytes.

The reference `mp3dec_t` occupies 6,668 bytes, of which 3,840 bytes hold synthesis history.
That structure also contains Layer III fields that Layer II does not need.
The reference-state probe records both sizes.
These measurements do not establish a state-memory lead for Fern.

## MP3 regression comparison

The shared synthesis path now accepts 12 or 18 subband columns.
MP3 still uses 18 columns and retains its prior decoder layout.
The seven-trial comparison uses the same compiler and libraries that differ only in this shared module.

Indexed complete-input time changes from 387.070 to 373.555 ms for 50 rounds.
Indexed frame time changes from 357.485 to 360.954 ms.
The corresponding ratios are 0.965 and 1.010.
These results show no material regression on this host and do not establish an MP3 speed improvement.

Allocation counts remain unchanged for indexed MP3 in both modes.
All scalar PCM remains bit for bit identical.
The native, WASI, and Android regression gates also pass.
`mp3-regression-results.json` retains the full comparison, including free-format controls.

## Reproduce

```sh
source /path/to/the/pinned/toolchain/environment
python3 scripts/gen-mp2-tables.py --source /path/to/minimp3.h --check
python3 scripts/gen-mp2-vectors.py --minimp3 /path/to/minimp3.h --check
python3 scripts/bench-mp2 \
  --compiler /path/to/qualified/stage2 \
  --reference-dir /path/to/hash-pinned/reference-headers \
  --output /tmp/mp2-benchmark --rounds 50 --trials 7

FERN_SANITIZE=1 FERN_LEAKCHECK=1 FERN_STRICT_IR=1 \
  /path/to/qualified/stage2 -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp2-boundaries tests/stdlib/mp2_boundary_test.fern
/tmp/mp2-boundaries

/path/to/qualified/stage2 -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp2-state docs/benchmarks/mp2-2026-11-09/state-probe.fern
/tmp/mp2-state 100
```

Full CI, native ARM64 timings, and Android power, latency, and thermal measurements remain open.
Layer I, free-format Layer II, tags, source playback, and seek indexes remain under #14689.
