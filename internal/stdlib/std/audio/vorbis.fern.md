# Native Vorbis packet decoder

This module decodes Vorbis version one packets to interleaved float PCM.
It supports one through eight channels, all standard block sizes, both floor types,
all residue types, multiple submaps, and channel coupling.
Output uses WAVE speaker order and retains unclipped float samples.

Create a decoder from the identification and setup packets.
The first audio packet primes overlap storage and produces no PCM.
`block_size` reads the time-domain block length without an overlap-state change.
Container code must apply its granule positions to trim the beginning and end.
Comment packets are read separately with `std/audio/vorbis/headers`.

Packet methods consume the decoder on error. Reset retains configuration and scratch storage.
The reusable-buffer method preserves storage after the valid output prefix.
Setup budgets bound codebook storage before allocation.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis decoder.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/dsp";
import "std/audio/vorbis/headers";
import "std/audio/vorbis/packet";
import "std/audio/vorbis/setup";
import "std/audio/vorbis/types";
import "std/audio/pcm";

pub opaque struct Decoder { info: types.Info, config: setup.Setup, previous: i32, scratch: dsp.Scratch, delay: f32[], spectra: f32[], floors: f32[], curve: f32[] }

pub struct Frame { sample_rate: i32, channels: i32, frames: i32, bytes_read: i32, samples: f32[] }

pub function create(identification: [u8], setup_packet: [u8], limits: types.Limits): Result[Decoder, pcm.AudioError] {
  let info = headers.identification(identification, limits)?;
  let config = setup.read(setup_packet, info, limits)?;
  let half = 1 << info.large_exponent - 1;
  return Ok(Decoder { info: info, config: config, previous: 0, scratch: dsp.create(info.large_exponent)?, delay: dsp.zeros(half * info.channels), spectra: dsp.zeros(half * info.channels), floors: dsp.zeros(half * info.channels), curve: dsp.zeros(half) });
}

pub function (decoder: Decoder) information(): types.Info {
  return decoder.info;
}

pub function (own decoder: Decoder) reset(): Decoder {
  let Decoder { info, config, previous, scratch, delay, spectra, floors, curve } = decoder;
  for i in 0..delay.len() {
    delay = delay.with(i, 0.0f32);
  }
  return Decoder { info: info, config: config, previous: 0, scratch: scratch, delay: delay, spectra: spectra, floors: floors, curve: curve };
}

pub function (decoder: Decoder) packet_frames(input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let current = packet.exponent(input, decoder.info, decoder.config)?;
  let frames = dsp.frames(current, decoder.previous)?;
  let _ = pcm.sample_count(frames, decoder.info.channels, limits)?;
  return Ok(frames);
}

pub function (decoder: Decoder) block_size(input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  return Ok(1 << packet.exponent(input, decoder.info, decoder.config)?);
}

pub function (own decoder: Decoder) decode_packet_into(input: [u8], own output: f32[], limits: pcm.Limits): Result[(Decoder, Frame), pcm.AudioError] {
  let frames = decoder.packet_frames(input, limits)?;
  if (output.len() < frames * decoder.info.channels) {
    return Err(pcm.Buffer);
  }
  let Decoder { info, config, previous, scratch, delay, spectra, floors, curve } = decoder;
  let packet.Run { work, spectra: values, floors: floor_output, curve: curve_output, exponent } = packet.read(input, info, config, previous, dsp.Work { scratch: scratch, delay: delay, pcm: output }, spectra, floors, curve)?;
  let dsp.Work { scratch: next_scratch, delay: next_delay, pcm: samples } = work;
  let next = Decoder { info: info, config: config, previous: exponent, scratch: next_scratch, delay: next_delay, spectra: values, floors: floor_output, curve: curve_output };
  return Ok((next, Frame { sample_rate: info.sample_rate, channels: info.channels, frames: frames, bytes_read: input.len(), samples: samples }));
}

pub function (own decoder: Decoder) decode_packet(input: [u8], limits: pcm.Limits): Result[(Decoder, Frame), pcm.AudioError] {
  let frames = decoder.packet_frames(input, limits)?;
  let output = dsp.zeros(frames * decoder.info.channels);
  return decoder.decode_packet_into(input, output, limits);
}
```
