# Native MP4 video packet index

This module indexes self-contained AVC and HEVC video tracks in a complete MP4 byte view.
It reuses the bounded ISO box, sample-table, clock, and data-reference readers of the audio library.
Packet ranges refer to media-data payloads in the caller's original view. The index does not copy packet payloads.

The index preserves decode order, decode times, composition times, durations, sample descriptions, and sync-sample flags.
Packet times use the track clock's ticks. Composition times can be negative.
Edit lists retain movie-duration and media-time units. The index does not apply edits to packet times.
The caller must apply edits for presentation on the movie timeline.

The index does not expose container color, HDR, or sample-group metadata.

`avc1`, `avc3`, `hvc1`, and `hev1` descriptions carry validated NAL configuration framing.
The configuration parser does not validate coded parameter-set syntax or establish decoder support.
This module does not decode compressed pixels. Fragmented media, encrypted descriptions, and external references return `Unsupported`.

Limits bound input bytes, boxes, tracks, packets, descriptions, edits, dimensions, and each packet.
The packet budget is cumulative across video tracks. The track budget includes non-video tracks.
These limits bound retained entries, not exact process memory.
The complete byte view uses signed 32-bit indices. This API does not stream a large file.

```fern
<<*>>=
// This module indexes bounded AVC and HEVC packets in a complete MP4 byte view.
import "std/video/frame";
import "std/video/nal";
import "std/video/mp4/timing";
import "std/audio/mp4/boxes";
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/mp4/config";
import "std/audio/pcm";
import "std/audio/wire";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub enum Codec { AVC, HEVC }

pub struct Limits { video: frame.Limits, max_input_bytes: i32, max_boxes: i32, max_tracks: i32, max_packets: i32, max_descriptions: i32, max_edits: i32 }

pub struct Description { codec: Codec, format: frame.Format, config: nal.Config }

pub struct Packet { offset: i64, bytes: i32, description: i32, decode_time: i64, presentation_time: i64, duration: u32, sync: boolean }

pub struct Track { id: u32, enabled: boolean, clock: time.Clock, descriptions: Description[], packets: Packet[], edits: time.Edit[] }

pub struct Index { movie: time.Clock, tracks: Track[], bytes: i64 }

struct Scan { budget: boxes.Budget, media: tables.Media[], limits: Limits, packets: i32, file_bytes: i64 }

pub function default_limits(): Limits {
  return Limits { video: frame.default_limits(), max_input_bytes: 268435456, max_boxes: 65536, max_tracks: 32, max_packets: 1048576, max_descriptions: 64, max_edits: 64 };
}

pub(package) function video_error(error: pcm.AudioError): frame.VideoError {
  return match (error) { pcm.Truncated => frame.Truncated, pcm.Unsupported => frame.Unsupported, pcm.Limit => frame.Limit, _ => frame.Header };
}

function audio_error(error: frame.VideoError): pcm.AudioError {
  return match (error) { frame.Truncated => pcm.Truncated, frame.Unsupported => pcm.Unsupported, frame.Limit => pcm.Limit, _ => pcm.Header };
}

function exclusive(values: [boxes.Box], first: string, second: string): Result[boxes.Box, pcm.AudioError] {
  let a = boxes.one(values, first)?;
  let b = boxes.one(values, second)?;
  return match ((a, b)) { (Some(value), None()) => Ok(value), (None(), Some(value)) => Ok(value), _ => Err(pcm.Header) };
}

function codec(kind: u32): Result[Codec, pcm.AudioError] {
  if (kind == boxes.tag("avc1") || kind == boxes.tag("avc3")) {
    return Ok(AVC);
  }
  if (kind == boxes.tag("hvc1") || kind == boxes.tag("hev1")) {
    return Ok(HEVC);
  }
  return Err(pcm.Unsupported);
}

function configuration(input: [u8], children: [boxes.Box], codec: Codec, limits: frame.Limits): Result[nal.Config, pcm.AudioError] {
  if let Some(_) = boxes.one(children, "sinf")? {
    return Err(pcm.Unsupported);
  }
  let box = boxes.required(children, if (codec == AVC) { "avcC" } else { "hvcC" })?;
  let value = if (codec == AVC) { nal.avc_config(input[box.data:box.end], limits) } else { nal.hevc_config(input[box.data:box.end], limits) };
  return match (value) { Ok(value) => Ok(value), Err(error) => Err(audio_error(error)) };
}

function description(input: [u8], entry: boxes.Box, budget: boxes.Budget, limits: frame.Limits): Result[(boxes.Budget, Description), pcm.AudioError] {
  let kind = codec(entry.kind)?;
  if (entry.end - entry.data < 78) {
    return Err(pcm.Truncated);
  }
  if (wire.be(input, entry.data + 6, 2) != 1u64) {
    return Err(pcm.Unsupported);
  }
  let format = frame.Format { width: wire.be(input, entry.data + 24, 2) as i32, height: wire.be(input, entry.data + 26, 2) as i32 };
  match (frame.pixel_count(format, limits)) {
    Err(error) => {
      return Err(audio_error(error));
    },
    _ => {}
  }
  let (next, children) = boxes.children(input, entry.data + 78, entry.end, budget)?;
  let value = configuration(input, children, kind, limits)?;
  return Ok((next, Description { codec: kind, format: format, config: value }));
}

function descriptions(input: [u8], box: boxes.Box, budget: boxes.Budget, limits: Limits): Result[(boxes.Budget, Description[]), pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let count = wire.be(input, box.data + 4, 4);
  if (count == 0u64 || count > limits.max_descriptions as u64) {
    return Err(pcm.Limit);
  }
  let (next, entries) = boxes.children(input, box.data + 8, box.end, budget)?;
  if (entries.len() != count as i32) {
    return Err(pcm.Header);
  }
  let output: Description[] = [];
  for entry in entries {
    let (after, value) = description(input, entry, next, limits.video)?;
    next = after;
    output = output.append(value);
  }
  return Ok((next, output));
}

function packet_sizes(input: [u8], children: [boxes.Box], scan: Scan): Result[i32[], pcm.AudioError] {
  let sizes = tables.sizes(input, exclusive(children, "stsz", "stz2")?, scan.limits.max_packets - scan.packets)?;
  for size in sizes {
    if (size > scan.limits.video.max_packet_bytes) {
      return Err(pcm.Limit);
    }
  }
  return Ok(sizes);
}

function reject_encryption(children: [boxes.Box]): Result[void, pcm.AudioError] {
  for name in ["senc", "saiz", "saio"] {
    if let Some(_) = boxes.one(children, name)? {
      return Err(pcm.Unsupported);
    }
  }
  return Ok(());
}

function raw_packets(input: [u8], children: [boxes.Box], scan: Scan, descriptions: i32): Result[tables.Packet[], pcm.AudioError] {
  reject_encryption(children)?;
  let sizes = packet_sizes(input, children, scan)?;
  let offsets = tables.offsets(input, exclusive(children, "stco", "co64")?, scan.limits.max_packets, scan.file_bytes)?;
  let chunks = tables.chunks(input, boxes.required(children, "stsc")?, scan.limits.max_packets)?;
  let times = tables.times(input, boxes.required(children, "stts")?, scan.limits.max_packets)?;
  return tables.packets(sizes, offsets, chunks, times, scan.media, descriptions);
}

function packets(input: [u8], children: [boxes.Box], scan: Scan, descriptions: i32): Result[Packet[], pcm.AudioError] {
  let raw = raw_packets(input, children, scan, descriptions)?;
  let composition = timing.composition(input, children, raw.len())?;
  let sync = timing.sync_samples(input, children, raw.len())?;
  let output: Packet[] = [];
  for i in 0..raw.len() {
    let packet = raw[i];
    let Some(pts) = packet.position +? composition[i] else {
      return Err(pcm.Limit);
    };
    output = output.append(Packet { offset: packet.offset, bytes: packet.bytes, description: packet.description, decode_time: packet.position, presentation_time: pts, duration: packet.duration, sync: sync[i] });
  }
  return Ok(output);
}

function sample_table(input: [u8], stbl: boxes.Box, scan: Scan): Result[(Scan, Description[], Packet[]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, stbl.data, stbl.end, scan.budget)?;
  let (b2, formats) = descriptions(input, boxes.required(children, "stsd")?, b1, scan.limits)?;
  let values = packets(input, children, Scan { ...scan, budget: b2 }, formats.len())?;
  return Ok((Scan { ...scan, budget: b2, packets: scan.packets + values.len() }, formats, values));
}

function track(input: [u8], box: boxes.Box, scan: Scan): Result[(Scan, Option[Track]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, box.data, box.end, scan.budget)?;
  let mdia = boxes.required(children, "mdia")?;
  let (b2, media) = boxes.children(input, mdia.data, mdia.end, b1)?;
  let hdlr = boxes.required(media, "hdlr")?;
  if (boxes.full(input, hdlr, 24)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (wire.be(input, hdlr.data + 8, 4) as u32 != boxes.tag("vide")) {
    return Ok((Scan { ...scan, budget: b2 }, None));
  }
  return video_track(input, children, media, Scan { ...scan, budget: b2 });
}

function video_track(input: [u8], children: [boxes.Box], media: [boxes.Box], scan: Scan): Result[(Scan, Option[Track]), pcm.AudioError] {
  let id = time.track_id(input, boxes.required(children, "tkhd")?)?;
  let clock = time.clock(input, boxes.required(media, "mdhd")?)?;
  let (next, formats, values) = track_samples(input, media, scan)?;
  let (b5, edits) = time.edits(input, children, next.budget, next.limits.max_edits)?;
  return Ok((Scan { ...next, budget: b5 }, Some(Track { id: id.id, enabled: id.enabled, clock: clock, descriptions: formats, packets: values, edits: edits })));
}

function track_samples(input: [u8], media: [boxes.Box], scan: Scan): Result[(Scan, Description[], Packet[]), pcm.AudioError] {
  let minf = boxes.required(media, "minf")?;
  let (b1, information) = boxes.children(input, minf.data, minf.end, scan.budget)?;
  let b2 = config.data_reference(input, information, b1)?;
  return sample_table(input, boxes.required(information, "stbl")?, Scan { ...scan, budget: b2 });
}

function media_boxes(top: [boxes.Box]): Result[tables.Media[], pcm.AudioError] {
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
  return Ok(media);
}

pub(package) function validate_limits(input: [u8], limits: Limits): Result[void, pcm.AudioError] {
  if (input.len() > limits.max_input_bytes || limits.max_input_bytes < 0) {
    return Err(pcm.Limit);
  }
  if (limits.max_boxes < 1 || limits.max_tracks < 1 || limits.max_packets < 1) {
    return Err(pcm.Limit);
  }
  if (limits.max_descriptions < 1 || limits.max_edits < 0) {
    return Err(pcm.Limit);
  }
  if (limits.video.max_pixels < 0 || limits.video.max_packet_bytes < 0) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

function add_track(input: [u8], box: boxes.Box, scan: Scan, own previous: Track[]): Result[(Scan, Track[]), pcm.AudioError] {
  let (next, value) = track(input, box, scan)?;
  if let Some(value) = value {
    for old in previous {
      if (old.id == value.id) {
        return Err(pcm.Header);
      }
    }
    return Ok((next, previous.append(value)));
  }
  return Ok((next, previous));
}

function headers(input: [u8], configured: Limits): Result[(Scan, time.Clock, boxes.Box[]), pcm.AudioError] {
  validate_limits(input, configured)?;
  let (budget, top) = boxes.children(input, 0, input.len(), boxes.Budget { boxes: 0, maximum: configured.max_boxes })?;
  file_type(input, top)?;
  let moov = boxes.required(top, "moov")?;
  let (next, children) = boxes.children(input, moov.data, moov.end, budget)?;
  if let Some(_) = boxes.one(children, "mvex")? {
    return Err(pcm.Unsupported);
  }
  return movie_header(input, top, children, next, configured);
}

function file_type(input: [u8], top: [boxes.Box]): Result[void, pcm.AudioError] {
  let ftyp = boxes.required(top, "ftyp")?;
  if (ftyp.end - ftyp.data < 8 || (ftyp.end - ftyp.data) % 4 != 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function movie_header(input: [u8], top: [boxes.Box], children: boxes.Box[], budget: boxes.Budget, configured: Limits): Result[(Scan, time.Clock, boxes.Box[]), pcm.AudioError] {
  let movie = time.clock(input, boxes.required(children, "mvhd")?)?;
  return Ok((Scan { budget: budget, media: media_boxes(top)?, limits: configured, packets: 0, file_bytes: input.len() as i64 }, movie, children));
}

function index(input: [u8], configured: Limits): Result[Index, pcm.AudioError] {
  let (scan, movie, children) = headers(input, configured)?;
  return collect_tracks(input, scan, movie, children);
}

function collect_tracks(input: [u8], scan: Scan, movie: time.Clock, children: [boxes.Box]): Result[Index, pcm.AudioError] {
  let tracks: Track[] = [];
  let count = 0;
  for box in children {
    if (box.kind == boxes.tag("trak")) {
      count += 1;
      if (count > scan.limits.max_tracks) {
        return Err(pcm.Limit);
      }
      let (after, output) = add_track(input, box, scan, tracks)?;
      scan = after;
      tracks = output;
    }
  }
  if (tracks.len() == 0) {
    return Err(pcm.Unsupported);
  }
  return Ok(Index { movie: movie, tracks: tracks, bytes: scan.file_bytes });
}

// The file scanner supplies one complete moov box and absolute mdat ranges.
// The nested box and packet readers remain the same as in the byte-view API.
pub(package) function inspect_movie(input: [u8], media: tables.Media[], file_bytes: i64, top_boxes: i32, configured: Limits): Result[Index, frame.VideoError] {
  return match (movie_index(input, media, file_bytes, top_boxes, configured)) { Ok(value) => Ok(value), Err(error) => Err(video_error(error)) };
}

function source_extent(input: [u8], media: [tables.Media], file_bytes: i64, top_boxes: i32, maximum: i32): Result[void, pcm.AudioError] {
  if (file_bytes < input.len() as i64 || media.len() == 0 || top_boxes < 1 || top_boxes > maximum || media.len() > top_boxes) {
    return Err(pcm.Header);
  }
  return media_extents(media, file_bytes);
}

function media_extents(media: [tables.Media], file_bytes: i64): Result[void, pcm.AudioError] {
  let end = 0i64;
  for range in media {
    if (range.start < end || range.end < range.start || range.end > file_bytes) {
      return Err(pcm.Header);
    }
    end = range.end;
  }
  return Ok(());
}

function metadata_children(input: [u8], budget: boxes.Budget): Result[(boxes.Budget, boxes.Box[]), pcm.AudioError] {
  let moov = boxes.read(input, 0, input.len())?;
  if (moov.kind != boxes.tag("moov") || moov.end != input.len()) {
    return Err(pcm.Header);
  }
  let (next, children) = boxes.children(input, moov.data, moov.end, budget)?;
  if let Some(_) = boxes.one(children, "mvex")? {
    return Err(pcm.Unsupported);
  }
  return Ok((next, children));
}

function movie_index(input: [u8], media: tables.Media[], file_bytes: i64, top_boxes: i32, configured: Limits): Result[Index, pcm.AudioError] {
  validate_limits(input, configured)?;
  source_extent(input, media, file_bytes, top_boxes, configured.max_boxes)?;
  let (budget, children) = metadata_children(input, boxes.Budget { boxes: top_boxes, maximum: configured.max_boxes })?;
  let movie = time.clock(input, boxes.required(children, "mvhd")?)?;
  return collect_tracks(input, Scan { budget: budget, media: media, limits: configured, packets: 0, file_bytes: file_bytes }, movie, children);
}

pub function inspect(input: [u8], limits: Limits): Result[Index, frame.VideoError] {
  return match (index(input, limits)) { Ok(value) => Ok(value), Err(error) => Err(video_error(error)) };
}
```
