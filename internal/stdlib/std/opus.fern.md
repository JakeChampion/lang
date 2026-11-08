# Opus audio packets

This module connects SILK and CELT to standard Opus packet framing.
It encodes fullband CELT packets at 48 kHz with one or two channels and frames of 2.5, 5, 10, or 20 ms.

The decoder accepts SILK, hybrid, and CELT packets at all five Opus output sample rates.
It accepts every packet frame-count code, including multiple frames and padding.
It handles mode transitions, packet loss, and in-band error correction.

Input is borrowed. Frame sizes, frame count, and duration bound decoder storage.
Padding can exceed the coded frame data without increasing decoded output.
A transport or container must enforce its encoded input budget.

Samples are interleaved `f32` values. The encoder requires finite values between -1 and 1.
Decoder output can exceed that interval. Apply clipping at the device conversion boundary.

Ogg containers, pre-skip, output gain, and end trimming are outside this module.
A file decoder must apply those container rules.

`decode_lost` takes the lost duration as samples per channel at the output sample rate.
`decode_fec` takes the next packet and the lost duration.
After recovery, decode that same packet normally to obtain its current audio.
Both durations must be multiples of 2.5 ms, with a maximum of 120 ms.

The encoder has a 120-sample lookahead at 48 kHz. A container must account for that delay and final padding.
After a decode error, reset the decoder before the next packet.

```fern
<<*>>=
// Encode CELT packets and decode Opus packets with interleaved samples.

import "std/audio/pcm";
import "std/opus/playback";
import "std/opus/celt_encoder";
import "std/f32";

pub struct Decoder { state: Option[playback.State], sample_rate: i32, channels: i32 }

pub struct Encoder { state: celt_encoder.Encoder, channels: i32 }

pub struct Encoded { encoder: Encoder, packet: u8[] }

pub function decoder(sample_rate: i32, channels: i32): Result[Decoder, pcm.AudioError] {
  return match (playback.decoder(sample_rate, channels)) {
    Ok(state) => Ok(Decoder { state: Some(state), sample_rate: sample_rate, channels: channels }),
    Err(error) => Err(error),
  };
}

pub function (own d: Decoder) reset(): Decoder {
  return match (decoder(d.sample_rate, d.channels)) {
    Ok(next) => next,
    Err(_) => Decoder { ...d, state: None },
  };
}

function decoded(result: Result[(playback.State, f32[]), pcm.AudioError], rate: i32, channels: i32): (Decoder, Result[f32[], pcm.AudioError]) {
  match (result) {
    Ok((next, samples)) => {
      return (Decoder { state: Some(next), sample_rate: rate, channels: channels }, Ok(samples));
    },
    Err(error) => {
      return (Decoder { state: None, sample_rate: rate, channels: channels }, Err(error));
    }
  }
}

pub function (own d: Decoder) decode(input: [u8]): (Decoder, Result[f32[], pcm.AudioError]) {
  let Decoder { state, sample_rate, channels } = d;
  let Some(current) = state else {
    return (Decoder { state: None, sample_rate: sample_rate, channels: channels }, Err(pcm.Header));
  };
  return decoded(playback.decode(current, input), sample_rate, channels);
}

pub function (own d: Decoder) decode_lost(frames: i32): (Decoder, Result[f32[], pcm.AudioError]) {
  let Decoder { state, sample_rate, channels } = d;
  let Some(current) = state else {
    return (Decoder { state: None, sample_rate: sample_rate, channels: channels }, Err(pcm.Header));
  };
  return decoded(playback.decode_lost(current, frames), sample_rate, channels);
}

pub function (own d: Decoder) decode_fec(input: [u8], frames: i32): (Decoder, Result[f32[], pcm.AudioError]) {
  let Decoder { state, sample_rate, channels } = d;
  let Some(current) = state else {
    return (Decoder { state: None, sample_rate: sample_rate, channels: channels }, Err(pcm.Header));
  };
  return decoded(playback.decode_fec(current, input, frames), sample_rate, channels);
}

pub function (d: Decoder) final_range(): u32 {
  return match (d.state) { Some(state) => state.control.final_range, None => 0u32 };
}

pub function encoder(channels: i32, bitrate: i32): Result[Encoder, pcm.AudioError] {
  if (channels != 1 && channels != 2 || bitrate < 6000 || bitrate > 510000) {
    return Err(pcm.Unsupported);
  }
  let state = celt_encoder.Encoder { ...celt_encoder.new_encoder(channels), bitrate: bitrate, vbr: true };
  return Ok(Encoder { state: state, channels: channels });
}

// fern-lint: allow cyclomatic-complexity
// One check per argument, and one arm per frame size.
pub function (own enc: Encoder) encode(samples: [f32], frame_size: i32, max_bytes: i32): Result[Encoded, pcm.AudioError] {
  if (enc.channels < 1 || enc.channels > 2 || frame_size != 120 && frame_size != 240 && frame_size != 480 && frame_size != 960) {
    return Err(pcm.Header);
  }
  if (samples.len() != frame_size * enc.channels) {
    return Err(pcm.Header);
  }
  if (max_bytes < 3 || max_bytes > 1276) {
    return Err(pcm.Limit);
  }
  for sample in samples {
    if (!sample.is_finite() || sample < -1.0f32 || sample > 1.0f32) {
      return Err(pcm.Sample);
    }
  }
  let lm = if (frame_size == 120) { 0 } else if (frame_size == 240) { 1 } else if (frame_size == 480) { 2 } else { 3 };
  let result = celt_encoder.encode(enc.state, samples, frame_size, max_bytes - 1);
  if (result.error != 0) {
    return Err(pcm.Header);
  }
  let toc = 224 + lm * 8 + (if (enc.channels == 2) { 4 } else { 0 });
  let out: u8[] = [toc as u8];
  for byte in result.packet {
    out = out.append(byte);
  }
  return Ok(Encoded { encoder: Encoder { state: result.st, channels: enc.channels }, packet: out });
}
```
