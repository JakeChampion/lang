# Bounded Ogg Opus input

This module connects an Ogg Opus decoder to a byte source without a complete encoded input view.
It retains one input chunk, bounded container state, and codec state.
Each output event contains the same metadata or PCM as `std/audio/opusfile`.

`create` accepts the file limits and an input chunk size from one byte through 1 MiB.
`step` borrows a `Reader`. The caller retains responsibility for the reader and closes it after use.
`step_with` instead accepts a byte-source callback for custom input.
The callback receives the maximum requested byte count and returns at most that many bytes.

An empty successful read means permanent end of input. An I/O error remains an error.

Each step performs at most one source read and one decoder feed.
Pending decoder events drain before another source read.
`Pending` lets an application pause or cancel between calls.
`Output` contains one event. `Finished` confirms complete container validation at end of input.
The `next` and `next_with` methods continue through pending steps until an event or the end.

Source reads can block. Use a source with appropriate deadlines when the application needs bounded latency.
An error consumes the input state but does not close the source.
After `Finished`, further calls return `Finished` without source reads.
The same source must continue from its current position across calls.
The adapter does not seek, retain the complete track, or collect PCM output.

```fern
<<*>>=
// Connect bounded byte reads to incremental Ogg Opus events.

import "core/cmp";
import "core/error";
import "std/audio/pcm";
import "std/audio/opusfile";

pub enum ReadError { ReadIo(IoError), ReadAudio(pcm.AudioError), ReadSize }

impl error.Error for ReadError {}

impl cmp.Display for ReadError {}

pub function (e: ReadError) message(): string {
  return match (e) {
    ReadIo(value) => value.message(),
    ReadAudio(value) => value.message(),
    ReadSize => "audio source read size is outside the configured limit",
  };
}

pub function (e: ReadError) to_string(): string {
  return e.message();
}

pub enum ReadEvent { Pending, Output(opusfile.Event), Finished }

pub struct Input { codec: opusfile.Decoder, chunk: u8[], position: i32, chunk_size: i32, drain: boolean, eof: boolean, done: boolean }

pub function create(limits: opusfile.Limits, chunk_size: i32): Result[Input, ReadError] {
  if (chunk_size < 1 || chunk_size > 1048576) {
    return Err(ReadSize);
  }
  let codec = match (opusfile.decoder(limits)) { Ok(value) => value, Err(e) => { return Err(ReadAudio(e)); } };
  return Ok(Input { codec: codec, chunk: [], position: 0, chunk_size: chunk_size, drain: false, eof: false, done: false });
}

function load(own state: Input, read: (i32) => Result[u8[], IoError]): Result[Input, ReadError] {
  if (state.drain || state.position < state.chunk.len() || state.eof) {
    return Ok(state);
  }
  let chunk = match (read(state.chunk_size)) { Ok(value) => value, Err(e) => { return Err(ReadIo(e)); } };
  if (chunk.len() > state.chunk_size) {
    return Err(ReadSize);
  }
  let eof = chunk.len() == 0;
  return Ok(Input { ...state, chunk: chunk, position: 0, eof: eof });
}

function feed(own state: Input): Result[(Input, ReadEvent), ReadError] {
  let Input { codec, chunk, position, chunk_size, drain, eof, done } = state;
  let start = if (drain) { chunk.len() } else { position };
  let (next, used, event) = match (codec.feed(chunk[start:])) { Ok(value) => value, Err(e) => { return Err(ReadAudio(e)); } };
  let at = position + used;
  let buffered = if (at == chunk.len()) { [] } else { chunk };
  let offset = if (at == chunk.len()) { 0 } else { at };
  let updated = Input { codec: next, chunk: buffered, position: offset, chunk_size: chunk_size, drain: !drain, eof: eof, done: done };
  return answer(updated, event);
}

function answer(own updated: Input, own event: Option[opusfile.Event]): Result[(Input, ReadEvent), ReadError] {
  match (event) {
    Some(value) => {
      return Ok((Input { ...updated, drain: true }, Output(value)));
    },
    None => {}
  }
  if (updated.eof && !updated.drain && updated.chunk.len() == 0) {
    if let Err(e) = updated.codec.finish() {
      return Err(ReadAudio(e));
    }
    return Ok((Input { ...updated, done: true }, Finished));
  }
  return Ok((updated, Pending));
}

pub function (own state: Input) step_with(read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), ReadError] {
  if (state.done) {
    return Ok((state, Finished));
  }
  let ready = load(state, read)?;
  return feed(ready);
}

pub function (own state: Input) step(source: Reader): Result[(Input, ReadEvent), ReadError] {
  return state.step_with((count: i32): Result[u8[], IoError] => source.read_chunk_bytes(count));
}

pub function (own state: Input) next_with(read: (i32) => Result[u8[], IoError]): Result[(Input, Option[opusfile.Event]), ReadError] {
  while (true) {
    let (next, event) = state.step_with(read)?;
    state = next;
    match (event) {
      Output(value) => {
        return Ok((state, Some(value)));
      },
      Finished => {
        return Ok((state, None));
      },
      Pending => {}
    }
  }
}

pub function (own state: Input) next(source: Reader): Result[(Input, Option[opusfile.Event]), ReadError] {
  return state.next_with((count: i32): Result[u8[], IoError] => source.read_chunk_bytes(count));
}
```
