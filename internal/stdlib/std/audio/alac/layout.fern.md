# ALAC container channel layouts

This module reads bounded Core Audio channel layouts for ALAC containers.
It identifies supported speaker masks and puts decoded PCM in WAVE speaker order.
The source PCM remains interleaved and retains its exact integer depth.

```fern
<<*>>=
// Read Core Audio channel layouts and retain exact ALAC sample values.
import "std/audio/alac/config";
import "std/audio/pcm";
import "std/audio/wire";

function back_mask(channels: i32): u32 {
  return match (channels) { 5 => 55u32, 6 => 63u32, 7 => 319u32, 8 => 255u32, _ => config.mask(channels) };
}

// The bounded Core Audio layout table identifies the supported speaker sets.
// fern-lint: allow cyclomatic-complexity
function layout_tag(tag: u32, channels: i32): Result[u32, pcm.AudioError] {
  if ((tag & 65535u32) != channels as u32) {
    return Err(pcm.Header);
  }
  let kind = tag >> 16u32;
  let supported = match (channels) {
    1 => kind == 100u32, 2 => kind == 101u32, 3 => kind == 112u32 || kind == 113u32,
    4 => kind == 115u32 || kind == 116u32, 5 => kind >= 117u32 && kind <= 120u32,
    6 => kind >= 121u32 && kind <= 124u32, 7 => kind == 125u32 || kind == 142u32,
    _ => kind == 127u32,
  };
  if (!supported) {
    return Err(pcm.Unsupported);
  }
  return Ok(back_mask(channels));
}

pub function read(input: [u8], info: config.Info): Result[u32, pcm.AudioError] {
  if (input.len() != 12) {
    return Err(pcm.Unsupported);
  }
  let tag = wire.be(input, 0, 4) as u32;
  let mask = wire.be(input, 4, 4) as u32;
  if (wire.be(input, 8, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  if (tag != 65536u32) {
    if (mask != 0u32) {
      return Err(pcm.Header);
    }
    return layout_tag(tag, info.format.channels);
  }
  if (mask != info.channel_mask && mask != back_mask(info.format.channels)) {
    return Err(pcm.Unsupported);
  }
  return Ok(mask);
}

function check_storage(samples: [i32], format: pcm.Format, mask: u32, frames: i32): Result[void, pcm.AudioError] {
  if (!pcm.valid(format) || frames < 0 || frames > samples.len() / format.channels) {
    return Err(pcm.Header);
  }
  if (mask != config.mask(format.channels) && mask != back_mask(format.channels)) {
    return Err(pcm.Unsupported);
  }
  return Ok(());
}

pub function reorder(own samples: i32[], format: pcm.Format, mask: u32, frames: i32): Result[i32[], pcm.AudioError] {
  check_storage(samples, format, mask, frames)?;
  if (format.channels == 7 && mask == 319u32) {
    for i in 0..frames {
      let at = i * 7;
      let center = samples[at + 4];
      let left = samples[at + 5];
      let right = samples[at + 6];
      samples = samples.with(at + 4, left).with(at + 5, right).with(at + 6, center);
    }
  } else if (format.channels == 8 && mask == 255u32) {
    for i in 0..frames {
      let at = i * 8;
      let near_left = samples[at + 4];
      let near_right = samples[at + 5];
      let surround_left = samples[at + 6];
      let surround_right = samples[at + 7];
      samples = samples.with(at + 4, surround_left).with(at + 5, surround_right).with(at + 6, near_left).with(at + 7, near_right);
    }
  }
  return Ok(samples);
}
```
