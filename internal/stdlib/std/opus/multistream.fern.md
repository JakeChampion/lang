# Opus multistream audio

This module decodes Opus multistream packets into interleaved float samples.
It supports coupled streams, independent mono streams, repeated channel maps, and silent channels.
All streams in a packet must have the same duration.
The first streams use self-delimited framing. The final stream uses ordinary packet framing.

`Config.mapping` follows RFC 7845.
Indices below twice the coupled count select stereo channels.
Other indices select mono channels. Index 255 produces silence.
The mapping declares sample positions. It does not assign speaker positions.

Limits bound input bytes, the stream count, and valid output samples per packet.
`decode_into` consumes a reusable output buffer. Entries after the valid samples retain their values.
The decoder still allocates bounded packet and codec scratch storage.
An error consumes the decoder. Create a new decoder after an error.

```fern
<<*>>=
// Opus multistream packet decode, channel maps, and reusable output buffers.

import "std/audio/pcm";
import "std/opus";
import "std/opus/packet";

pub struct Config { sample_rate: i32, channels: i32, streams: i32, coupled: i32, mapping: u8[] }

pub struct Limits { max_streams: i32, max_samples: i32, max_input_bytes: i32 }

pub struct Route { output: i32, input: i32 }

pub struct Decoder { config: Config, limits: Limits, states: opus.Decoder[], routes: Route[][] }

pub struct Audio { samples: f32[], frames: i32, channels: i32, sample_rate: i32 }

struct Packets { bytes: u8[][], frames: i32 }

pub function default_limits(): Limits {
  return Limits { max_streams: 8, max_samples: 46080, max_input_bytes: 1048576 };
}

function valid_layout(config: Config): boolean {
  return config.channels >= 1 && config.channels <= 255 && config.mapping.len() == config.channels
    && config.streams >= 1 && config.coupled >= 0 && config.coupled <= config.streams
    && config.streams + config.coupled <= 255;
}

function routes(config: Config, stream: i32): Result[Route[], pcm.AudioError] {
  let out: Route[] = [];
  for channel in 0..config.channels {
    let index = config.mapping[channel] as i32;
    if (index != 255) {
      if (index >= config.streams + config.coupled) {
        return Err(pcm.Header);
      }
      let owner = if (index < 2 * config.coupled) { index / 2 } else { index - config.coupled };
      if (owner == stream) {
        out = out.append(Route { output: channel, input: if (stream < config.coupled) { index & 1 } else { 0 } });
      }
    }
  }
  return Ok(out);
}

function valid_limits(limits: Limits, streams: i32): boolean {
  return limits.max_streams >= 1 && limits.max_streams <= 255 && limits.max_samples >= 0
    && limits.max_input_bytes >= 0 && streams <= limits.max_streams;
}

pub function decoder(config: Config, limits: Limits): Result[Decoder, pcm.AudioError] {
  if (!valid_layout(config)) {
    return Err(pcm.Header);
  }
  if (!valid_limits(limits, config.streams)) {
    return Err(pcm.Limit);
  }
  let states: opus.Decoder[] = [];
  let map: Route[][] = [];
  for stream in 0..config.streams {
    let channels = if (stream < config.coupled) { 2 } else { 1 };
    states = states.append(opus.decoder(config.sample_rate, channels)?);
    map = map.append(routes(config, stream)?);
  }
  return Ok(Decoder { config: config, limits: limits, states: states, routes: map });
}

function copied(input: [u8]): u8[] {
  let out: u8[] = [];
  for byte in input {
    out = out.append(byte);
  }
  return out;
}

function split(input: [u8], config: Config, limits: Limits): Result[Packets, pcm.AudioError] {
  if (input.len() > limits.max_input_bytes) {
    return Err(pcm.Limit);
  }
  let bodies: u8[][] = [];
  let at = 0;
  let frames = 0;
  for stream in 0..config.streams {
    let body: u8[] = [];
    if (stream + 1 < config.streams) {
      let parsed = match (packet.parse_delimited(input[at:])) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
      body = parsed.packet;
      at += parsed.consumed;
    } else {
      body = copied(input[at:]);
    }
    let count = match (packet.sample_count(body, config.sample_rate)) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
    if (stream > 0 && frames != count) {
      return Err(pcm.Header);
    }
    frames = count;
    bodies = bodies.append(body);
  }
  return Ok(Packets { bytes: bodies, frames: frames });
}

function sample_count(config: Config, limits: Limits, frames: i32): Result[i32, pcm.AudioError] {
  if (frames <= 0 || frames > config.sample_rate * 120 / 1000 || frames % (config.sample_rate / 400) != 0) {
    return Err(pcm.Header);
  }
  let Some(count) = frames *? config.channels else {
    return Err(pcm.Limit);
  };
  if (count > limits.max_samples) {
    return Err(pcm.Limit);
  }
  return Ok(count);
}

function zeros(count: i32): f32[] {
  let out: f32[] = [];
  for _ in 0..count {
    out = out.append(0.0f32);
  }
  return out;
}

function stream_decode(own state: opus.Decoder, input: [u8], action: i32, frames: i32): (opus.Decoder, Result[f32[], pcm.AudioError]) {
  if (action == 1) {
    return state.decode_lost(frames);
  }
  if (action == 2) {
    return state.decode_fec(input, frames);
  }
  return state.decode(input);
}

function routed(own output: f32[], samples: [f32], routes: [Route], frames: i32, channels: i32, source_channels: i32): f32[] {
  for route in routes {
    for frame in 0..frames {
      output = output.with(frame * channels + route.output, samples[frame * source_channels + route.input]);
    }
  }
  return output;
}

function decode_packets(own d: Decoder, packets: Packets, action: i32, frames: i32, own output: f32[]): Result[(Decoder, Audio), pcm.AudioError] {
  let count = sample_count(d.config, d.limits, frames)?;
  if (output.len() < count) {
    return Err(pcm.Buffer);
  }
  for i in 0..count {
    output = output.with(i, 0.0f32);
  }
  let Decoder { config, limits, states, routes } = d;
  let next_states: opus.Decoder[] = [];
  let stream = 0;
  for state in states {
    let input: u8[] = if (action == 1) { [] } else { packets.bytes[stream] };
    let (next, result) = stream_decode(state, input, action, frames);
    let samples = result?;
    let source_channels = if (stream < config.coupled) { 2 } else { 1 };
    if (samples.len() != frames * source_channels) {
      return Err(pcm.Header);
    }
    output = routed(output, samples, routes[stream], frames, config.channels, source_channels);
    next_states = next_states.append(next);
    stream += 1;
  }
  return Ok((Decoder { config: config, limits: limits, states: next_states, routes: routes }, Audio { samples: output, frames: frames, channels: config.channels, sample_rate: config.sample_rate }));
}

pub function (own d: Decoder) decode_into(input: [u8], own output: f32[]): Result[(Decoder, Audio), pcm.AudioError] {
  let packets = split(input, d.config, d.limits)?;
  return decode_packets(d, packets, 0, packets.frames, output);
}

pub function (own d: Decoder) decode(input: [u8]): Result[(Decoder, Audio), pcm.AudioError] {
  let packets = split(input, d.config, d.limits)?;
  let count = sample_count(d.config, d.limits, packets.frames)?;
  return decode_packets(d, packets, 0, packets.frames, zeros(count));
}

pub function (own d: Decoder) decode_lost(frames: i32): Result[(Decoder, Audio), pcm.AudioError] {
  let count = sample_count(d.config, d.limits, frames)?;
  return d.decode_lost_into(frames, zeros(count));
}

pub function (own d: Decoder) decode_lost_into(frames: i32, own output: f32[]): Result[(Decoder, Audio), pcm.AudioError] {
  return decode_packets(d, Packets { bytes: [], frames: frames }, 1, frames, output);
}

pub function (own d: Decoder) decode_fec(input: [u8], frames: i32): Result[(Decoder, Audio), pcm.AudioError] {
  let count = sample_count(d.config, d.limits, frames)?;
  return d.decode_fec_into(input, frames, zeros(count));
}

pub function (own d: Decoder) decode_fec_into(input: [u8], frames: i32, own output: f32[]): Result[(Decoder, Audio), pcm.AudioError] {
  let count = sample_count(d.config, d.limits, frames)?;
  let packets = split(input, d.config, d.limits)?;
  if (packets.frames > frames) {
    return Err(pcm.Buffer);
  }
  return decode_packets(d, packets, 2, frames, output);
}

pub function (d: Decoder) final_range(): u32 {
  let range = 0u32;
  for state in d.states {
    range ^= state.final_range();
  }
  return range;
}

pub function (own d: Decoder) reset(): Decoder {
  let Decoder { config, limits, states, routes } = d;
  let next: opus.Decoder[] = [];
  for state in states {
    next = next.append(state.reset());
  }
  return Decoder { config: config, limits: limits, states: next, routes: routes };
}
```
