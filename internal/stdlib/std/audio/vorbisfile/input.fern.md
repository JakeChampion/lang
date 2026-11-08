# Bounded Ogg Vorbis input

This module connects Ogg Vorbis playback to a borrowed `Reader` or a byte-source callback.
It retains one bounded input chunk, bounded Ogg state, and codec state.
Events use the same metadata, PCM, positions, and final boundaries as `std/audio/vorbisfile`.

Each `step` performs at most one source read and one decoder feed.
Pending decoder events drain before the next source read.
`next` continues through pending steps until an event or completion.
An empty successful source read means permanent EOF. I/O and codec errors remain distinct.

The caller retains and closes the source after success or failure.
An error consumes the input state. Completion prevents further source reads.
Source reads can block, so applications must set appropriate I/O deadlines.

```fern
<<*>>=
import "std/audio/source";
import "std/audio/vorbisfile";

pub enum ReadEvent { Pending, Output(vorbisfile.Event), Finished }

pub opaque struct Input { codec: vorbisfile.Decoder, buffer: source.Buffer, drain: boolean, done: boolean }

pub function create(limits: vorbisfile.Limits, chunk_size: i32): Result[Input, source.ReadError] {
  let buffer = source.create(source.Limits { chunk_size: chunk_size, max_buffer_bytes: chunk_size })?;
  let codec = match (vorbisfile.decoder(limits)) { Ok(value) => value, Err(e) => { return Err(source.ReadAudio(e)); } };
  return Ok(Input { codec: codec, buffer: buffer, drain: false, done: false });
}

pub function (own state: Input) step_with(read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  if (state.done) {
    return Ok((state, Finished));
  }
  let Input { codec, buffer, drain, done } = state;
  if (!drain) {
    let (loaded, _) = buffer.ensure(1, read)?;
    buffer = loaded;
  }
  let bytes = buffer.view();
  let input = if (drain) { bytes[bytes.len():] } else { bytes };
  let (next, used, event) = match (codec.feed(input)) { Ok(value) => value, Err(e) => { return Err(source.ReadAudio(e)); } };
  buffer = buffer.advance(used)?;
  let updated = Input { codec: next, buffer: buffer, drain: !drain, done: false };
  return answer(updated, event);
}

function answer(own state: Input, own event: Option[vorbisfile.Event]): Result[(Input, ReadEvent), source.ReadError] {
  if let Some(value) = event {
    return Ok((Input { ...state, drain: true }, Output(value)));
  }
  if (state.buffer.eof && state.buffer.available() == 0 && !state.drain) {
    if let Err(e) = state.codec.finish() {
      return Err(source.ReadAudio(e));
    }
    return Ok((Input { ...state, done: true }, Finished));
  }
  return Ok((state, Pending));
}

pub function (own state: Input) step(reader: Reader): Result[(Input, ReadEvent), source.ReadError] {
  return state.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (own state: Input) next_with(read: (i32) => Result[u8[], IoError]): Result[(Input, Option[vorbisfile.Event]), source.ReadError] {
  while (true) {
    let (next, event) = state.step_with(read)?;
    state = next;
    match (event) {
      Pending => {},
      Output(value) => {
        return Ok((state, Some(value)));
      },
      Finished => {
        return Ok((state, None));
      }
    }
  }
}

pub function (own state: Input) next(reader: Reader): Result[(Input, Option[vorbisfile.Event]), source.ReadError] {
  return state.next_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
