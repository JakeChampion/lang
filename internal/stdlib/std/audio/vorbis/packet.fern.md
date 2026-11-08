# Vorbis audio packet synthesis

This module reads floor data and spectral residues, reverses channel coupling,
and synthesizes interleaved PCM. Inverse coupling follows the reverse order
required by the Vorbis specification, including steps that share channels.

An incomplete floor becomes unused. An early residue end preserves its completed values.
Packet type and mode fields must be complete.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/bits";
import "std/audio/vorbis/codebook";
import "std/audio/vorbis/dsp";
import "std/audio/vorbis/floor0";
import "std/audio/vorbis/floor1";
import "std/audio/vorbis/residue";
import "std/audio/vorbis/setup";
import "std/audio/vorbis/types";
import "std/audio/pcm";
import "std/f64";

struct Floors { reader: bits.Reader, active: i32[], output: f32[], curve: f32[] }

pub struct Run { work: dsp.Work, spectra: f32[], floors: f32[], curve: f32[], exponent: i32 }

// Handle both floor types and legal early endings before returning one curve.
// fern-lint: allow cyclomatic-complexity
function floor(input: [u8], reader: bits.Reader, value: setup.Floor, books: [codebook.Book], exponent: i32, own curve: f32[]): Result[(bits.Reader, boolean, f32[]), pcm.AudioError] {
  return match (value) {
    setup.Zero(config) => match (floor0.read(input, reader, config, books)) {
      Ok((next, data)) => {
        let used = data.amplitude != 0u64;
        Ok((next, used, floor0.synth(data, config, exponent, curve)?))
      },
      Err(pcm.Truncated) => Ok((bits.Reader { at: input.len(), bit: 0 }, false, curve)),
      Err(error) => Err(error),
    },
    setup.One(config) => match (floor1.read(input, reader, config, books)) {
      Ok((next, data)) => {
        let used = data.used;
        Ok((next, used, floor1.synth(data, config, 1 << exponent - 1, curve)?))
      },
      Err(pcm.Truncated) => Ok((bits.Reader { at: input.len(), bit: 0 }, false, curve)),
      Err(error) => Err(error),
    },
  };
}

function floors(input: [u8], reader: bits.Reader, config: setup.Setup, mapping: setup.Mapping, channels: i32, exponent: i32, stride: i32, own output: f32[], own curve: f32[]): Result[Floors, pcm.AudioError] {
  let active: i32[] = [];
  let n = 1 << exponent - 1;
  for channel in 0..channels {
    let index = mapping.submaps[mapping.multiplex[channel]].floor;
    let (next, used, values) = floor(input, reader, config.floors[index], config.books, exponent, curve)?;
    reader = next;
    curve = values;
    active = active.append(if (used) { 1 } else { 0 });
    for i in 0..n {
      output = output.with(channel * stride + i, if (used) { curve[i] } else { 0.0f32 });
    }
  }
  return Ok(Floors { reader: reader, active: active, output: output, curve: curve });
}

// Keep all four sign quadrants beside channel and finite-output checks.
// fern-lint: allow cyclomatic-complexity
pub function uncouple(own values: f32[], couplings: [setup.Couple], channels: i32, n: i32, stride: i32): Result[f32[], pcm.AudioError] {
  if (channels < 1 || channels > 8 || n < 1 || n > 4096 || stride < n || stride > 4096 || values.len() < channels * stride) {
    return Err(pcm.Buffer);
  }
  let step = couplings.len();
  while (step > 0) {
    step -= 1;
    let pair = couplings[step];
    if (pair.magnitude < 0 || pair.magnitude >= channels || pair.angle < 0 || pair.angle >= channels || pair.magnitude == pair.angle) {
      return Err(pcm.Header);
    }
    let m_at = pair.magnitude * stride;
    let a_at = pair.angle * stride;
    for i in 0..n {
      let m = values[m_at + i];
      let a = values[a_at + i];
      let magnitude = if (a > 0.0f32) { m } else if (m > 0.0f32) { m + a } else { m - a };
      let angle = if (a <= 0.0f32) { m } else if (m > 0.0f32) { m - a } else { m + a };
      if (!magnitude.is_finite() || !angle.is_finite()) {
        return Err(pcm.Sample);
      }
      values = values.with(m_at + i, magnitude).with(a_at + i, angle);
    }
  }
  return Ok(values);
}

