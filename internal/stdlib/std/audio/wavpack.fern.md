# Native WavPack playback

This module decodes lossless WavPack version 4 files.
Integer PCM uses signed, right-aligned samples at the declared bit depth.
`decode_bits` retains every source IEEE 754 bit, including signed zero and NaN payloads.
Hybrid streams, correction files, and DSD return `Unsupported`.

`inspect` builds a bounded index without PCM decode or an input copy.
The caller must use the same unchanged input view for later reads.
Limits bound input bytes, blocks, metadata entries, and samples in each decoded group.
Whole-file decode also bounds the complete output sample count.

`FileReader.read` reuses caller output storage and keeps one packet scratch array.
Only the prefix with `frame.frames * frame.info.format.channels` samples is valid.
Float samples in this reader use raw IEEE 754 bits in the integer array.
`decode_float` returns float values under Fern's floating-point contract.
That conversion can quiet signaling NaNs.

`seek` selects an exact sample frame, and the next read verifies its complete compressed group.
The module performs no file or device I/O.

```fern
<<*>>=
// Bounded native lossless WavPack indexing, decode, and sample-accurate seeks.
import "std/audio/pcm";
import "std/audio/wavpack/block";
import "std/audio/wavpack/config";
import "std/audio/wavpack/packet";
import "std/audio/wavpack/tags";

pub struct Limits { audio: pcm.Limits, max_blocks: i32, max_metadata: i32 }

pub struct Info { format: pcm.Format, floating: boolean, norm_exp: i32, channel_mask: u32, channel_identities: u8[], total_frames: i64 }

struct Group { first: i32, count: i32, frame: i64, frames: i32 }

struct Scan { layout: config.Layout, blocks: block.Block[], groups: Group[], group_first: i32, group_frames: i32, group_channels: i32, channels: i32, frame: i64, declared_total: i64 }

pub opaque struct Index { info: Info, blocks: block.Block[], groups: Group[], input_bytes: i32, limits: Limits }

pub opaque struct FileReader { index: Index, group: i32, skip: i32, scratch: i32[] }

pub struct Frame { info: Info, first_frame: i64, frames: i32, samples: i32[] }

pub struct Decoded { info: Info, samples: i32[] }

pub struct FloatDecoded { info: Info, samples: f32[] }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_blocks: 65536, max_metadata: 4096 };
}

function close_group(groups: Group[], first: i32, blocks: i32, frame: i64, frames: i32, channels: i32, expected: i32, limits: Limits): Result[Group[], pcm.AudioError] {
  if (channels != expected) {
    return Err(pcm.Header);
  }
  let _ = pcm.sample_count(frames, channels, limits.audio)?;
  return Ok(groups.append(Group { first: first, count: blocks - first, frame: frame, frames: frames }));
}

function compatible(input: [u8], value: block.Block, first: pcm.Format, floating: boolean, norm: i32): Result[void, pcm.AudioError] {
  if (value.frames == 0) {
    return packet.validate_metadata(value);
  }
  let rate = config.rate(input, value, first.sample_rate)?;
  let format = config.format(value, rate)?;
  if (format.bits_per_sample != first.bits_per_sample || (value.flags & 128u32) != 0u32 != floating || config.norm(input, value)? != norm) {
    return Err(pcm.Unsupported);
  }
  return packet.validate_metadata(value);
}

function merge_total(previous: i64, current: i64): Result[i64, pcm.AudioError] {
  if (current < 0i64) {
    return Ok(previous);
  }
  if (previous >= 0i64 && previous != current) {
    return Err(pcm.Header);
  }
  return Ok(current);
}

function finish_group(own state: Scan, limits: Limits): Result[Scan, pcm.AudioError] {
  let channels = state.channels;
  if (channels == 0) {
    channels = if (state.layout.declared) { state.layout.channels } else { state.group_channels };
  }
  let groups = close_group(state.groups, state.group_first, state.blocks.len(), state.frame, state.group_frames, state.group_channels, channels, limits)?;
  return Ok(Scan { ...state, groups: groups, channels: channels, frame: state.frame + state.group_frames as i64, group_first: -1 });
}

function push_audio(own state: Scan, value: block.Block, limits: Limits): Result[Scan, pcm.AudioError] {
  let initial = (value.flags & 2048u32) != 0u32;
  let final = (value.flags & 4096u32) != 0u32;
  if (initial != state.group_first < 0 || value.index != state.frame) {
    return Err(pcm.Header);
  }
  if (initial) {
    state = Scan { ...state, group_first: state.blocks.len(), group_frames: value.frames, group_channels: 0 };
  }
  if (value.frames != state.group_frames) {
    return Err(pcm.Header);
  }
  let group_channels = state.group_channels + packet.channels(value);
  if (group_channels > 8) {
    return Err(pcm.Unsupported);
  }
  state = Scan { ...state, blocks: state.blocks.append(value), group_channels: group_channels };
  if (final) {
    return finish_group(state, limits);
  }
  return Ok(state);
}

function push_block(input: [u8], own state: Scan, value: block.Block, format: pcm.Format, floating: boolean, norm: i32, limits: Limits): Result[Scan, pcm.AudioError] {
  compatible(input, value, format, floating, norm)?;
  let layout = config.layout(input, value, state.layout)?;
  let total = if (value.frames == 0) { state.declared_total } else { merge_total(state.declared_total, value.total)? };
  state = Scan { ...state, layout: layout, declared_total: total };
  if (value.frames != 0) {
    return push_audio(state, value, limits);
  }
  if (state.group_first >= 0) {
    return Err(pcm.Header);
  }
  return Ok(Scan { ...state, blocks: state.blocks.append(value) });
}

function speaker_mask(layout: config.Layout, channels: i32): u32 {
  if (layout.declared) {
    return layout.mask;
  }
  if (channels == 1) {
    return 4u32;
  }
  return if (channels == 2) { 3u32 } else { 0u32 };
}

function finish_scan(state: Scan, input_bytes: i32, format: pcm.Format, floating: boolean, norm: i32, limits: Limits): Result[Index, pcm.AudioError] {
  if (state.group_first >= 0 || state.declared_total >= 0i64 && state.frame != state.declared_total) {
    return Err(pcm.Truncated);
  }
  let channels = state.channels;
  if (channels == 0) {
    channels = if (state.layout.declared) { state.layout.channels } else { format.channels };
  }
  if (state.layout.declared && channels != state.layout.channels) {
    return Err(pcm.Unsupported);
  }
  let info = Info { format: pcm.Format { ...format, channels: channels }, floating: floating, norm_exp: norm, channel_mask: speaker_mask(state.layout, channels), channel_identities: state.layout.identities, total_frames: state.frame };
  return Ok(Index { info: info, blocks: state.blocks, groups: state.groups, input_bytes: input_bytes, limits: limits });
}

function check_limits(input: [u8], limits: Limits): Result[void, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_blocks < 1 || limits.max_metadata < 0) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

function read_format(input: [u8], first: block.Block): Result[(pcm.Format, i32), pcm.AudioError] {
  let format = config.format(first, config.rate(input, first, 0)?)?;
  return Ok((format, config.norm(input, first)?));
}

function indexed_block(input: [u8], at: i32, first: block.Block, end: i32, limits: Limits): Result[block.Block, pcm.AudioError] {
  let value = if (at == first.at) { first } else { block.read(input, at, limits.max_metadata, limits.audio)? };
  if (value.end > end) {
    return Err(pcm.Truncated);
  }
  return Ok(value);
}

function audio_header(input: [u8], end: i32, limits: Limits): Result[block.Block, pcm.AudioError] {
  let at = 0;
  let count = 0;
  while (at < end) {
    if (count >= limits.max_blocks) {
      return Err(pcm.Limit);
    }
    let value = block.read(input, at, limits.max_metadata, limits.audio)?;
    if (value.end > end) {
      return Err(pcm.Truncated);
    }
    if (value.frames > 0) {
      return Ok(value);
    }
    at = value.end;
    count += 1;
  }
  return Err(pcm.Truncated);
}

pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  check_limits(input, limits)?;
  let end = tags.audio_end(input, limits.max_metadata)?;
  let first = audio_header(input, end, limits)?;
  let (format, norm) = read_format(input, first)?;
  let floating = (first.flags & 128u32) != 0u32;
  let layout = config.Layout { channels: format.channels, mask: 0u32, identities: [], declared: false };
  let state = Scan { layout: layout, blocks: [], groups: [], group_first: -1, group_frames: 0, group_channels: 0, channels: 0, frame: 0i64, declared_total: -1i64 };
  let at = 0;
  while (at < end) {
    if (state.blocks.len() >= limits.max_blocks) {
      return Err(pcm.Limit);
    }
    let value = indexed_block(input, at, first, end, limits)?;
    at = value.end;
    state = push_block(input, state, value, format, floating, norm, limits)?;
  }
  return finish_scan(state, input.len(), format, floating, norm, limits);
}

pub function (value: Index) information(): Info {
  return value.info;
}

pub function (value: Index) group_count(): i32 {
  return value.groups.len();
}

pub function reader(index: Index): FileReader {
  return FileReader { index: index, group: 0, skip: 0, scratch: [] };
}

pub function (value: FileReader) information(): Info {
  return value.index.info;
}

pub function (own value: FileReader) seek(frame: i64): Result[FileReader, pcm.AudioError] {
  if (frame < 0i64 || frame > value.index.info.total_frames) {
    return Err(pcm.Sample);
  }
  if (frame == value.index.info.total_frames) {
    return Ok(FileReader { ...value, group: value.index.groups.len(), skip: 0 });
  }
  let low = 0;
  let high = value.index.groups.len();
  while (low < high) {
    let mid = low + (high - low) / 2;
    let group = value.index.groups[mid];
    if (group.frame + group.frames as i64 <= frame) {
      low = mid + 1;
    } else {
      high = mid;
    }
  }
  let group = value.index.groups[low];
  return Ok(FileReader { ...value, group: low, skip: (frame - group.frame) as i32 });
}

function copy_packet(own output: i32[], scratch: [i32], value: block.Block, channels: i32, to: i32, channel: i32, skip: i32): i32[] {
  let width = packet.channels(value);
  let padding = if ((value.flags & 128u32) != 0u32) { 0 } else { (value.flags >> 13u32 & 31u32) as i32 };
  for frame in skip..value.frames {
    for c in 0..width {
      output = output.with(to + (frame - skip) * channels + channel + c, scratch[frame * width + c] >> padding);
    }
  }
  return output;
}

function decode_group(input: [u8], index: Index, group: Group, skip: i32, to: i32, own scratch: i32[], own output: i32[]): Result[(i32[], i32[]), pcm.AudioError] {
  if (input.len() != index.input_bytes) {
    return Err(pcm.Header);
  }
  let channels = index.info.format.channels;
  let count = pcm.sample_count(group.frames - skip, channels, index.limits.audio)?;
  if (to < 0 || to as i64 + count as i64 > 2147483647i64) {
    return Err(pcm.Limit);
  }
  while (output.len() < to + count) {
    output = output.append(0);
  }
  let channel = 0;
  for i in group.first..group.first + group.count {
    let value = index.blocks[i];
    scratch = packet.decode(input, value, index.limits.audio, scratch)?;
    output = copy_packet(output, scratch, value, channels, to, channel, skip);
    channel += packet.channels(value);
  }
  return Ok((scratch, output));
}

pub function (own value: FileReader) read(input: [u8], own output: i32[]): Result[(FileReader, Frame), pcm.AudioError] {
  let FileReader { index, group: at, skip, scratch } = value;
  if (input.len() != index.input_bytes) {
    return Err(pcm.Header);
  }
  if (at == index.groups.len()) {
    return Ok((FileReader { index: index, group: at, skip: 0, scratch: scratch }, Frame { info: index.info, first_frame: index.info.total_frames, frames: 0, samples: output }));
  }
  let group = index.groups[at];
  let (reused, samples) = decode_group(input, index, group, skip, 0, scratch, output)?;
  return Ok((FileReader { index: index, group: at + 1, skip: 0, scratch: reused }, Frame { info: index.info, first_frame: group.frame + skip as i64, frames: group.frames - skip, samples: samples }));
}

function decode_all(input: [u8], index: Index): Result[Decoded, pcm.AudioError] {
  let channels = index.info.format.channels;
  if (index.info.total_frames > index.limits.audio.max_samples as i64 / (channels as i64)) {
    return Err(pcm.Limit);
  }
  let output = pcm.zeros(index.info.total_frames as i32 * channels);
  let scratch: i32[] = [];
  for group in index.groups {
    let (reused, samples) = decode_group(input, index, group, 0, group.frame as i32 * channels, scratch, output)?;
    scratch = reused;
    output = samples;
  }
  return Ok(Decoded { info: index.info, samples: output });
}

pub function decode(input: [u8], limits: Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  if (index.info.floating) {
    return Err(pcm.Unsupported);
  }
  return decode_all(input, index);
}

pub function decode_bits(input: [u8], limits: Limits): Result[Decoded, pcm.AudioError] {
  return decode_all(input, inspect(input, limits)?);
}

pub function decode_float(input: [u8], limits: Limits): Result[FloatDecoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  if (!index.info.floating) {
    return Err(pcm.Unsupported);
  }
  let decoded = decode_all(input, index)?;
  let samples: f32[] = [];
  for value in decoded.samples {
    samples = samples.append(f32_from_bits(value));
  }
  return Ok(FloatDecoded { info: decoded.info, samples: samples });
}
```
