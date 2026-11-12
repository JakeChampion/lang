# Vorbis spectral residues

This module decodes residue types zero, one, and two in up to eight passes.
Type two writes interleaved vectors directly into planar channel storage.
All codebook references and vector extents are checked before use.

An early packet end preserves the completed residue values and leaves the rest zero.
The returned cursor marks the packet end so subsequent submaps cannot read stale bits.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis residue module.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/bits";
import "std/audio/vorbis/codebook";
import "std/audio/pcm";
import "std/f32";
import "std/f64";

pub struct Setup { kind: i32, begin: i32, end: i32, partition: i32, classifications: i32, classbook: i32, books: i32[], passes: i32 }

pub struct Work { reader: bits.Reader, output: f32[], eof: boolean }

// Validate cascaded stage-book references in their wire order.
// fern-lint: allow cyclomatic-complexity
pub function setup(input: [u8], reader: bits.Reader, kind: i32, books: [codebook.Book]): Result[(bits.Reader, Setup), pcm.AudioError] {
  if (kind < 0 || kind > 2 || books.len() < 1 || books.len() > 256) {
    return Err(pcm.Header);
  }
  let (b1, begin) = bits.read(input, reader, 24)?;
  let (b2, end) = bits.read(input, b1, 24)?;
  let (b3, partition) = bits.read(input, b2, 24)?;
  let (b4, classifications) = bits.read(input, b3, 6)?;
  let (b5, classbook) = bits.read(input, b4, 8)?;
  if (end < begin || classbook >= books.len() as u32 || books[classbook as i32].dimensions < 1) {
    return Err(pcm.Header);
  }
  reader = b5;
  let masks: i32[] = [];
  for _ in 0..classifications as i32 + 1 {
    let (b6, low) = bits.read(input, reader, 3)?;
    let (b7, flag) = bits.read(input, b6, 1)?;
    reader = b7;
    let high = 0u32;
    if (flag != 0u32) {
      let (next, value) = bits.read(input, reader, 5)?;
      reader = next;
      high = value;
    }
    masks = masks.append((high << 3u32 | low) as i32);
  }
  let list: i32[] = [];
  let passes = 0;
  for mask in masks {
    for pass in 0..8 {
      let index = -1;
      if ((mask & 1 << pass) != 0) {
        let (next, value) = bits.read(input, reader, 8)?;
        reader = next;
        if (value >= books.len() as u32) {
          return Err(pcm.Header);
        }
        let book = books[value as i32];
        if (book.lookup == 0 || book.dimensions < 1 || book.vectors.len() as i64 != book.entries as i64 * (book.dimensions as i64)) {
          return Err(pcm.Header);
        }
        index = value as i32;
        if (pass + 1 > passes) {
          passes = pass + 1;
        }
      }
      list = list.append(index);
    }
  }
  return Ok((reader, Setup { kind: kind, begin: begin as i32, end: end as i32, partition: partition as i32 + 1, classifications: classifications as i32 + 1, classbook: classbook as i32, books: list, passes: passes }));
}

function scalar(input: [u8], reader: bits.Reader, book: codebook.Book): Result[(bits.Reader, Option[i32]), pcm.AudioError] {
  return match (codebook.scalar(input, reader, book)) {
    Ok((next, value)) => Ok((next, Some(value))),
    Err(pcm.Truncated) => Ok((bits.Reader { at: input.len(), bit: 0 }, None)),
    Err(error) => Err(error),
  };
}

function classes(own values: i32[], first: i32, remaining: i32, width: i32, count: i32, symbol: i32): Result[i32[], pcm.AudioError] {
  if (symbol < 0) {
    return Err(pcm.Header);
  }
  if (count == 1) {
    return if (symbol == 0) { Ok(values) } else { Err(pcm.Header) };
  }
  let value = symbol;
  let skip = width - remaining;
  while (skip > 0 && value != 0) {
    value /= count;
    skip -= 1;
  }
  let i = remaining;
  while (i > 0) {
    i -= 1;
    if (i < remaining) {
      values = values.with(first + i, value % count);
    }
    value /= count;
  }
  if (value != 0) {
    return Err(pcm.Header);
  }
  return Ok(values);
}

