# Incremental Ogg pages and packets

This module reads Ogg pages from partial input.
Each call consumes at most one page and returns its completed packets.
It checks page checksums, stream sequences, and packet continuation.
It supports interleaved logical streams and chained streams.

The reader buffers at most one Ogg page, plus incomplete packets within the configured limits.
A returned page contains at most 255 completed packets.
`Packet.offset` identifies its first payload byte. `Packet.page_offset` identifies its first page.
The page granule position belongs to the last completed packet on that page.

Call `feed` again with the unconsumed input bytes.
Call `finish` after the last input bytes to check for incomplete pages, packets, or streams.
After an error, create a new decoder.

```fern
<<*>>=
// Bounded, incremental Ogg pages and packet continuation for audio containers.

import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/ogg/tables";

pub const MAX_PAGE_BYTES: i32 = 65307;

pub struct Limits { max_streams: i32, max_packet_bytes: i32, max_pending_bytes: i32 }

pub struct Packet { bytes: u8[], offset: i64, page_offset: i64 }

pub struct Page {
  serial: u32,
  sequence: u32,
  granule: i64,
  continued: boolean,
  bos: boolean,
  eos: boolean,
  offset: i64,
  packets: Packet[],
}

pub struct Stream {
  serial: u32,
  next_sequence: u32,
  partial: u8[],
  incomplete: boolean,
  offset: i64,
  page_offset: i64,
}

pub struct Decoder {
  limits: Limits,
  buffer: u8[],
  target: i32,
  phase: i32,
  offset: i64,
  streams: Stream[],
  pending_bytes: i32,
}

struct Header { serial: u32, sequence: u32, granule: i64, flags: i32, payload: i32 }

pub function default_limits(): Limits {
  return Limits { max_streams: 16, max_packet_bytes: 1048576, max_pending_bytes: 4194304 };
}

pub function decoder(limits: Limits): Result[Decoder, pcm.AudioError] {
  if (limits.max_streams < 1 || limits.max_streams > 256 || limits.max_packet_bytes < 0 || limits.max_pending_bytes < limits.max_packet_bytes) {
    return Err(pcm.Limit);
  }
  return Ok(Decoder { limits: limits, buffer: [], target: 27, phase: 0, offset: 0i64, streams: [], pending_bytes: 0 });
}

function checksum(bytes: [u8]): u32 {
  let crc = 0u32;
  let lookup = tables.CRC;
  for i in 0..bytes.len() {
    let byte = if (i >= 22 && i < 26) { 0u32 } else { bytes[i] as u32 };
    crc = crc << 8u32 ^ lookup[(crc >> 24u32 ^ byte) as i32];
  }
  return crc;
}

function prefix(bytes: [u8]): Result[void, pcm.AudioError] {
  if (!wire.tag(bytes, 0, "OggS")) {
    return Err(pcm.Header);
  }
  if (bytes[4] != 0u8) {
    return Err(pcm.Unsupported);
  }
  if ((bytes[5] as i32 & 248) != 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function header(bytes: [u8]): Result[Header, pcm.AudioError] {
  if (checksum(bytes) != wire.le(bytes, 22, 4) as u32) {
    return Err(pcm.Checksum);
  }
  let granule = wire.le(bytes, 6, 8) as i64;
  if (granule < -1i64) {
    return Err(pcm.Header);
  }
  return Ok(Header {
    serial: wire.le(bytes, 14, 4) as u32, sequence: wire.le(bytes, 18, 4) as u32,
    granule: granule, flags: bytes[5] as i32, payload: 27 + bytes[26] as i32,
  });
}

function detach(own streams: Stream[], serial: u32): (Stream[], Option[Stream]) {
  let others: Stream[] = [];
  let selected: Option[Stream] = None;
  for stream in streams {
    if (stream.serial == serial) {
      selected = Some(stream);
    } else {
      others = others.append(stream);
    }
  }
  return (others, selected);
}

function select(own selected: Option[Stream], h: Header, others: i32, limits: Limits): Result[Stream, pcm.AudioError] {
  match (selected) {
    Some(stream) => {
      if ((h.flags & 2) != 0 || stream.next_sequence != h.sequence || stream.incomplete != ((h.flags & 1) != 0)) {
        return Err(pcm.Header);
      }
      return Ok(stream);
    },
    None => {
      if ((h.flags & 3) != 2 || h.sequence != 0u32) {
        return Err(pcm.Header);
      }
      if (others >= limits.max_streams) {
        return Err(pcm.Limit);
      }
      return Ok(Stream { serial: h.serial, next_sequence: 0u32, partial: [], incomplete: false, offset: 0i64, page_offset: 0i64 });
    }
  }
}

function packets(own current: Stream, bytes: [u8], h: Header, offset: i64, own pending: i32, limits: Limits): Result[(Stream, i32, Packet[]), pcm.AudioError] {
  let Stream { serial, next_sequence, partial, incomplete, offset: first, page_offset } = current;
  let completed: Packet[] = [];
  let at = h.payload;
  for i in 27..h.payload {
    let length = bytes[i] as i32;
    if (length > limits.max_packet_bytes - partial.len() || length > limits.max_pending_bytes - pending) {
      return Err(pcm.Limit);
    }
    if (!incomplete) {
      first = offset + at as i64;
      page_offset = offset;
    }
    for j in 0..length {
      partial = partial.append(bytes[at + j]);
    }
    at += length;
    pending += length;
    incomplete = length == 255;
    if (!incomplete) {
      pending -= partial.len();
      completed = completed.append(Packet { bytes: partial, offset: first, page_offset: page_offset });
      partial = [];
    }
  }
  if ((h.flags & 4) != 0 && incomplete) {
    return Err(pcm.Header);
  }
  return Ok((Stream { serial: serial, next_sequence: h.sequence + 1u32, partial: partial, incomplete: incomplete, offset: first, page_offset: page_offset }, pending, completed));
}

function page(own bytes: u8[], offset: i64, own streams: Stream[], pending: i32, limits: Limits): Result[(Decoder, Page), pcm.AudioError] {
  let Some(end) = offset +? bytes.len() as i64 else {
    return Err(pcm.Limit);
  };
  let h = header(bytes)?;
  let (others, selected) = detach(streams, h.serial);
  let current = select(selected, h, others.len(), limits)?;
  let (next, pending_bytes, completed) = packets(current, bytes, h, offset, pending, limits)?;
  if ((h.flags & 4) == 0) {
    others = others.append(next);
  }
  let decoder = Decoder { limits: limits, buffer: [], target: 27, phase: 0, offset: end, streams: others, pending_bytes: pending_bytes };
  let parsed = Page { serial: h.serial, sequence: h.sequence, granule: h.granule, continued: (h.flags & 1) != 0, bos: (h.flags & 2) != 0, eos: (h.flags & 4) != 0, offset: offset, packets: completed };
  return Ok((decoder, parsed));
}

function copy_count(target: i32, buffered: i32, available: i32): i32 {
  let count = target - buffered;
  return if (count > available) { available } else { count };
}

pub function (own d: Decoder) feed(input: [u8]): Result[(Decoder, i32, Option[Page]), pcm.AudioError] {
  let Decoder { limits, buffer, target, phase, offset, streams, pending_bytes } = d;
  let used = 0;
  while (true) {
    if (buffer.len() == target) {
      if (phase == 0) {
        prefix(buffer)?;
        target = 27 + buffer[26] as i32;
        phase = 1;
      } else if (phase == 1) {
        let lacing_end = target;
        for i in 27..lacing_end {
          target += buffer[i] as i32;
        }
        phase = 2;
      } else {
        let (next, parsed) = page(buffer, offset, streams, pending_bytes, limits)?;
        return Ok((next, used, Some(parsed)));
      }
    } else {
      if (used == input.len()) {
        return Ok((Decoder { limits: limits, buffer: buffer, target: target, phase: phase, offset: offset, streams: streams, pending_bytes: pending_bytes }, used, None));
      }
      let count = copy_count(target, buffer.len(), input.len() - used);
      for i in 0..count {
        buffer = buffer.append(input[used + i]);
      }
      used += count;
    }
  }
}

pub function (d: Decoder) finish(): Result[void, pcm.AudioError] {
  if (d.buffer.len() != 0 || d.streams.len() != 0) {
    return Err(pcm.Truncated);
  }
  return Ok(());
}
```
