# Opus CELT packets

This module connects the existing CELT codec to standard Opus packet framing.
It encodes fullband CELT packets at 48 kHz with one or two channels and frames of 2.5, 5, 10, or 20 ms.

The decoder accepts CELT-only packets at every Opus bandwidth and all five Opus output sample rates.
It accepts every packet frame-count code, including multiple frames and padding.

Samples are interleaved `f32` values. The encoder requires finite values between -1 and 1.
Decoder output can exceed that interval. Apply clipping at the device conversion boundary.

This is partial Opus support. SILK, hybrid, Ogg containers, pre-skip, output gain, and end trimming are outside this module.
SILK and hybrid packets return `Unsupported`. Do not use this module as a decoder for arbitrary `.opus` files.

The encoder has a 120-sample lookahead at 48 kHz. A container must account for that delay and final padding.
After a decode error, reset the decoder before the next packet.

```fern
<<*>>=
import "std/audio/pcm";
import "std/opus/packet";
import "std/opus/celt";
import "std/opus/celt_encoder";
import "std/f32";

pub struct Decoder { state: celt.Decoder, sample_rate: i32, channels: i32 }

pub struct Encoder { state: celt_encoder.Encoder, channels: i32 }

pub struct Encoded { encoder: Encoder, packet: u8[] }

pub function decoder(sample_rate: i32, channels: i32): Result[Decoder, pcm.AudioError] {
  return match (celt.decoder(sample_rate, channels)) {
    Ok(state) => Ok(Decoder { state: state, sample_rate: sample_rate, channels: channels }),
    Err(_) => Err(pcm.Unsupported),
  };
}

pub function (own d: Decoder) reset(): Decoder {
  return Decoder { ...d, state: d.state.reset() };
}

pub function (own d: Decoder) decode(input: [u8]): (Decoder, Result[f32[], pcm.AudioError]) {
  if (input.len() > 61440) {
    return (d, Err(pcm.Limit));
  }
  let Ok(p) = packet.parse(input) else {
    return (d, Err(pcm.Header));
  };
  match (packet.mode(p.toc)) {
    Silk => {
      return (d, Err(pcm.Unsupported));
    },
    Hybrid => {
      return (d, Err(pcm.Unsupported));
    },
    Celt => {}
  }
  let size = packet.samples_per_frame(p.toc, d.sample_rate);
  let cc = if (packet.stereo(p.toc)) { 2 } else { 1 };
  let bandwidth = packet.bandwidth(p.toc);
  let end = if (bandwidth == packet.NARROWBAND) { 13 } else if (bandwidth == packet.WIDEBAND) { 17 } else if (bandwidth == packet.SUPERWIDEBAND) { 19 } else { 21 };
  let f = celt.Frame { frame_size: size, stream_channels: cc, start: 0, end: end };
  let Decoder { state, sample_rate, channels } = d;
  let samples: f32[] = [];
  for frame in p.frames {
    let (next, result) = state.decode(frame, f);
    state = next;
    let Ok(values) = result else {
      return (Decoder { state: state, sample_rate: sample_rate, channels: channels }, Err(pcm.Header));
    };
    for value in values {
      samples = samples.append(value);
    }
  }
  return (Decoder { state: state, sample_rate: sample_rate, channels: channels }, Ok(samples));
}

pub function encoder(channels: i32, bitrate: i32): Result[Encoder, pcm.AudioError] {
  if (channels != 1 && channels != 2 || bitrate < 6000 || bitrate > 510000) {
    return Err(pcm.Unsupported);
  }
  let state = celt_encoder.Encoder { ...celt_encoder.new_encoder(channels), bitrate: bitrate, vbr: true };
  return Ok(Encoder { state: state, channels: channels });
}

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
