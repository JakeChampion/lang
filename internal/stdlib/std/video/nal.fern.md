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

```fern
<<*>>=
import "std/video/frame";
import "std/audio/wire";

pub struct Config { length_size: i32, csd0: u8[], csd1: u8[] }

struct Units { bytes: u8[], next_offset: i32 }

function length_size(n: i32): Result[void, frame.VideoError] {
  if (n != 1 && n != 2 && n != 4) {
    return Err(frame.Unsupported);
  }
  return Ok(());
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

function read_units(input: [u8], at: i32, count: i32, nal_type: i32, hevc: boolean, own output: u8[], budget: i32): Result[Units, frame.VideoError] {
  let pos = at;
  for _ in 0..count {
    if (input.len() - pos < 2) {
      return Err(frame.Truncated);
    }
    let size = wire.be(input, pos, 2) as i32;
    pos += 2;
    if (size < (if (hevc) { 2 } else { 1 })) {
      return Err(frame.Header);
    }
    if (size > input.len() - pos) {
      return Err(frame.Truncated);
    }
    if ((input[pos] & 128u8) != 0u8) {
      return Err(frame.Header);
    }
    let kind = if (hevc) { input[pos] as i32 >> 1 & 63 } else { input[pos] as i32 & 31 };
    if (kind != nal_type || hevc && (input[pos + 1] & 7u8) == 0u8) {
      return Err(frame.Header);
    }
    output = append_unit(output, input, pos, size, budget)?;
    pos += size;
  }
  return Ok(Units { bytes: output, next_offset: pos });
}

function extended_profile(profile: u8): boolean {
  return profile == 100 || profile == 110 || profile == 122 || profile == 144 || profile == 244 || profile == 44 || profile == 83 || profile == 86 || profile == 118 || profile == 128 || profile == 138 || profile == 139 || profile == 134 || profile == 135;
}

pub function avc_config(input: [u8], limits: frame.Limits): Result[Config, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() < 7) {
    return Err(frame.Truncated);
  }
  if (input[0] != 1) {
    return Err(frame.Unsupported);
  }
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
  at = pps.next_offset;
  let csd0 = sps.bytes;
  if (at < input.len()) {
    if (!extended_profile(input[1])) {
      return Err(frame.Unsupported);
    }
    if (input.len() - at < 4) {
      return Err(frame.Truncated);
    }
    if ((input[at] & 252u8) != 252u8 || (input[at + 1] & 248u8) != 248u8 || (input[at + 2] & 248u8) != 248u8) {
      return Err(frame.Header);
    }
    let extensions = read_units(input, at + 4, input[at + 3] as i32, 13, false, csd0, limits.max_packet_bytes - pps.bytes.len())?;
    csd0 = extensions.bytes;
    at = extensions.next_offset;
  }
  if (at != input.len()) {
    return Err(frame.Header);
  }
  return Ok(Config { length_size: prefix, csd0: csd0, csd1: pps.bytes });
}

pub function hevc_config(input: [u8], limits: frame.Limits): Result[Config, frame.VideoError] {
  frame.check_packet(input, limits)?;
  if (input.len() < 23) {
    return Err(frame.Truncated);
  }
  if (input[0] != 1) {
    return Err(frame.Unsupported);
  }
  if ((input[13] & 240u8) != 240u8 || (input[15] & 252u8) != 252u8 || (input[16] & 252u8) != 252u8 || (input[17] & 248u8) != 248u8 || (input[18] & 248u8) != 248u8) {
    return Err(frame.Header);
  }
  let prefix = (input[21] & 3u8) as i32 + 1;
  length_size(prefix)?;
  let vps: u8[] = [];
  let sps: u8[] = [];
  let pps: u8[] = [];
  let sei: u8[] = [];
  let at = 23;
  for _ in 0..input[22] as i32 {
    if (input.len() - at < 3) {
      return Err(frame.Truncated);
    }
    if ((input[at] & 64u8) != 0u8) {
      return Err(frame.Header);
    }
    let kind = (input[at] & 63u8) as i32;
    if ((kind < 32 || kind > 34) && kind != 39 && kind != 40) {
      return Err(frame.Unsupported);
    }
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
  if (at != input.len() || vps.len() == 0 || sps.len() == 0 || pps.len() == 0) {
    return Err(frame.Header);
  }
  let output = vps;
  for b in sps {
    output = output.append(b);
  }
  for b in pps {
    output = output.append(b);
  }
  for b in sei {
    output = output.append(b);
  }
  return Ok(Config { length_size: prefix, csd0: output, csd1: [] });
}
```
