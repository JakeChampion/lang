# Reader and input helpers

This module provides methods for readers and writers.

## Model

The helpers collect reader input into owned bytes or text. File and standard-input helpers share that result-based I/O interface.

## Contracts

A line read distinguishes end of input from an empty line. An I/O failure remains an error instead of ordinary end of input.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function read_all_buffer(r: Reader): Result[usize, IoError]>>

<<4 pub function read_all_bytes(r: Reader): Result[u8[], IoError]>>

<<5 function read_stdin_buffer(): Result[usize, IoError]>>

<<6 pub function read_all_stdin_bytes(): Result[u8[], IoError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides methods for readers and writers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/io_outcome";
import "std/utf8" as utf8;
```

### `3 function read_all_buffer(r: Reader): Result[usize, IoError]`

```fern
<<3 function read_all_buffer(r: Reader): Result[usize, IoError]>>=
function read_all_buffer(r: Reader): Result[usize, IoError] {
  let b: usize = buf_new(4096);
  while (true) {
    match (r.read_chunk_bytes(4096)) {
      Ok(chunk) => {
        if (chunk.len() == 0) {
          return Ok(b);
        }
        buf_push_bytes_range(b, chunk, 0, chunk.len());
      },
      Err(e) => {
        buf_free(b);
        return Err(e);
      }
    }
  }
  return Ok(b);
}
```

### `4 pub function read_all_bytes(r: Reader): Result[u8[], IoError]`

```fern
<<4 pub function read_all_bytes(r: Reader): Result[u8[], IoError]>>=
pub function read_all_bytes(r: Reader): Result[u8[], IoError] {
  match (read_all_buffer(r)) {
    Err(e) => {
      return Err(e);
    },
    Ok(b) => {
      let bytes: u8[] = buf_take_bytes(b);
      buf_free(b);
      return Ok(bytes);
    }
  }
}
```

### `5 function read_stdin_buffer(): Result[usize, IoError]`

```fern
<<5 function read_stdin_buffer(): Result[usize, IoError]>>=
function read_stdin_buffer(): Result[usize, IoError] {
  let r: Reader = stdin();
  let result: Result[usize, IoError] = read_all_buffer(r);
  let closed: Option[IoError] = r.close().failure();
  match (result) {
    Err(e) => {
      return Err(e);
    },
    Ok(b) => {
      match (closed) {
        Some(e) => {
          buf_free(b);
          return Err(e);
        },
        None => {
          return Ok(b);
        }
      }
    }
  }
}
```

### `6 pub function read_all_stdin_bytes(): Result[u8[], IoError]`

```fern
<<6 pub function read_all_stdin_bytes(): Result[u8[], IoError]>>=
pub function read_all_stdin_bytes(): Result[u8[], IoError] {
  match (read_stdin_buffer()) {
    Err(e) => {
      return Err(e);
    },
    Ok(b) => {
      let bytes: u8[] = buf_take_bytes(b);
      buf_free(b);
      return Ok(bytes);
    }
  }
}

pub function read_input_bytes(path: string): Result[u8[], IoError] {
  if (path == "-" || path == "") {
    return read_all_stdin_bytes();
  }
  return read_file_bytes(path);
}

pub function read_line(r: Reader): Result[Option[string], IoError] {
  let b: usize = buf_new(128);
  let more: boolean = true;
  while (more) {
    match (r.read_chunk_bytes(1)) {
      Ok(byte) => {
        if (byte.len() == 0) {
          more = false;
        } else {
          buf_push_byte(b, byte[0] as i32);
          more = byte[0] != 10;
        }
      },
      Err(e) => {
        buf_free(b);
        return Err(e);
      }
    }
  }
  if (buf_len(b) == 0) {
    buf_free(b);
    return Ok(None);
  }
  let bytes: u8[] = buf_take_bytes(b);
  buf_free(b);
  match (utf8.from_bytes(bytes)) {
    Some(line) => {
      return Ok(Some(line));
    },
    None => {
      return Err(InvalidUtf8(""));
    }
  }
}

pub function read_all_stdin(): Result[string, IoError] {
  match (read_stdin_buffer()) {
    Err(e) => {
      return Err(e);
    },
    Ok(b) => {
      let bytes: u8[] = buf_take_bytes(b);
      buf_free(b);
      match (utf8.from_bytes(bytes)) {
        Some(text) => {
          return Ok(text);
        },
        None => {
          return Err(InvalidUtf8("stdin"));
        }
      }
    }
  }
}

pub function read_input(path: string): Result[string, IoError] {
  if (path == "-" || path == "") {
    return read_all_stdin();
  }
  return read_file(path);
}
```
