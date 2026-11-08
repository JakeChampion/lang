# The Opus packet

This module reads and writes the framing of an Opus packet (RFC 6716, section 3). It is a port of `opus_packet_parse_impl`, the `opus_packet_get_*` functions, and `opus_repacketizer_out_range_impl` from libopus 1.4. For the same bytes, it gives the same frames, sizes and errors as libopus. For the same frames, it writes the same packet.

## Model

A packet starts with a table-of-contents byte, the TOC. The TOC gives the mode, the audio bandwidth, the frame duration and the channel count. Its two low bits give the frame-count code:

- Code 0 has one frame.
- Code 1 has two frames of the same size.
- Code 2 has two frames of different sizes. The size of the first frame follows the TOC.
- Code 3 has 1 to 48 frames. A second byte gives the count, a padding flag and a flag for frames of different sizes. The padding length follows, then the size of each frame except the last when the sizes differ.

A frame size of 0 to 251 takes one byte. A larger size takes two bytes, and the largest size is 1275.

`parse` gives a `Packet`: the TOC, the frames, the offset of the first frame, and the padding length. `write` gives the smallest packet for a list of frames that share one TOC. With `pad`, it fills the packet to `max` bytes with code 3 padding. `pad` does the same for a packet that is already written.

The bandwidth numbers are the libopus numbers, from 1101 for narrowband to 1105 for fullband. An error has the libopus error number as its `code`.

## Contracts

- `parse` reads a packet of one stream, not a self-delimited packet of a multistream.
- `parse_delimited` reads the first self-delimited stream and returns a normal packet plus its consumed byte count.
- `write` takes 1 to 48 frames of at most 1275 bytes each. Their duration must not be more than 120 ms.
- `write` and `pad` refuse to make a packet of more than `max` bytes.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<errors>>

<<toc>>

<<parse>>

<<write>>
```

### `module introduction`

```fern
<<module introduction>>=
// The Opus packet framing, ported from libopus 1.4.
//
// Copyright (c) 2010-2011 Xiph.Org Foundation, Skype Limited
// Copyright (c) 2011 Jean-Marc Valin
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
//
// - Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//
// - Redistributions in binary form must reproduce the above copyright
// notice, this list of conditions and the following disclaimer in the
// documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER
// OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
import "core/cmp";
import "core/error";
```

### `errors`

```fern
<<errors>>=
pub enum PacketError {
  BadArg,
  BufferTooSmall,
  Invalid,
}

impl error.Error for PacketError {}

impl cmp.Display for PacketError {}

pub function (e: PacketError) message(): string {
  match (e) {
    BadArg => {
      return "bad Opus packet argument";
    },
    BufferTooSmall => {
      return "the Opus packet does not fit in the space";
    },
    Invalid => {
      return "invalid Opus packet";
    }
  }
  return "";
}

pub function (e: PacketError) to_string(): string {
  return e.message();
}

// The libopus error number.
pub function (e: PacketError) code(): i32 {
  match (e) {
    BadArg => {
      return 0 - 1;
    },
    BufferTooSmall => {
      return 0 - 2;
    },
    Invalid => {
      return 0 - 4;
    }
  }
  return 0;
}
```

### `toc`

The top bit of the TOC selects CELT. Otherwise, bits 5 and 6 both set select the hybrid mode, and other values select SILK.

```fern
<<toc>>=
pub const NARROWBAND: i32 = 1101;

pub const MEDIUMBAND: i32 = 1102;

pub const WIDEBAND: i32 = 1103;

pub const SUPERWIDEBAND: i32 = 1104;

pub const FULLBAND: i32 = 1105;

pub enum Mode { Silk, Hybrid, Celt }

pub function mode(toc: u8): Mode {
  let t = toc as i32;
  if ((t & 128) != 0) {
    return Celt;
  }
  if ((t & 96) == 96) {
    return Hybrid;
  }
  return Silk;
}

