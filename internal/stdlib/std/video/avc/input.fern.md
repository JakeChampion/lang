# Bounded AVC access-unit input

This module groups ordinary progressive AVC Annex B NAL units into access units.
It reuses the bounded NAL input reader and the native SPS and PPS validators.
Each step performs at most one source read and emits at most one event.
The caller retains and closes the source.

The stream requires an SPS and PPS before their first slice.
It retains at most 32 sequence records and 256 picture records.
Separate limits bound NAL input, encoded access-unit bytes, units per access unit, and access-unit count.
Slice identity uses frame numbers, parameter identifiers, reference status, IDR identifiers, and picture-order fields.
Only the first 64 unescaped slice bytes enter temporary header storage.

Packets use four-byte Annex B prefixes and preserve each NAL payload.
Their offsets identify the first original prefix, including its leading zeros.
Packets report the IDR flag without recovery-point SEI interpretation.
The reader assigns no timestamp.
Interlaced pictures, separate color planes, slice groups, redundant pictures, and partitioned or extended slices return `Unsupported`.

The reader does not validate entropy data or complete slice coverage and does not decode pixels.
The byte limits exclude retained output packets and do not define an exact process-memory bound.
Blocking I/O must run outside the Android loop callback.
After an error, discard the consumed state and close or restart the source.

