# MPEG Layer II frame headers

This module parses indexed MPEG-1 and MPEG-2 Layer II headers.
Header inspection does not validate the complete audio payload or its checksum.
Free-format Layer II and the reserved MPEG version are unsupported.

```fern
<<*>>=
// Parse bounded MPEG-1 and MPEG-2 Layer II headers.
import "std/audio/pcm";

pub struct Header { version: i32, channels: i32, sample_rate: i32, bitrate: i32, bytes: i32, crc: boolean, mode: i32, mode_ext: i32, rate_index: i32, padding: i32 }

const RATES: i32[] = [44100, 48000, 32000];

const MPEG1: i32[] = [0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384];

const MPEG2: i32[] = [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160];

function grammar(a: i32, b: i32): Result[void, pcm.AudioError] {
  if ((a & 224) != 224 || (a >> 3 & 3) == 1 || (a & 6) == 0 || b >> 4 == 15 || (b >> 2 & 3) == 3) {
    return Err(pcm.Header);
  }
  if ((a & 6) != 4 || (a >> 3 & 3) == 0 || b >> 4 == 0) {
    return Err(pcm.Unsupported);
  }
  return Ok(());
}

pub function parse(input: [u8], at: i32): Result[Header, pcm.AudioError] {
  if (at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  if (input[at] != 255u8) {
    return Err(pcm.Header);
  }
  let a = input[at + 1] as i32;
  let b = input[at + 2] as i32;
  let c = input[at + 3] as i32;
  grammar(a, b)?;
  let version = if ((a & 8) != 0) { 1 } else { 2 };
  let rate_index = b >> 2 & 3;
  let rate = RATES[rate_index] / version;
  let bitrate = if (version == 1) { MPEG1[b >> 4] } else { MPEG2[b >> 4] };
  let mode = c >> 6;
  let padding = b >> 1 & 1;
  return Ok(Header { version: version, channels: if (mode == 3) { 1 } else { 2 }, sample_rate: rate, bitrate: bitrate, bytes: 144000 * bitrate / rate + padding, crc: (a & 1) == 0, mode: mode, mode_ext: c >> 4 & 3, rate_index: rate_index, padding: padding });
}
```
