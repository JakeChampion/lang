# Vorbis packet setup

This module validates the codebooks, floors, residues, mappings, and modes.
It prepares all stream configuration before any audio packet is decoded.
Shared setup budgets apply before codebook allocation.

Vorbis version one permits time, mapping, window, and transform type zero.
Unknown floor or residue types are invalid headers.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis setup reader.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/bits";
import "std/audio/vorbis/codebook";
import "std/audio/vorbis/floor0";
import "std/audio/vorbis/floor1";
import "std/audio/vorbis/headers";
import "std/audio/vorbis/residue";
import "std/audio/vorbis/types";
import "std/audio/pcm";

pub enum Floor { Zero(floor0.Setup), One(floor1.Setup) }

pub struct Couple { magnitude: i32, angle: i32 }

pub struct Submap { floor: i32, residue: i32 }

pub struct Mapping { couplings: Couple[], multiplex: i32[], submaps: Submap[] }

pub struct Mode { long: boolean, mapping: i32 }

pub struct Setup { books: codebook.Book[], floors: Floor[], residues: residue.Setup[], mappings: Mapping[], modes: Mode[] }

// Validate coupling, multiplex, and submap references in their wire order.
// fern-lint: allow cyclomatic-complexity
function mapping(input: [u8], reader: bits.Reader, channels: i32, floors: i32, residues: i32): Result[(bits.Reader, Mapping), pcm.AudioError] {
  let (b1, kind) = bits.read(input, reader, 16)?;
  let (b2, submap_flag) = bits.read(input, b1, 1)?;
  if (kind != 0u32) {
    return Err(pcm.Header);
  }
  reader = b2;
  let count = 1;
  if (submap_flag != 0u32) {
    let (next, value) = bits.read(input, reader, 4)?;
    reader = next;
    count = value as i32 + 1;
  }
  let (b3, coupling_flag) = bits.read(input, reader, 1)?;
  reader = b3;
  let couplings: Couple[] = [];
  if (coupling_flag != 0u32) {
    let (b4, steps) = bits.read(input, reader, 8)?;
    reader = b4;
    let width = types.ilog(channels - 1);
    for _ in 0..steps as i32 + 1 {
      let (b5, magnitude) = bits.read(input, reader, width)?;
      let (b6, angle) = bits.read(input, b5, width)?;
      reader = b6;
      if (magnitude == angle || magnitude >= channels as u32 || angle >= channels as u32) {
        return Err(pcm.Header);
      }
      couplings = couplings.append(Couple { magnitude: magnitude as i32, angle: angle as i32 });
    }
  }
  let (b7, reserved) = bits.read(input, reader, 2)?;
  reader = b7;
  if (reserved != 0u32) {
    return Err(pcm.Header);
  }
  let multiplex: i32[] = [];
  for _ in 0..channels {
    let index = 0;
    if (count > 1) {
      let (next, value) = bits.read(input, reader, 4)?;
      reader = next;
      index = value as i32;
      if (index >= count) {
        return Err(pcm.Header);
      }
    }
    multiplex = multiplex.append(index);
  }
  let submaps: Submap[] = [];
  for _ in 0..count {
    let (b8, _) = bits.read(input, reader, 8)?;
    let (b9, floor) = bits.read(input, b8, 8)?;
    let (b10, residue) = bits.read(input, b9, 8)?;
    reader = b10;
    if (floor >= floors as u32 || residue >= residues as u32) {
      return Err(pcm.Header);
    }
    submaps = submaps.append(Submap { floor: floor as i32, residue: residue as i32 });
  }
  return Ok((reader, Mapping { couplings: couplings, multiplex: multiplex, submaps: submaps }));
}

function books(input: [u8], reader: bits.Reader, limits: types.Limits): Result[(bits.Reader, codebook.Book[]), pcm.AudioError] {
  let (b1, book_count) = bits.read(input, reader, 8)?;
  reader = b1;
  let budget = types.budget();
  let books: codebook.Book[] = [];
  for _ in 0..book_count as i32 + 1 {
    let (next, book, used) = codebook.read(input, reader, budget, limits)?;
    reader = next;
    budget = used;
    books = books.append(book);
  }
  return Ok((reader, books));
}

