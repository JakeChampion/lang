# Incremental Ogg Vorbis playback

This module decodes Ogg Vorbis to interleaved float PCM in WAVE speaker order.
It selects the first Vorbis logical stream and supports sequential chains.
The shared Ogg reader validates every logical stream, including ignored streams.

Call `feed` with partial input and continue with the unconsumed bytes.
Each call returns the next decoder, the consumed byte count, and an optional event.
An event can consume zero bytes. Each sample event contains at most one audio packet.
After the final bytes, drain `feed([])` until it returns no event, then call `finish`.

The decoder checks header placement and applies initial and final granule boundaries.
Positions count playable frames from the start of each logical stream.
Granules retain the corresponding absolute sample timestamps.
Packets that produce no playable samples return empty sample events.

Storage holds one completed page, bounded Ogg partial packets, and codec state.
Limits apply per packet. Complete-file output budgets belong to the caller.
Errors consume the decoder. Create a new decoder after an error.

`scan` follows the same input protocol and validates the same container and setup boundaries.
It returns header locations and packet sample ranges without a transform or PCM allocation.
Setup validation temporarily uses the configured codebook budget, then retains only packet modes.
The scanner checks complete mode headers. A decoder read validates floor and residue payloads.

Packet ranges include initial and final trim. Empty ranges retain monotone sample positions.
This interface supplies `std/audio/vorbisfile/seek` with bounded index records.

