# Native MP3 encoding

This module encodes normalized `f32` PCM as MPEG-1 Layer III frames at a constant bitrate.
It accepts 32000, 44100 and 48000 Hz, one or two channels, and the 14 MPEG-1 bitrates from 32 to 320 kbit/s.
Two channels are coded as independent left and right channels. The encoder does not use joint stereo.

The analysis filterbank uses the ISO 11172-3 window, and each granule uses the long-block MDCT.
The encoder does not use short blocks, scalefactors, or a psychoacoustic model.
Thus the quantization noise has the same shape as the spectrum, and a transient can spread before its attack.
The encoder does not use the bit reservoir. Each frame holds its own main data.
The granules of one frame share the main data bytes of that frame.

Quantization selects the smallest global gain whose Huffman code fits the bits of its granule.
`std/audio/mp3/codes` selects the Huffman tables and the region split for each granule.

The encoder does not write a Xing, Info, or ID3 tag.
A decoded stream starts with `DELAY` samples of delay in each channel, and ends with the padding of the final frame.
`std/audio/mp3.decode_raw` returns those samples. A container must account for the delay and the padding.

`encoder` creates bounded state. `push` appends each complete frame to the caller's output buffer.
It keeps less than one frame of PCM. `finish` codes the remaining PCM and enough silence to flush the delay.
`encode` codes a complete input through the same state.

Each sample must be finite and between -1 and 1. Interleaved input must hold whole frames.
The total encoded size uses `pcm.Limits.max_input_bytes`.

The encoder does not claim competitive quality or speed.

