# ULC2 audio codec

This example implements [Aikku93's ULC2 audio codec](https://github.com/Aikku93/ulc-codec) in Fern.
The library and CLI use no C code or external codec process.
The decoder supports the complete documented ULC2 syntax.
The encoder includes transient windows, Bark-band masking, noise substitution, and all three rate-control modes.

## Build

Build the compiler from the repository root:

```sh
make build
make -C examples/ulc
```

The example Makefile defaults to x86-64 Linux.
Select another target with `TARGET=arm64-linux` or `TARGET=arm64-darwin`.
Set `FERN` to an absolute compiler path to use another compiler.
Run `make -C examples/ulc clean` before you change the compiler or target.

## Use

Run these commands from the repository root:

```sh
examples/ulc/build/ulc encode input.wav output.ulc
examples/ulc/build/ulc encode input.wav output.ulc 90 512
examples/ulc/build/ulc encode-cbr input.wav output.ulc 96 2048
examples/ulc/build/ulc encode-abr input.wav output.ulc 96 2048
examples/ulc/build/ulc analyze input.wav 2048
examples/ulc/build/ulc decode input.ulc output.wav
examples/ulc/build/ulc decode input.ulc output.wav FLOAT32
examples/ulc/build/ulc info input.ulc
```

The optional encoder arguments are quality and block size.
Quality is a positive number up to 100, with a default of 75.
VBR uses the reference encoder's complexity model.
Block size is a power of two from 256 to 32768, with a default of 2048.
The CLI returns status 1 for invalid arguments, unsupported formats, or malformed input.

Input WAV formats are unsigned PCM8, signed PCM16/PCM24/PCM32, and IEEE FLOAT32.
Input samples must be finite and within `[-1, 1]`.
Output formats are PCM8, PCM16, PCM24, and FLOAT32.
PCM16 is the default output format.
Integer output uses rounding and clipping.

The WAV parser supports unknown chunks and padding after chunks of odd length.
It does not support WAVE_FORMAT_EXTENSIBLE or RF64.

## Implementation

- `codec.fern` implements coefficient commands, quantizers, noise fill, window patterns, overlap scales, and M/S channel pairs.
- `encoder.fern` manages lookahead, transforms, and rate control.
- `windows.fern` detects transients.
- `analysis.fern` calculates masking levels, noise spectra, and coefficient priorities.
- `bitstream.fern` selects quantizer zones and emits coefficient commands.
- `transform.fern` implements DCT-IV through an N/2-point complex FFT and the sine-window transforms.
- `audio.fern` implements WAV and ULC2 containers.
- `packed_transform.fern` implements four parallel FFTs with packed float SIMD.
- `file_decoder.fern` and `file_encoder.fern` manage parallel file operations.
- `ulc.fern` implements command-line file I/O.

The transform cost is `O(N log N)`.
Each decoder owns its xorshift seed and overlap buffers.
Packed float SIMD computes the FFT. Window and overlap operations retain double precision.

Each decoder starts with seed 1234567.
The reference decoder uses a global seed for the process.
Different trigonometric functions and operation order cause small sample differences from the reference decoder.
The parser rejects reserved commands, truncated commands, and runs that exceed their subblock.

The encoder follows the reference algorithm for transient windows, overlap scales, M/S channel pairs, and coefficient priorities.
It includes Bark-band masking, noise runs, and exponential noise tails.
It uses stable radix sorting instead of heapsort.
The transform uses cached tables and SIMD operations.
CBR and ABR measure candidate sizes without allocation of an output bitstream for each search pass.

CBR enforces a byte-aligned budget for each block.
An impossible budget returns an error.
ABR distributes the target bitrate according to block complexity.
The CLI calculates average complexity in a separate pass unless the caller supplies it as the final argument.
Silent ABR blocks still require the minimum header and stop commands.

Floating-point precision and coefficient ties can change the output bytes relative to C.
These differences do not change the format or the supported features.
The tests compare transient decisions, transform coefficients, noise analysis, and complexity with the reference implementation.

ULC2 does not store the original sample count.
File output preserves the initial delay and final padding, as the reference tools do.
The encoder adds two blocks to the input length after rounding to a complete block.
The reconstructed input starts two blocks into the decoded audio.

On Linux, the encoder uses up to four workers for files with at least 32 blocks.
File decode uses workers for at least four blocks and 65,536 samples.
The CPU affinity limits the default worker count.
Set `ULC_THREADS=1`, `2`, or `4` to select it explicitly.
The final decode argument also accepts this count after the output format.
The serial and parallel paths produce identical bytes.

Encoder workers use exact detector checkpoints and two blocks of overlap preroll.
Decoder workers use a syntax index, a noise-seed jump, and one block of overlap preroll.
Linux file decode writes disjoint ranges of one shared mapping.
All workers join before the mapping leaves the address space.
Other targets use the serial CLI by default.

The CLI reads complete files into memory.
ULC2 input and output must be smaller than 1 GiB.
Audio input and decoded output have a limit of 134,217,728 samples.
WAV output has a similar byte limit of 1 GiB.
The block API supports applications that process a stream.

The codec accepts 1 to 255 channels.
It pairs adjacent channels for M/S and leaves an odd final channel alone.

## Library API

Use this import from a program in the repository root:

```fern
import "./examples/ulc/codec";

function example(input: u8[]): Result[f64[], string] {
  let state = codec.decoder(2048, 2)?;
  let block = codec.decode_block(state, input, 0)?;
  return Ok(block.samples);
}
```

Save `block.state` for the next call.
Advance the input offset to `block.next_byte`.
Output samples are interleaved.

`encoder.create(block_size, channels, rate, mode)` creates an encoder state.
Modes are `encoder.Vbr(quality)`, `encoder.Cbr(kbps)`, and `encoder.Abr(kbps, average_complexity)`.
`encoder.encode_block(state, samples)` returns the next state and a byte-aligned block.
An input block contains exactly `block_size * channels` interleaved samples.
The encoder owns the initial lookahead buffer.
The caller supplies two final zero blocks with this API.

`audio.encode` and `audio.decode` manage the file container, delay, and padding.

## Tests

Run the local tests on a host that matches `TARGET`:

```sh
make -C examples/ulc check
make -C examples/ulc test
```

Enable comparisons with the reference C implementation:

```sh
git clone --recurse-submodules https://github.com/Aikku93/ulc-codec /tmp/ulc-reference
make -C /tmp/ulc-reference -j2
ULC_REFERENCE_DIR=/tmp/ulc-reference/release make -C examples/ulc test
```

Run the Fern memory sanitizer:

```sh
bin/fern -target x86-64-linux -sanitize -o examples/ulc/build/unit-sanitize examples/ulc/tests/unit.fern
examples/ulc/build/unit-sanitize
```

The tests compare DCT-IV with its direct definition and check exact reconstruction through the transform pair.
They check coefficient commands, malformed input, WAV formats, and codec reconstruction with delay.
Native CLI tests cover all block sizes, three rate-control modes, and audio with one, two, or three channels.

The optional C tests require a C compiler and a complete reference checkout.
They check compatibility in both directions.
They check every window pattern at all eight overlap scales, with noise and silence.
Python 3 is necessary for the CLI tests.
The example CI entry point is `examples/tests/ulc_codec.fern`.

Validation used x86-64 Linux and the compiler from this checkout.
It also used Fern nightly `e8033eacb25d`.

The reference version is ulc-codec `b47df8d50c2ef2d765e5dbec0bf567b5e558173a`.
The reference checkout used its pinned libfourier submodule.
The maximum FLOAT32 sample difference across all windows was `2.38e-6`.
The comparison tolerance is `1e-5`.
The sanitized unit tests reported zero live allocations at exit.

## Performance

[Benchmark instructions and results](bench/README.md) describe the workloads and comparison limits.
The benchmark records input hashes, executable hashes, machine details, and individual timings.
A larger speed ratio means that Fern is faster.
These results do not establish a worldwide speed or perceptual-quality ranking.

## License

The implementation uses the [Unlicense](LICENSE).
The bitstream and overlap code derive from Ruben Nunez's ulc-codec and libfourier sources.
