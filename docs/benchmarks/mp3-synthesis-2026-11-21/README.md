# MP3 synthesis scratch and retained history

MPEG-1 places both granules in a single synthesis line array. The second granule reads the first granule’s trailing 15 columns directly. The decoder copies those columns to the public history array only at the end of the frame and shares 128 transform floats between channels and granules. MPEG-2 and MPEG-2.5 keep the single-granule synthesis path. No scratch is retained between frames. The decoder still retains 960 synthesis floats and 576 overlap floats plus up to 511 reservoir bytes. For MPEG-1 the logical line array grows from 2,112 to 3,264 floats and replaces two separate line initializations per frame.

The prior library was taken from `d3a88182`. Both libraries used the same compiler on x86-64 Linux. Each of seven trials used two warmups followed by 30 complete stream decodes. Execution order alternated and the producer was pinned to CPU 0. Each campaign compared indexed and free-format inputs, whole-file and reusable frame entry points, and both scalar and SIMD minimp3. The producer checks every final PCM sample outside the timed region. The raw reports include process CPU, output digests, allocation events, observed peak RSS, and all 336 timed runs.

| Free-format frame decode | Allocations per stream, prior / new | Median ms per stream, prior / new | Scalar / SIMD minimp3 ms | Process peak KiB, prior / new |
| --- | ---: | ---: | ---: | ---: |
| 48 kHz, 2 ch | 2,959 / 2,701 | 5.586 / 5.522 | 0.968 / 0.659 | 280 / 312 |
| 24 kHz, 1 ch | 1,187 / 1,187 | 1.480 / 1.517 | 0.392 / 0.230 | 248 / 256 |
| 8 kHz, 1 ch | 661 / 661 | 0.663 / 0.661 | 0.161 / 0.101 | 244 / 252 |

The MPEG-1 stereo frame path used 258 fewer allocation events per stream (8.7%) and its observed peak RSS rose by 32 KiB. It still took 8.4 times the SIMD reference time. The two single-granule controls had unchanged allocation counts. The frame and whole-file outputs remained bit-identical to scalar minimp3. The JSON reports give the separate SIMD error for every case. [48 kHz stereo](48000.json) · [24 kHz mono](24000.json) · [8 kHz mono](8000.json)

The prior and candidate diagnostic executables used the same free-format stereo input as the timing executable. CPU sampling covered 600 rounds and allocation sampling covered 50. Each folded file groups identical stacks by total weight and preserves the runtime sample and drop counts. CPU weights count samples and allocation weights estimate bytes. [Prior CPU](prior-cpu.folded) · [Candidate CPU](new-cpu.folded) · [Prior allocations](prior-alloc.folded) · [Candidate allocations](new-alloc.folded)

The eleven MP3 and shared Layer II suites passed x86-64 Linux sanitizers, WASI execution, and emulated ARM64 Linux with zero live bytes. All eleven built as Android ARM64 libraries. A stereo–mono–stereo regression compares all 960 history entries and all output sample bits with the original granule helper. The self-host interpreter passed the free-format smoke suite.

## Reproduce

Use the compiler and prior stdlib from the same checkout before changing the decoder. Set `prior` to that checkout and `headers` to the directory containing the two pinned minimp3 headers. Run each sample-rate/channel pair from the table. The producer checks both header hashes before building the C references.

```sh
taskset -c 0 scripts/bench-mp3-freeformat --compiler bin/fern-selfhost \
  --baseline-stdlib "$prior/internal/stdlib" --baseline-free-format --reference-dir "$headers" \
  --sample-rate 48000 --channels 2 --rounds 30 --trials 7 --output /tmp/mp3-synthesis
FERN_PROFILE=cpu bin/fern-selfhost -stdlib internal/stdlib -target x86-64-linux \
  -o /tmp/mp3-profile bench/audio/mp3_freeformat.fern
/tmp/mp3-profile /tmp/mp3-synthesis/free.mp3 frames 600 /tmp/mp3-pcm.f32 decode 2>/tmp/cpu.folded
```

Use `FERN_PROFILE=alloc` with 50 rounds for allocation sampling. Run both profile modes against the prior library as well.
