# Sun and NeXT AU audio

This module indexes complete AU byte views with integer PCM, IEEE float, or G.711 samples.
It accepts big-endian signed PCM at 8, 16, 24, and 32 bits.
Float samples use 32 or 64 bits. G.711 uses A-law or mu-law.
An unknown data size extends to the end of the input.
Annotations remain in the borrowed input and require no copy.

The returned linear index supports exact frame ranges and reusable integer or float output.
Integer reads reject float input. Float reads retain finite values without clipping.
Multichannel samples retain their stored order.

```fern
<<*>>=
// Validate an AU header and retain only its linear sample metadata.

import "std/audio/linear";
import "std/audio/pcm";
import "std/audio/wire";

function encoding(code: u64): Result[(linear.Kind, i32, i32), pcm.AudioError] {
  return match (code) {
    1u64 => Ok((linear.MuLaw, 16, 8)),
    2u64 => Ok((linear.Signed, 8, 8)),
    3u64 => Ok((linear.Signed, 16, 16)),
    4u64 => Ok((linear.Signed, 24, 24)),
    5u64 => Ok((linear.Signed, 32, 32)),
    6u64 => Ok((linear.Float, 32, 32)),
    7u64 => Ok((linear.Float, 64, 64)),
    27u64 => Ok((linear.ALaw, 16, 8)),
    _ => Err(pcm.Unsupported),
  };
}

function extent(input: [u8]): Result[(i32, i32), pcm.AudioError] {
  let offset = wire.be(input, 4, 4);
  let declared = wire.be(input, 8, 4);
  if (offset < 24u64) {
    return Err(pcm.Header);
  }
  if (offset > input.len() as u64) {
    return Err(pcm.Truncated);
  }
  let bytes = input.len() - offset as i32;
  if (declared != 4294967295u64 && declared != bytes as u64) {
    return Err(if (declared > bytes as u64) { pcm.Truncated } else { pcm.Header });
  }
  return Ok((offset as i32, bytes));
}

pub function inspect(input: [u8], limits: pcm.Limits): Result[linear.Index, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() < 24) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, ".snd")) {
    return Err(pcm.Header);
  }
  let (offset, bytes) = extent(input)?;
  let (kind, bits, storage) = encoding(wire.be(input, 12, 4))?;
  let rate = wire.be(input, 16, 4);
  let channels = wire.be(input, 20, 4);
  if (rate > 16777215u64 || channels > 8u64) {
    return Err(pcm.Header);
  }
  let format = linear.Format { sample_rate: rate as i32, channels: channels as i32, bits: bits, storage_bits: storage, little_endian: false, kind: kind };
  return linear.create(input, offset, bytes, format, limits);
}

pub function decode(input: [u8], limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_integer(input, 0, index.information().frames, limits);
}

pub function decode_float(input: [u8], limits: pcm.Limits): Result[linear.Floats, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_float(input, 0, index.information().frames, limits);
}
```
