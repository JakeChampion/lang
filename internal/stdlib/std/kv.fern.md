# Key-value files

This module keeps a small set of named values in one file, such as the settings of an app.

## Model

A `Store` is the path of a file and an ordered map of keys to text values. `open` reads the file, and a file that does not exist gives an empty store. `get`, `text`, `int` and `flag` read a value. `set`, `set_int`, `set_flag` and `remove` give a changed store and do not write the file. `save` writes the file.

`save` writes the new contents to a temporary file next to the file, with the name of the file and `.tmp` at its end. It flushes the temporary file to the disk with `fsync`, and then renames it to the name of the file. A rename replaces the file in one step. So after a crash or a power loss the file has the old contents or the new contents, and never a part of each.

The file is text. Its first line is `fern-kv 1`. Each next line is a key, a tab and a value, in the order of the keys. A backslash, a tab, a line feed and a carriage return in a key or a value are written as `\\`, `\t`, `\n` and `\r`.

## Contracts

`int` and `flag` give the default when the key has no value or when its value is not an integer or a flag. A flag is `true` or `false`.

`open` gives `Corrupt` with the number of the line when the file does not have the form above. Line numbers start at 1.

Only one store must write a file at a time. Two programs that save the same file at the same time each write the temporary file, and the last rename wins.

## Executable example

```fern test name=std-kv-example
import "std/kv";
import "std/option";

function main(): i32 {
  let s = kv.empty("settings.kv").set("theme", "dark").set_int("volume", 7).set_flag("shuffle", true);
  let Ok(back) = kv.decode("settings.kv", s.encode()) else { return 1; };
  if (back.text("theme", "") != "dark" || back.int("volume", 0) != 7i64 || !back.flag("shuffle", false)) { return 2; }
  if (back.int("missing", 3i64) != 3i64 || back.keys() != ["shuffle", "theme", "volume"]) { return 3; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<errors>>

<<store>>

<<encoding>>

<<files>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module keeps a small set of named values in one file.
import "core/error";
import "std/i64";
import "std/ordmap";
```

### `errors`

```fern
<<errors>>=
pub enum KvError {
  Io(IoError),
  Corrupt(i32),
}

pub function (e: KvError) message(): string {
  return match (e) {
    Io(io) => io.message(),
    Corrupt(line) => "the key-value file is not valid at line " + line.to_string(),
  };
}
```

### `store`

```fern
<<store>>=
pub struct Store { path: string, values: ordmap.OrdMap[string, string] }

// A store of the file at `path` with no values.
pub function empty(path: string): Store {
  return Store { path: path, values: ordmap.new[string, string]() };
}

pub function (s: Store) get(key: string): Option[string] {
  return s.values.get(key);
}

pub function (s: Store) text(key: string, fallback: string): string {
  return s.values.get_or(key, fallback);
}

pub function (s: Store) int(key: string, fallback: i64): i64 {
  let Some(v) = s.values.get(key) else {
    return fallback;
  };
  return match (i64.parse_i64(v)) {
    Ok(n) => n,
    Err(_) => fallback,
  };
}

pub function (s: Store) flag(key: string, fallback: boolean): boolean {
  return match (s.values.get(key)) {
    Some("true") => true,
    Some("false") => false,
    _ => fallback,
  };
}

pub function (s: Store) has(key: string): boolean {
  return s.values.contains(key);
}

// The keys in order.
pub function (s: Store) keys(): string[] {
  return s.values.keys();
}

pub function (s: Store) set(key: string, value: string): Store {
  return Store { ...s, values: s.values.insert(key, value) };
}

pub function (s: Store) set_int(key: string, value: i64): Store {
  return s.set(key, value.to_string());
}

pub function (s: Store) set_flag(key: string, value: boolean): Store {
  return s.set(key, if (value) { "true" } else { "false" });
}

pub function (s: Store) remove(key: string): Store {
  return Store { ...s, values: s.values.remove(key) };
}
```

### `encoding`

