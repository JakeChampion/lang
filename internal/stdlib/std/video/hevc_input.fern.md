# Bounded HEVC access-unit input

This module groups base-layer HEVC Annex B NAL units into coded-picture access units.
It reuses the bounded NAL reader and the native SPS and PPS validators.
Each step performs at most one source read and emits at most one event.
The caller retains and closes the source.

The first-slice flag starts each picture. Subsequent slices retain the same PPS, NAL type, and temporal identifier.
AUD, VPS, SPS, PPS, and prefix SEI belong to the next picture.
Suffix SEI, filler, EOS, and EOB remain with the current picture.
EOS requires an IRAP picture in the next access unit. EOB permits no subsequent NAL unit.

Packets use canonical four-byte prefixes and preserve original prefix offsets.
The IRAP flag identifies types 16 through 21. The IDR flag identifies types 19 and 20 only.
These flags do not establish random-access safety for CRA or BLA pictures.

The reader retains at most 16 SPS records and 64 PPS records.
Each record retains its bounded parameter NAL. An SPS replacement invalidates the cached validation of its PPS records.

Before a slice uses a stale PPS, the reader validates it against the current SPS.
Unused records do not reject unrelated pictures. A new PPS can replace its old record before use.
The separate SPS and PPS ceilings permit at most 4,325,376 bytes of parameter payloads.

Separate limits bound encoded packet bytes, units per packet, packet count, input windows, NAL bytes, and source operations.
At most one pending NAL remains outside the current packet.
Slice inspection copies at most eight unescaped bytes.
The limits exclude retained output packets and do not define an exact process-memory bound.

Multilayer input, separate color planes, and reserved or unspecified NAL types return `Unsupported`.
VPS, SEI, and filler payloads remain opaque. The reader does not resolve VPS references.
It does not validate entropy data, complete slice headers or coverage, or decoded pixels.

Packets have no assigned timestamps. Run blocking I/O outside the Android loop callback.
After an error, discard the consumed state and close or restart the source.

