# ULC2 audio codec

This example implements [Aikku93's ULC2 audio codec](https://github.com/Aikku93/ulc-codec) in Fern.
The library and CLI use no C code or external codec process.
The decoder supports the complete documented ULC2 syntax.
The encoder uses a simpler quality model than the reference encoder.

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
examples/ulc/build/ulc decode input.ulc output.wav
examples/ulc/build/ulc decode input.ulc output.wav FLOAT32
examples/ulc/build/ulc info input.ulc
```

The optional encoder arguments are quality and block size.
Quality is an integer from 1 to 100, with a default of 75.
Quality values do not correspond to the reference encoder's quality values.
Block size is a power of two from 256 to 32768, with a default of 2048.
The CLI returns status 1 for invalid arguments, unsupported formats, or malformed input.

Input WAV formats are unsigned PCM8, signed PCM16, signed PCM24, and IEEE FLOAT32.
Input samples must be finite and within `[-1, 1]`.
Output formats are PCM8, PCM16, PCM24, and FLOAT32.
PCM16 is the default output format.
Integer output uses rounding and clipping.

The WAV parser supports unknown chunks and padding after chunks of odd length.
It does not support WAVE_FORMAT_EXTENSIBLE or RF64.

## Implementation

- `codec.fern` implements coefficient commands, quantizers, noise fill, window patterns, overlap scales, and M/S channel pairs.
- `transform.fern` implements DCT-IV through a radix-2 complex FFT and the sine-window transforms.
- `audio.fern` implements WAV and ULC2 containers.
- `ulc.fern` implements command-line file I/O.

The transform cost is `O(N log N)`.
Each decoder owns its xorshift seed and overlap buffers.
Each decoder starts with seed 1234567.
The reference decoder uses a global seed for the process.
Different trigonometric functions and operation order cause small sample differences from the reference decoder.
The parser rejects reserved commands, truncated commands, and runs that exceed their subblock.

The encoder uses long windows, M/S channel pairs, adaptive quantizers, signed-square coefficients, and zero runs.
Higher quality permits smaller quantizers and smaller groups of coefficients.
The encoder does not implement transient window selection, perceptual masking, noise substitution, or CBR/ABR rate targets.
The reference decoder accepts its output.
Its compression and perceptual quality differ from the reference encoder.

ULC2 does not store the original sample count.
File output preserves the initial delay and final padding, as the reference tools do.
The encoder adds two blocks to the input length after rounding to a complete block.
The reconstructed input starts two blocks into the decoded audio.

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

`codec.encoder(block_size, channels, quality)` creates an encoder state.
`codec.encode_block(state, samples)` returns the next state and a byte-aligned block.
An input block contains exactly `block_size * channels` interleaved samples.
The caller manages delay and final flush blocks with this API.
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
Native CLI tests cover all block sizes and audio with one, two, or three channels.

The optional C tests check compatibility in both directions.
They check every window pattern at all eight overlap scales, with noise and silence.
Python 3 is necessary for the CLI tests.
The example CI entry point is `examples/tests/ulc_codec.fern`.

Validation used x86-64 Linux, Fern `e8033eacb25d`, and reference ulc-codec `b47df8d50c2ef2d765e5dbec0bf567b5e558173a`.
The reference checkout used its pinned libfourier submodule.
The maximum FLOAT32 sample difference across all windows was `2.38e-6`.
The comparison tolerance is `1e-5`.
The sanitized unit tests reported zero live allocations at exit.

## License

The implementation uses the [Unlicense](LICENSE).
The bitstream and overlap code derive from Ruben Nunez's ulc-codec and libfourier sources.
