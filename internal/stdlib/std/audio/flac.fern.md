# Native FLAC decoder

The decoder reads native FLAC streams with integer PCM at 4 through 32 bits and up to eight channels.
It supports constant, verbatim, fixed predictor, and LPC subframes, Rice and Rice2 residuals, wasted bits, and all stereo assignments.

It checks the header CRC-8, frame CRC-16, zero alignment bits, sample ranges, and frame sequence.
`decode` also checks the STREAMINFO MD5 when the stream supplies one.

Ogg FLAC and FLAC encoding are outside this module.

`inspect` reads STREAMINFO and skips bounded metadata without sample allocation.
`decode_frame` reads one frame at a byte offset. The result supplies the next offset and frame or sample number.

`decode_frame_into` consumes a reusable output buffer. Only the first `frames * format.channels` entries contain output.
Frame decode checks frame checksums, but cannot check the MD5 of the complete stream.

`decode` checks the total sample count when STREAMINFO declares one.
Limits apply before sample allocation. They also bound streams with an unknown sample count.

```fern
<<*>>=
import "std/audio/pcm";
import "std/audio/wire";
import "std/crypto";
import "std/u32";

pub struct Header { format: pcm.Format, frames: u64, min_block: i32, max_block: i32, data_offset: i32, md5: u8[] }

pub struct Frame { format: pcm.Format, frames: i32, bytes_read: i32, number: u64, variable: boolean, samples: i32[] }

struct Bits { at: i32, bit: i32, bad: boolean }

struct FrameHeader { bits: Bits, frames: i32, assignment: i32, number: u64, variable: boolean }

const CRC8: i32[] = [0, 7, 14, 9, 28, 27, 18, 21, 56, 63, 54, 49, 36, 35, 42, 45, 112, 119, 126, 121, 108, 107, 98, 101, 72, 79, 70, 65, 84, 83, 90, 93, 224, 231, 238, 233, 252, 251, 242, 245, 216, 223, 214, 209, 196, 195, 202, 205, 144, 151, 158, 153, 140, 139, 130, 133, 168, 175, 166, 161, 180, 179, 186, 189, 199, 192, 201, 206, 219, 220, 213, 210, 255, 248, 241, 246, 227, 228, 237, 234, 183, 176, 185, 190, 171, 172, 165, 162, 143, 136, 129, 134, 147, 148, 157, 154, 39, 32, 41, 46, 59, 60, 53, 50, 31, 24, 17, 22, 3, 4, 13, 10, 87, 80, 89, 94, 75, 76, 69, 66, 111, 104, 97, 102, 115, 116, 125, 122, 137, 142, 135, 128, 149, 146, 155, 156, 177, 182, 191, 184, 173, 170, 163, 164, 249, 254, 247, 240, 229, 226, 235, 236, 193, 198, 207, 200, 221, 218, 211, 212, 105, 110, 103, 96, 117, 114, 123, 124, 81, 86, 95, 88, 77, 74, 67, 68, 25, 30, 23, 16, 5, 2, 11, 12, 33, 38, 47, 40, 61, 58, 51, 52, 78, 73, 64, 71, 82, 85, 92, 91, 118, 113, 120, 127, 106, 109, 100, 99, 62, 57, 48, 55, 34, 37, 44, 43, 6, 1, 8, 15, 26, 29, 20, 19, 174, 169, 160, 167, 178, 181, 188, 187, 150, 145, 152, 159, 138, 141, 132, 131, 222, 217, 208, 215, 194, 197, 204, 203, 230, 225, 232, 239, 250, 253, 244, 243];

const CRC16: i32[] = [0, 32773, 32783, 10, 32795, 30, 20, 32785, 32819, 54, 60, 32825, 40, 32813, 32807, 34, 32867, 102, 108, 32873, 120, 32893, 32887, 114, 80, 32853, 32863, 90, 32843, 78, 68, 32833, 32963, 198, 204, 32969, 216, 32989, 32983, 210, 240, 33013, 33023, 250, 33003, 238, 228, 32993, 160, 32933, 32943, 170, 32955, 190, 180, 32945, 32915, 150, 156, 32921, 136, 32909, 32903, 130, 33155, 390, 396, 33161, 408, 33181, 33175, 402, 432, 33205, 33215, 442, 33195, 430, 420, 33185, 480, 33253, 33263, 490, 33275, 510, 500, 33265, 33235, 470, 476, 33241, 456, 33229, 33223, 450, 320, 33093, 33103, 330, 33115, 350, 340, 33105, 33139, 374, 380, 33145, 360, 33133, 33127, 354, 33059, 294, 300, 33065, 312, 33085, 33079, 306, 272, 33045, 33055, 282, 33035, 270, 260, 33025, 33539, 774, 780, 33545, 792, 33565, 33559, 786, 816, 33589, 33599, 826, 33579, 814, 804, 33569, 864, 33637, 33647, 874, 33659, 894, 884, 33649, 33619, 854, 860, 33625, 840, 33613, 33607, 834, 960, 33733, 33743, 970, 33755, 990, 980, 33745, 33779, 1014, 1020, 33785, 1000, 33773, 33767, 994, 33699, 934, 940, 33705, 952, 33725, 33719, 946, 912, 33685, 33695, 922, 33675, 910, 900, 33665, 640, 33413, 33423, 650, 33435, 670, 660, 33425, 33459, 694, 700, 33465, 680, 33453, 33447, 674, 33507, 742, 748, 33513, 760, 33533, 33527, 754, 720, 33493, 33503, 730, 33483, 718, 708, 33473, 33347, 582, 588, 33353, 600, 33373, 33367, 594, 624, 33397, 33407, 634, 33387, 622, 612, 33377, 544, 33317, 33327, 554, 33339, 574, 564, 33329, 33299, 534, 540, 33305, 520, 33293, 33287, 514];

function crc(input: [u8], wide: boolean): i32 {
  let c = 0;
  for b in input {
    if (wide) {
      c = c << 8 & 65535 ^ CRC16[c >> 8 ^ b as i32];
    } else {
      c = CRC8[c ^ b as i32];
    }
  }
  return c;
}

// STREAMINFO validation and metadata traversal enforce the native FLAC grammar.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: pcm.Limits): Result[Header, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() < 8) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "fLaC")) {
    return Err(pcm.Header);
  }
  if ((input[4] as i32 & 127) != 0 || wire.be(input, 5, 3) != 34u64) {
    return Err(pcm.Header);
  }
  if (input.len() < 42) {
    return Err(pcm.Truncated);
  }
  let min_block = wire.be(input, 8, 2) as i32;
  let max_block = wire.be(input, 10, 2) as i32;
  let min_frame = wire.be(input, 12, 3);
  let max_frame = wire.be(input, 15, 3);
  let packed = wire.be(input, 18, 8);
  let format = pcm.Format { sample_rate: (packed >> 44) as i32, channels: ((packed >> 41 & 7u64) + 1u64) as i32, bits_per_sample: ((packed >> 36 & 31u64) + 1u64) as i32 };
  if (!pcm.valid(format) || format.sample_rate > 655350 || format.bits_per_sample < 4
    || min_block < 16 || max_block < min_block || min_frame > max_frame && max_frame != 0u64) {
    return Err(pcm.Header);
  }
  let digest: u8[] = [];
  for i in 26..42 {
    digest = digest.append(input[i]);
  }
  let at = 4;
  let first = true;
  while (true) {
    if (input.len() - at < 4) {
      return Err(pcm.Truncated);
    }
    let kind = input[at] as i32 & 127;
    let last = (input[at] as i32 & 128) != 0;
    let size = wire.be(input, at + 1, 3) as i32;
    if (kind == 127 || !first && kind == 0) {
      return Err(pcm.Header);
    }
    if (size > input.len() - at - 4) {
      return Err(pcm.Truncated);
    }
    at += 4 + size;
    first = false;
    if (last) {
      break;
    }
  }
  return Ok(Header { format: format, frames: packed & 68719476735u64, min_block: min_block, max_block: max_block, data_offset: at, md5: digest });
}

function read(input: [u8], own b: Bits, n: i32): (Bits, u64) {
  let value = 0u64;
  let left = n;
  while (left > 0 && !b.bad) {
    if (b.at >= input.len()) {
      b = Bits { ...b, bad: true };
      break;
    }
    let take = 8 - b.bit;
    if (take > left) {
      take = left;
    }
    let v = input[b.at] as i32 >> 8 - b.bit - take & (1 << take) - 1;
    value = value << take as u64 | v as u64;
    left -= take;
    let bit = b.bit + take;
    b = Bits { at: b.at + bit / 8, bit: bit % 8, bad: false };
  }
  return (b, value);
}

function signed(input: [u8], own b: Bits, n: i32): (Bits, i64) {
  if (n == 0) {
    return (b, 0i64);
  }
  let (next, v) = read(input, b, n);
  let shift = (64 - n) as i64;
  return (next, v as i64 << shift >> shift);
}

function unary(input: [u8], own b: Bits, max: u64): (Bits, u64) {
  let count = 0u64;
  while (!b.bad) {
    if (b.at >= input.len()) {
      return (Bits { ...b, bad: true }, 0u64);
    }
    let bits = 8 - b.bit;
    let value = input[b.at] as i32 & (1 << bits) - 1;
    if (value == 0) {
      count += bits as u64;
      if (count > max) {
        return (Bits { ...b, bad: true }, 0u64);
      }
      b = Bits { at: b.at + 1, bit: 0, bad: false };
    } else {
      let zeros = (value as u32).leading_zeros() - (32 - bits);
      count += zeros as u64;
      if (count > max) {
        return (Bits { ...b, bad: true }, 0u64);
      }
      let (next, _) = read(input, b, zeros + 1);
      return (next, count);
    }
  }
  return (b, 0u64);
}

// The variable-length frame number grammar checks continuation and overlong encodings.
// fern-lint: allow cyclomatic-complexity
function number(input: [u8], own b: Bits, variable: boolean): Result[(Bits, u64), pcm.AudioError] {
  let (next, first) = read(input, b, 8);
  b = next;
  if (first < 128u64) {
    if (b.bad) {
      return Err(pcm.Truncated);
    }
    return Ok((b, first));
  }
  let bytes = 0;
  let mask = 128u64;
  while ((first & mask) != 0u64 && bytes < 8) {
    bytes += 1;
    mask = mask >> 1;
  }
  if (bytes < 2 || bytes > 7 || !variable && bytes > 6) {
    return Err(pcm.Header);
  }
  let value = first & mask - 1u64;
  for _ in 1..bytes {
    let (n, v) = read(input, b, 8);
    b = n;
    if ((v & 192u64) != 128u64) {
      return Err(pcm.Header);
    }
    value = value << 6 | v & 63u64;
  }
  let minimum = if (bytes == 2) { 128u64 } else { 1u64 << (5 * bytes - 4) as u64 };
  if (value < minimum || value > 68719476735u64 || !variable && value > 2147483647u64) {
    return Err(pcm.Header);
  }
  if (b.bad) {
    return Err(pcm.Truncated);
  }
  return Ok((b, value));
}

// Frame headers contain conditional block-size and sample-rate fields before the CRC.
// fern-lint: allow cyclomatic-complexity
function header(input: [u8], at: i32, stream: Header, limits: pcm.Limits): Result[FrameHeader, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (!pcm.valid(stream.format) || stream.format.bits_per_sample < 4 || stream.max_block < 1 || stream.max_block > 65535 || at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 6) {
    return Err(pcm.Truncated);
  }
  if (input[at] != 255 || input[at + 1] as i32 >> 1 != 124) {
    return Err(pcm.Header);
  }
  let variable = (input[at + 1] as i32 & 1) != 0;
  let block_code = input[at + 2] as i32 >> 4;
  let rate_code = input[at + 2] as i32 & 15;
  let assignment = input[at + 3] as i32 >> 4;
  let depth_code = input[at + 3] as i32 >> 1 & 7;
  if (block_code == 0 || rate_code == 15 || assignment > 10 || depth_code == 3 || (input[at + 3] as i32 & 1) != 0) {
    return Err(pcm.Header);
  }
  let depths: i32[] = [0, 8, 12, 0, 16, 20, 24, 32];
  let cc = if (assignment < 8) { assignment + 1 } else { 2 };
  if (cc != stream.format.channels || depth_code != 0 && depths[depth_code] != stream.format.bits_per_sample) {
    return Err(pcm.Header);
  }
  let (b, num) = number(input, Bits { at: at + 4, bit: 0, bad: false }, variable)?;
  let frames = 0;
  if (block_code == 1) {
    frames = 192;
  } else if (block_code < 6) {
    frames = 576 << block_code - 2;
  } else if (block_code == 6 || block_code == 7) {
    let (next, v) = read(input, b, if (block_code == 6) { 8 } else { 16 });
    b = next;
    frames = v as i32 + 1;
  } else {
    frames = 256 << block_code - 8;
  }
  let rates: i32[] = [0, 88200, 176400, 192000, 8000, 16000, 22050, 24000, 32000, 44100, 48000, 96000];
  let rate = stream.format.sample_rate;
  if (rate_code > 0 && rate_code < 12) {
    rate = rates[rate_code];
  }
  if (rate_code >= 12) {
    let (next, v) = read(input, b, if (rate_code == 12) { 8 } else { 16 });
    b = next;
    rate = v as i32 * (if (rate_code == 12) { 1000 } else if (rate_code == 14) { 10 } else { 1 });
  }
  if (b.bad || b.at >= input.len()) {
    return Err(pcm.Truncated);
  }
  if (rate != stream.format.sample_rate || frames > stream.max_block) {
    return Err(pcm.Header);
  }
  let _ = pcm.sample_count(frames, cc, limits)?;
  if (crc(input[at:b.at], false) != input[b.at] as i32) {
    return Err(pcm.Checksum);
  }
  return Ok(FrameHeader { bits: Bits { at: b.at + 1, bit: 0, bad: false }, frames: frames, assignment: assignment, number: num, variable: variable });
}

function fits(value: i64, bits: i32): boolean {
  let bound = 1i64 << (bits - 1) as i64;
  return value >= -bound && value < bound;
}

// Rice partitions share the bit cursor and predictor-dependent output range.
// fern-lint: allow cyclomatic-complexity
function residuals(input: [u8], own b: Bits, own samples: i64[], order: i32): Result[(Bits, i64[]), pcm.AudioError] {
  let (b1, method) = read(input, b, 2);
  let (b2, partition) = read(input, b1, 4);
  b = b2;
  if (method > 1u64) {
    return Err(pcm.Header);
  }
  let partitions = 1 << partition as i32;
  let width = if (method == 0u64) { 4 } else { 5 };
  if (samples.len() % partitions != 0 || samples.len() / partitions < order) {
    return Err(pcm.Header);
  }
  let per = samples.len() / partitions;
  let at = order;
  for p in 0..partitions {
    let (next, param) = read(input, b, width);
    b = next;
    let count = per - (if (p == 0) { order } else { 0 });
    if (param == ((1 << width) - 1) as u64) {
      let (n, raw) = read(input, b, 5);
      b = n;
      for _ in 0..count {
        let (n1, value) = signed(input, b, raw as i32);
        b = n1;
        samples = samples.with(at, value);
        at += 1;
      }
    } else {
      for _ in 0..count {
        let (n, high) = unary(input, b, 4294967294u64 >> param);
        let (n1, low) = read(input, n, param as i32);
        b = n1;
        let unsigned = high << param | low;
        if (unsigned > 4294967294u64) {
          return Err(pcm.Header);
        }
        let value = (unsigned >> 1) as i64 ^ -((unsigned & 1u64) as i64);
        samples = samples.with(at, value);
        at += 1;
      }
    }
    if (b.bad) {
      return Err(pcm.Truncated);
    }
  }
  return Ok((b, samples));
}

// Subframe reconstruction keeps wasted bits, predictor history, and residuals together.
// fern-lint: allow cyclomatic-complexity
function subframe(input: [u8], own b: Bits, count: i32, bits: i32): Result[(Bits, i64[]), pcm.AudioError] {
  let (b1, tag) = read(input, b, 8);
  b = b1;
  if ((tag & 128u64) != 0u64) {
    return Err(pcm.Header);
  }
  let kind = (tag >> 1) as i32;
  let wasted = 0;
  if ((tag & 1u64) != 0u64) {
    let (next, n) = unary(input, b, (bits - 2) as u64);
    b = next;
    wasted = n as i32 + 1;
  }
  let depth = bits - wasted;
  if (depth < 1 || depth > 33 || b.bad) {
    return Err(pcm.Header);
  }
  let samples: i64[] = [];
  for _ in 0..count {
    samples = samples.append(0i64);
  }
  if (kind == 0 || kind == 1) {
    let value = 0i64;
    for i in 0..count {
      if (i == 0 || kind == 1) {
        let (next, v) = signed(input, b, depth);
        b = next;
        value = v;
      }
      samples = samples.with(i, value);
    }
  } else {
    let lpc = kind >= 32;
    if (!lpc && (kind < 8 || kind > 12)) {
      return Err(pcm.Header);
    }
    let order = if (lpc) { kind - 31 } else { kind - 8 };
    if (order > count) {
      return Err(pcm.Header);
    }
    for i in 0..order {
      let (next, v) = signed(input, b, depth);
      b = next;
      samples = samples.with(i, v);
    }
    let coeff: i64[] = [];
    let shift = 0;
    if (lpc) {
      let (next, precision) = read(input, b, 4);
      let (next1, sh) = signed(input, next, 5);
      b = next1;
      if (precision == 15u64 || sh < 0i64) {
        return Err(pcm.Header);
      }
      shift = sh as i32;
      for _ in 0..order {
        let (n, c) = signed(input, b, precision as i32 + 1);
        b = n;
        coeff = coeff.append(c);
      }
    } else {
      if (order == 1) {
        coeff = [1i64];
      }
      if (order == 2) {
        coeff = [2i64, -1i64];
      }
      if (order == 3) {
        coeff = [3i64, -3i64, 1i64];
      }
      if (order == 4) {
        coeff = [4i64, -6i64, 4i64, -1i64];
      }
    }
    let (next, values) = residuals(input, b, samples, order)?;
    b = next;
    samples = values;
    for i in order..count {
      let prediction = 0i64;
      for j in 0..order {
        prediction += coeff[j] * samples[i - j - 1];
      }
      prediction = prediction >> shift as i64;
      let Some(value) = prediction +? samples[i] else {
        return Err(pcm.Header);
      };
      if (!fits(value, depth)) {
        return Err(pcm.Sample);
      }
      samples = samples.with(i, value);
    }
  }
  if (b.bad) {
    return Err(pcm.Truncated);
  }
  if (wasted > 0) {
    for i in 0..count {
      samples = samples.with(i, samples[i] << wasted as i64);
    }
  }
  return Ok((b, samples));
}

// Frame traversal couples channel reconstruction, alignment, and the frame CRC.
// fern-lint: allow cyclomatic-complexity
pub function decode_frame_into(input: [u8], at: i32, stream: Header, own output: i32[], limits: pcm.Limits): Result[Frame, pcm.AudioError] {
  let h = header(input, at, stream, limits)?;
  let cc = stream.format.channels;
  let depth = stream.format.bits_per_sample;
  if (output.len() < h.frames * cc) {
    return Err(pcm.Buffer);
  }
  let b = h.bits;
  let channels: i64[][] = [];
  for c in 0..cc {
    let extra = if (h.assignment == 8 && c == 1 || h.assignment == 9 && c == 0 || h.assignment == 10 && c == 1) { 1 } else { 0 };
    let (next, values) = subframe(input, b, h.frames, depth + extra)?;
    b = next;
    channels = channels.append(values);
  }
  if (b.bit != 0) {
    let padding_bits = 8 - b.bit;
    let (next, padding) = read(input, b, padding_bits);
    b = next;
    if (padding != 0u64) {
      return Err(pcm.Header);
    }
  }
  if (b.bad || input.len() - b.at < 2) {
    return Err(pcm.Truncated);
  }
  if (crc(input[at:b.at], true) != wire.be(input, b.at, 2) as i32) {
    return Err(pcm.Checksum);
  }
  for i in 0..h.frames {
    for c in 0..cc {
      let sample = channels[c][i];
      if (h.assignment == 8 && c == 1) {
        sample = channels[0][i] - sample;
      }
      if (h.assignment == 9 && c == 0) {
        sample += channels[1][i];
      }
      if (h.assignment == 10) {
        let mid = channels[0][i] << 1 | channels[1][i] & 1i64;
        sample = if (c == 0) { mid + channels[1][i] >> 1 } else { mid - channels[1][i] >> 1 };
      }
      if (!fits(sample, depth)) {
        return Err(pcm.Sample);
      }
      output = output.with(i * cc + c, sample as i32);
    }
  }
  return Ok(Frame { format: stream.format, frames: h.frames, bytes_read: b.at + 2 - at, number: h.number, variable: h.variable, samples: output });
}

pub function decode_frame(input: [u8], at: i32, stream: Header, limits: pcm.Limits): Result[Frame, pcm.AudioError] {
  let h = header(input, at, stream, limits)?;
  return decode_frame_into(input, at, stream, pcm.zeros(h.frames * stream.format.channels), limits);
}

// Stream traversal checks sequence, cumulative limits, total samples, and the digest.
// fern-lint: allow cyclomatic-complexity
pub function decode(input: [u8], limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let stream = inspect(input, limits)?;
  let cc = stream.format.channels;
  if (stream.frames > limits.max_samples as u64 / (cc as u64)) {
    return Err(pcm.Limit);
  }
  let output: i32[] = [];
  let at = stream.data_offset;
  let frames = 0u64;
  let index = 0u64;
  let variable = false;
  let block_size = 0;
  let previous = 0;
  let hash = crypto.md5_new();
  let check_md5 = false;
  for b in stream.md5 {
    if (b != 0) {
      check_md5 = true;
    }
  }
  while (at < input.len()) {
    let frame = decode_frame(input, at, stream, limits)?;
    if (index == 0u64) {
      variable = frame.variable;
      block_size = frame.frames;
    } else if (frame.variable != variable || !variable && previous != block_size) {
      return Err(pcm.Header);
    }
    if (frame.number != (if (variable) { frames } else { index })) {
      return Err(pcm.Header);
    }
    if (!variable && frame.frames > block_size) {
      return Err(pcm.Header);
    }
    frames += frame.frames as u64;
    if (frames > limits.max_samples as u64 / (cc as u64)) {
      return Err(pcm.Limit);
    }
    if (stream.frames != 0u64 && frames > stream.frames) {
      return Err(pcm.Header);
    }
    let bytes: u8[] = [];
    for sample in frame.samples {
      output = output.append(sample);
      if (check_md5) {
        bytes = wire.put_le(bytes, sample as u64, (stream.format.bits_per_sample + 7) / 8);
      }
    }
    if (check_md5) {
      hash = hash.update(bytes);
    }
    at += frame.bytes_read;
    previous = frame.frames;
    index += 1u64;
  }
  if (index == 0u64 || stream.frames != 0u64 && frames != stream.frames) {
    return Err(pcm.Truncated);
  }
  if (check_md5) {
    let digest = hash.finish();
    for i in 0..16 {
      if (digest[i] != stream.md5[i]) {
        return Err(pcm.Checksum);
      }
    }
  }
  return Ok(pcm.Block { format: stream.format, samples: output });
}
```