```fern
<<*>>=
// Group bounded AVC NAL input by coded-picture identity.
import "std/video/avc";
import "std/video/nal/input" as nals;
import "std/video/frame";
import "std/option";

pub struct Limits { video: frame.Limits, input: nals.Limits, max_packets: i32, max_units_per_packet: i32 }

pub struct Packet { offset: i64, idr: boolean, bytes: u8[] }

pub enum Event { Waiting, PacketReady(Packet), End }

struct SequenceSlot { value: avc.SliceSequence }

struct PictureSlot { value: avc.PictureParameters }

struct Identity { parameter_id: i32, frame_num: i32, reference: boolean, idr: boolean, idr_id: i32, order_type: i32, order_a: i32, order_b: i32 }

pub struct Stream { units: nals.Stream, sequences: SequenceSlot[], pictures: PictureSlot[], packet: u8[], packet_offset: i64, packet_units: i32, last: Option[Identity], pending: Option[nals.Unit], pending_offset: i64, forced: boolean, finished: boolean, packets: i32, limits: Limits }

pub struct Step { stream: Stream, event: Event }

pub function default_limits(): Limits {
  return Limits { video: frame.default_limits(), input: nals.default_limits(), max_packets: 1048576, max_units_per_packet: 4096 };
}

pub function create(limits: Limits): Result[Stream, nals.ReadError] {
  if (limits.video.max_pixels < 0 || limits.video.max_packet_bytes < 5 || limits.max_packets < 0 || limits.max_units_per_packet < 1) {
    return Err(nals.ReadVideo(frame.Limit));
  }
  let units = nals.create(limits.input)?;
  return Ok(Stream { units: units, sequences: [], pictures: [], packet: [], packet_offset: 0i64, packet_units: 0, last: None, pending: None, pending_offset: 0i64, forced: false, finished: false, packets: 0, limits: limits });
}

function sequence(stream: Stream, id: i32): Result[avc.SliceSequence, frame.VideoError] {
  for slot in stream.sequences {
    if (slot.value.sequence.sequence_id == id) {
      return Ok(slot.value);
    }
  }
  return Err(frame.State);
}

function picture(stream: Stream, id: i32): Result[avc.PictureParameters, frame.VideoError] {
  for slot in stream.pictures {
    if (slot.value.parameter_id == id) {
      return Ok(slot.value);
    }
  }
  return Err(frame.State);
}

function store_sequence(own stream: Stream, input: [u8]): Result[Stream, frame.VideoError] {
  let value = avc.inspect_slice_sequence(input, stream.limits.video)?;
  if (!value.sequence.frame_mbs_only || value.sequence.separate_colour_plane) {
    return Err(frame.Unsupported);
  }
  for i in 0..stream.sequences.len() {
    if (stream.sequences[i].value.sequence.sequence_id == value.sequence.sequence_id) {
      return Ok(Stream { ...stream, sequences: stream.sequences.with(i, SequenceSlot { value: value }) });
    }
  }
  if (stream.sequences.len() >= 32) {
    return Err(frame.Limit);
  }
  return Ok(Stream { ...stream, sequences: stream.sequences.append(SequenceSlot { value: value }) });
}

function picture_ids(input: [u8]): Result[(i32, i32), frame.VideoError] {
  if (input.len() > avc.MAX_PPS_BYTES) {
    return Err(frame.Limit);
  }
  let bytes = avc.rbsp_prefix(input[1:], 64)?;
  let (at, id) = avc.bounded_ue(bytes, 0i64, 255u32)?;
  let (_, parent) = avc.bounded_ue(bytes, at, 31u32)?;
  return Ok((id as i32, parent as i32));
}

function store_picture(own stream: Stream, input: [u8]): Result[Stream, frame.VideoError] {
  let (_, parent) = picture_ids(input)?;
  let context = sequence(stream, parent)?;
  let value = avc.inspect_pps(input, context.sequence, stream.limits.video)?;
  if (value.slice_group_count != 1 || value.redundant_pic_cnt_present) {
    return Err(frame.Unsupported);
  }
  for i in 0..stream.pictures.len() {
    if (stream.pictures[i].value.parameter_id == value.parameter_id) {
      return Ok(Stream { ...stream, pictures: stream.pictures.with(i, PictureSlot { value: value }) });
    }
  }
  if (stream.pictures.len() >= 256) {
    return Err(frame.Limit);
  }
  return Ok(Stream { ...stream, pictures: stream.pictures.append(PictureSlot { value: value }) });
}

function parameters(own stream: Stream, input: [u8], kind: i32): Result[Stream, frame.VideoError] {
  return match (kind) { 7 => store_sequence(stream, input), 8 => store_picture(stream, input), _ => Ok(stream) };
}

function optional_delta(input: [u8], at: i64, present: boolean): Result[i32, frame.VideoError] {
  if (present) {
    let (_, value) = avc.se(input, at)?;
    return Ok(value);
  }
  return Ok(0);
}

function order_fields(input: [u8], at: i64, context: avc.SliceSequence, pps: avc.PictureParameters): Result[(i32, i32), frame.VideoError] {
  if (context.order_type == 0) {
    let (next, lsb) = avc.read(input, at, context.order_bits)?;
    return Ok((lsb as i32, optional_delta(input, next, pps.bottom_field_pic_order_in_frame_present)?));
  }
  if (context.order_type == 1 && !context.delta_zero) {
    let (next, first) = avc.se(input, at)?;
    return Ok((first, optional_delta(input, next, pps.bottom_field_pic_order_in_frame_present)?));
  }
  return Ok((0, 0));
}

function slice_fields(input: [u8], at: i64, context: avc.SliceSequence, pps: avc.PictureParameters, header: u8): Result[Identity, frame.VideoError] {
  let (at, number) = avc.read(input, at, context.frame_bits)?;
  let idr = (header & 31u8) == 5u8;
  let id = 0u32;
  if (idr) {
    let (next, value) = avc.bounded_ue(input, at, 65535u32)?;
    at = next;
    id = value;
  }
  let (a, b) = order_fields(input, at, context, pps)?;
  return Ok(Identity { parameter_id: pps.parameter_id, frame_num: number as i32, reference: (header & 96u8) != 0u8, idr: idr, idr_id: id as i32, order_type: context.order_type, order_a: a, order_b: b });
}

function slice_identity(stream: Stream, input: [u8]): Result[Identity, frame.VideoError] {
  let bytes = avc.rbsp_prefix(input[1:], 64)?;
  let (kind_at, first) = avc.ue(bytes, 0i64)?;
  let (parameter_at, kind) = avc.bounded_ue(bytes, kind_at, 9u32)?;
  let (fields_at, id) = avc.bounded_ue(bytes, parameter_at, 255u32)?;
  let pps = picture(stream, id as i32)?;
  let context = sequence(stream, pps.sequence_id)?;
  let units = context.sequence.coded.width / 16 * (context.sequence.coded.height / 16);
  if (first >= units as u32) {
    return Err(frame.Header);
  }
  if ((input[0] & 31u8) == 5u8 && kind % 5u32 != 2u32 && kind % 5u32 != 4u32) {
    return Err(frame.Header);
  }
  return slice_fields(bytes, fields_at, context, pps, input[0]);
}

function different(a: Identity, b: Identity): boolean {
  return a.parameter_id != b.parameter_id || a.frame_num != b.frame_num || a.reference != b.reference || a.idr != b.idr || a.idr && a.idr_id != b.idr_id || a.order_type != b.order_type || a.order_a != b.order_a || a.order_b != b.order_b;
}

function boundary(stream: Stream, kind: i32, next: Option[Identity]): boolean {
  let Some(last) = stream.last else {
    return false;
  };
  if (stream.forced || kind >= 6 && kind <= 9) {
    return true;
  }
  return match (next) { Some(value) => different(last, value), None => false };
}

function supported_kind(kind: i32): boolean {
  return kind == 1 || kind == 5 || kind >= 6 && kind <= 12;
}

function valid_reference(kind: i32, reference: boolean): boolean {
  return kind == 1 || (kind == 5 || kind == 7 || kind == 8) == reference;
}

function nal_kind(input: [u8]): Result[i32, frame.VideoError] {
  if (input.len() == 0) {
    return Err(frame.Truncated);
  }
  if ((input[0] & 128u8) != 0u8) {
    return Err(frame.Header);
  }
  let kind = (input[0] & 31u8) as i32;
  if (!supported_kind(kind)) {
    return Err(frame.Unsupported);
  }
  if (!valid_reference(kind, (input[0] & 96u8) != 0u8)) {
    return Err(frame.Header);
  }
  return Ok(kind);
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
  let key = match (stream.last) { Some(value) => value.idr, None => false };
  let packet = Packet { offset: stream.packet_offset, idr: key, bytes: stream.packet };
  return Ok(Step { stream: Stream { ...stream, packet: [], packet_units: 0, last: None, forced: false, packets: stream.packets + 1 }, event: PacketReady(packet) });
}

function accept(own stream: Stream, unit: nals.Unit, offset: i64): Result[Step, frame.VideoError] {
  if (stream.packets >= stream.limits.max_packets) {
    return Err(frame.Limit);
  }
  let kind = nal_kind(unit.bytes)?;
  let identity = if (kind == 1 || kind == 5) { Some(slice_identity(stream, unit.bytes)?) } else { None };
  if (boundary(stream, kind, identity)) {
    return emit(Stream { ...stream, pending: Some(unit), pending_offset: offset });
  }
  if (stream.packet_units >= stream.limits.max_units_per_packet) {
    return Err(frame.Limit);
  }
  stream = parameters(stream, unit.bytes, kind)?;
  let at = if (stream.packet_units == 0) { offset } else { stream.packet_offset };
  let bytes = append_packet(stream.packet, unit.bytes, stream.limits.video.max_packet_bytes)?;
  let last = if (identity.is_some()) { identity } else { stream.last };
  return Ok(Step { stream: Stream { ...stream, packet: bytes, packet_offset: at, packet_units: stream.packet_units + 1, last: last, forced: kind == 10 || kind == 11 }, event: Waiting });
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
