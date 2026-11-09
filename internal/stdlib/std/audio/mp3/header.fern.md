# MP3 frame headers

This helper parses Layer III frame headers and derives frame byte counts.
Header inspection does not verify a complete frame, its checksum, or its audio data.
Free-format headers report bitrate zero and byte count zero.
Their frame extent requires a known width or repeated compatible headers.

```fern
<<*>>=
// Parse MPEG Layer III frame headers and their padding bit.
import "std/audio/pcm";

pub struct Header { version: i32, channels: i32, sample_rate: i32, bitrate: i32, frames: i32, bytes: i32, crc: boolean, mode: i32, mode_ext: i32, rate_index: i32, padding: i32 }

// The Layer III header grammar validates dependent version, rate, and layer fields.
// fern-lint: allow cyclomatic-complexity
pub function parse(input: [u8], at: i32): Result[Header, pcm.AudioError] {
  if (at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  let a = input[at + 1] as i32;
  let b = input[at + 2] as i32;
  let c = input[at + 3] as i32;
  if (input[at] != 255 || (a & 224) != 224 || (a >> 3 & 3) == 1 || (a >> 1 & 3) == 0 || b >> 4 == 15 || (b >> 2 & 3) == 3) {
    return Err(pcm.Header);
  }
  if ((a & 6) != 2) {
    return Err(pcm.Unsupported);
  }
  let version = if ((a & 8) != 0) { 1 } else if ((a & 16) != 0) { 2 } else { 25 };
  let rates: i32[] = [44100, 48000, 32000];
  let br1: i32[] = [0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320];
  let br2: i32[] = [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160];
  let bitrate = if (version == 1) { br1[b >> 4] } else { br2[b >> 4] };
  let sample_rate = rates[b >> 2 & 3] / (if (version == 1) { 1 } else if (version == 2) { 2 } else { 4 });
  let mode = c >> 6;
  let cc = if (mode == 3) { 1 } else { 2 };
  let frames = if (version == 1) { 1152 } else { 576 };
  let padding = b >> 1 & 1;
  let bytes = if (bitrate == 0) { 0 } else { (if (version == 1) { 144000 } else { 72000 }) * bitrate / sample_rate + padding };
  let sr = (b >> 2 & 3) + (if ((a & 8) != 0) { 3 } else { 0 }) + (if ((a & 16) != 0) { 3 } else { 0 });
  return Ok(Header { version: version, channels: cc, sample_rate: sample_rate, bitrate: bitrate, frames: frames, bytes: bytes, crc: (a & 1) == 0, mode: mode, mode_ext: c >> 4 & 3, rate_index: sr, padding: padding });
}

pub function side_bytes(h: Header): i32 {
  if (h.version == 1) {
    return if (h.channels == 1) { 17 } else { 32 };
  }
  return if (h.channels == 1) { 9 } else { 17 };
}

pub function midside(h: Header): boolean {
  return h.mode == 1 && (h.mode_ext & 2) != 0;
}

pub function intensity(h: Header): boolean {
  return h.mode == 1 && (h.mode_ext & 1) != 0;
}
```
