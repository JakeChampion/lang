# Layer II allocation and quantization

This module reconstructs Layer II subband samples from bounded frame payloads.
It selects MPEG allocation tables, decodes scale factors, and reconstructs grouped and direct quantizers.
The decoder supplies a validated header and one complete frame payload.
The arithmetic follows the pinned scalar minimp3 reference under CC0.

```fern
<<*>>=
// Reconstruct MPEG Layer II subbands with bounded fields and scalar reference arithmetic.
import "std/audio/mp2/header";
import "std/audio/mp2/tables";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/pcm";

pub opaque struct Scale { bands: i32, stereo: i32, allocation: u8[], factors: f32[], protected: i32 }

struct Allocation { bands: i32, stereo: i32, family: i32 }

function allocation(h: header.Header): Allocation {
  let bands = 30;
  let family = 2;
  if (h.version == 1) {
    let kbps = h.bitrate / h.channels;
    family = 1;
    bands = 27;
    if (kbps < 56) {
      family = 0;
      bands = if (h.rate_index == 2) { 12 } else { 8 };
    } else if (kbps >= 96 && h.rate_index != 1) {
      bands = 30;
    }
  }
  let stereo = if (h.mode == 3) { 0 } else if (h.mode == 1) { 4 * (h.mode_ext + 1) } else { 32 };
  return Allocation { bands: bands, stereo: if (stereo < bands) { stereo } else { bands }, family: family };
}

function code_table(family: i32, band: i32): (i32, i32) {
  if (family == 0) {
    return (44, if (band < 2) { 4 } else { 3 });
  }
  if (family == 2) {
    if (band < 4) {
      return (60, 4);
    }
    return (44, if (band < 11) { 3 } else { 2 });
  }
  if (band < 3) {
    return (0, 4);
  }
  if (band < 11) {
    return (16, 4);
  }
  return if (band < 23) { (32, 3) } else { (40, 2) };
}

function bitalloc(input: [u8], b: bits.Bits, shape: Allocation): (bits.Bits, u8[]) {
  let output = __alloc_u8(64);
  for band in 0..shape.bands {
    let (offset, width) = code_table(shape.family, band);
    let (next, value) = bits.read(input, b, width);
    b = next;
    let left = tables.BITALLOC[offset + value];
    let right = left;
    if (band < shape.stereo) {
      let (after, other) = bits.read(input, b, width);
      b = after;
      right = tables.BITALLOC[offset + other];
    }
    output = output.with(band * 2, left).with(band * 2 + 1, if (shape.stereo == 0) { 0u8 } else { right });
  }
  return (b, output);
}

function scale_codes(input: [u8], b: bits.Bits, allocation: [u8], bands: i32): (bits.Bits, u8[]) {
  let codes = __alloc_u8(64);
  for i in 0..bands * 2 {
    if (allocation[i] != 0u8) {
      let (next, code) = bits.read(input, b, 2);
      b = next;
      codes = codes.with(i, code as u8);
    }
  }
  return (b, codes);
}

function factors(input: [u8], b: bits.Bits, allocation: [u8], codes: [u8], bands: i32): (bits.Bits, f32[]) {
  let output = spectral.zeros(192);
  for i in 0..bands * 2 {
    let ba = allocation[i] as i32;
    let mask = if (ba != 0) { 4 + (19 >> codes[i] as i32 & 3) } else { 0 };
    let scale = 0.0f32;
    for part in 0..3 {
      if ((mask & 4 >> part) != 0) {
        let (next, index) = bits.read(input, b, 6);
        b = next;
        scale = tables.DEQUANT[ba * 3 - 6 + index % 3] * ((1 << 21 >> index / 3) as f32);
      }
      output = output.with(i * 3 + part, scale);
    }
  }
  return (b, output);
}

pub function read(input: [u8], h: header.Header): Result[(bits.Bits, Scale), pcm.AudioError] {
  let shape = allocation(h);
  let (b1, values) = bitalloc(input, bits.new(input.len() * 8), shape);
  let (b2, codes) = scale_codes(input, b1, values, shape.bands);
  if (b2.bad) {
    return Err(pcm.Truncated);
  }
  let (b3, scales) = factors(input, b2, values, codes, shape.bands);
  if (b3.bad) {
    return Err(pcm.Truncated);
  }
  for band in shape.stereo..shape.bands {
    values = values.with(band * 2 + 1, 0u8);
  }
  return Ok((b3, Scale { bands: shape.bands, stereo: shape.stereo, allocation: values, factors: scales, protected: b2.pos }));
}

pub function (value: Scale) protected_bits(): i32 {
  return value.protected;
}

function samples(input: [u8], b: bits.Bits, ba: i32, own output: f32[], at: i32): Result[(bits.Bits, f32[]), pcm.AudioError] {
  if (ba < 17) {
    let half = (1 << ba - 1) - 1;
    for i in 0..3 {
      let (next, value) = bits.read(input, b, ba);
      b = next;
      output = output.with(at + i, (value - half) as f32);
    }
  } else {
    let modulus = (2 << ba - 17) + 1;
    let (next, code) = bits.read(input, b, modulus + 2 - (modulus >> 3));
    b = next;
    if (code >= modulus * modulus * modulus) {
      return Err(pcm.Header);
    }
    for i in 0..3 {
      output = output.with(at + i, (code % modulus - modulus / 2) as f32);
      code /= modulus;
    }
  }
  if (b.bad) {
    return Err(pcm.Truncated);
  }
  return Ok((b, output));
}

pub function granule(input: [u8], b: bits.Bits, scale: Scale, part: i32): Result[(bits.Bits, f32[]), pcm.AudioError] {
  return granule_into(input, b, scale, part, spectral.zeros(1152));
}

function clear_inactive(scale: Scale, own output: f32[]): f32[] {
  // Synthesis changes every band. Clear inactive bands before the next reuse.
  for band in 0..32 {
    for channel in 0..2 {
      if (band >= scale.bands || scale.allocation[band * 2 + channel] == 0u8) {
        for i in 0..12 {
          output = output.with(channel * 576 + band * 18 + i, 0.0f32);
        }
      }
    }
  }
  return output;
}

pub function granule_into(input: [u8], b: bits.Bits, scale: Scale, part: i32, own output: f32[]): Result[(bits.Bits, f32[]), pcm.AudioError] {
  output = clear_inactive(scale, output);
  for group in 0..4 {
    for band in 0..scale.bands {
      for channel in 0..2 {
        let ba = scale.allocation[band * 2 + channel] as i32;
        if (ba != 0) {
          let (next, values) = samples(input, b, ba, output, channel * 576 + band * 18 + group * 3)?;
          b = next;
          output = values;
        }
      }
    }
  }
  for band in scale.stereo..scale.bands {
    for i in 0..12 {
      output = output.with(576 + band * 18 + i, output[band * 18 + i]);
    }
  }
  for band in 0..scale.bands {
    for i in 0..12 {
      output = output.with(band * 18 + i, output[band * 18 + i] * scale.factors[band * 6 + part]);
      output = output.with(576 + band * 18 + i, output[576 + band * 18 + i] * scale.factors[band * 6 + 3 + part]);
    }
  }
  return Ok((b, output));
}

function crc_bit(crc: i32, bit: i32): i32 {
  let feedback = (crc >> 15 ^ bit) & 1;
  return (crc << 1 ^ (if (feedback != 0) { 32773 } else { 0 })) & 65535;
}

pub function checksum(header: [u8], input: [u8], count: i32): i32 {
  let crc = 65535;
  for i in 0..16 {
    crc = crc_bit(crc, header[i / 8] as i32 >> 7 - i % 8 & 1);
  }
  for i in 0..count {
    crc = crc_bit(crc, input[i / 8] as i32 >> 7 - i % 8 & 1);
  }
  return crc;
}
```
