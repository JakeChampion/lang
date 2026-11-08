# Incremental Ogg Opus playback

This module decodes Ogg Opus files into 48 kHz interleaved float samples.
It selects the first Opus logical stream and supports sequential chains.
It ignores other logical streams while the selected stream is active.
The generic Ogg reader still checks those streams and applies its storage limits.

The decoder applies header gain, pre-skip, initial granule offsets, and final sample boundaries.
Channel mapping families 0, 1, and 255 use the multistream decoder.
Input sample rate remains metadata. Decoder output can exceed the device sample range.

Each `feed` call returns the next decoder, the consumed input count, and an optional event.
Continue with the unconsumed input. An event can consume zero input bytes.
An empty sample event records a packet that pre-skip or final trimming removes.
After the final input, call `feed([])` until it returns no event.
Then call `finish` to check the complete stream.

An error consumes the decoder. Create a new decoder after an error.

The decoder retains at most one page of completed packets, plus bounded Ogg partial packets and codec state.
A completed packet can start on an earlier page. The Ogg packet limit bounds its size.
Each sample event contains at most one decoded packet.
`Audio.position` counts playable frames from the start of its logical stream.
`Audio.granule` retains the corresponding absolute Ogg timestamp.

```fern
<<*>>=
// Incremental Ogg Opus files with bounded packets and exact sample boundaries.

import "std/audio/ogg";
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/opusfile/headers";
import "std/audio/opusfile/timing";
import "std/opus/multistream";
import "std/opus/packet";
import "std/f32";
import "std/f64";

pub struct Limits { ogg: ogg.Limits, tags: headers.TagLimits, audio: multistream.Limits, max_links: i32 }

pub struct Info { index: i32, serial: u32, header: headers.Header }

pub struct Metadata { info: Info, tags: headers.Tags }

pub struct Audio {
  info: Info,
  samples: f32[],
  frames: i32,
  position: i64,
  granule: i64,
  packet_offset: i64,
  page_offset: i64,
}

pub struct Summary { info: Info, frames: i64, initial_granule: i64 }

pub enum Event { Begin(Info), Tags(Metadata), Samples(Audio), End(Summary) }

struct Link { info: Info, codec: multistream.Decoder, time: timing.Timing, gain: f32, stage: i32, tag_page: i64 }

struct Control { limits: Limits, links: i32 }

pub struct Decoder { control: Control, container: ogg.Decoder, link: Option[Link], queue: Option[ogg.Page], index: i32 }

pub function default_limits(): Limits {
  return Limits { ogg: ogg.default_limits(), tags: headers.default_tag_limits(), audio: multistream.default_limits(), max_links: 4096 };
}

function valid_limits(limits: Limits): boolean {
  return limits.max_links > 0 && limits.tags.max_bytes >= 0 && limits.tags.max_comments >= 0
    && limits.audio.max_streams > 0 && limits.audio.max_streams <= 255
    && limits.audio.max_input_bytes >= 0 && limits.audio.max_samples >= 0;
}

pub function decoder(limits: Limits): Result[Decoder, pcm.AudioError] {
  if (!valid_limits(limits)) {
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

function head_page(page: ogg.Page, container: ogg.Decoder): boolean {
  return page.bos && !page.eos && !page.continued && page.granule == 0i64
    && page.sequence == 0u32 && page.packets.len() == 1 && !incomplete(container, page.serial);
}

function gain(value: i32): f32 {
  let exponent = 0.000648814081f32 * (value as f32);
  return (0.6931471805599453094f64 * (exponent as f64)).exp() as f32;
}

function begin(control: Control, own container: ogg.Decoder, page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (page.packets.len() == 0 || !wire.tag(page.packets[0].bytes, 0, "OpusHead")) {
    return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, None));
  }
  if (!head_page(page, container)) {
    return Err(pcm.Header);
  }
  if (control.links >= control.limits.max_links) {
    return Err(pcm.Limit);
  }
  let header = headers.inspect(page.packets[0].bytes)?;
  let config = multistream.Config { sample_rate: 48000, channels: header.channels, streams: header.streams, coupled: header.coupled, mapping: header.mapping };
  let info = Info { index: control.links, serial: page.serial, header: header };
  let link = Link { info: info, codec: multistream.decoder(config, control.limits.audio)?, time: timing.create(header.pre_skip)?, gain: gain(header.output_gain), stage: 0, tag_page: -1i64 };
  let next = Decoder { control: Control { ...control, links: control.links + 1 }, container: container, link: Some(link), queue: None, index: 0 };
  return Ok((next, Some(Begin(info))));
}

function tag_page(page: ogg.Page, link: Link, container: ogg.Decoder): boolean {
  return page.packets.len() == 1 && !page.eos && page.granule == 0i64
    && page.packets[0].page_offset == link.tag_page && !incomplete(container, page.serial);
}

function metadata(control: Control, own container: ogg.Decoder, own link: Link, page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (link.tag_page < 0i64) {
    if (page.sequence != 1u32) {
      return Err(pcm.Header);
    }
    link = Link { ...link, tag_page: page.offset };
  }
  if (page.packets.len() == 0) {
    if (page.granule != -1i64 || !incomplete(container, page.serial)) {
      return Err(pcm.Header);
    }
    return Ok((Decoder { control: control, container: container, link: Some(link), queue: None, index: 0 }, None));
  }
  if (!tag_page(page, link, container)) {
    return Err(pcm.Header);
  }
  let tags = headers.tags(page.packets[0].bytes, control.limits.tags)?;
  let info = link.info;
  return Ok((Decoder { control: control, container: container, link: Some(Link { ...link, stage: 1 }), queue: None, index: 0 }, Some(Tags(Metadata { info: info, tags: tags }))));
}

function duration(packets: [ogg.Packet]): Result[i64, pcm.AudioError] {
  let frames = 0i64;
  for encoded in packets {
    let count = match (packet.sample_count(encoded.bytes, 48000)) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
    if (count <= 0) {
      return Err(pcm.Header);
    }
    frames += count as i64;
  }
  return Ok(frames);
}

function audio_page(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (page.packets.len() == 0) {
    if (page.granule != -1i64) {
      return Err(pcm.Header);
    }
    if (!page.eos) {
      return Ok((Decoder { control: control, container: container, link: Some(link), queue: None, index: 0 }, None));
    }
  } else {
    link = Link { ...link, time: timing.page(link.time, page.granule, duration(page.packets)?, page.eos)? };
  }
  return Ok((Decoder { control: control, container: container, link: Some(link), queue: Some(page), index: 0 }, None));
}

function accept(own d: Decoder, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  match (link) {
    None => {
      return begin(control, container, page);
    },
    Some(selected) => {
      if (selected.info.serial != page.serial) {
        return Ok((Decoder { control: control, container: container, link: Some(selected), queue: None, index: 0 }, None));
      }
      if (selected.stage == 0) {
        return metadata(control, container, selected, page);
      }
      return audio_page(control, container, selected, page);
    }
  }
}

function amplified(value: f32, scale: f32): Result[f32, pcm.AudioError] {
  let output = value * scale;
  if (!output.is_finite()) {
    return Err(pcm.Sample);
  }
  return Ok(output);
}

function clipped(own input: f32[], range: timing.Range, channels: i32, scale: f32): Result[f32[], pcm.AudioError] {
  let start = range.start * channels;
  let end = range.end * channels;
  if (start == 0 && end == input.len()) {
    if (scale != 1.0f32) {
      for i in 0..input.len() {
        input = input.with(i, amplified(input[i], scale)?);
      }
    }
    return Ok(input);
  }
  let output: f32[] = [];
  for i in start..end {
    output = output.append(amplified(input[i], scale)?);
  }
  return Ok(output);
}

function decoded(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Link { info, codec, time, gain, stage, tag_page } = link;
  let encoded = page.packets[index];
  let (next, audio) = codec.decode(encoded.bytes)?;
  let (next_time, range) = timing.advance(time, audio.frames)?;
  let samples = clipped(audio.samples, range, audio.channels, gain)?;
  let value = Audio { info: info, samples: samples, frames: range.end - range.start, position: range.position, granule: range.granule, packet_offset: encoded.offset, page_offset: encoded.page_offset };
  let next_link = Link { info: info, codec: next, time: next_time, gain: gain, stage: stage, tag_page: tag_page };
  return Ok((Decoder { control: control, container: container, link: Some(next_link), queue: Some(page), index: index + 1 }, Some(Samples(value))));
}

function drain(own d: Decoder): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  let Some(page) = queue else {
    return Err(pcm.Header);
  };
  let Some(selected) = link else {
    return Err(pcm.Header);
  };
  if (index < page.packets.len()) {
    return decoded(control, container, selected, page, index);
  }
  if (!page.eos) {
    return Ok((Decoder { control: control, container: container, link: Some(selected), queue: None, index: 0 }, None));
  }
  let frames = timing.finish(selected.time)?;
  let initial = match (selected.time.initial) { Some(value) => value, None => 0i64 };
  let summary = Summary { info: selected.info, frames: frames, initial_granule: initial };
  return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, Some(End(summary))));
}

pub function (own d: Decoder) feed(input: [u8]): Result[(Decoder, i32, Option[Event]), pcm.AudioError] {
  if let Some(_) = d.queue {
    let (next, event) = drain(d)?;
    if let Some(_) = event {
      return Ok((next, 0, event));
    }
    return next.feed(input);
  }
  let Decoder { control, container, link, queue, index } = d;
  let (next_container, used, page) = container.feed(input)?;
  let next = Decoder { control: control, container: next_container, link: link, queue: None, index: 0 };
  match (page) {
    None => {
      return Ok((next, used, None));
    },
    Some(value) => {
      let (next, event) = accept(next, value)?;
      return Ok((next, used, event));
    }
  }
}

pub function (d: Decoder) finish(): Result[void, pcm.AudioError] {
  if let Some(_) = d.queue {
    return Err(pcm.Buffer);
  }
  d.container.finish()?;
  if let Some(_) = d.link {
    return Err(pcm.Truncated);
  }
  if (d.control.links == 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}
```