pub function bandwidth(toc: u8): i32 {
  let t = toc as i32;
  if ((t & 128) != 0) {
    let bw = MEDIUMBAND + (t >> 5 & 3);
    if (bw == MEDIUMBAND) {
      return NARROWBAND;
    }
    return bw;
  }
  if ((t & 96) == 96) {
    if ((t & 16) != 0) {
      return FULLBAND;
    }
    return SUPERWIDEBAND;
  }
  return NARROWBAND + (t >> 5 & 3);
}

pub function stereo(toc: u8): boolean {
  return (toc as i32 & 4) != 0;
}

// The samples in one frame at `rate` samples a second.
pub function samples_per_frame(toc: u8, rate: i32): i32 {
  let t = toc as i32;
  if ((t & 128) != 0) {
    return (rate << (t >> 3 & 3)) / 400;
  }
  if ((t & 96) == 96) {
    if ((t & 8) != 0) {
      return rate / 50;
    }
    return rate / 100;
  }
  let size = t >> 3 & 3;
  if (size == 3) {
    return rate * 60 / 1000;
  }
  return (rate << size) / 100;
}

// The number of frames in a packet.
pub function frame_count(packet: [u8]): Result[i32, PacketError] {
  if (packet.len() < 1) {
    return Err(BadArg);
  }
  let code = packet[0] as i32 & 3;
  if (code == 0) {
    return Ok(1);
  }
  if (code != 3) {
    return Ok(2);
  }
  if (packet.len() < 2) {
    return Err(Invalid);
  }
  return Ok(packet[1] as i32 & 63);
}