```fern
<<*>>=
// Incremental Ogg Vorbis playback with bounded packet storage.

import "std/audio/ogg";
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/vorbis";
import "std/audio/vorbis/headers";
import "std/audio/vorbis/types";
import "std/audio/vorbisfile/timing";

pub struct Limits { ogg: ogg.Limits, codec: types.Limits, max_links: i32 }

pub struct Info { index: i32, serial: u32, audio: types.Info }

pub struct Metadata { info: Info, tags: headers.Tags }

pub struct Audio { info: Info, samples: f32[], frames: i32, position: i64, granule: i64, packet_offset: i64, page_offset: i64 }

pub struct Summary { info: Info, frames: i64, initial_granule: i64 }

pub enum Event { Begin(Info), Tags(Metadata), Samples(Audio), End(Summary) }

pub struct Location { page_offset: i64, offset: i64, bytes: i32 }

pub struct Prepared { info: Info, identification: Location, setup: Location }

pub struct Indexed { info: Info, location: Location, raw_frames: i32, start: i32, frames: i32, position: i64, granule: i64 }

pub enum ScanEvent { ScanBegin(Info), ScanTags(Metadata), ScanSetup(Prepared), ScanPacket(Indexed), ScanEnd(Summary) }

enum CoreEvent { Public(Event), Configuration(Prepared), Packet(Indexed) }

struct Link { info: Info, identification: u8[], identification_location: Location, codec: Option[vorbis.Decoder], layout: Option[vorbis.Layout], stage: i32, time: timing.Timing, previous: i32 }

struct Control { limits: Limits, links: i32 }

struct Plan { frames: i64, first: i32, last: i32, packets: i32 }

pub opaque struct Decoder { control: Control, container: ogg.Decoder, link: Option[Link], queue: Option[ogg.Page], index: i32 }

pub opaque struct Scan { decoder: Decoder }

function location(packet: ogg.Packet): Location {
  return Location { page_offset: packet.page_offset, offset: packet.offset, bytes: packet.bytes.len() };
}

function block(link: Link, input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  if let Some(codec) = link.codec {
    return codec.block_size(input, limits);
  }
  if let Some(layout) = link.layout {
    return layout.block_size(input, limits);
  }
  return Err(pcm.Header);
}

pub function scan(limits: Limits): Result[Scan, pcm.AudioError] {
  return Ok(Scan { decoder: decoder(limits)? });
}

pub function (own value: Scan) feed(input: [u8]): Result[(Scan, i32, Option[ScanEvent]), pcm.AudioError] {
  let Scan { decoder } = value;
  let (next, used, event) = feed_core(decoder, input, true)?;
  let answer = match (event) {
    None => None,
    Some(Public(Begin(info))) => Some(ScanBegin(info)),
    Some(Public(Tags(tags))) => Some(ScanTags(tags)),
    Some(Public(End(summary))) => Some(ScanEnd(summary)),
    Some(Configuration(config)) => Some(ScanSetup(config)),
    Some(Packet(packet)) => Some(ScanPacket(packet)),
    _ => { return Err(pcm.Header); },
  };
  return Ok((Scan { decoder: next }, used, answer));
}

pub function (value: Scan) finish(): Result[void, pcm.AudioError] {
  return value.decoder.finish();
}

pub function (own value: Decoder) feed(input: [u8]): Result[(Decoder, i32, Option[Event]), pcm.AudioError] {
  let (next, used, event) = feed_core(value, input, false)?;
  let answer = match (event) { None => None, Some(Public(value)) => Some(value), _ => { return Err(pcm.Header); } };
  return Ok((next, used, answer));
}

pub function default_limits(): Limits {
  return Limits { ogg: ogg.default_limits(), codec: types.default_limits(), max_links: 4096 };
}

pub function decoder(limits: Limits): Result[Decoder, pcm.AudioError] {
  if (limits.max_links < 1 || !types.valid(limits.codec)) {
    return Err(pcm.Limit);
  }
  return Ok(Decoder { control: Control { limits: limits, links: 0 }, container: ogg.decoder(limits.ogg)?, link: None, queue: None, index: 0 });
}

function incomplete(container: ogg.Decoder, serial: u32): boolean {
  for stream in container.streams {
    if (stream.serial == serial) {
      return stream.incomplete;
    }
  }
  return false;
}

// Identification occupies its complete BOS page before the remaining headers.
// fern-lint: allow cyclomatic-complexity
function begin(control: Control, own container: ogg.Decoder, page: ogg.Page): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  if (!page.bos || page.packets.len() == 0 || !wire.tag(page.packets[0].bytes, 0, "\x01vorbis")) {
    return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, None));
  }
  if (page.eos || page.continued || page.granule != 0i64 || page.packets.len() != 1 || incomplete(container, page.serial)) {
    return Err(pcm.Header);
  }
  if (control.links >= control.limits.max_links) {
    return Err(pcm.Limit);
  }
  let info = Info { index: control.links, serial: page.serial, audio: headers.identification(page.packets[0].bytes, control.limits.codec)? };
  let identification: u8[] = [];
  for byte in page.packets[0].bytes {
    identification = identification.append(byte);
  }
  let link = Link { info: info, identification: identification, identification_location: location(page.packets[0]), codec: None, layout: None, stage: 0, time: timing.create(), previous: 0 };
  return Ok((Decoder { control: Control { ...control, links: control.links + 1 }, container: container, link: Some(link), queue: None, index: 0 }, Some(Public(Begin(info)))));
}

function duration(link: Link, packets: [ogg.Packet], limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  let previous = link.previous;
  let frames = 0i64;
  let first = 0;
  let last = 0;
  for packet in packets {
    let current = block(link, packet.bytes, limits)?;
    let count = if (previous == 0) { 0 } else { (previous + current) / 4 };
    let _ = pcm.sample_count(count, link.info.audio.channels, limits)?;
    frames += count as i64;
    if (first == 0) {
      first = count;
    }
    last = count;
    previous = current;
  }
  return Ok(Plan { frames: frames, first: first, last: last, packets: packets.len() });
}

// Header and audio pages have different granule and packet-boundary rules.
// fern-lint: allow cyclomatic-complexity
function selected(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  if (link.stage < 2) {
    if (page.eos || page.granule != 0i64 && !(page.packets.len() == 0 && page.granule == -1i64)) {
      return Err(pcm.Header);
    }
  } else if (page.packets.len() == 0) {
    if (page.granule != -1i64) {
      return Err(pcm.Header);
    }
  } else {
    let plan = duration(link, page.packets, control.limits.codec.audio)?;
    let time = boundaries(link.time, page, plan)?;
    link = Link { ...link, time: time };
  }
  return Ok((Decoder { control: control, container: container, link: Some(link), queue: Some(page), index: 0 }, None));
}

// Nonzero origins flush the second packet; final trimming stays within the last packet.
// fern-lint: allow cyclomatic-complexity
function boundaries(previous: timing.Timing, page: ogg.Page, plan: Plan): Result[timing.Timing, pcm.AudioError] {
  let next = timing.page(previous, page.granule, plan.frames, page.eos)?;
  if (!previous.seen && !page.eos && (next.start != 0i64 || next.initial != 0i64)) {
    if (plan.packets > 2 || next.start > plan.first as i64) {
      return Err(pcm.Header);
    }
  }
  if (previous.seen && previous.decoded == 0i64 && previous.initial != 0i64 && plan.packets != 1) {
    return Err(pcm.Header);
  }
  if let Some(end) = next.trim_at {
    if (next.page_end - end > plan.last as i64) {
      return Err(pcm.Header);
    }
  }
  return Ok(next);
}

function accept(own decoder: Decoder, own page: ogg.Page): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = decoder;
  return match (link) {
    None => begin(control, container, page),
    Some(value) => if (value.info.serial != page.serial) {
      Ok((Decoder { control: control, container: container, link: Some(value), queue: None, index: 0 }, None))
    } else {
      selected(control, container, value, page)
    },
  };
}

function clipped(own samples: f32[], range: timing.Range, channels: i32): f32[] {
  let first = range.start * channels;
  let end = range.end * channels;
  if (first == 0 && end == samples.len()) {
    return samples;
  }
  let output: f32[] = [];
  for i in first..end {
    output = output.append(samples[i]);
  }
  return output;
}

// Read the comment and setup packets in order, then require a fresh audio page.
// fern-lint: allow cyclomatic-complexity
function header(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32, scanning: boolean): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  let event: Option[CoreEvent] = None;
  if (link.stage == 0) {
    let tags = headers.comments(page.packets[index].bytes, control.limits.codec)?;
    event = Some(Public(Tags(Metadata { info: link.info, tags: tags })));
    link = Link { ...link, stage: 1 };
  } else {
    if (index + 1 != page.packets.len() || incomplete(container, page.serial)) {
      return Err(pcm.Header);
    }
    if (scanning) {
      let layout = vorbis.layout(link.identification, page.packets[index].bytes, control.limits.codec)?;
      event = Some(Configuration(Prepared { info: link.info, identification: link.identification_location, setup: location(page.packets[index]) }));
      link = Link { ...link, identification: [], stage: 2, layout: Some(layout) };
    } else {
      let codec = vorbis.create(link.identification, page.packets[index].bytes, control.limits.codec)?;
      link = Link { ...link, identification: [], stage: 2, codec: Some(codec) };
    }
  }
  return Ok((Decoder { control: control, container: container, link: Some(link), queue: Some(page), index: index + 1 }, event));
}

function audio(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  let Link { info, identification, identification_location, codec, layout, stage, time, previous } = link;
  let Some(current) = codec else {
    return Err(pcm.Header);
  };
  let packet = page.packets[index];
  let block = current.block_size(packet.bytes, control.limits.codec.audio)?;
  let (next_codec, frame) = current.decode_packet(packet.bytes, control.limits.codec.audio)?;
  let (next_time, range) = timing.advance(time, frame.frames)?;
  let value = Audio { info: info, samples: clipped(frame.samples, range, info.audio.channels), frames: range.end - range.start, position: range.position, granule: range.granule, packet_offset: packet.offset, page_offset: packet.page_offset };
  let next = Link { info: info, identification: identification, identification_location: identification_location, codec: Some(next_codec), layout: layout, stage: stage, time: next_time, previous: block };
  return Ok((Decoder { control: control, container: container, link: Some(next), queue: Some(page), index: index + 1 }, Some(Public(Samples(value)))));
}

function indexed(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  let packet = page.packets[index];
  let current = block(link, packet.bytes, control.limits.codec.audio)?;
  let count = if (link.previous == 0) { 0 } else { (link.previous + current) / 4 };
  let (time, range) = timing.advance(link.time, count)?;
  let position = if (range.end == range.start) { if (link.time.decoded > link.time.start) { link.time.decoded - link.time.start } else { 0i64 } } else { range.position };
  let value = Indexed { info: link.info, location: location(packet), raw_frames: count, start: range.start, frames: range.end - range.start, position: position, granule: range.granule };
  let next = Link { ...link, previous: current, time: time };
  return Ok((Decoder { control: control, container: container, link: Some(next), queue: Some(page), index: index + 1 }, Some(Packet(value))));
}

function drain(own decoder: Decoder, scanning: boolean): Result[(Decoder, Option[CoreEvent]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = decoder;
  let Some(page) = queue else {
    return Err(pcm.Header);
  };
  let Some(value) = link else {
    return Err(pcm.Header);
  };
  if (index < page.packets.len()) {
    if (value.stage < 2) {
      return header(control, container, value, page, index, scanning);
    }
    return if (scanning) { indexed(control, container, value, page, index) } else { audio(control, container, value, page, index) };
  }
  if (!page.eos) {
    return Ok((Decoder { control: control, container: container, link: Some(value), queue: None, index: 0 }, None));
  }
  let summary = Summary { info: value.info, frames: timing.finish(value.time)?, initial_granule: value.time.initial };
  return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, Some(Public(End(summary)))));
}

function feed_core(own decoder: Decoder, input: [u8], scanning: boolean): Result[(Decoder, i32, Option[CoreEvent]), pcm.AudioError] {
  if let Some(_) = decoder.queue {
    let (next, event) = drain(decoder, scanning)?;
    if let Some(_) = event {
      return Ok((next, 0, event));
    }
    return feed_core(next, input, scanning);
  }
  let Decoder { control, container, link, queue, index } = decoder;
  let (next_container, used, parsed) = container.feed(input)?;
  let next = Decoder { control: control, container: next_container, link: link, queue: None, index: 0 };
  return match (parsed) {
    None => Ok((next, used, None)),
    Some(value) => {
      let (next, event) = accept(next, value)?;
      Ok((next, used, event))
    },
  };
}

pub function (decoder: Decoder) finish(): Result[void, pcm.AudioError] {
  if let Some(_) = decoder.queue {
    return Err(pcm.Buffer);
  }
  decoder.container.finish()?;
  if let Some(_) = decoder.link {
    return Err(pcm.Truncated);
  }
  if (decoder.control.links == 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}
```
