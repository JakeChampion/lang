# Native AAC-LC packet decoder

This module decodes 1024-sample AAC-LC raw access units and ADTS frames to interleaved float PCM.
It supports long and grouped short windows, spectral codebooks, escape values, pulse data, stereo tools, noise substitution, and temporal noise shaping.
The declared channel configuration bounds decoder state and scratch storage.
Interleaved output uses WAVE speaker order for the declared AAC channel configuration.
Configuration 7 places its extra front pair after the back pair.

Packet methods consume the decoder on error. The caller can create a new decoder from its retained configuration.
The reusable-buffer methods retain storage beyond the valid output prefix.
ADTS configuration changes reset overlap state before the new format is decoded.

HE-AAC, coupling-channel elements, program configurations, and 960-sample frames are explicit unsupported cases.
ADTS header inspection exposes transport CRC fields. Protected frame decode is unsupported until CRC verification is implemented.
This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's AAC decoder.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// Previous author: Kostya Shishkov <kostya.shiskov@gmail.com>.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/bits";
import "std/audio/aac/config";
import "std/audio/aac/dsp";
import "std/audio/aac/ics";
import "std/audio/aac/stereo";
import "std/audio/pcm" as audio;

struct Element { kind: i32, tag: i32, channel: i32 }

const ELEMENT_STARTS: i32[] = [0, 0, 1, 2, 4, 7, 10, 14, 19];

const ELEMENT_KINDS: i32[] = [0, 1, 0, 1, 0, 1, 0, 0, 1, 1, 0, 1, 1, 3, 0, 1, 1, 1, 3];

pub opaque struct Decoder { config: config.Config, delay: f32[], scratch: dsp.Scratch, shapes: i32[], seed: u32, elements: Element[] }

pub struct Frame { sample_rate: i32, channels: i32, channel_config: i32, frames: i32, bytes_read: i32, samples: f32[] }

pub struct Decoded { sample_rate: i32, channels: i32, channel_config: i32, samples: f32[] }

struct Run { config: config.Config, reader: bits.Reader, work: dsp.Work, shapes: i32[], seed: u32, elements: Element[], seen: i32[], channels: i32, count: i32, first: boolean, out_at: i32 }

pub function create(c: config.Config, limits: audio.Limits): Result[Decoder, audio.AudioError] {
  let canonical = config.create(c.object_type, c.sample_rate, c.channel_config)?;
  if (c.channels != canonical.channels || c.rate_index != canonical.rate_index || c.frame_size != 1024) {
    return Err(audio.Header);
  }
  let _ = audio.sample_count(1024, c.channels, limits)?;
  if (limits.max_input_bytes < 0) {
    return Err(audio.Limit);
  }
  return Ok(Decoder { config: canonical, delay: dsp.zeros(c.channels * 1024), scratch: dsp.create(), shapes: audio.zeros(c.channels), seed: 523124044u32, elements: [] });
}

pub function (decoder: Decoder) configuration(): config.Config {
  return decoder.config;
}

pub function (own decoder: Decoder) reset(): Decoder {
  let Decoder { config, delay, scratch, shapes, seed, elements } = decoder;
  for i in 0..delay.len() {
    delay = delay.with(i, 0.0f32);
  }
  for i in 0..shapes.len() {
    shapes = shapes.with(i, 0);
  }
  return Decoder { config: config, delay: delay, scratch: scratch, shapes: shapes, seed: 523124044u32, elements: [] };
}

function expected_element(config: i32, index: i32): i32 {
  if (config < 1 || config > 7 || index < 0) {
    return -1;
  }
  if (index >= ELEMENT_STARTS[config + 1] - ELEMENT_STARTS[config]) {
    return -1;
  }
  return ELEMENT_KINDS[ELEMENT_STARTS[config] + index];
}

function output_channel(config: i32, channel: i32): i32 {
  if (config <= 2) {
    return channel;
  }
  if (channel < 3) {
    return if (channel == 0) { 2 } else { channel - 1 };
  }
  if (config <= 5) {
    return channel;
  }
  if (config == 6) {
    return if (channel == 5) { 3 } else { channel + 1 };
  }
  if (channel == 7) {
    return 3;
  }
  return if (channel < 5) { channel + 3 } else { channel - 1 };
}

function element(own run: Run, kind: i32, tag: i32): Result[(Run, i32), audio.AudioError] {
  let index = -1;
  for i in 0..run.elements.len() {
    if (run.elements[i].kind == kind && run.elements[i].tag == tag) {
      index = i;
    }
  }
  if (index < 0) {
    if (!run.first) {
      return Err(audio.Header);
    }
    index = run.elements.len();
    if (kind != expected_element(run.config.channel_config, index)) {
      return Err(audio.Header);
    }
    run = Run { ...run, elements: run.elements.append(Element { kind: kind, tag: tag, channel: run.channels }) };
  }
  for used in run.seen {
    if (used == index) {
      return Err(audio.Header);
    }
  }
  let channel = run.elements[index].channel;
  let count = if (kind == 1) { 2 } else { 1 };
  if (channel + count > run.config.channels || run.channels + count > run.config.channels) {
    return Err(audio.Header);
  }
  return Ok((Run { ...run, seen: run.seen.append(index), channels: run.channels + count }, channel));
}

