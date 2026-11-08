# MPEG-4 audio sample descriptions

This module reads self-contained `mp4a` sample descriptions and their AAC configuration.
Descriptor lengths use bounded base-128 fields.
Each decoder-specific configuration must fit within its enclosing descriptor.

External data references, encryption, and unknown audio descriptions are unsupported.
The AAC configuration reader identifies unsupported AAC profiles explicitly.

```fern
<<*>>=
// Read mp4a sample descriptions and their AAC decoder configuration.

import "std/audio/aac/config" as aac;
import "std/audio/mp4/boxes";
import "std/audio/pcm";
import "std/audio/wire";

struct Descriptor { tag: i32, data: i32, end: i32 }

function descriptor(input: [u8], at: i32, end: i32): Result[Descriptor, pcm.AudioError] {
  if (at < 0 || end > input.len() || at >= end) {
    return Err(pcm.Truncated);
  }
  let tag = input[at] as i32;
  at += 1;
  let size = 0;
  for _ in 0..4 {
    if (at >= end) {
      return Err(pcm.Truncated);
    }
    let value = input[at] as i32;
    at += 1;
    size = size * 128 + (value & 127);
    if ((value & 128) == 0) {
      if (size > end - at) {
        return Err(pcm.Truncated);
      }
      return Ok(Descriptor { tag: tag, data: at, end: at + size });
    }
  }
  return Err(pcm.Header);
}

function stream_start(input: [u8], d: Descriptor): Result[i32, pcm.AudioError] {
  if (d.end - d.data < 3) {
    return Err(pcm.Truncated);
  }
  let flags = input[d.data + 2] as i32;
  let at = d.data + 3;
  if ((flags & 128) != 0) {
    at += 2;
  }
  if ((flags & 64) != 0) {
    if (at >= d.end) {
      return Err(pcm.Truncated);
    }
    at += 1 + input[at] as i32;
  }
  if ((flags & 32) != 0) {
    at += 2;
  }
  if (at > d.end) {
    return Err(pcm.Truncated);
  }
  return Ok(at);
}

// Check the decoder descriptor and each nested configuration boundary together.
// fern-lint: allow cyclomatic-complexity
function decoder_config(input: [u8], d: Descriptor, limits: pcm.Limits): Result[aac.Config, pcm.AudioError] {
  if (d.end - d.data < 13) {
    return Err(pcm.Truncated);
  }
  let object_type = input[d.data];
  if (object_type != 64u8 && object_type != 103u8) {
    return Err(pcm.Unsupported);
  }
  if ((input[d.data + 1] & 253u8) != 21u8) {
    return Err(pcm.Unsupported);
  }
  let at = d.data + 13;
  let found: Option[aac.Config] = None;
  while (at < d.end) {
    let child = descriptor(input, at, d.end)?;
    if (child.tag == 5) {
      if let Some(_) = found {
        return Err(pcm.Header);
      }
      found = Some(aac.inspect(input[child.data:child.end], limits)?);
    }
    at = child.end;
  }
  return match (found) { Some(value) => Ok(value), None => Err(pcm.Header) };
}

// Follow the bounded elementary-stream descriptor hierarchy without recursion.
// fern-lint: allow cyclomatic-complexity
function esds(input: [u8], box: boxes.Box, limits: pcm.Limits): Result[aac.Config, pcm.AudioError] {
  if (boxes.full(input, box, 4)? != 0) {
    return Err(pcm.Unsupported);
  }
  let root = descriptor(input, box.data + 4, box.end)?;
  if (root.tag != 3 || root.end != box.end) {
    return Err(pcm.Header);
  }
  let at = stream_start(input, root)?;
  let found: Option[aac.Config] = None;
  while (at < root.end) {
    let child = descriptor(input, at, root.end)?;
    if (child.tag == 4) {
      if let Some(_) = found {
        return Err(pcm.Header);
      }
      found = Some(decoder_config(input, child, limits)?);
    }
    at = child.end;
  }
  return match (found) { Some(value) => Ok(value), None => Err(pcm.Header) };
}

// Accept the two audio-entry headers and their direct or wave-wrapped descriptor.
// fern-lint: allow cyclomatic-complexity
function sample_entry(input: [u8], entry: boxes.Box, budget: boxes.Budget, limits: pcm.Limits): Result[(boxes.Budget, aac.Config), pcm.AudioError] {
  if (entry.kind != boxes.tag("mp4a")) {
    return Err(pcm.Unsupported);
  }
  if (entry.end - entry.data < 28) {
    return Err(pcm.Truncated);
  }
  if (wire.be(input, entry.data, 6) != 0u64 || wire.be(input, entry.data + 6, 2) != 1u64) {
    return Err(pcm.Unsupported);
  }
  let version = wire.be(input, entry.data + 8, 2);
  let header = if (version == 0u64) { 28 } else if (version == 1u64) { 44 } else { return Err(pcm.Unsupported); };
  if (header > entry.end - entry.data) {
    return Err(pcm.Truncated);
  }
  let (next, children) = boxes.children(input, entry.data + header, entry.end, budget)?;
  if let Some(descriptor) = boxes.one(children, "esds")? {
    return Ok((next, esds(input, descriptor, limits)?));
  }
  let wave = boxes.required(children, "wave")?;
  let (after, nested) = boxes.children(input, wave.data, wave.end, next)?;
  let descriptor = boxes.required(nested, "esds")?;
  return Ok((after, esds(input, descriptor, limits)?));
}

pub function descriptions(input: [u8], box: boxes.Box, budget: boxes.Budget, maximum: i32, limits: pcm.Limits): Result[(boxes.Budget, aac.Config[]), pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let count = wire.be(input, box.data + 4, 4);
  if (count == 0u64) {
    return Err(pcm.Header);
  }
  if (maximum < 0 || count > maximum as u64) {
    return Err(pcm.Limit);
  }
  let (next, entries) = boxes.children(input, box.data + 8, box.end, budget)?;
  if (entries.len() as u64 != count) {
    return Err(pcm.Header);
  }
  budget = next;
  let out: aac.Config[] = [];
  for entry in entries {
    let (after, config) = sample_entry(input, entry, budget, limits)?;
    budget = after;
    out = out.append(config);
  }
  return Ok((budget, out));
}

// Prove that the single sample data reference identifies this same file.
// fern-lint: allow cyclomatic-complexity
pub function data_reference(input: [u8], minf: [boxes.Box], budget: boxes.Budget): Result[boxes.Budget, pcm.AudioError] {
  let dinf = boxes.required(minf, "dinf")?;
  let (b1, children) = boxes.children(input, dinf.data, dinf.end, budget)?;
  let dref = boxes.required(children, "dref")?;
  if (boxes.full(input, dref, 8)? != 0 || wire.be(input, dref.data + 4, 4) != 1u64) {
    return Err(pcm.Unsupported);
  }
  let (b2, references) = boxes.children(input, dref.data + 8, dref.end, b1)?;
  if (references.len() != 1) {
    return Err(pcm.Header);
  }
  let reference = references[0];
  if (reference.kind != boxes.tag("url ") || reference.end - reference.data != 4 || wire.be(input, reference.data, 4) != 1u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(b2);
}
```
