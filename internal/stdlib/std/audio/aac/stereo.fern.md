# AAC stereo tools

This module reads common-window stereo masks and applies mid-side and intensity stereo.
Channel streams retain their scalefactor bands and independent temporal noise shaping filters.
Correlated noise bands are reconstructed during spectral decode.

This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's AAC channel-pair implementation.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// Previous author: Kostya Shishkov <kostya.shiskov@gmail.com>.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/bits";
import "std/audio/aac/ics";
import "std/audio/aac/tables";
import "std/audio/pcm";

pub struct Mask { kind: i32, bands: i32[] }

pub function read(input: [u8], reader: bits.Reader, info: ics.Info, rate: i32): Result[(bits.Reader, Mask), pcm.AudioError] {
  if (!ics.valid_info(info, rate)) {
    return Err(pcm.Header);
  }
  let (b1, kind) = bits.read(input, reader, 2)?;
  reader = b1;
  if (kind == 3u32) {
    return Err(pcm.Header);
  }
  let bands: i32[] = [];
  for _ in 0..(info.groups.len() - 1) * info.max_sfb {
    let used = if (kind == 2u32) { 1 } else { 0 };
    if (kind == 1u32) {
      let (next, value) = bits.read(input, reader, 1)?;
      reader = next;
      used = value as i32;
    }
    bands = bands.append(used);
  }
  return Ok((reader, Mask { kind: kind as i32, bands: bands }));
}

function intensity(scale: f32, book: i32, kind: i32, used: i32): f32 {
  let direction = if (book == 15) { 1.0f32 } else { -1.0f32 };
  return direction * (if (kind == 1 && used != 0) { -1.0f32 } else { 1.0f32 }) * scale;
}

// Apply stereo tools in one pass over the checked grouped spectral bands.
// fern-lint: allow cyclomatic-complexity
pub function apply(own left: ics.Channel, own right: ics.Channel, mask: Mask, rate: i32): Result[(ics.Channel, ics.Channel), pcm.AudioError] {
  let ics.Channel { info: li, books: lb, scales: ls, coefficients: lc, filters: lt } = left;
  let ics.Channel { info: ri, books: rb, scales: rs, coefficients: rc, filters: rt } = right;
  if (!ics.valid_info(li, rate) || !ics.valid_info(ri, rate) || mask.kind < 0 || mask.kind > 2) {
    return Err(pcm.Header);
  }
  if (li.sequence != ri.sequence || li.shape != ri.shape || li.max_sfb != ri.max_sfb || li.groups != ri.groups) {
    return Err(pcm.Header);
  }
  let count = (li.groups.len() - 1) * li.max_sfb;
  if (lb.len() != count || rb.len() != count || ls.len() != count || rs.len() != count || mask.bands.len() != count || lc.len() < 1024 || rc.len() < 1024) {
    return Err(pcm.Header);
  }
  let bands = tables.bands(rate, li.sequence == 2);
  for group in 0..li.groups.len() - 1 {
    for sfb in 0..li.max_sfb {
      let band = group * li.max_sfb + sfb;
      for window in li.groups[group]..li.groups[group + 1] {
        let start = window * 128 + tables.BANDS[bands.start + sfb];
        let end = window * 128 + tables.BANDS[bands.start + sfb + 1];
        if (rb[band] == 14 || rb[band] == 15) {
          let factor = intensity(rs[band], rb[band], mask.kind, mask.bands[band]);
          for at in start..end {
            rc = rc.with(at, lc[at] * factor);
          }
        } else if (lb[band] != 13 && rb[band] != 13 && mask.bands[band] != 0) {
          for at in start..end {
            let mid = lc[at];
            let side = rc[at];
            lc = lc.with(at, mid + side);
            rc = rc.with(at, mid - side);
          }
        }
      }
    }
  }
  return Ok((ics.Channel { info: li, books: lb, scales: ls, coefficients: lc, filters: lt }, ics.Channel { info: ri, books: rb, scales: rs, coefficients: rc, filters: rt }));
}
```
