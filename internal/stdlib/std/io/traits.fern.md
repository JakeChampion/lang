# Byte input and output

A reader returns the next nonempty byte chunk and its updated state. A successful
read with no chunk means end of input. A read error carries the original I/O error or stream fault.

Both operations consume the current state. Continue with the returned reader or writer after a successful call.

A writer accepts the whole chunk and returns its updated state on success.
A buffered writer can still report an error when it flushes or closes.

Raw file handles, buffered file handles, in-memory byte writers, and byte streams implement these interfaces. For example, the same function can write a header to a file or to a byte buffer.

```fern test name=write-header
import "std/io/traits";
import "std/io_buffered" as buffered;

function header[W: traits.Write](own w: W): Result[W, IoError] {
  return w.put_bytes("Fern".bytes());
}

function main(): i32 {
  let Ok(w) = header(buffered.bytes_writer_new()) else { return 1; };
  return if (w.len() == 4) { 0 } else { 1 };
}
```

```fern
<<*>>=
import "std/io_buffered" as buffered;
import "std/stream";

pub enum ReadError { ReadIo(IoError), ReadStream(StreamFault) }

pub trait Read {
  function pull_bytes(own self: Self): Result[(Option[u8[]], Self), ReadError];
}

pub trait Write {
  function put_bytes(own self: Self, bytes: u8[]): Result[Self, IoError];
}

impl Read for Reader {
  function pull_bytes(own self: Self): Result[(Option[u8[]], Reader), ReadError] {
    match (self.read_chunk_bytes(4096)) {
      Ok(bytes) => {
        if (bytes.len() == 0) {
          return Ok((None, self));
        }
        return Ok((Some(bytes), self));
      },
      Err(e) => {
        return Err(ReadIo(e));
      }
    }
  }
}

impl Write for Writer {
  function put_bytes(own self: Self, bytes: u8[]): Result[Writer, IoError] {
    self.write_bytes(bytes)?;
    return Ok(self);
  }
}

impl Read for buffered.ByteLineReader {
  function pull_bytes(own self: Self): Result[(Option[u8[]], buffered.ByteLineReader), ReadError] {
    match (self.next_chunk_bytes()) {
      Ok(step) => {
        return Ok(step);
      },
      Err(e) => {
        return Err(ReadIo(e));
      }
    }
  }
}

impl Write for BytesWriter {
  function put_bytes(own self: Self, bytes: u8[]): Result[BytesWriter, IoError] {
    return Ok(self.write_bytes(bytes));
  }
}

impl Write for buffered.BufWriter {
  function put_bytes(own self: Self, bytes: u8[]): Result[buffered.BufWriter, IoError] {
    return self.write_bytes(bytes);
  }
}

impl Read for ByteStream {
  function pull_bytes(own self: Self): Result[(Option[u8[]], ByteStream), ReadError] {
    let (bytes, next) = self.read_some();
    if (bytes.len() != 0) {
      return Ok((Some(bytes), next));
    }
    if let Some(e) = next.fault() {
      return Err(ReadStream(e));
    }
    return Ok((None, next));
  }
}
```