```fern
<<encoding>>=
const HEADER: string = "fern-kv 1";

function escaped(out: u8[], s: string): u8[] {
  for c in s.as_bytes() {
    if (c == 92u8) {
      out = out.append(92u8).append(92u8);
    } else if (c == 9u8) {
      out = out.append(92u8).append(116u8);
    } else if (c == 10u8) {
      out = out.append(92u8).append(110u8);
    } else if (c == 13u8) {
      out = out.append(92u8).append(114u8);
    } else {
      out = out.append(c);
    }
  }
  return out;
}

// The text of the file of `s`.
pub function (s: Store) encode(): string {
  let out = escaped([], HEADER).append(10u8);
  for (k, v) in s.values.entries() {
    out = escaped(out, k).append(9u8);
    out = escaped(out, v).append(10u8);
  }
  return unsafe { string_from_bytes_unchecked(out) };
}

// The text of `bytes` from `from` to `to`, with its escapes replaced, or
// None for an escape that is not valid.
function unescaped(bytes: [u8], from: i32, to: i32): Option[string] {
  let out: u8[] = [];
  let i = from;
  while (i < to) {
    let c = bytes[i];
    if (c != 92u8) {
      out = out.append(c);
      i += 1;
      continue;
    }
    if (i + 1 >= to) {
      return None;
    }
    let n = bytes[i + 1];
    if (n == 92u8) {
      out = out.append(92u8);
    } else if (n == 116u8) {
      out = out.append(9u8);
    } else if (n == 110u8) {
      out = out.append(10u8);
    } else if (n == 114u8) {
      out = out.append(13u8);
    } else {
      return None;
    }
    i += 2;
  }
  return Some(unsafe { string_from_bytes_unchecked(out) });
}

// The store of the file at `path` from the text of the file.
pub function decode(path: string, text: string): Result[Store, KvError] {
  let bytes = text.as_bytes();
  let s = empty(path);
  let line = 1;
  let start = 0;
  let tab = -1;
  for i in 0..bytes.len() {
    if (bytes[i] == 9u8 && tab < 0) {
      tab = i;
    }
    if (bytes[i] != 10u8) {
      continue;
    }
    if (line == 1) {
      if (unescaped(bytes, start, i) != Some(HEADER)) {
        return Err(Corrupt(1));
      }
    } else {
      if (tab < 0) {
        return Err(Corrupt(line));
      }
      let (Some(k), Some(v)) = (unescaped(bytes, start, tab), unescaped(bytes, tab + 1, i)) else {
        return Err(Corrupt(line));
      };
      s = s.set(k, v);
    }
    line += 1;
    start = i + 1;
    tab = -1;
  }
  if (start != bytes.len() || line == 1) {
    return Err(Corrupt(line));
  }
  return Ok(s);
}
```

### `files`

```fern
<<files>>=
// The store of the file at `path`, or an empty store when there is no
// file.
pub function open(path: string): Result[Store, KvError] {
  return match (read_file(path)) {
    Ok(text) => decode(path, text),
    Err(NotFound(_)) => Ok(empty(path)),
    Err(e) => Err(Io(e)),
  };
}

function write_synced(path: string, text: string): Result[void, IoError] {
  let w = open_writer(path)?;
  match (w.write(text)) {
    Ok(_) => {},
    Err(e) => {
      w.close();
      return Err(e);
    }
  }
  match (w.fsync()) {
    Ok(_) => {},
    Err(e) => {
      w.close();
      return Err(e);
    }
  }
  return w.close();
}

// Writes the file of `s`. The file has the old contents or the new
// contents at each time.
pub function (s: Store) save(): Result[void, KvError] {
  let tmp = s.path + ".tmp";
  match (write_synced(tmp, s.encode())) {
    Ok(_) => {},
    Err(e) => {
      remove_file(tmp);
      return Err(Io(e));
    }
  }
  return match (rename(tmp, s.path)) {
    Ok(_) => Ok(()),
    Err(e) => Err(Io(e)),
  };
}
```