```fern
<<*>>=
// Group bounded base-layer HEVC NAL input by coded-picture boundaries.
import "std/video/hevc";
import "std/video/hevc/pps";
import "std/video/avc" as bits;
import "std/video/nal/input" as nals;
import "std/video/frame";
import "std/option";

pub struct Limits { video: frame.Limits, input: nals.Limits, max_packets: i32, max_units_per_packet: i32 }

pub struct Packet { offset: i64, irap: boolean, idr: boolean, bytes: u8[] }

pub enum Event { Waiting, PacketReady(Packet), End }

struct Header { kind: i32, temporal: i32 }

struct Identity { parameter_id: i32, kind: i32, temporal: i32, first: boolean }

struct SequenceSlot { value: hevc.Sequence, encoded: u8[], generation: i64 }

struct PictureSlot { value: pps.PictureParameters, encoded: u8[], generation: i64 }

pub struct Stream { units: nals.Stream, sequences: SequenceSlot[], pictures: PictureSlot[], next_sequence: i64, packet: u8[], packet_offset: i64, packet_units: i32, last: Option[Identity], pending: Option[nals.Unit], pending_offset: i64, ended: boolean, need_irap: boolean, bitstream_end: boolean, finished: boolean, packets: i32, limits: Limits }

pub struct Step { stream: Stream, event: Event }

pub function default_limits(): Limits {
  return Limits { video: frame.default_limits(), input: nals.default_limits(), max_packets: 1048576, max_units_per_packet: 4096 };
}

pub function create(limits: Limits): Result[Stream, nals.ReadError] {
  if (limits.video.max_pixels < 0 || limits.video.max_packet_bytes < 7 || limits.max_packets < 0 || limits.max_units_per_packet < 1) {
    return Err(nals.ReadVideo(frame.Limit));
  }
  let units = nals.create(limits.input)?;
  return Ok(Stream { units: units, sequences: [], pictures: [], next_sequence: 0i64, packet: [], packet_offset: 0i64, packet_units: 0, last: None, pending: None, pending_offset: 0i64, ended: false, need_irap: false, bitstream_end: false, finished: false, packets: 0, limits: limits });
}

function irap(kind: i32): boolean {
  return kind >= 16 && kind <= 21;
}

function supported(kind: i32): boolean {
  return kind <= 9 || irap(kind) || kind >= 32 && kind <= 40;
}

function temporal_zero(kind: i32): boolean {
  return irap(kind) || kind >= 32 && kind <= 34 || kind == 36 || kind == 37;
}

function nal_header(input: [u8]): Result[Header, frame.VideoError] {
  if (input.len() < 2) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) != 0u8 || (input[1] & 7u8) == 0u8) {
    return Err(frame.Header);
  }
  if ((input[0] & 1u8) != 0u8 || input[1] >> 3u8 != 0u8) {
    return Err(frame.Unsupported);
  }
  let kind = (input[0] >> 1u8 & 63u8) as i32;
  let temporal = (input[1] & 7u8) as i32 - 1;
  if (!supported(kind)) {
    return Err(frame.Unsupported);
  }
  if (temporal_zero(kind) && temporal != 0) {
    return Err(frame.Header);
  }
  return Ok(Header { kind: kind, temporal: temporal });
}

function delimiter(input: [u8], kind: i32): Result[void, frame.VideoError] {
  if (kind == 36 || kind == 37) {
    return if (input.len() == 2) { Ok(()) } else { Err(frame.Header) };
  }
  if (kind == 35) {
    if (input.len() != 3) {
      return Err(if (input.len() < 3) { frame.Truncated } else { frame.Header });
    }
    let valid = input[2] >> 5u8 <= 2u8 && (input[2] & 31u8) == 16u8;
    if (!valid) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

function sequence(stream: Stream, id: i32): Result[SequenceSlot, frame.VideoError] {
  for slot in stream.sequences {
    if (slot.value.sequence_id == id) {
      return Ok(slot);
    }
  }
  return Err(frame.State);
}

function store_sequence(own stream: Stream, input: u8[]): Result[Stream, frame.VideoError] {
  let value = hevc.inspect_sps(input, stream.limits.video)?;
  if (value.separate_colour_plane) {
    return Err(frame.Unsupported);
  }
  let slot = SequenceSlot { value: value, encoded: input, generation: stream.next_sequence };
  stream = Stream { ...stream, next_sequence: stream.next_sequence + 1i64 };
  for i in 0..stream.sequences.len() {
    if (stream.sequences[i].value.sequence_id == value.sequence_id) {
      return Ok(Stream { ...stream, sequences: stream.sequences.with(i, slot) });
    }
  }
  if (stream.sequences.len() >= 16) {
    return Err(frame.Limit);
  }
  return Ok(Stream { ...stream, sequences: stream.sequences.append(slot) });
}

function picture_ids(input: [u8]): Result[(i32, i32), frame.VideoError] {
  if (input.len() > pps.MAX_PPS_BYTES) {
    return Err(frame.Limit);
  }
  let bytes = bits.rbsp_prefix(input[2:], 8)?;
  let (at, id) = bits.bounded_ue(bytes, 0i64, 63u32)?;
  let (_, parent) = bits.bounded_ue(bytes, at, 15u32)?;
  return Ok((id as i32, parent as i32));
}

function store_picture(own stream: Stream, input: u8[]): Result[Stream, frame.VideoError] {
  let (_, parent) = picture_ids(input)?;
  let context = sequence(stream, parent)?;
  let value = pps.inspect(input, context.encoded, stream.limits.video)?;
  let slot = PictureSlot { value: value, encoded: input, generation: context.generation };
  for i in 0..stream.pictures.len() {
    if (stream.pictures[i].value.picture_id == value.picture_id) {
      return Ok(Stream { ...stream, pictures: stream.pictures.with(i, slot) });
    }
  }
  if (stream.pictures.len() >= 64) {
    return Err(frame.Limit);
  }
  return Ok(Stream { ...stream, pictures: stream.pictures.append(slot) });
}

function parameters(own stream: Stream, input: u8[], kind: i32): Result[Stream, frame.VideoError] {
  return match (kind) { 33 => store_sequence(stream, input), 34 => store_picture(stream, input), _ => Ok(stream) };
}

function current_picture(own stream: Stream, id: i32): Result[(Stream, pps.PictureParameters, hevc.Sequence), frame.VideoError] {
  for i in 0..stream.pictures.len() {
    let slot = stream.pictures[i];
    if (slot.value.picture_id == id) {
      let context = sequence(stream, slot.value.sequence_id)?;
      if (slot.generation == context.generation) {
        return Ok((stream, slot.value, context.value));
      }
      let value = pps.inspect(slot.encoded, context.encoded, stream.limits.video)?;
      let updated = PictureSlot { value: value, encoded: slot.encoded, generation: context.generation };
      return Ok((Stream { ...stream, pictures: stream.pictures.with(i, updated) }, value, context.value));
    }
  }
  return Err(frame.State);
}

function slice_identity(own stream: Stream, input: [u8], header: Header): Result[(Stream, Identity), frame.VideoError] {
  let bytes = bits.rbsp_prefix(input[2:], 8)?;
  let (at, first) = bits.read(bytes, 0i64, 1)?;
  let next = at;
  if (irap(header.kind)) {
    let (after, _) = bits.read(bytes, at, 1)?;
    next = after;
  }
  let (_, id) = bits.bounded_ue(bytes, next, 63u32)?;
  let (updated, picture, context) = current_picture(stream, id as i32)?;
  if (header.temporal >= context.max_sub_layers) {
    return Err(frame.Header);
  }
  return Ok((updated, Identity { parameter_id: picture.picture_id, kind: header.kind, temporal: header.temporal, first: first != 0u32 }));
}

function unit_identity(own stream: Stream, input: [u8], header: Header): Result[(Stream, Option[Identity]), frame.VideoError] {
  if (header.kind < 32) {
    let (updated, value) = slice_identity(stream, input, header)?;
    return Ok((updated, Some(value)));
  }
  return Ok((stream, None));
}

function continuation(stream: Stream, identity: Option[Identity]): Result[void, frame.VideoError] {
  if let Some(next) = identity {
    if (!next.first) {
      let Some(last) = stream.last else {
        return Err(frame.Header);
      };
      if (stream.ended || last.parameter_id != next.parameter_id || last.kind != next.kind || last.temporal != next.temporal) {
        return Err(frame.Header);
      }
    }
  }
  return Ok(());
}

function prefix(kind: i32): boolean {
  return kind >= 32 && kind <= 35 || kind == 39;
}

function sequence_start(stream: Stream, identity: Option[Identity]): Result[void, frame.VideoError] {
  if let Some(next) = identity {
    if (stream.need_irap && next.first && !irap(next.kind)) {
      return Err(frame.Header);
    }
  }
  return Ok(());
}

function aud_position(stream: Stream, kind: i32): Result[void, frame.VideoError] {
  if (kind == 35 && stream.packet_units != 0 && !stream.last.is_some()) {
    return Err(frame.Header);
  }
  return Ok(());
}

function boundary(stream: Stream, kind: i32, identity: Option[Identity]): boolean {
  let first = match (identity) { Some(value) => value.first, None => false };
  return stream.last.is_some() && (prefix(kind) || first);
}

function suffix(stream: Stream, kind: i32): Result[void, frame.VideoError] {
  if ((kind >= 36 && kind <= 38 || kind == 40) && !stream.last.is_some()) {
    return Err(frame.Header);
  }
  if (stream.ended && !prefix(kind) && kind >= 32 && kind != 37) {
    return Err(frame.Header);
  }
  return Ok(());
}

function append_packet(own bytes: u8[], input: [u8], maximum: i32): Result[u8[], frame.VideoError] {
  if (maximum - bytes.len() < 4 || input.len() > maximum - bytes.len() - 4) {
    return Err(frame.Limit);
  }
  for byte in [0u8, 0u8, 0u8, 1u8] {
    bytes = bytes.append(byte);
  }
  for byte in input {
    bytes = bytes.append(byte);
  }
  return Ok(bytes);
}

function emit(own stream: Stream): Result[Step, frame.VideoError] {
  if (stream.packets >= stream.limits.max_packets) {
    return Err(frame.Limit);
  }
  let kind = match (stream.last) { Some(value) => value.kind, None => -1 };
  let packet = Packet { offset: stream.packet_offset, irap: irap(kind), idr: kind == 19 || kind == 20, bytes: stream.packet };
  return Ok(Step { stream: Stream { ...stream, packet: [], packet_units: 0, last: None, ended: false, packets: stream.packets + 1 }, event: PacketReady(packet) });
}

function append_unit(own stream: Stream, unit: nals.Unit, offset: i64, kind: i32, identity: Option[Identity]): Result[Step, frame.VideoError] {
  if (stream.packet_units >= stream.limits.max_units_per_packet) {
    return Err(frame.Limit);
  }
  let updated = parameters(stream, unit.bytes, kind)?;
  let at = if (updated.packet_units == 0) { offset } else { updated.packet_offset };
  let bytes = append_packet(updated.packet, unit.bytes, updated.limits.video.max_packet_bytes)?;
  let last = if (identity.is_some()) { identity } else { updated.last };
  let need_irap = if (kind == 36) { true } else if (identity.is_some()) { false } else { updated.need_irap };
  return Ok(Step { stream: Stream { ...updated, packet: bytes, packet_offset: at, packet_units: updated.packet_units + 1, last: last, ended: kind == 36 || kind == 37, need_irap: need_irap, bitstream_end: kind == 37 }, event: Waiting });
}

function checked_identity(own stream: Stream, input: [u8], header: Header): Result[(Stream, Option[Identity]), frame.VideoError] {
  delimiter(input, header.kind)?;
  aud_position(stream, header.kind)?;
  suffix(stream, header.kind)?;
  let (prepared, identity) = unit_identity(stream, input, header)?;
  continuation(prepared, identity)?;
  sequence_start(prepared, identity)?;
  return Ok((prepared, identity));
}

function accept(own stream: Stream, unit: nals.Unit, offset: i64): Result[Step, frame.VideoError] {
  if (stream.bitstream_end) {
    return Err(frame.Header);
  }
  if (stream.packets >= stream.limits.max_packets) {
    return Err(frame.Limit);
  }
  let header = nal_header(unit.bytes)?;
  let (prepared, identity) = checked_identity(stream, unit.bytes, header)?;
  if (boundary(prepared, header.kind, identity)) {
    return emit(Stream { ...prepared, pending: Some(unit), pending_offset: offset });
  }
  return append_unit(prepared, unit, offset, header.kind, identity);
}

function finish(own stream: Stream): Result[Step, frame.VideoError] {
  stream = Stream { ...stream, finished: true };
  if (stream.last.is_some()) {
    return emit(stream);
  }
  if (stream.packet.len() != 0) {
    return Err(frame.Truncated);
  }
  return Ok(Step { stream: stream, event: End });
}

function converted(result: Result[Step, frame.VideoError]): Result[Step, nals.ReadError] {
  return match (result) { Ok(value) => Ok(value), Err(error) => Err(nals.ReadVideo(error)) };
}

pub function (own stream: Stream) step_with(read: (i32) => Result[u8[], IoError]): Result[Step, nals.ReadError] {
  if let Some(unit) = stream.pending {
    let offset = stream.pending_offset;
    return converted(accept(Stream { ...stream, pending: None }, unit, offset));
  }
  if (stream.finished) {
    return Ok(Step { stream: stream, event: End });
  }
  let at = stream.units.offset;
  let next = stream.units.step_with(read)?;
  stream = Stream { ...stream, units: next.stream };
  return match (next.event) {
    nals.Waiting => Ok(Step { stream: stream, event: Waiting }),
    nals.UnitReady(unit) => converted(accept(stream, unit, at)),
    nals.End => converted(finish(stream)),
  };
}

pub function (own stream: Stream) step(reader: Reader): Result[Step, nals.ReadError] {
  return stream.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