pub function output_channel(channels: i32, channel: i32): i32 {
  return match (channels) {
    3 => [0, 2, 1][channel],
    5 => [0, 2, 1, 3, 4][channel],
    6 => [0, 2, 1, 4, 5, 3][channel],
    7 => [0, 2, 1, 5, 6, 4, 3][channel],
    8 => [0, 2, 1, 6, 7, 4, 5, 3][channel],
    _ => channel,
  };
}

function mode(input: [u8], config: setup.Setup): Result[(bits.Reader, setup.Mode), pcm.AudioError] {
  let (b1, kind) = bits.read(input, bits.create(), 1)?;
  if (kind != 0u32) {
    return Err(pcm.Header);
  }
  let (b2, index) = bits.read(input, b1, types.ilog(config.modes.len() - 1))?;
  if (index >= config.modes.len() as u32) {
    return Err(pcm.Header);
  }
  return Ok((b2, config.modes[index as i32]));
}

pub function exponent(input: [u8], info: types.Info, config: setup.Setup): Result[i32, pcm.AudioError] {
  let (_, current) = mode(input, config)?;
  return Ok(if (current.long) { info.large_exponent } else { info.small_exponent });
}

// Keep packet stage order explicit through nonzero propagation and submap decoding.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], info: types.Info, config: setup.Setup, previous: i32, own work: dsp.Work, own spectra: f32[], own floor_values: f32[], own curve: f32[]): Result[Run, pcm.AudioError] {
  let (b2, mode) = mode(input, config)?;
  let exponent = if (mode.long) { info.large_exponent } else { info.small_exponent };
  let reader = b2;
  if (mode.long) {
    reader = bits.skip(input, reader, 2)?;
  }
  let channels = info.channels;
  let stride = 1 << info.large_exponent - 1;
  let n = 1 << exponent - 1;
  if (spectra.len() < channels * stride || floor_values.len() < channels * stride || curve.len() < n) {
    return Err(pcm.Buffer);
  }
  let mapping = config.mappings[mode.mapping];
  let Floors { reader: next, active, output, curve: curve_output } = floors(input, reader, config, mapping, channels, exponent, stride, floor_values, curve)?;
  reader = next;
  for pair in mapping.couplings {
    if (active[pair.magnitude] != 0 || active[pair.angle] != 0) {
      active = active.with(pair.magnitude, 1).with(pair.angle, 1);
    }
  }
  for channel in 0..channels {
    for i in 0..n {
      spectra = spectra.with(channel * stride + i, 0.0f32);
    }
  }
  let eof = false;
  for submap in 0..mapping.submaps.len() {
    let selected: i32[] = [];
    for channel in 0..channels {
      if (mapping.multiplex[channel] == submap) {
        selected = selected.append(channel);
      }
    }
    if (selected.len() == 0 || eof) {
      continue;
    }
    let residue.Work { reader: end, output: values, eof: ended } = residue.read(input, reader, config.residues[mapping.submaps[submap].residue], config.books, selected, active, n, stride, spectra)?;
    reader = end;
    spectra = values;
    eof = ended;
  }
  spectra = uncouple(spectra, mapping.couplings, channels, n, stride)?;
  for channel in 0..channels {
    let base = channel * stride;
    for i in 0..n {
      let value = spectra[base + i] * output[base + i];
      if (!value.is_finite()) {
        return Err(pcm.Sample);
      }
      spectra = spectra.with(base + i, value);
    }
    work = dsp.synth(spectra[base:base + n], work, output_channel(channels, channel), channels, info.large_exponent, exponent, previous)?;
  }
  return Ok(Run { work: work, spectra: spectra, floors: output, curve: curve_output, exponent: exponent });
}
```