function synthesize(own run: Run, own channel: ics.Channel, at: i32): Result[Run, audio.AudioError] {
  let shaped = ics.shape(channel, run.config.rate_index)?;
  let previous = run.shapes[at];
  let work = dsp.synth(shaped.coefficients, run.work, output_channel(run.config.channel_config, at), run.config.channels, run.out_at, shaped.info.sequence, shaped.info.shape, previous)?;
  return Ok(Run { ...run, work: work, shapes: run.shapes.with(at, shaped.info.shape) });
}

function single(input: [u8], own run: Run, kind: i32): Result[Run, audio.AudioError] {
  let (b1, tag) = bits.read(input, run.reader, 4)?;
  let (next, channel) = element(Run { ...run, reader: b1 }, kind, tag as i32)?;
  let (end, value, seed) = ics.decode(input, next.reader, None, next.config.rate_index, next.seed, None, [])?;
  return synthesize(Run { ...next, reader: end, seed: seed }, value, channel);
}

function pair(input: [u8], own run: Run): Result[Run, audio.AudioError] {
  let (b1, tag) = bits.read(input, run.reader, 4)?;
  let (b2, common_window) = bits.read(input, b1, 1)?;
  let (next, channel) = element(Run { ...run, reader: b2 }, 1, tag as i32)?;
  run = next;
  let common: Option[ics.Info] = None;
  let mask = stereo.Mask { kind: 0, bands: [] };
  if (common_window != 0u32) {
    let (b3, info) = ics.info(input, run.reader, run.config.rate_index)?;
    let (b4, value) = stereo.read(input, b3, info, run.config.rate_index)?;
    run = Run { ...run, reader: b4 };
    common = Some(info);
    mask = value;
  }
  let (b5, left, seed1) = ics.decode(input, run.reader, common, run.config.rate_index, run.seed, None, [])?;
  let other = if (common_window != 0u32) { Some(left) } else { None };
  let (b6, right, seed2) = ics.decode(input, b5, common, run.config.rate_index, seed1, other, mask.bands)?;
  run = Run { ...run, reader: b6, seed: seed2 };
  if (common_window != 0u32) {
    let (l, r) = stereo.apply(left, right, mask, run.config.rate_index)?;
    return synthesize(synthesize(run, l, channel)?, r, channel + 1);
  }
  return synthesize(synthesize(run, left, channel)?, right, channel + 1);
}

function data_element(input: [u8], own run: Run): Result[Run, audio.AudioError] {
  let (b1, _) = bits.read(input, run.reader, 4)?;
  let (b2, aligned) = bits.read(input, b1, 1)?;
  let (b3, size) = bits.read(input, b2, 8)?;
  let reader = b3;
  let count = size as i32;
  if (size == 255u32) {
    let (next, extra) = bits.read(input, reader, 8)?;
    reader = next;
    count += extra as i32;
  }
  if (aligned != 0u32) {
    reader = bits.align(input, reader)?;
  }
  return Ok(Run { ...run, reader: bits.skip(input, reader, count * 8)? });
}

function fill(input: [u8], own run: Run): Result[Run, audio.AudioError] {
  let (b1, size) = bits.read(input, run.reader, 4)?;
  let reader = b1;
  let count = size as i32;
  if (size == 15u32) {
    let (next, extra) = bits.read(input, reader, 8)?;
    reader = next;
    count += extra as i32 - 1;
  }
  if (count > 0) {
    let (_, extension) = bits.read(input, reader, 4)?;
    if (extension == 13u32 || extension == 14u32) {
      return Err(audio.Unsupported);
    }
  }
  return Ok(Run { ...run, reader: bits.skip(input, reader, count * 8)? });
}

// Dispatch the fixed three-bit raw-data-block element identifiers.
// fern-lint: allow cyclomatic-complexity
function step(input: [u8], own run: Run): Result[(Run, boolean), audio.AudioError] {
  if (run.count >= 64) {
    return Err(audio.Limit);
  }
  let (reader, kind) = bits.read(input, run.reader, 3)?;
  run = Run { ...run, reader: reader, count: run.count + 1 };
  match (kind) {
    0u32 => {
      return Ok((single(input, run, 0)?, false));
    },
    1u32 => {
      return Ok((pair(input, run)?, false));
    },
    3u32 => {
      return Ok((single(input, run, 3)?, false));
    },
    4u32 => {
      return Ok((data_element(input, run)?, false));
    },
    6u32 => {
      return Ok((fill(input, run)?, false));
    },
    7u32 => {
      return Ok((run, true));
    },
    _ => {
      return Err(audio.Unsupported);
    }
  }
}

