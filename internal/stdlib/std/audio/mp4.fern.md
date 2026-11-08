# Native MP4 and M4A audio

This module indexes self-contained AAC tracks in a complete ISO base media byte view.
It validates packet ranges against media-data boxes before decode.
The AAC-LC packet decoder supplies PCM without a platform codec.

Track times and edit lists control encoder-delay and final-padding removal.
The whole-track API retains interleaved float PCM within explicit cumulative limits.
Fragmented media, encrypted entries, external references, and non-AAC tracks are unsupported for decode.

```fern
<<*>>=
import "std/audio/aac";
import "std/audio/aac/config" as aac_config;
import "std/audio/aac/dsp";
import "std/audio/mp4/boxes";
import "std/audio/mp4/config";
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Limits { audio: pcm.Limits, max_boxes: i32, max_tracks: i32, max_packets: i32, max_descriptions: i32, max_edits: i32 }

pub struct Track { id: u32, enabled: boolean, clock: time.Clock, configurations: aac_config.Config[], packets: tables.Packet[], edits: time.Edit[] }

pub struct Index { movie: time.Clock, tracks: Track[], bytes: i64 }

pub struct Decoded { sample_rate: i32, channels: i32, channel_config: i32, track_id: u32, frames: i64, samples: f32[] }

struct Scan { budget: boxes.Budget, media: tables.Media[], limits: Limits, packets: i32 }

struct Span { start: i64, end: i64, output: i32 }

struct Plan { frames: i32, spans: Span[] }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_boxes: 65536, max_tracks: 32, max_packets: 1048576, max_descriptions: 64, max_edits: 64 };
}

function exclusive(values: [boxes.Box], first: string, second: string): Result[boxes.Box, pcm.AudioError] {
  let a = boxes.one(values, first)?;
  let b = boxes.one(values, second)?;
  match ((a, b)) {
    (Some(value), None()) => {
      return Ok(value);
    },
    (None(), Some(value)) => {
      return Ok(value);
    },
    _ => {
      return Err(pcm.Header);
    }
  }
}

// Validate every composition row before accepting the zero-offset audio case.
// fern-lint: allow cyclomatic-complexity
function composition(input: [u8], values: [boxes.Box], maximum: i32): Result[void, pcm.AudioError] {
  let Some(box) = boxes.one(values, "ctts")? else {
    return Ok(());
  };
  let version = boxes.full(input, box, 8)?;
  if (version != 0 && version != 1) {
    return Err(pcm.Unsupported);
  }
  let count = wire.be(input, box.data + 4, 4);
  if (count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if ((box.end - box.data) as i64 != 8i64 + 8i64 * (count as i64)) {
    return Err(pcm.Header);
  }
  let total = 0u64;
  for i in 0..count as i32 {
    let entries = wire.be(input, box.data + 8 + 8 * i, 4);
    if (entries == 0u64) {
      return Err(pcm.Header);
    }
    total += entries;
    if (total > maximum as u64) {
      return Err(pcm.Header);
    }
    if (wire.be(input, box.data + 12 + 8 * i, 4) != 0u64) {
      return Err(pcm.Unsupported);
    }
  }
  if (total != maximum as u64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Check the dependent sample tables before constructing packet ranges.
// fern-lint: allow cyclomatic-complexity
function sample_table(input: [u8], stbl: boxes.Box, own scan: Scan): Result[(Scan, aac_config.Config[], tables.Packet[]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, stbl.data, stbl.end, scan.budget)?;
  let (b2, descriptions) = config.descriptions(input, boxes.required(children, "stsd")?, b1, scan.limits.max_descriptions, scan.limits.audio)?;
  let remaining = scan.limits.max_packets - scan.packets;
  let sizes = tables.sizes(input, exclusive(children, "stsz", "stz2")?, remaining)?;
  composition(input, children, sizes.len())?;
  let offsets = tables.offsets(input, exclusive(children, "stco", "co64")?, scan.limits.max_packets, input.len() as i64)?;
  let chunks = tables.chunks(input, boxes.required(children, "stsc")?, scan.limits.max_packets)?;
  let times = tables.times(input, boxes.required(children, "stts")?, scan.limits.max_packets)?;
  let packets = tables.packets(sizes, offsets, chunks, times, scan.media, descriptions.len())?;
  return Ok((Scan { ...scan, budget: b2, packets: scan.packets + packets.len() }, descriptions, packets));
}

// Follow the fixed audio-track hierarchy with checked reads at each level.
// fern-lint: allow cyclomatic-complexity
function track(input: [u8], box: boxes.Box, own scan: Scan): Result[(Scan, Option[Track]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, box.data, box.end, scan.budget)?;
  let mdia = boxes.required(children, "mdia")?;
  let (b2, media) = boxes.children(input, mdia.data, mdia.end, b1)?;
  let hdlr = boxes.required(media, "hdlr")?;
  if (boxes.full(input, hdlr, 24)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (wire.be(input, hdlr.data + 8, 4) as u32 != boxes.tag("soun")) {
    return Ok((Scan { ...scan, budget: b2 }, None));
  }
  let id = time.track_id(input, boxes.required(children, "tkhd")?)?;
  let clock = time.clock(input, boxes.required(media, "mdhd")?)?;
  let minf = boxes.required(media, "minf")?;
  let (b3, information) = boxes.children(input, minf.data, minf.end, b2)?;
  let b4 = config.data_reference(input, information, b3)?;
  let (next, configurations, packets) = sample_table(input, boxes.required(information, "stbl")?, Scan { ...scan, budget: b4 })?;
  let (b5, edits) = time.edits(input, children, next.budget, next.limits.max_edits)?;
  let last = packets[packets.len() - 1];
  if (clock.duration != 0i64 && clock.duration != last.position + last.duration as i64) {
    return Err(pcm.Header);
  }
  let value = Track { id: id.id, enabled: id.enabled, clock: clock, configurations: configurations, packets: packets, edits: edits };
  return Ok((Scan { ...next, budget: b5 }, Some(value)));
}

function file_type(input: [u8], top: [boxes.Box]): Result[void, pcm.AudioError] {
  let box = boxes.required(top, "ftyp")?;
  if (box.end - box.data < 8 || (box.end - box.data) % 4 != 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Apply shared budgets while selecting media boxes and complete audio tracks.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_boxes < 1 || limits.max_tracks < 1 || limits.max_packets < 1 || limits.max_descriptions < 1 || limits.max_edits < 0) {
    return Err(pcm.Limit);
  }
  let (budget, top) = boxes.children(input, 0, input.len(), boxes.Budget { boxes: 0, maximum: limits.max_boxes })?;
  file_type(input, top)?;
  let moov = boxes.required(top, "moov")?;
  let (b1, children) = boxes.children(input, moov.data, moov.end, budget)?;
  if let Some(_) = boxes.one(children, "mvex")? {
    return Err(pcm.Unsupported);
  }
  let media: tables.Media[] = [];
  for box in top {
    if (box.kind == boxes.tag("moof")) {
      return Err(pcm.Unsupported);
    }
    if (box.kind == boxes.tag("mdat")) {
      media = media.append(tables.Media { start: box.data as i64, end: box.end as i64 });
    }
  }
  if (media.len() == 0) {
    return Err(pcm.Header);
  }
  let movie = time.clock(input, boxes.required(children, "mvhd")?)?;
  let scan = Scan { budget: b1, media: media, limits: limits, packets: 0 };
  let tracks: Track[] = [];
  let count = 0;
  for box in children {
    if (box.kind == boxes.tag("trak")) {
      count += 1;
      if (count > limits.max_tracks) {
        return Err(pcm.Limit);
      }
      let (next, value) = track(input, box, scan)?;
      scan = next;
      if let Some(value) = value {
        for previous in tracks {
          if (previous.id == value.id) {
            return Err(pcm.Header);
          }
        }
        tracks = tracks.append(value);
      }
    }
  }
  if (tracks.len() == 0) {
    return Err(pcm.Unsupported);
  }
  return Ok(Index { movie: movie, tracks: tracks, bytes: input.len() as i64 });
}

function packet_config(track: Track): Result[aac_config.Config, pcm.AudioError] {
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
function render(input: [u8], track: Track, configuration: aac_config.Config, plan: Plan, limits: pcm.Limits): Result[f32[], pcm.AudioError] {
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

function add_span(own plan: Plan, start: i64, end: i64, channels: i32, limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  if (start < 0i64 || end < start) {
    return Err(pcm.Header);
  }
  let length = end - start;
  let maximum = limits.max_samples / channels;
  if (length > (maximum - plan.frames) as i64) {
    return Err(pcm.Limit);
  }
  let span = Span { start: start, end: end, output: plan.frames * channels };
  return Ok(Plan { frames: plan.frames + length as i32, spans: plan.spans.append(span) });
}

// Resolve media edits and empty edits before allocating presentation PCM.
// fern-lint: allow cyclomatic-complexity
function presentation(track: Track, movie: time.Clock, configuration: aac_config.Config, limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  let last = track.packets[track.packets.len() - 1];
  let media_frames = time.nearest(last.position + last.duration as i64, configuration.sample_rate as u32, track.clock.scale)?;
  let decoded_frames = track.packets.len() as i64 * 1024i64;
  let plan = Plan { frames: 0, spans: [] };
  if (track.edits.len() == 0) {
    if (media_frames > decoded_frames) {
      return Err(pcm.Header);
    }
    return add_span(plan, 0i64, media_frames, configuration.channels, limits);
  }
  let elapsed = 0i64;
  for edit in track.edits {
    let before = time.nearest(elapsed, configuration.sample_rate as u32, movie.scale)?;
    if (edit.duration < 0i64) {
      return Err(pcm.Header);
    }
    let Some(next_time) = elapsed +? edit.duration else {
      return Err(pcm.Limit);
    };
    elapsed = next_time;
    let after = time.nearest(elapsed, configuration.sample_rate as u32, movie.scale)?;
    let frames = after - before;
    if (edit.media_time == -1i64) {
      if (frames > (limits.max_samples / configuration.channels - plan.frames) as i64) {
        return Err(pcm.Limit);
      }
      plan = Plan { ...plan, frames: plan.frames + frames as i32 };
    } else {
      let start = time.nearest(edit.media_time, configuration.sample_rate as u32, track.clock.scale)?;
      let Some(stop) = start +? frames else {
        return Err(pcm.Limit);
      };
      let end = if (stop > media_frames) { media_frames } else { stop };
      if (end > decoded_frames) {
        return Err(pcm.Header);
      }
      plan = add_span(plan, start, end, configuration.channels, limits)?;
    }
  }
  return Ok(plan);
}

// Recheck retained-index bounds before planning and decoding the selected track.
// fern-lint: allow cyclomatic-complexity
pub function decode_track(input: [u8], index: Index, track_number: i32, limits: Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (index.bytes != input.len() as i64 || track_number < 0 || track_number >= index.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = index.tracks[track_number];
  if (track.packets.len() > limits.max_packets || track.configurations.len() > limits.max_descriptions || track.edits.len() > limits.max_edits) {
    return Err(pcm.Limit);
  }
  let configuration = packet_config(track)?;
  let plan = presentation(track, index.movie, configuration, limits.audio)?;
  let samples = render(input, track, configuration, plan, limits.audio)?;
  return Ok(Decoded { sample_rate: configuration.sample_rate, channels: configuration.channels, channel_config: configuration.channel_config, track_id: track.id, frames: (samples.len() / configuration.channels) as i64, samples: samples });
}

pub function decode(input: [u8], limits: Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  for i in 0..index.tracks.len() {
    if (index.tracks[i].enabled) {
      return decode_track(input, index, i, limits);
    }
  }
  return Err(pcm.Unsupported);
}
```
