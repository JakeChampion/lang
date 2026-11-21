# Indexed linear PCM file reads

Use a borrowed seekable Reader for an immutable AIFF, AIFC, AU, CAF PCM or WAVE file.
Build its linear index with `inspect_aiff`, `inspect_au`, `inspect_caf` or `inspect_wav`.
Inspection reads only chunk headers and the format or presentation fields needed for decoding. Each nonempty frame read seeks to its starting sample and reads
only the requested bytes. Empty reads preserve the Reader position and output buffer.
The input byte limit bounds the indexed file and each requested window.
The caller retains and closes the Reader.

```fern
<<*>>=
// Index seekable linear PCM files and decode bounded sample ranges.

import "std/audio/au";
import "std/audio/aiff";
import "std/audio/wav";
import "std/audio/caf/linear" as caf_linear;
import "std/audio/caf/source_chunks";
import "std/audio/wire";
import "std/audio/file";
import "std/audio/linear";
import "std/audio/pcm";
import "std/audio/source";

function bytes_at(reader: Reader, offset: i64, bytes: i32): Result[u8[], source.ReadError] {
  if (bytes == 0) {
    return Ok([]);
  }
  let window = file.create(offset, bytes, source.Limits { chunk_size: source.default_limits().chunk_size, max_buffer_bytes: bytes })?;
  while (!window.ready) {
    window = window.step(reader)?;
  }
  return Ok(window.buffer.bytes);
}

function file_size(reader: Reader, limits: pcm.Limits): Result[i32, source.ReadError] {
  let size = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
  if (size < 0i64 || size > limits.max_input_bytes as i64 || limits.max_samples < 0) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  return Ok(size as i32);
}

pub function inspect_au(reader: Reader, limits: pcm.Limits): Result[linear.Index, source.ReadError] {
  let size = file_size(reader, limits)?;
  if (size < 24) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  let header = bytes_at(reader, 0i64, 24)?;
  return match (au.inspect_header(header, size, limits)) { Ok(index) => Ok(index), Err(e) => Err(source.ReadAudio(e)) };
}

function caf_table(reader: Reader, chunks: source_chunks.File): Result[u8[], source.ReadError] {
  let table: u8[] = [];
  if let Some(chunk) = chunks.packets {
    if (chunk.end - chunk.start != 24i64) {
      return Err(source.ReadAudio(pcm.Header));
    }
    table = bytes_at(reader, chunk.start, 24)?;
  }
  return Ok(table);
}

function check_caf_channels(reader: Reader, chunks: source_chunks.File, channels: i32): Result[void, source.ReadError] {
  if let Some(chunk) = chunks.channels {
    let bytes = (chunk.end - chunk.start) as i32;
    if (bytes < 12) {
      return Err(source.ReadAudio(pcm.Header));
    }
    let prefix = bytes_at(reader, chunk.start, 12)?;
    if let Err(e) = caf_linear.check_layout(prefix, bytes, channels) {
      return Err(source.ReadAudio(e));
    }
  }
  return Ok(());
}

function caf_index(reader: Reader, size: i32, chunks: source_chunks.File, limits: pcm.Limits): Result[linear.Index, source.ReadError] {
  if (chunks.description.end - chunks.description.start != 32i64) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let desc = bytes_at(reader, chunks.description.start, 32)?;
  let table = caf_table(reader, chunks)?;
  let edit = bytes_at(reader, chunks.data.start, 4)?;
  if (wire.be(edit, 0, 4) != 0u64) {
    return Err(source.ReadAudio(pcm.Unsupported));
  }
  let start = chunks.data.start as i32 + 4;
  let index = match (caf_linear.inspect_metadata(size, start, chunks.data.end as i32 - start, desc, table, limits)) {
    Ok(value) => value,
    Err(e) => { return Err(source.ReadAudio(e)); },
  };
  check_caf_channels(reader, chunks, index.information().format.channels)?;
  return Ok(index);
}

pub function inspect_caf(reader: Reader, limits: caf_linear.Limits): Result[linear.Index, source.ReadError] {
  let size = file_size(reader, limits.audio)?;
  let scan = source_chunks.create(size as i64, limits.max_chunks, source.default_limits())?;
  loop {
    let (next, event) = scan.step(reader)?;
    scan = next;
    match (event) {
      source_chunks.Ready(chunks) => {
        return caf_index(reader, size, chunks, limits.audio);
      },
      source_chunks.Finished => {
        return Err(source.ReadAudio(pcm.Header));
      },
      source_chunks.Pending => {}
    }
  }
}

struct Chunk { header: u8[], data: i32, size: i32, next: i32 }

function chunk_at(reader: Reader, at: i32, end: i32, little: boolean): Result[Chunk, source.ReadError] {
  if (end - at < 8) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  let header = bytes_at(reader, at as i64, 8)?;
  let n = if (little) { wire.le(header, 4, 4) } else { wire.be(header, 4, 4) };
  let data = at + 8;
  if (n + n % 2u64 > (end - data) as u64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  let size = n as i32;
  return Ok(Chunk { header: header, data: data, size: size, next: data + size + size % 2 });
}

function check_form(header: [u8], little: boolean, compressed: boolean): Result[void, source.ReadError] {
  if (little) {
    if (wire.tag(header, 0, "RF64") || wire.tag(header, 0, "RIFX")) {
      return Err(source.ReadAudio(pcm.Unsupported));
    }
    if (!wire.tag(header, 0, "RIFF") || !wire.tag(header, 8, "WAVE")) {
      return Err(source.ReadAudio(pcm.Header));
    }
  } else {
    if (!wire.tag(header, 0, "FORM")) {
      return Err(source.ReadAudio(pcm.Header));
    }
    if (!compressed && !wire.tag(header, 8, "AIFF")) {
      return Err(source.ReadAudio(pcm.Unsupported));
    }
  }
  return Ok(());
}

function form(reader: Reader, size: i32, little: boolean): Result[boolean, source.ReadError] {
  let header = bytes_at(reader, 0i64, 12)?;
  let compressed = wire.tag(header, 8, "AIFC");
  check_form(header, little, compressed)?;
  let declared = 8u64 + (if (little) { wire.le(header, 4, 4) } else { wire.be(header, 4, 4) });
  if (declared != size as u64) {
    return Err(source.ReadAudio(if (declared > size as u64) { pcm.Truncated } else { pcm.Header }));
  }
  return Ok(compressed);
}

function wav_format(reader: Reader, chunk: Chunk, previous: Option[linear.Format]): Result[linear.Format, source.ReadError] {
  if let Some(_) = previous {
    return Err(source.ReadAudio(pcm.Header));
  }
  let head = bytes_at(reader, chunk.data as i64, if (chunk.size > 40) { 40 } else { chunk.size })?;
  return match (wav.format_prefix(head, chunk.size)) { Ok(f) => Ok(f), Err(e) => Err(source.ReadAudio(e)) };
}

function wav_data(size: i32, chunk: Chunk, format: Option[linear.Format], index: Option[linear.Index], limits: pcm.Limits): Result[linear.Index, source.ReadError] {
  if let Some(_) = index {
    return Err(source.ReadAudio(pcm.Header));
  }
  let Some(f) = format else {
    return Err(source.ReadAudio(pcm.Header));
  };
  return match (linear.create_extent(size, chunk.data, chunk.size, f, limits)) { Ok(i) => Ok(i), Err(e) => Err(source.ReadAudio(e)) };
}

pub function inspect_wav(reader: Reader, limits: pcm.Limits): Result[linear.Index, source.ReadError] {
  let size = file_size(reader, limits)?;
  form(reader, size, true)?;
  let at = 12;
  let format: Option[linear.Format] = None;
  let index: Option[linear.Index] = None;
  while (at < size) {
    let chunk = chunk_at(reader, at, size, true)?;
    if (wire.tag(chunk.header, 0, "fmt ")) {
      format = Some(wav_format(reader, chunk, format)?);
    } else if (wire.tag(chunk.header, 0, "data")) {
      index = Some(wav_data(size, chunk, format, index, limits)?);
    }
    at = chunk.next;
  }
  return match (index) { Some(i) => Ok(i), None => Err(source.ReadAudio(pcm.Header)) };
}

function sound_range(reader: Reader, chunk: Chunk): Result[(i32, i32), source.ReadError] {
  if (chunk.size < 8) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let head = bytes_at(reader, chunk.data as i64, 8)?;
  let offset = wire.be(head, 0, 4);
  if (offset > (chunk.size - 8) as u64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  if (wire.be(head, 4, 4) != 0u64) {
    return Err(source.ReadAudio(pcm.Unsupported));
  }
  return Ok((chunk.data + 8 + offset as i32, chunk.size - 8 - offset as i32));
}

function check_version(reader: Reader, chunk: Chunk, compressed: boolean, version: boolean): Result[void, source.ReadError] {
  if (!compressed || version || chunk.size != 4) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let head = bytes_at(reader, chunk.data as i64, 4)?;
  if (wire.be(head, 0, 4) != 2726318400u64) {
    return Err(source.ReadAudio(pcm.Unsupported));
  }
  return Ok(());
}

function aiff_description(reader: Reader, chunk: Chunk, compressed: boolean, previous: Option[aiff.Common]): Result[aiff.Common, source.ReadError] {
  if let Some(_) = previous {
    return Err(source.ReadAudio(pcm.Header));
  }
  let head = bytes_at(reader, chunk.data as i64, if (chunk.size > 24) { 24 } else { chunk.size })?;
  return match (aiff.common_prefix(head, chunk.size, compressed)) { Ok(v) => Ok(v), Err(e) => Err(source.ReadAudio(e)) };
}

function aiff_extent(size: i32, desc: Option[aiff.Common], offset: i32, bytes: i32, compressed: boolean, version: boolean, limits: pcm.Limits): Result[linear.Index, source.ReadError] {
  let Some(d) = desc else {
    return Err(source.ReadAudio(pcm.Header));
  };
  if (offset < 0 || compressed && !version) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let index = match (linear.create_extent(size, offset, bytes, d.format, limits)) { Ok(i) => i, Err(e) => { return Err(source.ReadAudio(e)); } };
  if (index.information().frames != d.frames) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return Ok(index);
}

pub function inspect_aiff(reader: Reader, limits: aiff.Limits): Result[linear.Index, source.ReadError] {
  let size = file_size(reader, limits.audio)?;
  if (limits.max_chunks < 1) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let compressed = form(reader, size, false)?;
  let at = 12;
  let seen = 0;
  let version = false;
  let desc: Option[aiff.Common] = None;
  let offset = -1;
  let bytes = 0;
  while (at < size) {
    if (seen >= limits.max_chunks) {
      return Err(source.ReadAudio(pcm.Limit));
    }
    let chunk = chunk_at(reader, at, size, false)?;
    if (wire.tag(chunk.header, 0, "COMM")) {
      desc = Some(aiff_description(reader, chunk, compressed, desc)?);
    } else if (wire.tag(chunk.header, 0, "FVER")) {
      check_version(reader, chunk, compressed, version)?;
      version = true;
    } else if (wire.tag(chunk.header, 0, "SSND")) {
      if (offset >= 0) {
        return Err(source.ReadAudio(pcm.Header));
      }
      let (start, count) = sound_range(reader, chunk)?;
      offset = start;
      bytes = count;
    }
    at = chunk.next;
    seen += 1;
  }
  return aiff_extent(size, desc, offset, bytes, compressed, version, limits.audio);
}

function frame_window(reader: Reader, index: linear.Index, start: i32, frames: i32, capacity: i32, limits: pcm.Limits): Result[(linear.Index, u8[]), source.ReadError] {
  let info = index.information();
  if (start < 0 || start > info.frames || frames < 0 || frames > info.frames - start) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let count = match (pcm.sample_count(frames, info.format.channels, limits)) { Ok(value) => value, Err(e) => { return Err(source.ReadAudio(e)); } };
  if (count > capacity) {
    return Err(source.ReadAudio(pcm.Buffer));
  }
  let stride = info.format.storage_bits / 8;
  let size = count * stride;
  if (size > limits.max_input_bytes) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let offset = index.data_offset() + start * info.format.channels * stride;
  let data = bytes_at(reader, offset as i64, size)?;
  let inner = match (linear.create(data, 0, size, info.format, limits)) { Ok(value) => value, Err(e) => { return Err(source.ReadAudio(e)); } };
  return Ok((inner, data));
}

pub function read_integer_into(reader: Reader, index: linear.Index, start: i32, frames: i32, own output: i32[], limits: pcm.Limits): Result[linear.Integers, source.ReadError] {
  if (index.information().format.kind == linear.Float) {
    return Err(source.ReadAudio(pcm.Unsupported));
  }
  let (inner, data) = frame_window(reader, index, start, frames, output.len(), limits)?;
  return match (inner.read_integer_into(data, 0, frames, output, limits)) { Ok(block) => Ok(block), Err(e) => Err(source.ReadAudio(e)) };
}

pub function read_float_into(reader: Reader, index: linear.Index, start: i32, frames: i32, own output: f64[], limits: pcm.Limits): Result[linear.Floats, source.ReadError] {
  let (inner, data) = frame_window(reader, index, start, frames, output.len(), limits)?;
  return match (inner.read_float_into(data, 0, frames, output, limits)) { Ok(block) => Ok(block), Err(e) => Err(source.ReadAudio(e)) };
}
```
