# Android system properties

This module reads the system properties of Android, for example `persist.sys.timezone`, with no C library.

## Model

Android keeps its system properties in memory areas that each process can map read-only. Each area is a file in `/dev/__properties__`, and the SELinux context of a property selects its area. `get` finds the context of the name, reads the file of that context, and finds the name in the file.

From Android 9, the file `/dev/__properties__/property_info` gives the context of each name: `info_context` reads it. Android 7 and 8 keep the contexts as text in `property_contexts` files: `text_context` reads that text. `area_value` finds a name in the bytes of one area.

The parts that read bytes are public, so a test on any host can give them the bytes of an area.

## Contracts

`get` gives None when the name is not set, when the format is not known, or when the process cannot read the area. A value can change after `get` reads it.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<get>>

<<property_info>>

<<property_contexts>>

<<areas>>

<<bytes>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the system properties of Android with no C library.
import "std/result";
import "std/string";
import "std/utf8";
```

### `get`

```fern
<<get>>=
pub const DIR: string = "/dev/__properties__";

// The files that hold the contexts as text before Android 9.
const CONTEXT_FILES: string[] = ["/system/etc/selinux/plat_property_contexts", "/vendor/etc/selinux/nonplat_property_contexts", "/plat_property_contexts", "/nonplat_property_contexts", "/property_contexts"];

// The value of the property `name`, or None.
pub function get(name: string): Option[string] {
  let Some(context) = context_of(name) else {
    return None;
  };
  match (read_file_bytes(DIR + "/" + context)) {
    Ok(area) => {
      return area_value(area, name);
    },
    Err(_) => {
      return None;
    }
  }
}

function context_of(name: string): Option[string] {
  match (read_file_bytes(DIR + "/property_info")) {
    Ok(info) => {
      return info_context(info, name);
    },
    Err(_) => {}
  }
  let text = "";
  for path in CONTEXT_FILES {
    match (read_file(path)) {
      Ok(t) => {
        text = text + t + "\n";
      },
      Err(_) => {}
    }
  }
  return text_context(text, name);
}
```

### `property_info`

The file starts with six 32-bit words. The first three are the version, the oldest version that can read the file, and the size. The other three are the offsets of the contexts, of the types and of the root node.

Each node of the trie holds one part of a name between dots. A node has an entry, its children, its prefixes and its exact names. An entry has the offset of its name, the length of the name, the index of its context and the index of its type. An index of all ones is no index.

The exact name in the last node gives the context of a name. Without an exact name, the longest node or prefix that matches gives it.

```fern
<<property_info>>=
// The context of `name` in the bytes of a property_info file, or None.
pub function info_context(info: [u8], name: string): Option[string] {
  if (le32(info, 4) != 1) {
    return None;
  }
  let ctx = -1;
  let node = le32(info, 20);
  let rest = 0;
  let sep = dot(name, rest);
  ctx = choose(entry_context(info, le32(info, node)), ctx);
  ctx = prefix_context(info, node, name, rest, ctx);
  while (sep >= 0) {
    let child = find_child(info, node, name, rest, sep);
    if (child < 0) {
      break;
    }
    node = child;
    rest = sep + 1;
    sep = dot(name, rest);
    ctx = choose(entry_context(info, le32(info, node)), ctx);
    ctx = prefix_context(info, node, name, rest, ctx);
  }
  let exact = le32(info, node + 24);
  for i in 0..le32(info, node + 20) {
    let e = le32(info, exact + 4 * i);
    if (c_equals(info, le32(info, e), name, rest, name.len())) {
      return context_name(info, choose(entry_context(info, e), ctx));
    }
  }
  return context_name(info, prefix_context(info, node, name, rest, ctx));
}

function entry_context(info: [u8], e: i32): i32 {
  return le32(info, e + 8);
}

// `found` when it is an index, or else `old`.
function choose(found: i32, old: i32): i32 {
  if (found < 0) {
    return old;
  }
  return found;
}

// The context of the first prefix of the node that starts the rest of the
// name, or `old`.
function prefix_context(info: [u8], node: i32, name: string, rest: i32, old: i32): i32 {
  let prefixes = le32(info, node + 16);
  for i in 0..le32(info, node + 12) {
    let e = le32(info, prefixes + 4 * i);
    let len = le32(info, e + 4);
    if (len >= 0 && len <= name.len() - rest && c_starts(info, le32(info, e), name, rest, rest + len)) {
      return choose(entry_context(info, e), old);
    }
  }
  return old;
}

// The child of the node whose name is the part of `name` from `from` to
// `to`, or -1.
function find_child(info: [u8], node: i32, name: string, from: i32, to: i32): i32 {
  let children = le32(info, node + 8);
  for i in 0..le32(info, node + 4) {
    let child = le32(info, children + 4 * i);
    if (c_equals(info, le32(info, le32(info, child)), name, from, to)) {
      return child;
    }
  }
  return -1;
}

function context_name(info: [u8], index: i32): Option[string] {
  let contexts = le32(info, 12);
  if (index < 0 || index >= le32(info, contexts)) {
    return None;
  }
  return c_string(info, le32(info, contexts + 4 + 4 * index));
}
```

### `property_contexts`

Each line of the text has a prefix of names and a context. A line that starts with `#` is a comment. The prefix `*` matches each name. The longest prefix that starts the name selects the context.

