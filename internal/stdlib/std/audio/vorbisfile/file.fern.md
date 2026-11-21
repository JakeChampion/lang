# Indexed Vorbis playback from a file

The caller lends a seekable Reader on the same immutable Ogg file for indexing and playback.
Indexing starts at byte zero and reads forward in bounded chunks. Seek positions are playable frames within each logical link.
Each playback step reads the indexed range needed for one cursor transition. The supplied source limits bound each read and the complete retained range.
The caller closes the Reader.

```fern
<<*>>=
import "std/audio/file" as packet_file;
import "std/audio/source";
import "std/audio/vorbisfile/seek";

pub function inspect(reader: Reader, limits: seek.Limits): Result[seek.Index, source.ReadError] {
  let size = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
  let start = match (reader.seek(0i64, 0)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
  if (start != 0i64) {
    return Err(source.ReadSize);
  }
  return seek.inspect_with(size, (_offset: i64, count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), limits);
}

pub function step(reader: Reader, own cursor: seek.Cursor, limits: source.Limits): Result[(seek.Cursor, seek.ReadEvent), source.ReadError] {
  let (offset, count) = cursor.want();
  if (count == 0) {
    return match (cursor.step_view([], offset)) { Ok(value) => Ok(value), Err(e) => Err(source.ReadAudio(e)) };
  }
  let window = packet_file.create(offset, count, limits)?;
  while (!window.ready) {
    window = window.step(reader)?;
  }
  return match (cursor.step_view(window.view(), offset)) { Ok(value) => Ok(value), Err(e) => Err(source.ReadAudio(e)) };
}
```
