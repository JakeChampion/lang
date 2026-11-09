# Incremental WavPack tags

This module validates trailing APE and ID3v1 tags through a bounded byte window.
It discards tag values as they arrive, so cover artwork does not require a complete tag buffer.
Each step performs at most one source read.

```fern
<<*>>=
// Validate trailing tags without retaining their values.
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";

enum Stage { Start, Marker, Key, Value, ProbeId3, AfterApe, CheckEof, Complete }

pub opaque struct TagInput { stage: Stage, bytes: i64, items: i32, key_bytes: i32, remaining: i64, header: u8[], max_bytes: i64, max_items: i32 }

pub struct Progress { state: TagInput, buffer: source.Buffer, done: boolean }

function error(value: pcm.AudioError): source.ReadError {
  return source.ReadAudio(value);
}

pub function create(max_bytes: i64, max_items: i32): Result[TagInput, source.ReadError] {
  if (max_bytes < 0i64 || max_items < 0) {
    return Err(error(pcm.Limit));
  }
  return Ok(TagInput { stage: Start, bytes: 0i64, items: 0, key_bytes: 0, remaining: 0i64, header: [], max_bytes: max_bytes, max_items: max_items });
}

function pending(own state: TagInput, own buffer: source.Buffer): Result[Progress, source.ReadError] {
  return Ok(Progress { state: state, buffer: buffer, done: false });
}

function consume(own state: TagInput, own buffer: source.Buffer, count: i32): Result[Progress, source.ReadError] {
  if (count as i64 > state.max_bytes - state.bytes) {
    return Err(error(pcm.Limit));
  }
  return pending(TagInput { ...state, bytes: state.bytes + count as i64 }, buffer.advance(count)?);
}

function required(own buffer: source.Buffer, count: i32, read: (i32) => Result[u8[], IoError]): Result[(source.Buffer, boolean), source.ReadError] {
  let (next, ready) = buffer.ensure(count, read)?;
  if (!ready && next.eof) {
    return Err(error(pcm.Truncated));
  }
  return Ok((next, ready));
}

function item(own state: TagInput, own buffer: source.Buffer): Result[Progress, source.ReadError] {
  if (state.items >= state.max_items) {
    return Err(error(pcm.Limit));
  }
  if (state.header.len() != 0 && state.items >= wire.le(state.header, 16, 4) as i32) {
    return Err(error(pcm.Header));
  }
  let view = buffer.view();
  let size = wire.le(view, 0, 4) as i64;
  if (wire.le(view, 4, 4) > 7u64) {
    return Err(error(pcm.Header));
  }
  if (size > state.max_bytes - state.bytes - 9i64) {
    return Err(error(pcm.Limit));
  }
  return consume(TagInput { ...state, stage: Key, items: state.items + 1, key_bytes: 0, remaining: size }, buffer, 8);
}

function start(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = required(buffer, 8, read)?;
  if (!ready) {
    return pending(state, next);
  }
  let view = next.view();
  if (wire.tag(view, 0, "APETAGEX")) {
    return pending(TagInput { ...state, stage: Marker }, next);
  }
  if (state.bytes == 0i64 && wire.tag(view, 0, "TAG")) {
    return pending(TagInput { ...state, stage: ProbeId3 }, next);
  }
  return item(state, next);
}

function marker_fields(view: [u8], state: TagInput): Result[void, source.ReadError] {
  let version = wire.le(view, 8, 4);
  if (version != 1000u64 && version != 2000u64) {
    return Err(error(pcm.Unsupported));
  }
  if (wire.le(view, 24, 8) != 0u64 || wire.le(view, 12, 4) < 32u64) {
    return Err(error(pcm.Header));
  }
  if (wire.le(view, 16, 4) > state.max_items as u64) {
    return Err(error(pcm.Limit));
  }
  return Ok(());
}

function header(own state: TagInput, own buffer: source.Buffer): Result[Progress, source.ReadError] {
  let view = buffer.view();
  if (state.bytes != 0i64 || state.header.len() != 0 || wire.le(view, 20, 4) != 2684354560u64) {
    return Err(error(pcm.Header));
  }
  if (wire.le(view, 12, 4) as i64 + 32i64 > state.max_bytes) {
    return Err(error(pcm.Limit));
  }
  let bytes: u8[] = [];
  for i in 0..32 {
    bytes = bytes.append(view[i]);
  }
  return consume(TagInput { ...state, stage: Start, header: bytes }, buffer, 32);
}

function footer_header(view: [u8], header: [u8]): Result[void, source.ReadError] {
  for i in 0..32 {
    let expected = view[i] | (if (i == 23) { 32u8 } else { 0u8 });
    if (header[i] != expected) {
      return Err(error(pcm.Header));
    }
  }
  return Ok(());
}

function footer(own state: TagInput, own buffer: source.Buffer): Result[Progress, source.ReadError] {
  let view = buffer.view();
  let has_header = state.header.len() != 0;
  let flags = if (has_header) { 2147483648u64 } else { 0u64 };
  if (wire.le(view, 20, 4) != flags || wire.le(view, 16, 4) != state.items as u64) {
    return Err(error(pcm.Header));
  }
  let size = state.bytes + 32i64 - (if (has_header) { 32i64 } else { 0i64 });
  if (wire.le(view, 12, 4) as i64 != size) {
    return Err(error(pcm.Header));
  }
  if (has_header) {
    footer_header(view, state.header)?;
  }
  return consume(TagInput { ...state, stage: AfterApe }, buffer, 32);
}

function marker(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = required(buffer, 32, read)?;
  if (!ready) {
    return pending(state, next);
  }
  marker_fields(next.view(), state)?;
  if ((wire.le(next.view(), 20, 4) & 536870912u64) != 0u64) {
    return header(state, next);
  }
  return footer(state, next);
}

function key(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = required(buffer, 1, read)?;
  if (!ready) {
    return pending(state, next);
  }
  let view = next.view();
  let count = 0;
  for byte in view {
    if (byte == 0u8) {
      if (state.key_bytes + count == 0) {
        return Err(error(pcm.Header));
      }
      return consume(TagInput { ...state, stage: Value }, next, count + 1);
    }
    if (byte < 32u8 || byte > 126u8 || state.key_bytes + count >= 255) {
      return Err(error(pcm.Header));
    }
    count += 1;
  }
  return consume(TagInput { ...state, key_bytes: state.key_bytes + count }, next, count);
}

function value(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  if (state.remaining == 0i64) {
    return pending(TagInput { ...state, stage: Start }, buffer);
  }
  let (next, ready) = required(buffer, 1, read)?;
  if (!ready) {
    return pending(state, next);
  }
  let count = if (state.remaining < next.available() as i64) { state.remaining as i32 } else { next.available() };
  return consume(TagInput { ...state, remaining: state.remaining - count as i64 }, next, count);
}

function probe_id3(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = buffer.ensure(129, read)?;
  if (ready) {
    if (state.bytes != 0i64) {
      return Err(error(pcm.Header));
    }
    return item(state, next);
  }
  if (!next.eof) {
    return pending(state, next);
  }
  if (next.available() != 128) {
    return Err(error(pcm.Truncated));
  }
  return consume(TagInput { ...state, stage: CheckEof }, next, 128);
}

function after_ape(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = buffer.ensure(3, read)?;
  if (!ready && !next.eof) {
    return pending(state, next);
  }
  if (next.available() == 0 && next.eof) {
    return pending(TagInput { ...state, stage: Complete }, next);
  }
  if (!ready || !wire.tag(next.view(), 0, "TAG")) {
    return Err(error(pcm.Header));
  }
  return pending(TagInput { ...state, stage: ProbeId3 }, next);
}

function check_eof(own state: TagInput, own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  let (next, ready) = buffer.ensure(1, read)?;
  if (ready) {
    return Err(error(pcm.Header));
  }
  if (!next.eof) {
    return pending(state, next);
  }
  return pending(TagInput { ...state, stage: Complete }, next);
}

pub function (own state: TagInput) step(own buffer: source.Buffer, read: (i32) => Result[u8[], IoError]): Result[Progress, source.ReadError] {
  return match (state.stage) {
    Start => start(state, buffer, read),
    Marker => marker(state, buffer, read),
    Key => key(state, buffer, read),
    Value => value(state, buffer, read),
    ProbeId3 => probe_id3(state, buffer, read),
    AfterApe => after_ape(state, buffer, read),
    CheckEof => check_eof(state, buffer, read),
    Complete => Ok(Progress { state: state, buffer: buffer, done: true }),
  };
}

pub function (state: TagInput) bytes_read(): i64 {
  return state.bytes;
}
```
