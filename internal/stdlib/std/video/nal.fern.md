# AVC and HEVC packet conversion

MP4 stores AVC and HEVC NAL units with length prefixes.
Android MediaCodec requires Annex B start codes for these streams.
`to_annex_b` replaces each length prefix with a four-byte start code.
It preserves every NAL payload byte.

`avc_config` reads version-one `avcC` records with SPS and PPS units.
It returns separate `csd0` and `csd1` buffers for Android.
It also preserves optional sequence parameter set extensions in `csd0`.
`hevc_config` reads version-one `hvcC` records with VPS, SPS, and PPS arrays.
It returns these arrays in VPS, SPS, PPS order in `csd0`.

It preserves prefix and suffix SEI arrays after the parameter sets.
Other HEVC configuration array types return `Unsupported`.

The functions validate framing and parameter-set NAL types.
They do not validate the coded parameter-set syntax or establish decoder support.
Length sizes one, two, and four are supported. Three is reserved.
Limits apply to the complete input record and the combined output buffers.

`next_annex_b` reads one NAL byte range from a complete Annex B byte view.
Start at offset zero. Continue with the returned `next_offset` until the function returns `None`.
The ranges exclude start codes, leading zeros, and trailing zeros.
Both three-byte and four-byte start codes are supported, including extra zero bytes between units.
The function preserves emulation-prevention bytes inside each NAL unit.

`to_length_prefixed` converts those ranges to one MP4 packet.
The prefix width must hold every unit length. Both the complete input and output must fit the packet limit.
These functions inspect framing only. They do not assign access-unit boundaries, timestamps, or coded-picture semantics.
The caller must provide packet boundaries and timing before container muxing.

An end cursor returns `None`. An incomplete start code or missing payload returns `Truncated`.
An invalid prefix or empty unit returns `Header`. A byte budget or prefix-capacity failure returns `Limit`.

