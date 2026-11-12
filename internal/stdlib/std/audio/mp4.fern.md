# Native MP4 and M4A audio

This module indexes self-contained AAC tracks in a complete ISO base media byte view.
It validates packet ranges against media-data boxes before decode.
The AAC-LC packet decoder supplies PCM without a platform codec.

Track times and edit lists control encoder-delay and final-padding removal.
The whole-track API retains interleaved float PCM within explicit cumulative limits.
`decode_alac` supplies exact integer PCM for ALAC tracks through the shared container parser.
Fragmented media, encrypted entries, and external references are unsupported.

```fern
<<*>>=
// Index and decode the AAC tracks of a complete MP4 or M4A byte view.

import "std/audio/aac";
import "std/audio/mp4/alac" as lossless;
import "std/audio/aac/config" as aac_config;
import "std/audio/aac/dsp";
import "std/audio/mp4/index" as container;
import "std/audio/mp4/boxes";
import "std/audio/mp4/presentation";
import "std/audio/mp4/config";
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/pcm";
pub use "std/audio/mp4/index".{Limits};

pub struct Track { id: u32, enabled: boolean, clock: time.Clock, configurations: aac_config.Config[], packets: tables.Packet[], edits: time.Edit[] }

pub struct Index { movie: time.Clock, tracks: Track[], bytes: i64 }

pub struct Decoded { sample_rate: i32, channels: i32, channel_config: i32, track_id: u32, frames: i64, samples: f32[] }

pub function default_limits(): container.Limits {
  return container.default_limits();
}

pub function inspect(input: [u8], limits: container.Limits): Result[Index, pcm.AudioError] {
  return configured(input, container.inspect(input, limits, boxes.tag("mp4a"))?, limits);
}

pub(package) function inspect_movie(input: [u8], media: tables.Media[], file_bytes: i64, top_boxes: i32, limits: container.Limits): Result[Index, pcm.AudioError] {
  return configured(input, container.inspect_movie(input, media, file_bytes, top_boxes, limits, boxes.tag("mp4a"))?, limits);
}

function configured(input: [u8], raw: container.Index, limits: container.Limits): Result[Index, pcm.AudioError] {
  let budget = raw.budget;
  let tracks: Track[] = [];
  for value in raw.tracks {
    let configurations: aac_config.Config[] = [];
    for entry in value.configurations {
      let (next, configuration) = config.sample_entry(input, entry, budget, limits.audio)?;
      budget = next;
      configurations = configurations.append(configuration);
    }
    tracks = tracks.append(Track { id: value.id, enabled: value.enabled, clock: value.clock, configurations: configurations, packets: value.packets, edits: value.edits });
  }
  return Ok(Index { movie: raw.movie, tracks: tracks, bytes: raw.bytes });
}

pub(package) function packet_config(track: Track): Result[aac_config.Config, pcm.AudioError] {
  if (track.packets.len() == 0 || track.configurations.len() == 0) {
    return Err(pcm.Header);
  }
  let first = track.configurations[0];
  let canonical = aac_config.create(first.object_type, first.sample_rate, first.channel_config)?;
  if (canonical != first) {
    return Err(pcm.Header);
  }
  for current in track.configurations {
    if (current != first) {
      return Err(pcm.Unsupported);
    }
  }
  return Ok(first);
}

// Decode each packet once and copy only its intersections with presentation spans.
// fern-lint: allow cyclomatic-complexity
function render(input: [u8], track: Track, configuration: aac_config.Config, plan: presentation.Plan, limits: pcm.Limits): Result[f32[], pcm.AudioError] {
  let decoder = aac.create(configuration, limits)?;
  let count = pcm.sample_count(1024, configuration.channels, limits)?;
  let buffer = dsp.zeros(count);
  let samples = dsp.zeros(pcm.sample_count(plan.frames, configuration.channels, limits)?);
  for packet_no in 0..track.packets.len() {
    let packet = track.packets[packet_no];
    if (packet.description < 0 || packet.description >= track.configurations.len()) {
      return Err(pcm.Header);
    }
    if (packet.offset < 0i64 || packet.bytes < 1 || packet.offset > input.len() as i64 || packet.bytes as i64 > input.len() as i64 - packet.offset) {
      return Err(pcm.Header);
    }
    let first = packet_no as i64 * 1024i64;
    let position = time.rescale(packet.position, configuration.sample_rate as u32, track.clock.scale)?;
    if (position < first - 1i64 || position > first + 1i64) {
      return Err(pcm.Unsupported);
    }
    let at = packet.offset as i32;
    let (next, frame) = decoder.decode_packet_into(input[at:at + packet.bytes], buffer, limits)?;
    decoder = next;
    buffer = frame.samples;
    for span in plan.spans {
      let start = if (span.start > first) { span.start } else { first };
      let end = if (span.end < first + 1024i64) { span.end } else { first + 1024i64 };
      if (end > start) {
        let from = ((start - first) * (configuration.channels as i64)) as i32;
        let to = span.output + ((start - span.start) * (configuration.channels as i64)) as i32;
        let length = ((end - start) * (configuration.channels as i64)) as i32;
        for i in 0..length {
          samples = samples.with(to + i, buffer[from + i]);
        }
      }
    }
  }
  return Ok(samples);
}

// Recheck retained-index bounds before planning and decoding the selected track.
// fern-lint: allow cyclomatic-complexity
pub function decode_track(input: [u8], index: Index, track_number: i32, limits: container.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (index.bytes != input.len() as i64 || track_number < 0 || track_number >= index.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = index.tracks[track_number];
  if (track.packets.len() > limits.max_packets || track.configurations.len() > limits.max_descriptions || track.edits.len() > limits.max_edits) {
    return Err(pcm.Limit);
  }
  let configuration = packet_config(track)?;
  let plan = presentation.create(track.packets, track.edits, track.clock, index.movie, configuration.sample_rate, configuration.channels, track.packets.len() as i64 * 1024i64, limits.audio)?;
  let samples = render(input, track, configuration, plan, limits.audio)?;
  return Ok(Decoded { sample_rate: configuration.sample_rate, channels: configuration.channels, channel_config: configuration.channel_config, track_id: track.id, frames: (samples.len() / configuration.channels) as i64, samples: samples });
}

pub function decode(input: [u8], limits: container.Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  for i in 0..index.tracks.len() {
    if (index.tracks[i].enabled) {
      return decode_track(input, index, i, limits);
    }
  }
  return Err(pcm.Unsupported);
}

pub function decode_alac(input: [u8], limits: container.Limits): Result[lossless.Decoded, pcm.AudioError] {
  return lossless.decode(input, limits);
}
```