function times(input: [u8], reader: bits.Reader): Result[bits.Reader, pcm.AudioError] {
  let (b2, time_count) = bits.read(input, reader, 6)?;
  reader = b2;
  for _ in 0..time_count as i32 + 1 {
    let (next, kind) = bits.read(input, reader, 16)?;
    reader = next;
    if (kind != 0u32) {
      return Err(pcm.Header);
    }
  }
  return Ok(reader);
}

function floors(input: [u8], reader: bits.Reader, info: types.Info, books: [codebook.Book]): Result[(bits.Reader, Floor[]), pcm.AudioError] {
  let (b3, floor_count) = bits.read(input, reader, 6)?;
  reader = b3;
  let floors: Floor[] = [];
  for _ in 0..floor_count as i32 + 1 {
    let (next, kind) = bits.read(input, reader, 16)?;
    reader = next;
    if (kind == 0u32) {
      let (end, value) = floor0.setup(input, reader, info.small_exponent, info.large_exponent, books)?;
      reader = end;
      floors = floors.append(Zero(value));
    } else if (kind == 1u32) {
      let (end, value) = floor1.setup(input, reader, books.len())?;
      reader = end;
      floors = floors.append(One(value));
    } else {
      return Err(pcm.Header);
    }
  }
  return Ok((reader, floors));
}

function residues(input: [u8], reader: bits.Reader, books: [codebook.Book]): Result[(bits.Reader, residue.Setup[]), pcm.AudioError] {
  let (b4, residue_count) = bits.read(input, reader, 6)?;
  reader = b4;
  let residues: residue.Setup[] = [];
  for _ in 0..residue_count as i32 + 1 {
    let (next, kind) = bits.read(input, reader, 16)?;
    let (end, value) = residue.setup(input, next, kind as i32, books)?;
    reader = end;
    residues = residues.append(value);
  }
  return Ok((reader, residues));
}

function mappings(input: [u8], reader: bits.Reader, info: types.Info, floors: [Floor], residues: [residue.Setup]): Result[(bits.Reader, Mapping[]), pcm.AudioError] {
  let (b5, mapping_count) = bits.read(input, reader, 6)?;
  reader = b5;
  let mappings: Mapping[] = [];
  for _ in 0..mapping_count as i32 + 1 {
    let (end, value) = mapping(input, reader, info.channels, floors.len(), residues.len())?;
    reader = end;
    mappings = mappings.append(value);
  }
  return Ok((reader, mappings));
}

function modes(input: [u8], reader: bits.Reader, mappings: [Mapping]): Result[(bits.Reader, Mode[]), pcm.AudioError] {
  let (b6, mode_count) = bits.read(input, reader, 6)?;
  reader = b6;
  let modes: Mode[] = [];
  for _ in 0..mode_count as i32 + 1 {
    let (b7, long) = bits.read(input, reader, 1)?;
    let (b8, window) = bits.read(input, b7, 16)?;
    let (b9, transform) = bits.read(input, b8, 16)?;
    let (b10, index) = bits.read(input, b9, 8)?;
    reader = b10;
    if (window != 0u32 || transform != 0u32 || index >= mappings.len() as u32) {
      return Err(pcm.Header);
    }
    modes = modes.append(Mode { long: long != 0u32, mapping: index as i32 });
  }
  return Ok((reader, modes));
}

// Check complete-packet limits and stream invariants before setup parsing.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], info: types.Info, limits: types.Limits): Result[Setup, pcm.AudioError] {
  if (!types.valid(limits) || input.len() > limits.max_setup_bytes) {
    return Err(pcm.Limit);
  }
  pcm.check_input(input, limits.audio)?;
  if (info.channels < 1 || info.channels > 8 || info.small_exponent < 6 || info.large_exponent < info.small_exponent || info.large_exponent > 13) {
    return Err(pcm.Header);
  }
  headers.signature(input, 5u8)?;
  let (b1, books) = books(input, bits.Reader { at: 7, bit: 0 }, limits)?;
  let b2 = times(input, b1)?;
  let (b3, floors) = floors(input, b2, info, books)?;
  let (b4, residues) = residues(input, b3, books)?;
  let (b5, mappings) = mappings(input, b4, info, floors, residues)?;
  let (reader, modes) = modes(input, b5, mappings)?;
  let (_, framing) = bits.read(input, reader, 1)?;
  if (framing != 1u32) {
    return Err(pcm.Header);
  }
  return Ok(Setup { books: books, floors: floors, residues: residues, mappings: mappings, modes: modes });
}
```
