# MP4 video sample times

This module validates composition offsets and sync-sample tables for MP4 video packets.
Version-zero composition offsets are unsigned. Version-one offsets are signed.
Run counts must cover exactly the declared samples.

A missing sync table means that every sample is a sync sample.
An empty sync table means that no sample is a sync sample.

The public readers return one value for each sample.
The packet indexer applies absent-table defaults directly.
Explicit sync tables use a validated source view without a per-sample temporary array.
Explicit composition tables use validated runs through the same source view.

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
  let table = indexed_composition(input, children, samples)?;
  let output: i64[] = [];
  if (table.count == 0) {
    for _ in 0..samples {
      output = output.append(0i64);
    }
  } else {
    for i in 0..table.count {
      let run = wire.be(input, table.data + 8 * i, 8);
      let count = (run >> 32u64) as i32;
      let offset = composition_offset(run, table.version);
      for _ in 0..count {
        output = output.append(offset);
      }
    }
  }
  return Ok(output);
}

pub(package) struct CompositionTable { data: i32, count: i32, version: i32, first: u64 }

// The complete run coverage is checked before a packet reads an offset.
pub(package) function indexed_composition(input: [u8], children: [boxes.Box], samples: i32): Result[CompositionTable, pcm.AudioError] {
  if (samples < 0) {
    return Err(pcm.Limit);
  }
  let found = boxes.one(children, "ctts")?;
  if let Some(box) = found {
    return offsets(input, box, samples);
  }
  return Ok(CompositionTable { data: 0, count: 0, version: 0, first: samples as u64 << 32u64 });
}

function offsets(input: [u8], box: boxes.Box, samples: i32): Result[CompositionTable, pcm.AudioError] {
  let (version, count) = rows(input, box, 8, samples)?;
  if (version != 0 && version != 1) {
    return Err(pcm.Unsupported);
  }
  let covered = 0;
  for i in 0..count {
    let at = box.data + 8 + 8 * i;
    let run = wire.be(input, at, 4);
    if (run == 0u64 || run > (samples - covered) as u64) {
      return Err(pcm.Header);
    }
    covered += run as i32;
  }
  if (covered != samples) {
    return Err(pcm.Header);
  }
  let first = if (count == 0) { 0u64 } else { wire.be(input, box.data + 8, 8) };
  return Ok(CompositionTable { data: box.data + 8, count: count, version: version, first: first });
}

// The source row packs the sample count above the raw offset.
pub(package) function composition_offset(run: u64, version: i32): i64 {
  return if (version == 0) { (run as u32) as i64 } else { (run as i32) as i64 };
}

pub function sync_samples(input: [u8], children: [boxes.Box], samples: i32): Result[boolean[], pcm.AudioError] {
  let table = indexed_sync_samples(input, children, samples)?;
  let output: boolean[] = [];
  if (table.all_sync) {
    for _ in 0..samples {
      output = output.append(true);
    }
    return Ok(output);
  }
  let previous = 0;
  for i in 0..table.count {
    let sample = sync_sample(input, table, i);
    for _ in previous..sample - 1 {
      output = output.append(false);
    }
    output = output.append(true);
    previous = sample;
  }
  for _ in previous..samples {
    output = output.append(false);
  }
  return Ok(output);
}

pub(package) struct SyncTable { data: i32, count: i32, all_sync: boolean }

// The view preserves the distinction between absent and explicit empty tables.
pub(package) function indexed_sync_samples(input: [u8], children: [boxes.Box], samples: i32): Result[SyncTable, pcm.AudioError] {
  if (samples < 0) {
    return Err(pcm.Limit);
  }
  let Some(box) = boxes.one(children, "stss")? else {
    return Ok(SyncTable { data: 0, count: 0, all_sync: true });
  };
  let (version, count) = rows(input, box, 4, samples)?;
  if (version != 0) {
    return Err(pcm.Unsupported);
  }
  let previous = 0u64;
  for i in 0..count {
    let sample = wire.be(input, box.data + 8 + 4 * i, 4);
    if (sample <= previous || sample > samples as u64) {
      return Err(pcm.Header);
    }
    previous = sample;
  }
  return Ok(SyncTable { data: box.data + 8, count: count, all_sync: count == samples });
}

pub(package) function sync_sample(input: [u8], table: SyncTable, index: i32): i32 {
  if (table.all_sync || index < 0 || index >= table.count) {
    return 0;
  }
  return wire.be(input, table.data + 4 * index, 4) as i32;
}
```
