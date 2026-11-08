# WavPack audio configuration

This module reads sample rates, sample formats, and channel metadata.
Integer bit depth excludes padding in the stored sample container.
The speaker mask uses WAVE speaker bits.
Additional channel identities retain their source order.

```fern
<<*>>=
// Sample formats and channel metadata for lossless WavPack streams.
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/wavpack/block";
import "std/audio/wavpack/packet";

pub struct Layout { channels: i32, mask: u32, identities: u8[], declared: boolean }

const RATES: i32[] = [6000, 8000, 9600, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 64000, 88200, 96000, 192000];

function custom_rate(input: [u8], entry: block.Entry): Result[i32, pcm.AudioError] {
  if (entry.size != 3 && entry.size != 4) {
    return Err(pcm.Header);
  }
  let custom = wire.le(input, entry.at, entry.size) & 2147483647u64;
  if (custom == 0u64 || custom > 16777215u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(custom as i32);
}

pub function rate(input: [u8], value: block.Block, previous: i32): Result[i32, pcm.AudioError] {
  let code = (value.flags >> 23u32 & 15u32) as i32;
  let rate = if (code < 15) { RATES[code] } else { previous };
  if let Some(entry) = packet.find(value, 39) {
    let custom = custom_rate(input, entry)?;
    if (code != 15 && custom != rate) {
      return Err(pcm.Header);
    }
    rate = custom;
  }
  if (rate <= 0 || previous > 0 && previous != rate) {
    return Err(pcm.Header);
  }
  return Ok(rate);
}

pub function format(value: block.Block, rate: i32): Result[pcm.Format, pcm.AudioError] {
  if ((value.flags & 2147483656u32) != 0u32) {
    return Err(pcm.Unsupported);
  }
  let bytes = (value.flags & 3u32) as i32 + 1;
  let shift = (value.flags >> 13u32 & 31u32) as i32;
  let bits = bytes * 8 - shift;
  let format = pcm.Format { sample_rate: rate, channels: packet.channels(value), bits_per_sample: bits };
  if (!pcm.valid(format) || (value.flags & 128u32) != 0u32 && (bits != 32 || bytes != 4)) {
    return Err(pcm.Header);
  }
  return Ok(format);
}

function read_layout(input: [u8], entry: block.Entry): Result[(i32, u32), pcm.AudioError] {
  if (entry.size < 1 || entry.size > 7) {
    return Err(pcm.Header);
  }
  let channels = input[entry.at] as i32;
  let mask = 0u32;
  if (entry.size >= 6) {
    channels = (channels | (input[entry.at + 2] as i32 & 15) << 8) + 1;
    let streams = (input[entry.at + 1] as i32 | (input[entry.at + 2] as i32 & 240) << 4) + 1;
    if (streams > channels || channels > streams * 2) {
      return Err(pcm.Header);
    }
    mask = wire.le(input, entry.at + 3, entry.size - 3) as u32;
  } else {
    mask = wire.le(input, entry.at + 1, entry.size - 1) as u32;
  }
  if (channels < 1 || channels > 8) {
    return Err(pcm.Unsupported);
  }
  let rest = mask;
  let count = 0;
  while (rest != 0u32) {
    count += (rest & 1u32) as i32;
    rest >>= 1u32;
  }
  if (count > channels) {
    return Err(pcm.Header);
  }
  return Ok((channels, mask));
}

function identities(input: [u8], entry: block.Entry): Result[u8[], pcm.AudioError] {
  if (entry.size < 1 || entry.size > 8) {
    return Err(pcm.Header);
  }
  let ids: u8[] = [];
  for i in 0..entry.size {
    if (input[entry.at + i] == 0u8) {
      return Err(pcm.Header);
    }
    ids = ids.append(input[entry.at + i]);
  }
  return Ok(ids);
}

pub function layout(input: [u8], value: block.Block, previous: Layout): Result[Layout, pcm.AudioError] {
  let result = previous;
  if let Some(entry) = packet.find(value, 13) {
    let (channels, mask) = read_layout(input, entry)?;
    if (previous.declared && (channels != previous.channels || mask != previous.mask)) {
      return Err(pcm.Unsupported);
    }
    result = Layout { ...result, channels: channels, mask: mask, declared: true };
  }
  if let Some(entry) = packet.find(value, 43) {
    let ids = identities(input, entry)?;
    if (previous.identities.len() > 0 && previous.identities != ids) {
      return Err(pcm.Unsupported);
    }
    result = Layout { ...result, identities: ids };
  }
  return Ok(result);
}

pub function norm(input: [u8], value: block.Block): Result[i32, pcm.AudioError] {
  if ((value.flags & 128u32) == 0u32) {
    return Ok(0);
  }
  let Some(entry) = packet.find(value, 8) else {
    return Err(pcm.Header);
  };
  if (entry.size != 4) {
    return Err(pcm.Header);
  }
  return Ok(input[entry.at + 3] as i32);
}
```