// The samples in a packet at `rate` samples a second. A packet of more than
// 120 ms is not valid.
pub function sample_count(packet: [u8], rate: i32): Result[i32, PacketError] {
  match (frame_count(packet)) {
    Ok(count) => {
      let samples = count * samples_per_frame(packet[0], rate);
      if (samples * 25 > rate * 3) {
        return Err(Invalid);
      }
      return Ok(samples);
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(BadArg);
}
```

### `parse`

`size_at` reads one frame size at `at`, from the `len` bytes that are left. It gives the size and the bytes it used, or a size of -1 when the bytes end.

`parse` follows `opus_packet_parse_impl`, and the checks come in the same order, so the same bytes give the same error. A `Layout` holds the state after the frame sizes. It has the position of the first frame and the bytes that are left. It also has the frame sizes and the padding length. Each frame-count code has its own function to read it.

```fern
<<parse>>=
pub struct Packet { toc: u8, frames: u8[][], offset: i32, padding: i32 }

pub struct Delimited { packet: u8[], consumed: i32 }

struct Layout { at: i32, len: i32, sizes: i32[], last: i32, pad: i32 }

function size_at(data: [u8], at: i32, len: i32): (i32, i32) {
  if (len < 1) {
    return (-1, -1);
  }
  let first = data[at] as i32;
  if (first < 252) {
    return (first, 1);
  }
  if (len < 2) {
    return (-1, -1);
  }
  return (4 * (data[at + 1] as i32) + first, 2);
}

// Code 1: two frames of one size.
function layout_two_same(len: i32): Result[Layout, PacketError] {
  if ((len & 1) != 0) {
    return Err(Invalid);
  }
  return Ok(Layout { at: 1, len: len, sizes: [len / 2], last: len / 2, pad: 0 });
}

// Code 2: two frames, and the size of the first.
function layout_two(data: [u8], len: i32): Result[Layout, PacketError] {
  let (size, bytes) = size_at(data, 1, len);
  let left = len - bytes;
  if (size < 0 || size > left) {
    return Err(Invalid);
  }
  return Ok(Layout { at: 1 + bytes, len: left, sizes: [size], last: left - size, pad: 0 });
}

// The padding length at `at`: each byte of 255 adds 254 and one more byte.
// It gives the position after the length, the bytes left, and the padding.
function padding_at(data: [u8], own at: i32, own len: i32): Result[(i32, i32, i32), PacketError] {
  let pad = 0;
  let p = 255;
  while (p == 255) {
    if (len <= 0) {
      return Err(Invalid);
    }
    p = data[at] as i32;
    at += 1;
    len -= 1;
    let tmp = p;
    if (p == 255) {
      tmp = 254;
    }
    len -= tmp;
    pad += tmp;
  }
  return Ok((at, len, pad));
}

// The sizes of all frames but the last, when the sizes can differ.
function sizes_at(data: [u8], own l: Layout, count: i32): Result[Layout, PacketError] {
  let last = l.len;
  let at = l.at;
  let len = l.len;
  let sizes: i32[] = [];
  for i in 0..count - 1 {
    let (size, bytes) = size_at(data, at, len);
    len -= bytes;
    if (size < 0 || size > len) {
      return Err(Invalid);
    }
    at += bytes;
    last = last - bytes - size;
    sizes = sizes.append(size);
  }
  if (last < 0) {
    return Err(Invalid);
  }
  return Ok(Layout { ...l, at: at, len: len, sizes: sizes, last: last });
}

// Code 3: the count byte, the padding length, and the frame sizes.
function layout_many(data: [u8], len: i32, framesize: i32): Result[Layout, PacketError] {
  if (len < 1) {
    return Err(Invalid);
  }
  let ch = data[1] as i32;
  let count = ch & 63;
  if (count <= 0 || framesize * count > 5760) {
    return Err(Invalid);
  }
  let l = Layout { at: 2, len: len - 1, sizes: [], last: 0, pad: 0 };
  if ((ch & 64) != 0) {
    match (padding_at(data, l.at, l.len)) {
      Ok((at, left, pad)) => {
        l = Layout { ...l, at: at, len: left, pad: pad };
      },
      Err(e) => {
        return Err(e);
      }
    }
  }
  if (l.len < 0) {
    return Err(Invalid);
  }
  if ((ch & 128) != 0) {
    return sizes_at(data, l, count);
  }
  return same_sizes(l, count);
}

// The sizes of `count` frames of one size in the bytes that are left.
function same_sizes(own l: Layout, count: i32): Result[Layout, PacketError] {
  let size = l.len / count;
  if (size * count != l.len) {
    return Err(Invalid);
  }
  let sizes: i32[] = [];
  for i in 0..count - 1 {
    sizes = sizes.append(size);
  }
  return Ok(Layout { ...l, sizes: sizes, last: size });
}

function layout(data: [u8]): Result[Layout, PacketError] {
  let len = data.len() - 1;
  let code = data[0] as i32 & 3;
  if (code == 1) {
    return layout_two_same(len);
  }
  if (code == 2) {
    return layout_two(data, len);
  }
  if (code == 3) {
    return layout_many(data, len, samples_per_frame(data[0], 48000));
  }
  return Ok(Layout { at: 1, len: len, sizes: [], last: len, pad: 0 });
}

// Read frame sizes without inferring the final size from the input length.
// The remaining bytes can contain other streams.
function delimited_many(data: [u8]): Result[(Layout, i32, boolean), PacketError] {
  if (data.len() < 2) {
    return Err(Invalid);
  }
  let flags = data[1] as i32;
  let count = flags & 63;
  if (count == 0 || samples_per_frame(data[0], 48000) * count > 5760) {
    return Err(Invalid);
  }
  let l = Layout { at: 2, len: data.len() - 2, sizes: [], last: 0, pad: 0 };
  if ((flags & 64) != 0) {
    let (at, left, pad) = padding_at(data, l.at, l.len)?;
    l = Layout { ...l, at: at, len: left, pad: pad };
  }
  if (l.len < 0) {
    return Err(Invalid);
  }
  let cbr = (flags & 128) == 0;
  if (!cbr) {
    l = sizes_at(data, l, count)?;
  }
  return Ok((l, count, cbr));
}

function delimited_layout(data: [u8]): Result[(Layout, i32, boolean), PacketError] {
  let len = data.len() - 1;
  let code = data[0] as i32 & 3;
  if (code == 3) {
    return delimited_many(data);
  }
  if (code == 2) {
    return Ok((layout_two(data, len)?, 2, false));
  }
  return Ok((Layout { at: 1, len: len, sizes: [], last: len, pad: 0 }, if (code == 1) { 2 } else { 1 }, code == 1));
}

function strip_delimiter(data: [u8], at: i32, bytes: i32, consumed: i32): Delimited {
  let packet: u8[] = [];
  for i in 0..at {
    packet = packet.append(data[i]);
  }
  for i in at + bytes..consumed {
    packet = packet.append(data[i]);
  }
  return Delimited { packet: packet, consumed: consumed };
}

pub function parse_delimited(data: [u8]): Result[Delimited, PacketError] {
  if (data.len() == 0) {
    return Err(Invalid);
  }
  let (l, count, cbr) = delimited_layout(data)?;
  let (size, bytes) = size_at(data, l.at, l.len);
  if (size < 0 || size > l.len - bytes) {
    return Err(Invalid);
  }
  let payload = size;
  if (cbr) {
    payload = size * count;
    if (payload > l.len - bytes) {
      return Err(Invalid);
    }
  } else {
    if (bytes + size > l.last) {
      return Err(Invalid);
    }
    for previous in l.sizes {
      payload += previous;
    }
  }
  let consumed = l.at + bytes + payload + l.pad;
  return Ok(strip_delimiter(data, l.at, bytes, consumed));
}

pub function parse(data: [u8]): Result[Packet, PacketError] {
  if (data.len() == 0) {
    return Err(Invalid);
  }
  match (layout(data)) {
    Ok(l) => {
      // The size of the last frame is not in the packet, so check it here.
      if (l.last > 1275) {
        return Err(Invalid);
      }
      let at = l.at;
      let frames: u8[][] = [];
      for size in l.sizes.append(l.last) {
        let frame: u8[] = [];
        for k in at..at + size {
          frame = frame.append(data[k]);
        }
        frames = frames.append(frame);
        at += size;
      }
      return Ok(Packet { toc: data[0], frames: frames, offset: l.at, padding: l.pad });
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(Invalid);
}
```

### `write`

`write` checks what `opus_repacketizer_cat` checks for each frame, then follows `opus_repacketizer_out_range_impl`. Two frames of one size take code 1, and two of different sizes take code 2. More frames, or a packet with padding, take code 3. When the packet with no padding fills `max` exactly, it takes no padding and keeps code 0, 1 or 2.

```fern
<<write>>=
function put_size(own out: u8[], size: i32): u8[] {
  if (size < 252) {
    return out.append(size as u8);
  }
  let first: i32 = 252 + (size & 3);
  return out.append(first as u8).append((size - first >> 2) as u8);
}

function big(size: i32): i32 {
  if (size >= 252) {
    return 1;
  }
  return 0;
}

// A frame of more than 1275 bytes, or more than 120 ms of frames, is not
// valid.
function frames_valid(toc: u8, frames: [u8[]]): boolean {
  for f in frames {
    if (f.len() > 1275) {
      return false;
    }
  }
  return frames.len() * samples_per_frame(toc, 8000) <= 960;
}

// The header of code 0, 1 or 2 for one or two frames, and the packet size.
// For more frames, the header is empty and the size is 0.
function short_header(base: i32, frames: [u8[]], max: i32): Result[(u8[], i32), PacketError] {
  let len0 = frames[0].len();
  let out: u8[] = [];
  let tot = 0;
  if (frames.len() == 1) {
    tot = len0 + 1;
    out = [base as u8];
  } else if (frames.len() == 2) {
    let len1 = frames[1].len();
    if (len1 == len0) {
      tot = 2 * len0 + 1;
      out = [(base | 1) as u8];
    } else {
      tot = len0 + len1 + 2 + big(len0);
      out = put_size([(base | 2) as u8], len0);
    }
  }
  if (tot > max) {
    return Err(BufferTooSmall);
  }
  return Ok((out, tot));
}

function sizes_differ(frames: [u8[]]): boolean {
  for f in frames {
    if (f.len() != frames[0].len()) {
      return true;
    }
  }
  return false;
}

// The size of the code 3 packet of `frames` with no padding.
function long_size(frames: [u8[]], vbr: boolean): i32 {
  let count = frames.len();
  if (!vbr) {
    return count * frames[0].len() + 2;
  }
  let tot = 2 + frames[count - 1].len();
  for i in 0..count - 1 {
    tot = tot + 1 + big(frames[i].len()) + frames[i].len();
  }
  return tot;
}

// The length of `amount` bytes of padding: each byte of 255 stands for 254
// bytes and itself.
function put_padding(own out: u8[], amount: i32): u8[] {
  let full: i32 = (amount - 1) / 255;
  for i in 0..full {
    out = out.append(255u8);
  }
  return out.append((amount - 255 * full - 1) as u8);
}

// The header of code 3: the count byte, the padding length when `pad` is
// set, and the frame sizes when they differ.
function long_header(base: i32, frames: [u8[]], max: i32, pad: boolean): Result[u8[], PacketError] {
  let count = frames.len();
  let vbr = sizes_differ(frames);
  let tot = long_size(frames, vbr);
  if (tot > max) {
    return Err(BufferTooSmall);
  }
  let flags = count;
  if (vbr) {
    flags = count | 128;
  }
  let amount = 0;
  if (pad) {
    amount = max - tot;
  }
  if (amount != 0) {
    flags |= 64;
  }
  let out = [(base | 3) as u8, flags as u8];
  if (amount != 0) {
    out = put_padding(out, amount);
  }
  if (vbr) {
    for i in 0..count - 1 {
      out = put_size(out, frames[i].len());
    }
  }
  return Ok(out);
}

// The packet of `frames` with the TOC `toc`, in at most `max` bytes. With
// `pad`, the packet is padded to `max` bytes.
pub function write(toc: u8, frames: [u8[]], max: i32, pad: boolean): Result[u8[], PacketError] {
  if (frames.len() == 0) {
    return Err(BadArg);
  }
  if (!frames_valid(toc, frames)) {
    return Err(Invalid);
  }
  match (header(toc as i32 & 252, frames, max, pad)) {
    Ok(head) => {
      return Ok(with_frames(head, frames, max, pad));
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(BadArg);
}

// The header of the smallest code for `frames`, or of code 3 with padding.
function header(base: i32, frames: [u8[]], max: i32, pad: boolean): Result[u8[], PacketError] {
  match (short_header(base, frames, max)) {
    Ok((head, tot)) => {
      if (frames.len() > 2 || pad && tot < max) {
        return long_header(base, frames, max, pad);
      }
      return Ok(head);
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(BadArg);
}

// The frames after the header, then the zeros of the padding.
function with_frames(own out: u8[], frames: [u8[]], max: i32, pad: boolean): u8[] {
  for f in frames {
    for b in f {
      out = out.append(b);
    }
  }
  while (pad && out.len() < max) {
    out = out.append(0u8);
  }
  return out;
}

// `packet` padded to `len` bytes.
pub function pad(packet: [u8], len: i32): Result[u8[], PacketError] {
  if (packet.len() < 1 || packet.len() > len) {
    return Err(BadArg);
  }
  if (packet.len() == len) {
    let same: u8[] = [];
    for b in packet {
      same = same.append(b);
    }
    return Ok(same);
  }
  match (parse(packet)) {
    Ok(p) => {
      return write(p.toc, p.frames, len, true);
    },
    Err(e) => {
      return Err(e);
    }
  }
  return Err(BadArg);
}
```