function block(input: [u8], own run: Run): Result[Run, audio.AudioError] {
  run = Run { ...run, first: run.elements.len() == 0, seen: [], channels: 0, count: 0 };
  while (true) {
    let (next, end) = step(input, run)?;
    run = next;
    if (end) {
      break;
    }
  }
  if (run.channels != run.config.channels || run.seen.len() != run.elements.len()) {
    return Err(audio.Header);
  }
  return Ok(Run { ...run, reader: bits.align(input, run.reader)? });
}

function begin(own decoder: Decoder, own output: f32[], at: i32, blocks: i32, limits: audio.Limits): Result[Run, audio.AudioError] {
  let count = audio.sample_count(1024 * blocks, decoder.config.channels, limits)?;
  if (output.len() < count) {
    return Err(audio.Buffer);
  }
  let Decoder { config, delay, scratch, shapes, seed, elements } = decoder;
  return Ok(Run { config: config, reader: bits.Reader { at: at, bit: 0 }, work: dsp.Work { delay: delay, scratch: scratch, pcm: output }, shapes: shapes, seed: seed, elements: elements, seen: [], channels: 0, count: 0, first: false, out_at: 0 });
}

function finish(own run: Run, frames: i32, bytes: i32): (Decoder, Frame) {
  let Run { config, reader, work, shapes, seed, elements, seen, channels, count, first, out_at } = run;
  let dsp.Work { delay, scratch, pcm } = work;
  let decoder = Decoder { config: config, delay: delay, scratch: scratch, shapes: shapes, seed: seed, elements: elements };
  let frame = Frame { sample_rate: config.sample_rate, channels: config.channels, channel_config: config.channel_config, frames: frames, bytes_read: bytes, samples: pcm };
  return (decoder, frame);
}

pub function (own decoder: Decoder) decode_packet_into(input: [u8], own output: f32[], limits: audio.Limits): Result[(Decoder, Frame), audio.AudioError] {
  audio.check_input(input, limits)?;
  let run = block(input, begin(decoder, output, 0, 1, limits)?)?;
  if (bits.left(input, run.reader) != 0i64) {
    return Err(audio.Header);
  }
  return Ok(finish(run, 1024, input.len()));
}

pub function (own decoder: Decoder) decode_packet(input: [u8], limits: audio.Limits): Result[(Decoder, Frame), audio.AudioError] {
  let count = audio.sample_count(1024, decoder.config.channels, limits)?;
  return decoder.decode_packet_into(input, dsp.zeros(count), limits);
}

pub function (own decoder: Decoder) decode_adts_into(input: [u8], at: i32, own output: f32[], limits: audio.Limits): Result[(Decoder, Frame), audio.AudioError] {
  let header = config.adts(input, at, limits)?;
  if (header.bytes > input.len() - at) {
    return Err(audio.Truncated);
  }
  if (header.protected) {
    return Err(audio.Unsupported);
  }
  if (header.config != decoder.config) {
    decoder = create(header.config, limits)?;
  }
  let bytes = input[at:at + header.bytes];
  let run = begin(decoder, output, header.header_bytes, header.blocks, limits)?;
  for block_no in 0..header.blocks {
    run = block(bytes, run)?;
    run = Run { ...run, out_at: (block_no + 1) * 1024 * header.config.channels };
  }
  if (bits.left(bytes, run.reader) != 0i64) {
    return Err(audio.Header);
  }
  return Ok(finish(run, 1024 * header.blocks, header.bytes));
}

pub function (own decoder: Decoder) decode_adts(input: [u8], at: i32, limits: audio.Limits): Result[(Decoder, Frame), audio.AudioError] {
  let header = config.adts(input, at, limits)?;
  let count = audio.sample_count(1024 * header.blocks, header.config.channels, limits)?;
  return decoder.decode_adts_into(input, at, dsp.zeros(count), limits);
}

pub function decode(input: [u8], limits: audio.Limits): Result[Decoded, audio.AudioError] {
  audio.check_input(input, limits)?;
  let header = config.adts(input, 0, limits)?;
  let decoder = create(header.config, limits)?;
  let samples: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let current = config.adts(input, at, limits)?;
    if (current.config != header.config) {
      return Err(audio.Unsupported);
    }
    let count = audio.sample_count(current.blocks * 1024, current.config.channels, limits)?;
    if (count > limits.max_samples - samples.len()) {
      return Err(audio.Limit);
    }
    let (next, frame) = decoder.decode_adts(input, at, limits)?;
    decoder = next;
    for sample in frame.samples {
      samples = samples.append(sample);
    }
    at += frame.bytes_read;
  }
  return Ok(Decoded { sample_rate: header.config.sample_rate, channels: header.config.channels, channel_config: header.config.channel_config, samples: samples });
}
```
