# Vorbis limits and shared values

These values bound encoded packets, decoded PCM, setup tables, and comment metadata.
Setup budgets apply across all codebooks.
The codec uses interleaved float PCM and the shared audio error categories.

This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/pcm";

pub struct Limits { audio: pcm.Limits, max_setup_bytes: i32, max_entries: i32, max_vectors: i32, max_nodes: i32, max_comments: i32, max_comment_bytes: i32 }

pub struct Budget { entries: i32, vectors: i32, nodes: i32 }

pub struct Info { sample_rate: i32, channels: i32, small_exponent: i32, large_exponent: i32, bitrate_upper: i32, bitrate_nominal: i32, bitrate_lower: i32 }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_setup_bytes: 4194304, max_entries: 1048576, max_vectors: 4194304, max_nodes: 2097152, max_comments: 65536, max_comment_bytes: 4194304 };
}

pub function valid(limits: Limits): boolean {
  return limits.audio.max_input_bytes >= 0 && limits.audio.max_samples >= 0 && limits.max_setup_bytes >= 1 && limits.max_entries >= 1 && limits.max_vectors >= 0 && limits.max_nodes >= 1 && limits.max_comments >= 0 && limits.max_comment_bytes >= 0;
}

pub function budget(): Budget {
  return Budget { entries: 0, vectors: 0, nodes: 0 };
}

pub function ilog(value: i32): i32 {
  let bits = 0;
  while (value > 0) {
    value >>= 1;
    bits += 1;
  }
  return bits;
}
```
