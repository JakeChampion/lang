# Native ALAC in MP4 and M4A

This module decodes self-contained ALAC tracks from a complete ISO media byte view.
It shares MP4 packet tables, clocks, edit lists, and input budgets with AAC playback.
PCM uses signed, right-aligned integers and an explicit WAVE speaker mask.
No float conversion or quantization occurs.

The decoder reuses packet scratch and a bounded PCM buffer across the track.
Normal media edits remove priming and padding. Empty edits insert silence.
Fragmented media, encryption, external references, and configuration changes are unsupported.
The ALAC packet decoder retains its declared format and predictor limits.
A retained index applies to the same immutable input bytes used during inspection.

```fern
<<*>>=
// Decode ALAC tracks from bounded ISO media byte views.
import "std/audio/alac" as codec;
import "std/audio/alac/config";
import "std/audio/alac/layout";
import "std/audio/mp4/boxes";
import "std/audio/mp4/entries";
import "std/audio/mp4/index" as container;
import "std/audio/mp4/presentation";
import "std/audio/mp4/time";
import "std/audio/pcm";
import "std/audio/wire";
pub use "std/audio/mp4/index".{Limits};

struct Configuration { raw: u8[], info: config.Info, mask: u32 }

struct Track { source: container.Track, configuration: Configuration }

pub opaque struct Index { tracks: Track[], movie: time.Clock, bytes: i64 }

pub struct Info { id: u32, enabled: boolean, format: pcm.Format, channel_mask: u32, frame_length: i32, packets: i32 }

pub struct Decoded { format: pcm.Format, channel_mask: u32, track_id: u32, frames: i64, samples: i32[] }

pub function default_limits(): container.Limits {
  return container.default_limits();
}

function equal(first: Configuration, second: Configuration): boolean {
  if (first.mask != second.mask || first.raw.len() != second.raw.len()) {
    return false;
  }
  for i in 0..first.raw.len() {
    if (first.raw[i] != second.raw[i]) {
      return false;
    }
  }
  return true;
}

function payload(input: [u8], children: [boxes.Box], budget: boxes.Budget): Result[(boxes.Budget, boxes.Box[], boxes.Box), pcm.AudioError] {
  if let Some(value) = boxes.one(children, "alac")? {
    return Ok((budget, [], value));
  }
  let wave = boxes.required(children, "wave")?;
  let (next, nested) = boxes.children(input, wave.data, wave.end, budget)?;
  return Ok((next, nested, boxes.required(nested, "alac")?));
}

function channel_layout(input: [u8], children: [boxes.Box], nested: [boxes.Box], info: config.Info): Result[u32, pcm.AudioError] {
  let outer = boxes.one(children, "chan")?;
  let inner = boxes.one(nested, "chan")?;
  let channels = match ((outer, inner)) {
    (Some(value), None()) => Some(value),
    (None(), Some(value)) => Some(value),
    (None(), None()) => None,
    _ => { return Err(pcm.Header); },
  };
  let mask = info.channel_mask;
  if let Some(value) = channels {
    if (boxes.full(input, value, 16)? != 0) {
      return Err(pcm.Unsupported);
    }
    mask = layout.read(input[value.data + 4:value.end], info)?;
  }
  return Ok(mask);
}

function read_configuration(input: [u8], entry: boxes.Box, budget: boxes.Budget, limits: pcm.Limits): Result[(boxes.Budget, Configuration), pcm.AudioError] {
  let start = entries.header(input, entry, boxes.tag("alac"))?;
  let (b1, children) = boxes.children(input, start, entry.end, budget)?;
  let (b2, nested, box) = payload(input, children, b1)?;
  if (boxes.full(input, box, 28)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (box.end - box.data != 28) {
    return Err(pcm.Header);
  }
  let raw: u8[] = [];
  for byte in input[box.data + 4:box.end] {
    raw = raw.append(byte);
  }
  let info = config.read(raw, limits)?;
  let mask = channel_layout(input, children, nested, info)?;
  return Ok((b2, Configuration { raw: raw, info: info, mask: mask }));
}

pub function inspect(input: [u8], limits: container.Limits): Result[Index, pcm.AudioError] {
  let raw = container.inspect(input, limits, boxes.tag("alac"))?;
  let budget = raw.budget;
  let tracks: Track[] = [];
  for source in raw.tracks {
    let first: Option[Configuration] = None;
    for entry in source.configurations {
      let (next, current) = read_configuration(input, entry, budget, limits.audio)?;
      budget = next;
      if let Some(value) = first {
        if (!equal(value, current)) {
          return Err(pcm.Unsupported);
        }
      } else {
        first = Some(current);
      }
    }
    let Some(configuration) = first else {
      return Err(pcm.Header);
    };
    tracks = tracks.append(Track { source: source, configuration: configuration });
  }
  return Ok(Index { tracks: tracks, movie: raw.movie, bytes: raw.bytes });
}

pub function (value: Index) track_count(): i32 {
  return value.tracks.len();
}

pub function (value: Index) information(track_number: i32): Result[Info, pcm.AudioError] {
  if (track_number < 0 || track_number >= value.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = value.tracks[track_number];
  let info = track.configuration.info;
  return Ok(Info { id: track.source.id, enabled: track.source.enabled, format: info.format, channel_mask: track.configuration.mask, frame_length: info.frame_length, packets: track.source.packets.len() });
}

// Validate packet timing and copy its intersections into the exact presentation spans.
// fern-lint: allow cyclomatic-complexity
function render(input: [u8], track: Track, plan: presentation.Plan, limits: pcm.Limits): Result[i32[], pcm.AudioError] {
  let info = track.configuration.info;
  let decoder = codec.create(track.configuration.raw, limits)?;
  let buffer = pcm.zeros(pcm.sample_count(info.frame_length, info.format.channels, limits)?);
  let output = pcm.zeros(pcm.sample_count(plan.frames, info.format.channels, limits)?);
  let decoded = 0i64;
  for packet_number in 0..track.source.packets.len() {
    let packet = track.source.packets[packet_number];
    if (packet.description < 0 || packet.description >= track.source.configurations.len() || packet.bytes < 1 || packet.offset < 0i64 || packet.offset > input.len() as i64 || packet.bytes as i64 > input.len() as i64 - packet.offset) {
      return Err(pcm.Header);
    }
    let position = time.nearest(packet.position, info.format.sample_rate as u32, track.source.clock.scale)?;
    if (position < decoded - 1i64 || position > decoded + 1i64) {
      return Err(pcm.Unsupported);
    }
    let at = packet.offset as i32;
    let (next, frame) = decoder.decode_packet_into(input[at:at + packet.bytes], buffer, limits)?;
    decoder = next;
    buffer = layout.reorder(frame.samples, info.format, track.configuration.mask, frame.frames)?;
    let end = decoded + frame.frames as i64;
    for span in plan.spans {
      let first = if (span.start > decoded) { span.start } else { decoded };
      let last = if (span.end < end) { span.end } else { end };
      if (last > first) {
        let from = ((first - decoded) * (info.format.channels as i64)) as i32;
        let to = span.output + ((first - span.start) * (info.format.channels as i64)) as i32;
        let count = ((last - first) * (info.format.channels as i64)) as i32;
        for i in 0..count {
          output = output.with(to + i, buffer[from + i]);
        }
      }
    }
    decoded = end;
  }
  for span in plan.spans {
    if (span.end > decoded) {
      return Err(pcm.Header);
    }
  }
  let last = track.source.packets[track.source.packets.len() - 1];
  let declared = time.nearest(last.position + last.duration as i64, info.format.sample_rate as u32, track.source.clock.scale)?;
  if (declared > decoded || decoded - declared >= info.frame_length as i64) {
    return Err(pcm.Header);
  }
  return Ok(output);
}

function check_limits(track: container.Track, tracks: i32, limits: container.Limits): Result[void, pcm.AudioError] {
  if (limits.max_boxes < 1 || limits.max_tracks < 1 || limits.max_packets < 1 || limits.max_descriptions < 1 || limits.max_edits < 0) {
    return Err(pcm.Limit);
  }
  if (tracks > limits.max_tracks || track.packets.len() > limits.max_packets || track.configurations.len() > limits.max_descriptions || track.edits.len() > limits.max_edits) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

pub function decode_track(input: [u8], index: Index, track_number: i32, limits: container.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (index.bytes != input.len() as i64 || track_number < 0 || track_number >= index.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = index.tracks[track_number];
  check_limits(track.source, index.tracks.len(), limits)?;
  let info = track.configuration.info;
  let plan = presentation.create(track.source.packets, track.source.edits, track.source.clock, index.movie, info.format.sample_rate, info.format.channels, track.source.packets.len() as i64 * (info.frame_length as i64), limits.audio)?;
  let samples = render(input, track, plan, limits.audio)?;
  return Ok(Decoded { format: info.format, channel_mask: track.configuration.mask, track_id: track.source.id, frames: plan.frames as i64, samples: samples });
}

pub function decode(input: [u8], limits: container.Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  for i in 0..index.tracks.len() {
    if (index.tracks[i].source.enabled) {
      return decode_track(input, index, i, limits);
    }
  }
  return Err(pcm.Unsupported);
}
```