```fern
<<*>>=
// Encode MPEG-1 Layer III frames from normalized PCM.

import "std/audio/pcm";
import "std/audio/mp3/codes";
import "std/audio/mp3/tables";
import "std/audio/mp3/window";
import "std/f32";
import "std/f64";

// The first input sample is sample DELAY of the raw decoded output.
pub const DELAY: i32 = 1057;

const BITRATES: i32[] = [0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320];

const ALIAS: f64[] = [-0.6, -0.535, -0.33, -0.185, -0.095, -0.041, -0.0142, -0.0037];

pub struct Options { bitrate: i32 }

struct Transform { analysis: f32[], matrix: f32[], mdct: f32[], cs: f32[], ca: f32[], codes: codes.Codes, bounds: i32[] }

pub opaque struct Encoder { sample_rate: i32, channels: i32, bitrate_index: i32, rate_index: i32, transform: Transform, history: f32[], offset: i32, previous: f32[], pending: f32[], remainder: i32, consumed: i64, coded: i64 }

pub function default_options(): Options {
  return Options { bitrate: 128 };
}

function bitrate_index(bitrate: i32): i32 {
  for i in 1..15 {
    if (BITRATES[i] == bitrate) {
      return i;
    }
  }
  return 0;
}

function f32_zeros(n: i32): f32[] {
  let output: f32[] = [];
  for _ in 0..n {
    output = output.append(0.0f32);
  }
  return output;
}

function transform(sample_rate: i32): Transform {
  let pi = 3.141592653589793;
  let analysis: f32[] = [];
  for d in window.SYNTHESIS_WINDOW {
    analysis = analysis.append((d as f64 / 2097152.0) as f32);
  }
  let matrix: f32[] = [];
  for i in 0..32 {
    for m in 0..32 {
      matrix = matrix.append(((2 * i + 1) as f64 * (m as f64) * pi / 64.0).cos() as f32);
    }
  }
  let mdct: f32[] = [];
  for k in 0..18 {
    for n in 0..36 {
      let w = (pi / 36.0 * (n as f64 + 0.5)).sin();
      mdct = mdct.append((w * (pi / 72.0 * ((2 * n + 19) as f64) * ((2 * k + 1) as f64)).cos() / 9.0) as f32);
    }
  }
  let cs: f32[] = [];
  let ca: f32[] = [];
  for c in ALIAS {
    let r = (1.0 + c * c).sqrt();
    cs = cs.append((1.0 / r) as f32);
    ca = ca.append((c / r) as f32);
  }
  let row = if (sample_rate == 44100) { 5 } else if (sample_rate == 48000) { 6 } else { 7 };
  let bounds: i32[] = [0];
  for i in 0..22 {
    bounds = bounds.append(bounds[i] + tables.G_SCF_LONG[row * 23 + i]);
  }
  return Transform { analysis: analysis, matrix: matrix, mdct: mdct, cs: cs, ca: ca, codes: codes.codes(), bounds: bounds };
}

pub function encoder(sample_rate: i32, channels: i32, options: Options): Result[Encoder, pcm.AudioError] {
  let index = bitrate_index(options.bitrate);
  if (index == 0 || channels < 1 || channels > 2) {
    return Err(pcm.Unsupported);
  }
  let rate_index = if (sample_rate == 44100) { 0 } else if (sample_rate == 48000) { 1 } else if (sample_rate == 32000) { 2 } else { 3 };
  if (rate_index == 3) {
    return Err(pcm.Unsupported);
  }
  return Ok(Encoder { sample_rate: sample_rate, channels: channels, bitrate_index: index, rate_index: rate_index, transform: transform(sample_rate), history: f32_zeros(512 * channels), offset: 0, previous: f32_zeros(576 * channels), pending: [], remainder: 0, consumed: 0i64, coded: 0i64 });
}

// Window the ring of one channel, and fold the 64 sums into the 32 inputs of the cosine matrix.
function fold(t: Transform, history: [f32], base: i32, at: i32, own y: f32[], own folded: f32[]): (f32[], f32[]) {
  for k in 0..64 {
    let sum = 0.0f32;
    for j in 0..8 {
      sum += t.analysis[k + 64 * j] * history[base + (at + k + 64 * j & 511)];
    }
    y = y.with(k, sum);
  }
  // The coefficient of y[k] is cos((2i + 1)(k - 16)pi/64). It is even about k = 16 and odd about k = 48.
  folded = folded.with(0, y[16]);
  for m in 1..17 {
    folded = folded.with(m, y[16 + m] + y[16 - m]);
  }
  for m in 17..32 {
    folded = folded.with(m, y[16 + m] - y[80 - m]);
  }
  return (y, folded);
}

// The 18 subband samples of each of the 32 subbands for one granule, in subband order.
// `history` holds the last 512 samples of each channel as a ring. Sample i before the newest is at `(offset + i) & 511`.
function analyse(t: Transform, own history: f32[], offset: i32, input: [f32], start: i32, channels: i32, channel: i32): (f32[], f32[]) {
  let base = 512 * channel;
  let output = f32_zeros(576);
  let y = f32_zeros(64);
  let folded = f32_zeros(32);
  let at = offset;
  for slot in 0..18 {
    at = at - 32 & 511;
    for k in 0..32 {
      history = history.with(base + (at + 31 - k & 511), input[(start + slot * 32 + k) * channels + channel]);
    }
    let (y1, f1) = fold(t, history, base, at, y, folded);
    y = y1;
    folded = f1;
    for band in 0..32 {
      let sum = 0.0f32;
      for m in 0..32 {
        sum += t.matrix[band * 32 + m] * folded[m];
      }
      // The decoder inverts each odd sample of each odd subband after its inverse MDCT.
      if ((band & 1) == 1 && (slot & 1) == 1) {
        sum = -sum;
      }
      output = output.with(band * 18 + slot, sum);
    }
  }
  return (history, output);
}

// The long-block MDCT of the previous and the current subband samples, then the alias butterflies.
function spectrum(t: Transform, previous: [f32], base: i32, current: [f32]): f32[] {
  let xr = f32_zeros(576);
  for band in 0..32 {
    for k in 0..18 {
      let sum = 0.0f32;
      for n in 0..18 {
        sum += t.mdct[k * 36 + n] * previous[base + band * 18 + n];
        sum += t.mdct[k * 36 + 18 + n] * current[band * 18 + n];
      }
      xr = xr.with(band * 18 + k, sum);
    }
  }
  for band in 1..32 {
    for i in 0..8 {
      let a = xr[18 * band - 1 - i];
      let b = xr[18 * band + i];
      xr = xr.with(18 * band - 1 - i, a * t.cs[i] + b * t.ca[i]);
      xr = xr.with(18 * band + i, b * t.cs[i] - a * t.ca[i]);
    }
  }
  return xr;
}

struct Granule { gain: i32, plan: codes.Plan, ix: i32[], negative: boolean[] }

function quantize(magnitude: [f32], gain: i32, own ix: i32[]): (i32[], boolean) {
  let step = (-0.1875f32 * ((gain - 210) as f32)).exp2();
  let fits = true;
  for i in 0..576 {
    let q = magnitude[i] * step + 0.4054f32;
    if (q > codes.MAX_VALUE as f32) {
      fits = false;
      ix = ix.with(i, codes.MAX_VALUE);
    } else {
      ix = ix.with(i, q as i32);
    }
  }
  return (ix, fits);
}

// Binary search for the smallest global gain whose code fits `budget` bits.
function granule(t: Transform, xr: [f32], budget: i32): Granule {
  let magnitude = f32_zeros(576);
  let negative: boolean[] = [];
  for i in 0..576 {
    magnitude = magnitude.with(i, xr[i].abs().pow(0.75f32));
    negative = negative.append(xr[i] < 0.0f32);
  }
  let low = 0;
  let high = 255;
  let ix = __alloc_i32(576);
  while (low < high) {
    let middle = (low + high) / 2;
    let (next, fits) = quantize(magnitude, middle, ix);
    ix = next;
    if (fits && codes.plan(t.codes, ix, t.bounds).bits <= budget) {
      high = middle;
    } else {
      low = middle + 1;
    }
  }
  let (result, _) = quantize(magnitude, low, ix);
  return Granule { gain: low, plan: codes.plan(t.codes, result, t.bounds), ix: result, negative: negative };
}

function side(own b: codes.Bits, g: Granule): codes.Bits {
  let p = g.plan;
  b = b.put(p.bits, 12).put(p.big, 9).put(g.gain, 8).put(0, 4).put(0, 1);
  b = b.put(p.table0, 5).put(p.table1, 5).put(p.table2, 5).put(p.region0, 4).put(p.region1, 3);
  return b.put(0, 1).put(0, 1).put(p.count1_table, 1);
}

// Code the 1152 frames of PCM that start at frame `start` of `input`.
// fern-lint: allow cyclomatic-complexity
function frame(own e: Encoder, input: [f32], start: i32, own output: u8[]): (Encoder, u8[]) {
  let numerator = 144000 * BITRATES[e.bitrate_index];
  let padding = 0;
  let remainder = e.remainder + numerator % e.sample_rate;
  if (remainder >= e.sample_rate) {
    remainder -= e.sample_rate;
    padding = 1;
  }
  let bytes = numerator / e.sample_rate + padding;
  let side_bytes = if (e.channels == 1) { 17 } else { 32 };
  let available = (bytes - 4 - side_bytes) * 8;
  let history = e.history;
  let offset = e.offset;
  let previous = e.previous;
  let granules: Granule[] = [];
  let used = 0;
  for gr in 0..2 {
    for ch in 0..e.channels {
      let (h, current) = analyse(e.transform, history, offset, input, start + gr * 576, e.channels, ch);
      history = h;
      let xr = spectrum(e.transform, previous, 576 * ch, current);
      for i in 0..576 {
        previous = previous.with(576 * ch + i, current[i]);
      }
      let left = 2 * e.channels - granules.len();
      let budget = (available - used) / left;
      let g = granule(e.transform, xr, if (budget > 4095) { 4095 } else { budget });
      used += g.plan.bits;
      granules = granules.append(g);
    }
    offset = offset - 576 & 511;
  }
  let b = codes.Bits { data: __alloc_u8(bytes), pos: 0 };
  b = b.put(65531, 16).put(e.bitrate_index, 4).put(e.rate_index, 2).put(padding, 1).put(0, 1);
  b = b.put(if (e.channels == 1) { 3 } else { 0 }, 2).put(0, 2).put(0, 1).put(1, 1).put(0, 2);
  b = b.put(0, 9).put(0, if (e.channels == 1) { 5 } else { 3 }).put(0, 4 * e.channels);
  for g in granules {
    b = side(b, g);
  }
  for g in granules {
    b = codes.write(e.transform.codes, b, g.ix, g.negative, g.plan, e.transform.bounds);
  }
  for byte in b.data {
    output = output.append(byte);
  }
  return (Encoder { ...e, history: history, offset: offset, previous: previous, remainder: remainder, coded: e.coded + 1152i64 }, output);
}

function valid(samples: [f32], channels: i32): boolean {
  if (samples.len() % channels != 0) {
    return false;
  }
  for s in samples {
    if (!s.is_finite() || s < -1.0f32 || s > 1.0f32) {
      return false;
    }
  }
  return true;
}

function frame_limit(e: Encoder, limits: pcm.Limits, output: [u8]): boolean {
  return output.len() as i64 + (144000 * BITRATES[e.bitrate_index] / e.sample_rate + 1) as i64 <= limits.max_input_bytes as i64;
}

// Append each complete frame of `samples` to `output`.
pub function (own e: Encoder) push(samples: [f32], own output: u8[], limits: pcm.Limits): Result[(Encoder, u8[]), pcm.AudioError] {
  if (!valid(samples, e.channels)) {
    return Err(pcm.Sample);
  }
  let pending = e.pending;
  for s in samples {
    pending = pending.append(s);
  }
  let span = 1152 * e.channels;
  let at = 0;
  while (pending.len() - at >= span) {
    if (!frame_limit(e, limits, output)) {
      return Err(pcm.Limit);
    }
    let first = at / e.channels;
    let (next, out) = frame(e, pending, first, output);
    e = next;
    output = out;
    at += span;
  }
  let rest: f32[] = [];
  for i in at..pending.len() {
    rest = rest.append(pending[i]);
  }
  return Ok((Encoder { ...e, pending: rest, consumed: e.consumed + (samples.len() / e.channels) as i64 }, output));
}

// Code the remaining PCM, then silence until the decoder has output each input sample.
pub function (own e: Encoder) finish(own output: u8[], limits: pcm.Limits): Result[(Encoder, u8[]), pcm.AudioError] {
  let pending = e.pending;
  let span = 1152 * e.channels;
  let at = 0;
  while (e.coded < e.consumed + DELAY as i64) {
    while (pending.len() - at < span) {
      pending = pending.append(0.0f32);
    }
    if (!frame_limit(e, limits, output)) {
      return Err(pcm.Limit);
    }
    let first = at / e.channels;
    let (next, out) = frame(e, pending, first, output);
    e = next;
    output = out;
    at += span;
  }
  return Ok((Encoder { ...e, pending: [] }, output));
}

pub function encode(samples: [f32], sample_rate: i32, channels: i32, options: Options, limits: pcm.Limits): Result[u8[], pcm.AudioError] {
  let e = encoder(sample_rate, channels, options)?;
  let (next, output) = e.push(samples, [], limits)?;
  let (_, done) = next.finish(output, limits)?;
  return Ok(done);
}
```
