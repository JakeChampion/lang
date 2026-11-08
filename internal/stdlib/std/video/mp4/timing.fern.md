# MP4 video sample times

This module validates composition offsets and sync-sample tables for MP4 video packets.
Version-zero composition offsets are unsigned. Version-one offsets are signed.
Run counts must cover exactly the declared samples.
A missing sync table means that every sample is a sync sample.
An empty sync table means that no sample is a sync sample.

```fern
<<*>>=
// This module validates MP4 video composition times and sync-sample tables.
import "std/audio/mp4/boxes";
import "std/audio/pcm";
import "std/audio/wire";

function rows(input: [u8], box: boxes.Box, width: i32, maximum: i32): Result[(i32, i32), pcm.AudioError] {
  let version = boxes.full(input, box, 8)?;
  let count = wire.be(input, box.data + 4, 4);
  if (maximum < 0 || count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if ((box.end - box.data) as i64 != 8i64 + count as i64 * (width as i64)) {
    return Err(pcm.Header);
  }
  return Ok((version, count as i32));
}

pub function composition(input: [u8], children: [boxes.Box], samples: i32): Result[i64[], pcm.AudioError] {
  if (samples < 0) {
    return Err(pcm.Limit);
  }
  let found = boxes.one(children, "ctts")?;
  if let Some(box) = found {
    return offsets(input, box, samples);
  }
  let output: i64[] = [];
  for _ in 0..samples {
    output = output.append(0i64);
  }
  return Ok(output);
}

function offsets(input: [u8], box: boxes.Box, samples: i32): Result[i64[], pcm.AudioError] {
  let (version, count) = rows(input, box, 8, samples)?;
  if (version != 0 && version != 1) {
    return Err(pcm.Unsupported);
  }
  let output: i64[] = [];
  for i in 0..count {
    let at = box.data + 8 + 8 * i;
    let run = wire.be(input, at, 4);
    let raw = wire.be(input, at + 4, 4);
    if (run == 0u64 || run > (samples - output.len()) as u64) {
      return Err(pcm.Header);
    }
    let offset = if (version == 0) { raw as i64 } else { (raw as i32) as i64 };
    for _ in 0..run as i32 {
      output = output.append(offset);
    }
  }
  if (output.len() != samples) {
    return Err(pcm.Header);
  }
  return Ok(output);
}

pub function sync_samples(input: [u8], children: [boxes.Box], samples: i32): Result[boolean[], pcm.AudioError] {
  if (samples < 0) {
    return Err(pcm.Limit);
  }
  let Some(box) = boxes.one(children, "stss")? else {
    let output: boolean[] = [];
    for _ in 0..samples {
      output = output.append(true);
    }
    return Ok(output);
  };
  return sync_table(input, box, samples);
}

function sync_table(input: [u8], box: boxes.Box, samples: i32): Result[boolean[], pcm.AudioError] {
  let (version, count) = rows(input, box, 4, samples)?;
  if (version != 0) {
    return Err(pcm.Unsupported);
  }
  let output: boolean[] = [];
  let previous = 0;
  for i in 0..count {
    let sample = wire.be(input, box.data + 8 + 4 * i, 4);
    if (sample <= previous as u64 || sample > samples as u64) {
      return Err(pcm.Header);
    }
    for _ in previous..sample as i32 - 1 {
      output = output.append(false);
    }
    output = output.append(true);
    previous = sample as i32;
  }
  for _ in previous..samples {
    output = output.append(false);
  }
  return Ok(output);
}
```
