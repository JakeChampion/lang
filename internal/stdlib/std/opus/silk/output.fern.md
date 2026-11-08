# SILK output channels and rates

This module combines SILK packet decoding with output resampling and channel conversion.
Call `begin_packet` once before the encoded frames of an Opus packet.
The packet boundary retains separate resampler histories during a stereo-to-mono change.
`decode_frame` accepts the shared range decoder and returns normalized, interleaved float PCM.
The outer Opus decoder handles CELT redundancy and mode transitions.

The output history rules follow [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2).
Copyright 2026 The Pion community. The MIT license is in `licenses/opus/PION-MIT.txt`.

```fern
<<*>>=
// SILK output-rate conversion with persistent channel and filter state.
// Copyright 2026 The Pion community. SPDX-License-Identifier: MIT

import "std/audio/pcm";
import "std/opus/range";
import "std/opus/silk";
import "std/opus/silk/entropy";
import "std/opus/silk/resample";

pub struct State {
  decoder: silk.Decoder,
  filters: resample.State[],
  input_rate: i32,
  output_rate: i32,
  output_channels: i32,
  stream_channels: i32,
  filter_channels: i32,
  input_buffer: i32[],
  output_buffer: i32[],
}

pub function decoder(output_rate: i32, channels: i32): Result[State, pcm.AudioError] {
  if (channels < 1 || channels > 2) {
    return Err(pcm.Header);
  }
  let filter = resample.resampler(8, output_rate / 1000)?;
  if (output_rate != filter.output_rate * 1000) {
    return Err(pcm.Header);
  }
  return Ok(State {
    decoder: silk.decoder(), filters: [filter, filter], input_rate: 0,
    output_rate: output_rate, output_channels: channels, stream_channels: 0,
    filter_channels: 0, input_buffer: pcm.zeros(960), output_buffer: pcm.zeros(2880),
  });
}

function filter_count(previous: State, rate: i32, stream_channels: i32): i32 {
  if (previous.output_channels != 2) {
    return 1;
  }
  if (stream_channels == 2) {
    return 2;
  }
  if (rate == previous.input_rate && previous.stream_channels == 2) {
    return 2;
  }
  return 1;
}

pub function begin_packet(own previous: State, rate: i32, stream_channels: i32): Result[State, pcm.AudioError] {
  if (!entropy.valid_frame(rate, 20) || stream_channels < 1 || stream_channels > 2) {
    return Err(pcm.Header);
  }
  let filters = previous.filters;
  let filter_channels = filter_count(previous, rate, stream_channels);
  if (rate != previous.input_rate) {
    let filter = resample.resampler(rate, previous.output_rate / 1000)?;
    filters = [filter, filter];
  } else {
    if (filter_channels == 2 && previous.filter_channels == 1) {
      filters = filters.with(1, filters[0]);
    }
  }
  return Ok(State {
    ...previous, filters: filters, input_rate: rate,
    stream_channels: stream_channels, filter_channels: filter_channels,
  });
}

function zeros(count: i32): f32[] {
  let output: f32[] = [];
  for _ in 0..count {
    output = output.append(0.0f32);
  }
  return output;
}

function convert(own previous: State, input: [i32], duration: i32, decoded_channels: i32): Result[(State, f32[]), pcm.AudioError] {
  let frames = previous.input_rate * duration;
  let output_frames = previous.output_rate / 1000 * duration;
  let input_buffer = previous.input_buffer;
  let output_buffer = previous.output_buffer;
  let filters = previous.filters;
  let output = zeros(output_frames * previous.output_channels);
  for channel in 0..previous.filter_channels {
    let source = if (decoded_channels == 1) { 0 } else { channel };
    for i in 0..frames {
      input_buffer = input_buffer.with(i, input[i * decoded_channels + source]);
    }
    let filter = filters[channel];
    let (next, samples) = resample.process_into(filter, input_buffer[0:frames], output_buffer)?;
    filters = filters.with(channel, next);
    output_buffer = samples;
    for i in 0..output_frames {
      let value = output_buffer[i] as f32 / 32768.0f32;
      output = output.with(i * previous.output_channels + channel, value);
      if (previous.filter_channels == 1 && previous.output_channels == 2) {
        output = output.with(i * 2 + 1, value);
      }
    }
  }
  return Ok((State { ...previous, filters: filters, input_buffer: input_buffer, output_buffer: output_buffer }, output));
}

pub function decode_frame(own previous: State, own range: range.Decoder, duration: i32): Result[(State, range.Decoder, f32[]), pcm.AudioError] {
  let State { decoder, filters, input_rate, output_rate, output_channels, stream_channels, filter_channels, input_buffer, output_buffer } = previous;
  let decoded_channels = if (stream_channels == 2 && output_channels == 2) { 2 } else { 1 };
  let (next, last, samples) = silk.decode_range(decoder, range, input_rate, duration, stream_channels, decoded_channels)?;
  let current = State {
    decoder: next, filters: filters, input_rate: input_rate, output_rate: output_rate,
    output_channels: output_channels, stream_channels: stream_channels, filter_channels: filter_channels,
    input_buffer: input_buffer, output_buffer: output_buffer,
  };
  let (state, output) = convert(current, samples, duration, decoded_channels)?;
  return Ok((state, last, output));
}
```
