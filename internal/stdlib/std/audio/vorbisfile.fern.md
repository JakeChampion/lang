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

```fern
<<*>>=
// This module decodes incremental Ogg Vorbis streams to PCM.
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

struct Link { info: Info, identification: u8[], codec: Option[vorbis.Decoder], stage: i32, time: timing.Timing, previous: i32 }

struct Control { limits: Limits, links: i32 }

struct Plan { frames: i64, first: i32, last: i32, packets: i32 }

pub opaque struct Decoder { control: Control, container: ogg.Decoder, link: Option[Link], queue: Option[ogg.Page], index: i32 }

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
function begin(control: Control, own container: ogg.Decoder, page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
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
  let link = Link { info: info, identification: identification, codec: None, stage: 0, time: timing.create(), previous: 0 };
  return Ok((Decoder { control: Control { ...control, links: control.links + 1 }, container: container, link: Some(link), queue: None, index: 0 }, Some(Begin(info))));
}

function duration(codec: vorbis.Decoder, previous: i32, packets: [ogg.Packet], limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  let frames = 0i64;
  let first = 0;
  let last = 0;
  for packet in packets {
    let current = codec.block_size(packet.bytes, limits)?;
    let count = if (previous == 0) { 0 } else { (previous + current) / 4 };
    let _ = pcm.sample_count(count, codec.information().channels, limits)?;
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
function selected(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (link.stage < 2) {
    if (page.eos || page.granule != 0i64 && !(page.packets.len() == 0 && page.granule == -1i64)) {
      return Err(pcm.Header);
    }
  } else if (page.packets.len() == 0) {
    if (page.granule != -1i64) {
      return Err(pcm.Header);
    }
  } else {
    let Some(codec) = link.codec else {
      return Err(pcm.Header);
    };
    let plan = duration(codec, link.previous, page.packets, control.limits.codec.audio)?;
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

function accept(own decoder: Decoder, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
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
function header(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let event: Option[Event] = None;
  if (link.stage == 0) {
    let tags = headers.comments(page.packets[index].bytes, control.limits.codec)?;
    event = Some(Tags(Metadata { info: link.info, tags: tags }));
    link = Link { ...link, stage: 1 };
  } else {
    if (index + 1 != page.packets.len() || incomplete(container, page.serial)) {
      return Err(pcm.Header);
    }
    let codec = vorbis.create(link.identification, page.packets[index].bytes, control.limits.codec)?;
    link = Link { ...link, identification: [], stage: 2, codec: Some(codec) };
  }
  return Ok((Decoder { control: control, container: container, link: Some(link), queue: Some(page), index: index + 1 }, event));
}

function audio(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Link { info, identification, codec, stage, time, previous } = link;
  let Some(current) = codec else {
    return Err(pcm.Header);
  };
  let packet = page.packets[index];
  let block = current.block_size(packet.bytes, control.limits.codec.audio)?;
  let (next_codec, frame) = current.decode_packet(packet.bytes, control.limits.codec.audio)?;
  let (next_time, range) = timing.advance(time, frame.frames)?;
  let value = Audio { info: info, samples: clipped(frame.samples, range, info.audio.channels), frames: range.end - range.start, position: range.position, granule: range.granule, packet_offset: packet.offset, page_offset: packet.page_offset };
  let next = Link { info: info, identification: identification, codec: Some(next_codec), stage: stage, time: next_time, previous: block };
  return Ok((Decoder { control: control, container: container, link: Some(next), queue: Some(page), index: index + 1 }, Some(Samples(value))));
}

function drain(own decoder: Decoder): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = decoder;
  let Some(page) = queue else {
    return Err(pcm.Header);
  };
  let Some(value) = link else {
    return Err(pcm.Header);
  };
  if (index < page.packets.len()) {
    if (value.stage < 2) {
      return header(control, container, value, page, index);
    }
    return audio(control, container, value, page, index);
  }
  if (!page.eos) {
    return Ok((Decoder { control: control, container: container, link: Some(value), queue: None, index: 0 }, None));
  }
  let summary = Summary { info: value.info, frames: timing.finish(value.time)?, initial_granule: value.time.initial };
  return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, Some(End(summary))));
}

pub function (own decoder: Decoder) feed(input: [u8]): Result[(Decoder, i32, Option[Event]), pcm.AudioError] {
  if let Some(_) = decoder.queue {
    let (next, event) = drain(decoder)?;
    if let Some(_) = event {
      return Ok((next, 0, event));
    }
    return next.feed(input);
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