```fern
<<*>>=
// This module converts AVC and HEVC configuration records and packets.
import "std/video/frame";
import "std/audio/wire";

pub struct Config { length_size: i32, csd0: u8[], csd1: u8[] }

pub struct AnnexUnit { offset: i32, size: i32, next_offset: i32 }

struct Units { bytes: u8[], next_offset: i32 }

function length_size(n: i32): Result[void, frame.VideoError] {
  if (n != 1 && n != 2 && n != 4) {
    return Err(frame.Unsupported);
  }
  return Ok(());
}

function annex_prefix(input: [u8], at: i32): Result[i32, frame.VideoError] {
  let start = at;
  while (at < input.len() && input[at] == 0u8) {
    at += 1;
  }
  if (at == input.len()) {
    return Err(frame.Truncated);
  }
  if (at - start < 2 || input[at] != 1u8) {
    return Err(frame.Header);
  }
  if (at + 1 == input.len()) {
    return Err(frame.Truncated);
  }
  return Ok(at + 1);
}

function annex_unit(start: i32, end: i32, next: i32): Result[Option[AnnexUnit], frame.VideoError] {
  if (start == end) {
    return Err(frame.Header);
  }
  return Ok(Some(AnnexUnit { offset: start, size: end - start, next_offset: next }));
}

function annex_body(input: [u8], start: i32): Result[Option[AnnexUnit], frame.VideoError] {
  let at = start;
  let zeros = 0;
  while (at < input.len()) {
    let byte = input[at];
    if (byte == 0u8) {
      zeros += 1;
    } else {
      if (byte == 1u8 && zeros >= 2) {
        return annex_unit(start, at - zeros, at - zeros);
      }
      zeros = 0;
    }
    at += 1;
  }
  return annex_unit(start, input.len() - zeros, input.len());
}

pub function next_annex_b(input: [u8], offset: i32, limits: frame.Limits): Result[Option[AnnexUnit], frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (offset < 0 || offset > input.len()) {
    return Err(frame.Header);
  }
  if (offset == input.len()) {
    return Ok(None);
  }
  return annex_body(input, annex_prefix(input, offset)?);
}

function append_length_unit(own output: u8[], input: [u8], prefix_bytes: i32, budget: i32): Result[u8[], frame.VideoError] {
  let maximum = (1u64 << (prefix_bytes * 8) as u64) - 1u64;
  if (input.len() as u64 > maximum || budget - output.len() < prefix_bytes || input.len() > budget - output.len() - prefix_bytes) {
    return Err(frame.Limit);
  }
  output = wire.put_be(output, input.len() as u64, prefix_bytes);
  for byte in input {
    output = output.append(byte);
  }
  return Ok(output);
}

pub function to_length_prefixed(input: [u8], prefix_bytes: i32, limits: frame.Limits): Result[u8[], frame.VideoError] {
  frame.check_packet(input, limits)?;
  length_size(prefix_bytes)?;
  if (input.len() == 0) {
    return Err(frame.Header);
  }
  let at = 0;
  let output: u8[] = [];
  while (at < input.len()) {
    let Some(unit) = next_annex_b(input, at, limits)? else {
      return Err(frame.State);
    };
    output = append_length_unit(output, input[unit.offset:unit.offset + unit.size], prefix_bytes, limits.max_packet_bytes)?;
    at = unit.next_offset;
  }
  return Ok(output);
}

function append_unit(own output: u8[], input: [u8], at: i32, size: i32, budget: i32): Result[u8[], frame.VideoError] {
  if (budget - output.len() < 4 || size > budget - output.len() - 4) {
    return Err(frame.Limit);
  }
  output = output.append(0).append(0).append(0).append(1);
  for i in 0..size {
    output = output.append(input[at + i]);
  }
  return Ok(output);
}

pub function to_annex_b(input: [u8], prefix_bytes: i32, limits: frame.Limits): Result[u8[], frame.VideoError] {
  frame.check_packet(input, limits)?;
  length_size(prefix_bytes)?;
  if (input.len() == 0) {
    return Err(frame.Header);
  }
  let at = 0;
  let output: u8[] = [];
  while (at < input.len()) {
    if (input.len() - at < prefix_bytes) {
      return Err(frame.Truncated);
    }
    let size = wire.be(input, at, prefix_bytes);
    at += prefix_bytes;
    if (size == 0u64) {
      return Err(frame.Header);
    }
    if (size > (input.len() - at) as u64) {
      return Err(frame.Truncated);
    }
    output = append_unit(output, input, at, size as i32, limits.max_packet_bytes)?;
    at += size as i32;
  }
  return Ok(output);
}

// Whether the unit at `pos` is of the parameter-set type the array holds.
function unit_kind_ok(input: [u8], pos: i32, nal_type: i32, hevc: boolean): boolean {
  if ((input[pos] & 128u8) != 0u8) {
    return false;
  }
  let kind = if (hevc) { input[pos] as i32 >> 1 & 63 } else { input[pos] as i32 & 31 };
  if (kind != nal_type) {
    return false;
  }
  return !hevc || (input[pos + 1] & 7u8) != 0u8;
}

function read_units(input: [u8], at: i32, count: i32, nal_type: i32, hevc: boolean, own output: u8[], budget: i32): Result[Units, frame.VideoError] {
  let pos = at;
  let least = if (hevc) { 2 } else { 1 };
  for _ in 0..count {
    if (input.len() - pos < 2) {
      return Err(frame.Truncated);
    }
    let size = wire.be(input, pos, 2) as i32;
    pos += 2;
    if (size < least) {
      return Err(frame.Header);
    }
    if (size > input.len() - pos) {
      return Err(frame.Truncated);
    }
    if (!unit_kind_ok(input, pos, nal_type, hevc)) {
      return Err(frame.Header);
    }
    output = append_unit(output, input, pos, size, budget)?;
    pos += size;
  }
  return Ok(Units { bytes: output, next_offset: pos });
}

// The AVC profiles whose avcC record may carry the extension block.
function extended_profile(profile: u8): boolean {
  for p in [100, 110, 122, 144, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135] {
    if (p == profile as i32) {
      return true;
    }
  }
  return false;
}

struct Sets { prefix: i32, sps: u8[], pps: u8[], at: i32 }

// The length size, then the SPS and PPS arrays of an avcC record.
function avc_sets(input: [u8], limits: frame.Limits): Result[Sets, frame.VideoError] {
  if ((input[4] & 252u8) != 252u8 || (input[5] & 224u8) != 224u8) {
    return Err(frame.Header);
  }
  let prefix = (input[4] & 3u8) as i32 + 1;
  length_size(prefix)?;
  let nsps = (input[5] & 31u8) as i32;
  if (nsps == 0) {
    return Err(frame.Unsupported);
  }
  let sps = read_units(input, 6, nsps, 7, false, [], limits.max_packet_bytes)?;
  let at = sps.next_offset;
  if (at == input.len()) {
    return Err(frame.Truncated);
  }
  let npps = input[at] as i32;
  if (npps == 0) {
    return Err(frame.Unsupported);
  }
  let pps = read_units(input, at + 1, npps, 8, false, [], limits.max_packet_bytes - sps.bytes.len())?;
  return Ok(Sets { prefix: prefix, sps: sps.bytes, pps: pps.bytes, at: pps.next_offset });
}

// The extension block after the PPS array, appended to the SPS units.
function avc_extensions(input: [u8], at: i32, own csd0: u8[], budget: i32): Result[Units, frame.VideoError] {
  if (!extended_profile(input[1])) {
    return Err(frame.Unsupported);
  }
  if (input.len() - at < 4) {
    return Err(frame.Truncated);
  }
  if ((input[at] & 252u8) != 252u8 || (input[at + 1] & 248u8) != 248u8 || (input[at + 2] & 248u8) != 248u8) {
    return Err(frame.Header);
  }
  return read_units(input, at + 4, input[at + 3] as i32, 13, false, csd0, budget);
}

pub function avc_config(input: [u8], limits: frame.Limits): Result[Config, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() < 7) {
    return Err(frame.Truncated);
  }
  if (input[0] != 1) {
    return Err(frame.Unsupported);
  }
  let sets = avc_sets(input, limits)?;
  let at = sets.at;
  let csd0 = sets.sps;
  if (at < input.len()) {
    let extensions = avc_extensions(input, at, csd0, limits.max_packet_bytes - sets.pps.len())?;
    csd0 = extensions.bytes;
    at = extensions.next_offset;
  }
  if (at != input.len()) {
    return Err(frame.Header);
  }
  return Ok(Config { length_size: sets.prefix, csd0: csd0, csd1: sets.pps });
}

struct Arrays { vps: u8[], sps: u8[], pps: u8[], sei: u8[] }

// The reserved bits of an hvcC record's fixed part, all set.
function hevc_header_ok(input: [u8]): boolean {
  return (input[13] & 240u8) == 240u8 && (input[15] & 252u8) == 252u8 && (input[16] & 252u8) == 252u8 && (input[17] & 248u8) == 248u8 && (input[18] & 248u8) == 248u8;
}

// The NAL type an array holds: VPS, SPS, PPS, or prefix or suffix SEI.
function hevc_array_kind(input: [u8], at: i32): Result[i32, frame.VideoError] {
  if ((input[at] & 64u8) != 0u8) {
    return Err(frame.Header);
  }
  let kind = (input[at] & 63u8) as i32;
  if ((kind < 32 || kind > 34) && kind != 39 && kind != 40) {
    return Err(frame.Unsupported);
  }
  return Ok(kind);
}

// The arrays of an hvcC record from offset 23, each into its own buffer,
// within one budget for all four.
function hevc_arrays(input: [u8], limits: frame.Limits): Result[Arrays, frame.VideoError] {
  let vps: u8[] = [];
  let sps: u8[] = [];
  let pps: u8[] = [];
  let sei: u8[] = [];
  let at = 23;
  for _ in 0..input[22] as i32 {
    if (input.len() - at < 3) {
      return Err(frame.Truncated);
    }
    let kind = hevc_array_kind(input, at)?;
    let count = wire.be(input, at + 1, 2) as i32;
    let used = vps.len() + sps.len() + pps.len() + sei.len();
    if (kind == 32) {
      let budget = limits.max_packet_bytes - used + vps.len();
      let units = read_units(input, at + 3, count, kind, true, vps, budget)?;
      vps = units.bytes;
      at = units.next_offset;
    } else if (kind == 33) {
      let budget = limits.max_packet_bytes - used + sps.len();
      let units = read_units(input, at + 3, count, kind, true, sps, budget)?;
      sps = units.bytes;
      at = units.next_offset;
    } else if (kind == 34) {
      let budget = limits.max_packet_bytes - used + pps.len();
      let units = read_units(input, at + 3, count, kind, true, pps, budget)?;
      pps = units.bytes;
      at = units.next_offset;
    } else {
      let budget = limits.max_packet_bytes - used + sei.len();
      let units = read_units(input, at + 3, count, kind, true, sei, budget)?;
      sei = units.bytes;
      at = units.next_offset;
    }
  }
  if (at != input.len()) {
    return Err(frame.Header);
  }
  return Ok(Arrays { vps: vps, sps: sps, pps: pps, sei: sei });
}

// The arrays in VPS, SPS, PPS, SEI order as one buffer.
function joined(a: Arrays): u8[] {
  let output = a.vps;
  for b in a.sps {
    output = output.append(b);
  }
  for b in a.pps {
    output = output.append(b);
  }
  for b in a.sei {
    output = output.append(b);
  }
  return output;
}

pub function hevc_config(input: [u8], limits: frame.Limits): Result[Config, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() < 23) {
    return Err(frame.Truncated);
  }
  if (input[0] != 1) {
    return Err(frame.Unsupported);
  }
  if (!hevc_header_ok(input)) {
    return Err(frame.Header);
  }
  let prefix = (input[21] & 3u8) as i32 + 1;
  length_size(prefix)?;
  let a = hevc_arrays(input, limits)?;
  if (a.vps.len() == 0 || a.sps.len() == 0 || a.pps.len() == 0) {
    return Err(frame.Header);
  }
  return Ok(Config { length_size: prefix, csd0: joined(a), csd1: [] });
}
```