// Keep strided and interleaved addressing beside vector and sample bounds.
// fern-lint: allow cyclomatic-complexity
function partition(input: [u8], reader: bits.Reader, book: codebook.Book, kind: i32, start: i32, size: i32, channels: [i32], channel: i32, stride: i32, own output: f32[]): Result[Work, pcm.AudioError] {
  if (book.lookup == 0 || book.dimensions < 1 || book.vectors.len() as i64 != book.dimensions as i64 * (book.entries as i64)) {
    return Err(pcm.Header);
  }
  let step = size / book.dimensions;
  let words = if (kind == 0) { step } else { size / book.dimensions + (if (size % book.dimensions != 0) { 1 } else { 0 }) };
  for word in 0..words {
    let (next, symbol) = scalar(input, reader, book)?;
    reader = next;
    let entry = match (symbol) { Some(value) => value, None => { return Ok(Work { reader: reader, output: output, eof: true }); } };
    if (entry < 0 || entry >= book.entries) {
      return Err(pcm.Header);
    }
    for component in 0..book.dimensions {
      let index = if (kind == 0) { word + component * step } else { word * book.dimensions + component };
      if (index >= size) {
        break;
      }
      let position = start + index;
      let at = if (kind == 2) { channels[position % channels.len()] * stride + position / channels.len() } else { channel * stride + position };
      if (at < 0 || at >= output.len()) {
        return Err(pcm.Buffer);
      }
      let value = output[at] + book.vectors[entry * book.dimensions + component];
      if (!value.is_finite()) {
        return Err(pcm.Sample);
      }
      output = output.with(at, value);
    }
  }
  return Ok(Work { reader: reader, output: output, eof: false });
}

// Check caller-forged configuration and buffer extents before any array access.
// fern-lint: allow cyclomatic-complexity
function validate(setup: Setup, books: [codebook.Book], channels: [i32], active: [i32], n: i32, stride: i32, output: [f32]): Result[void, pcm.AudioError] {
  if (setup.kind < 0 || setup.kind > 2 || setup.begin < 0 || setup.end < setup.begin || setup.partition < 1 || setup.classifications < 1 || setup.classifications > 64 || setup.classbook < 0 || setup.classbook >= books.len() || setup.books.len() != setup.classifications * 8 || setup.passes < 0 || setup.passes > 8) {
    return Err(pcm.Header);
  }
  if (channels.len() < 1 || channels.len() > 8 || n < 1 || n > 4096 || stride < n || stride > 4096 || active.len() < 1 || active.len() > 8 || output.len() < active.len() * stride) {
    return Err(pcm.Buffer);
  }
  return Ok(());
}

function select(channels: [i32], active: [i32]): Result[i32[], pcm.AudioError] {
  let selected: i32[] = [];
  for i in 0..channels.len() {
    let channel = channels[i];
    if (channel < 0 || channel >= active.len()) {
      return Err(pcm.Header);
    }
    for prior in 0..i {
      if (channels[prior] == channel) {
        return Err(pcm.Header);
      }
    }
    if (active[channel] != 0) {
      selected = selected.append(channel);
    }
  }
  return Ok(selected);
}

// Keep eight-pass classword and channel order explicit, including legal packet ends.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], reader: bits.Reader, setup: Setup, books: [codebook.Book], channels: [i32], active: [i32], n: i32, stride: i32, own output: f32[]): Result[Work, pcm.AudioError] {
  validate(setup, books, channels, active, n, stride, output)?;
  let selected = select(channels, active)?;
  if (selected.len() == 0) {
    return Ok(Work { reader: reader, output: output, eof: false });
  }
  let classbook = books[setup.classbook];
  if (classbook.dimensions < 1 || classbook.dimensions > 65535) {
    return Err(pcm.Header);
  }
  let length = if (setup.kind == 2) { n * channels.len() } else { n };
  let begin = if (setup.begin < length) { setup.begin } else { length };
  let end = if (setup.end < length) { setup.end } else { length };
  let parts = (end - begin) / setup.partition;
  let streams = if (setup.kind == 2) { 1 } else { selected.len() };
  let assignments: i32[] = [];
  for _ in 0..parts * streams {
    assignments = assignments.append(0);
  }
  for pass in 0..setup.passes {
    let first = 0;
    while (first < parts) {
      let count = if (parts - first < classbook.dimensions) { parts - first } else { classbook.dimensions };
      if (pass == 0) {
        for stream in 0..streams {
          let (next, symbol) = scalar(input, reader, classbook)?;
          reader = next;
          let value = match (symbol) { Some(current) => current, None => { return Ok(Work { reader: reader, output: output, eof: true }); } };
          assignments = classes(assignments, stream * parts + first, count, classbook.dimensions, setup.classifications, value)?;
        }
      }
      for part in first..first + count {
        for stream in 0..streams {
          let book = setup.books[assignments[stream * parts + part] * 8 + pass];
          if (book < 0) {
            continue;
          }
          if (book >= books.len()) {
            return Err(pcm.Header);
          }
          let Work { reader: next, output: values, eof } = partition(input, reader, books[book], setup.kind, begin + part * setup.partition, setup.partition, channels, selected[stream], stride, output)?;
          reader = next;
          output = values;
          if (eof) {
            return Ok(Work { reader: reader, output: output, eof: true });
          }
        }
      }
      first += count;
    }
  }
  return Ok(Work { reader: reader, output: output, eof: false });
}
```