```fern
<<property_contexts>>=
// The context of `name` in the text of property_contexts files, or None.
pub function text_context(text: string, name: string): Option[string] {
  let best: Option[string] = None;
  let best_len = -1;
  for line in text.split("\n") {
    let words = words_of(line);
    if (words.len() < 2 || words[0].starts_with("#")) {
      continue;
    }
    let len = words[0].len();
    if (words[0] == "*") {
      len = 0;
    } else if (!name.starts_with(words[0])) {
      continue;
    }
    if (len > best_len) {
      best = Some(words[1]);
      best_len = len;
    }
  }
  return best;
}

// The words of `line` between spaces and tabs.
function words_of(line: string): string[] {
  let out: string[] = [];
  let start = -1;
  for i in 0..line.len() + 1 {
    let space = i == line.len() || line[i] as i32 == 32 || line[i] as i32 == 9 || line[i] as i32 == 13;
    if (space && start >= 0) {
      out = out.append(slice_unchecked(line, start, i) + "");
      start = -1;
    } else if (!space && start < 0) {
      start = i;
    }
  }
  return out;
}
```

### `areas`

An area starts with a header of 128 bytes: the bytes in use, a serial, the magic `PROP` and the version. Offsets in the area count from the end of the header.

Each node of the area holds one part of a name between dots, in a binary tree of its siblings: a shorter part, or a part of the same length that is less byte by byte, is to the left. A node has the length of its part and the offset of its value. Then it has the offsets of its left sibling, its right sibling and its first child, and then its part.

A value starts with a serial word. Bit 16 of the serial marks a long value, whose offset from the start of the value is at byte 60. A short value is the text after the serial, with a NUL at its end.

```fern
<<areas>>=
const AREA_MAGIC: i32 = 1347375696;

const AREA_VERSION: i32 = -59846485;

const AREA_DATA: i32 = 128;

const LONG_VALUE: i32 = 65536;

// The value of `name` in the bytes of one property area, or None.
pub function area_value(area: [u8], name: string): Option[string] {
  if (le32(area, 8) != AREA_MAGIC || le32(area, 12) != AREA_VERSION) {
    return None;
  }
  let node = find_node(area, name);
  if (node < 0) {
    return None;
  }
  let value = le32(area, AREA_DATA + node + 4);
  if (value <= 0) {
    return None;
  }
  let at = AREA_DATA + value;
  if ((le32(area, at) & LONG_VALUE) != 0) {
    return c_string(area, at + le32(area, at + 60));
  }
  return c_string(area, at + 4);
}

// The node of `name` in an area, or -1.
function find_node(area: [u8], name: string): i32 {
  let node = 0;
  let rest = 0;
  while (node >= 0) {
    let sep = dot(name, rest);
    let end = sep;
    if (end < 0) {
      end = name.len();
    }
    let first = le32(area, AREA_DATA + node + 16);
    if (end == rest || first <= 0) {
      return -1;
    }
    node = find_sibling(area, first, name, rest, end);
    if (sep < 0) {
      return node;
    }
    rest = sep + 1;
  }
  return -1;
}

// The node of the part of `name` from `from` to `to` in the tree of
// siblings at `at`, or -1.
function find_sibling(area: [u8], at: i32, name: string, from: i32, to: i32): i32 {
  let node = at;
  let steps = area.len() / 20;
  while (node > 0 && steps > 0) {
    let order = compare_part(area, node, name, from, to);
    if (order == 0) {
      return node;
    }
    if (order < 0) {
      node = le32(area, AREA_DATA + node + 8);
    } else {
      node = le32(area, AREA_DATA + node + 12);
    }
    steps -= 1;
  }
  return -1;
}

// Less than 0 when the part of `name` comes before the part of the node,
// 0 when they are equal, and more than 0 when it comes after.
function compare_part(area: [u8], node: i32, name: string, from: i32, to: i32): i32 {
  let len = le32(area, AREA_DATA + node);
  if (to - from != len) {
    return to - from - len;
  }
  let part = AREA_DATA + node + 20;
  if (part + len > area.len()) {
    return 1;
  }
  for i in 0..len {
    let d = name[from + i] as i32 - area[part + i] as i32;
    if (d != 0) {
      return d;
    }
  }
  return 0;
}
```

### `bytes`

```fern
<<bytes>>=
// The little-endian word at `at`, or -1 outside the bytes.
function le32(bs: [u8], at: i32): i32 {
  if (at < 0 || at + 4 > bs.len()) {
    return -1;
  }
  return bs[at] as i32 | bs[at + 1] as i32 << 8 | bs[at + 2] as i32 << 16 | bs[at + 3] as i32 << 24;
}

// The index of the first dot of `name` at or after `from`, or -1.
function dot(name: string, from: i32): i32 {
  for i in from..name.len() {
    if (name[i] as i32 == 46) {
      return i;
    }
  }
  return -1;
}

// The text with a NUL at its end at `at`, or None.
function c_string(bs: [u8], at: i32): Option[string] {
  if (at < 0) {
    return None;
  }
  let out: u8[] = [];
  let i = at;
  while (i < bs.len() && bs[i] as i32 != 0) {
    out = out.append(bs[i]);
    i += 1;
  }
  if (i >= bs.len()) {
    return None;
  }
  return utf8.from_bytes(out).ok();
}

// Whether the bytes at `at` start with the part of `name` from `from` to
// `to`.
function c_starts(bs: [u8], at: i32, name: string, from: i32, to: i32): boolean {
  if (at < 0 || at + to - from > bs.len()) {
    return false;
  }
  for i in from..to {
    if (bs[at + i - from] as i32 != name[i] as i32) {
      return false;
    }
  }
  return true;
}

// Whether the text with a NUL at its end at `at` is the part of `name` from
// `from` to `to`.
function c_equals(bs: [u8], at: i32, name: string, from: i32, to: i32): boolean {
  let end = at + to - from;
  return c_starts(bs, at, name, from, to) && end < bs.len() && bs[end] as i32 == 0;
}
```
