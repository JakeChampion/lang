# JSON trees and documents

This module encodes and decodes JSON values.

## Model

Tree values represent JSON data as enums and ordered objects. A document instead retains source text and a compact tape for node access.

## Contracts

Result-based parsers retain a concrete parse error. Optional parsers provide a simpler success test. Typed conversion reports field, index, or variant context.

## Executable example

```fern test name=std-json-example
import "std/json";

function main(): i32 {
  let Ok(doc) = json.parse_document("[true,null]") else { return 1; };
  if (!doc.is_array(doc.root()) || doc.len(doc.root()) != 2) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum JsonValue>>

<<3 pub function object(): JsonObject>>

<<4 function __json_dedupe(keys: string[], values: JsonValue[]): JsonObject>>

<<5 pub function (o: JsonObject) index_of(key: string): Option[i32]>>

<<6 function __json_member_at(o: JsonObject, key: string): i32>>

<<7 pub function (o: JsonObject) len(): i32>>

<<8 pub function (o: JsonObject) for_each(f: (string, JsonValue)>>

<<9 function __json_hex_lower(d: i32): i32>>

<<10 const JSON_ESCAPES: u8[]>>

<<11 pub function json_escape(s: string): string>>

<<12 function __json_escape_width(b: i32): i32>>

<<13 pub function json_encode(v: JsonValue): string>>

<<14 function __json_spaces(n: i32): string>>

<<15 pub function json_encode_pretty(v: JsonValue, indent: i32): string>>

<<16 const JSON_NOT_WS: u8[]>>

<<17 function __json_ws(s: string, i: i32): i32>>

<<18 const JSON_NOT_DIGIT: u8[]>>

<<19 function __json_cp_bytes(cp: i32): u8[]>>

<<20 function __json_p_string(s: string, start: i32, at: Cell[i32]): string>>

<<21 const JSON_STRING_STOPS: u8[]>>

<<22 function __json_append_run(out: u8[], s: string, from: i32, stop: i32): u8[]>>

<<23 function __json_word(s: string, i: i32, word: string): boolean>>

<<24 pub struct JsonError>>

<<25 pub function (e: JsonError) message(): string>>

<<26 impl error.Error for JsonError>>

<<27 pub function (e: JsonError) to_string(): string>>

<<28 function __json_message(s: string, off: i32): string>>

<<29 pub function json_parse_result(s: string): Result[JsonValue, JsonError]>>

<<30 pub struct Document>>

<<31 function __json_t_word(kind: i32, flags: i32, a: i32): u64>>

<<32 function __json_t_value(s: string, i: i32, own tape: u64[], depth: i32): (u64[], i32)>>

<<33 pub function parse_document(s: string): Result[Document, JsonError]>>

<<34 function __json_t_a(d: Document, n: i32): i32>>

<<35 pub function (d: Document) root(): i32>>

<<36 function __json_t_key_is(d: Document, k: i32, key: string): boolean>>

<<37 pub function (d: Document) as_bool(n: i32): Option[boolean]>>

<<38 function __json_t_elements(d: Document, n: i32): JsonValue[]>>

<<39 pub function json_get(obj: JsonValue, key: string): Option[JsonValue]>>

<<40 function __json_number_i32(s: string): Option[i32]>>

<<41 pub function json_get_i64(obj: JsonValue, key: string): Option[i64]>>

<<42 function __json_number_i64(s: string): Option[i64]>>

<<43 pub function json_get_f64(obj: JsonValue, key: string): Option[f64]>>

<<44 function __json_ptr_unescape(tok: string): string>>

<<45 pub function json_pointer(doc: JsonValue, pointer: string): Option[JsonValue]>>

<<46 pub trait Json>>

<<47 pub function (xs: T[]) to_json[T: Json](): string>>

<<Optional JSON values>>

<<48 pub trait FromJson>>

<<49 pub function json_kind(v: JsonValue): string>>

<<50 impl FromJson for i32>>

<<51 impl FromJson for boolean>>

<<52 pub function decode[T: FromJson](text: string): Result[T, string]>>

<<53 pub function from_json_payload(v: JsonValue): JsonValue>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module encodes and decodes JSON values.
import "core/map";
import "std/string";
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/f64";
import "std/error";
import "core/cmp";
```

### `2 pub enum JsonValue`

```fern
<<2 pub enum JsonValue>>=
pub enum JsonValue {
  JNull,
  JBool(boolean),
  JNumber(string),
  JString(string),
  JArray(JsonValue[]),
  JObject(JsonObject),
}

pub struct JsonObject { names: string[], items: JsonValue[], index: i32[] }

const JSON_LINEAR_MAX: i32 = 8;
```

### `3 pub function object(): JsonObject`

```fern
<<3 pub function object(): JsonObject>>=
pub function object(): JsonObject {
  return JsonObject { names: [], items: [], index: [] };
}

// A repeated key retains its first position and last value.
pub function from(pairs: (string, JsonValue)[]): JsonObject {
  let keys: string[] = [];
  let values: JsonValue[] = [];
  for pair in pairs {
    keys = keys.append(pair.0);
    values = values.append(pair.1);
  }
  return object_of(keys, values);
}

pub function object_of(keys: string[], values: JsonValue[]): JsonObject {
  let n: i32 = keys.len();
  if (n <= JSON_LINEAR_MAX) {
    let i: i32 = 1;
    while (i < n) {
      if (__json_linear_find(keys, i, keys[i]) >= 0) {
        return __json_dedupe(keys, values);
      }
      i = i + 1;
    }
    return JsonObject { names: keys, items: values, index: [] };
  }
  let index: i32[] = __json_index_new(n);
  let seed: i32 = __map_hash_seed();
  let i: i32 = 0;
  while (i < n) {
    let slot: i32 = __json_index_slot(index, keys, keys[i], seed);
    if (index[slot] != 0) {
      return __json_dedupe(keys, values);
    }
    index = index.with(slot, i + 1);
    i = i + 1;
  }
  return JsonObject { names: keys, items: values, index: index };
}
```

### `4 function __json_dedupe(keys: string[], values: JsonValue[]): JsonObject`

```fern
<<4 function __json_dedupe(keys: string[], values: JsonValue[]): JsonObject>>=
function __json_dedupe(keys: string[], values: JsonValue[]): JsonObject {
  let o: JsonObject = object();
  let i: i32 = 0;
  while (i < keys.len()) {
    o = o.insert(keys[i], values[i]);
    i = i + 1;
  }
  return o;
}

function __json_linear_find(keys: string[], n: i32, key: string): i32 {
  let i: i32 = 0;
  while (i < n) {
    if (keys[i] == key) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __json_index_new(n: i32): i32[] {
  let cap: i32 = 16;
  while (cap < 2 * n) {
    cap = cap * 2;
  }
  let index: i32[] = [];
  while (index.len() < cap) {
    index = index.append(0);
  }
  return index;
}

function __json_index_slot(index: i32[], keys: string[], key: string, seed: i32): i32 {
  let mask: i32 = index.len() - 1;
  let h: i32 = __map_fnv_str_seeded(key, seed) & mask;
  while (true) {
    let at: i32 = index[h];
    if (at == 0 || keys[at - 1] == key) {
      return h;
    }
    h = h + 1 & mask;
  }
  return h;
}

function __json_index_of(keys: string[], n: i32): i32[] {
  let index: i32[] = __json_index_new(n);
  let seed: i32 = __map_hash_seed();
  let i: i32 = 0;
  while (i < n) {
    index = index.with(__json_index_slot(index, keys, keys[i], seed), i + 1);
    i = i + 1;
  }
  return index;
}
```

### `5 pub function (o: JsonObject) index_of(key: string): Option[i32]`

```fern
<<5 pub function (o: JsonObject) index_of(key: string): Option[i32]>>=
pub function (o: JsonObject) index_of(key: string): Option[i32] {
  let at: i32 = __json_member_at(o, key);
  if (at < 0) {
    return None;
  }
  return Some(at);
}
```

### `6 function __json_member_at(o: JsonObject, key: string): i32`

```fern
<<6 function __json_member_at(o: JsonObject, key: string): i32>>=
function __json_member_at(o: JsonObject, key: string): i32 {
  if (o.index.len() == 0) {
    return __json_linear_find(o.names, o.names.len(), key);
  }
  return o.index[__json_index_slot(o.index, o.names, key, __map_hash_seed())] - 1;
}
```

### `7 pub function (o: JsonObject) len(): i32`

```fern
<<7 pub function (o: JsonObject) len(): i32>>=
pub function (o: JsonObject) len(): i32 {
  return o.names.len();
}

pub function (o: JsonObject) is_empty(): boolean {
  return o.names.len() == 0;
}

pub function (o: JsonObject) contains(key: string): boolean {
  return __json_member_at(o, key) >= 0;
}

pub function (o: JsonObject) get(key: string): Option[JsonValue] {
  let at: i32 = __json_member_at(o, key);
  if (at < 0) {
    return None;
  }
  return Some(o.items[at]);
}

pub function (o: JsonObject) get_or(key: string, fallback: JsonValue): JsonValue {
  let at: i32 = __json_member_at(o, key);
  if (at < 0) {
    return fallback;
  }
  return o.items[at];
}

pub function (own o: JsonObject) insert(key: string, value: JsonValue): JsonObject {
  let at: i32 = __json_member_at(o, key);
  if (at >= 0) {
    return JsonObject { ...o, items: o.items.with(at, value) };
  }
  let n: i32 = o.names.len();
  let keys: string[] = o.names.append(key);
  let values: JsonValue[] = o.items.append(value);
  let index: i32[] = o.index;
  if (n + 1 > JSON_LINEAR_MAX) {
    if (2 * (n + 1) > index.len()) {
      index = __json_index_of(keys, n + 1);
    } else {
      index = index.with(__json_index_slot(index, keys, key, __map_hash_seed()), n + 1);
    }
  }
  return JsonObject { names: keys, items: values, index: index };
}

pub function (o: JsonObject) remove(key: string): JsonObject {
  let at: i32 = __json_member_at(o, key);
  if (at < 0) {
    return o;
  }
  let keys: string[] = [];
  let values: JsonValue[] = [];
  let i: i32 = 0;
  while (i < o.names.len()) {
    if (i != at) {
      keys = keys.append(o.names[i]);
      values = values.append(o.items[i]);
    }
    i = i + 1;
  }
  return object_of(keys, values);
}

pub function (own o: JsonObject) update(key: string, init: JsonValue, f: (JsonValue) => JsonValue): JsonObject {
  let v: JsonValue = f(o.get_or(key, init));
  return o.insert(key, v);
}

pub function (own o: JsonObject) merge(other: JsonObject): JsonObject {
  let out: JsonObject = o;
  let i: i32 = 0;
  while (i < other.names.len()) {
    out = out.insert(other.names[i], other.items[i]);
    i = i + 1;
  }
  return out;
}

pub function (o: JsonObject) keys(): string[] {
  return o.names;
}

pub function (o: JsonObject) values(): JsonValue[] {
  return o.items;
}

pub function (o: JsonObject) entries(): (string, JsonValue)[] {
  let out: (string, JsonValue)[] = [];
  let i: i32 = 0;
  while (i < o.names.len()) {
    out = out.append((o.names[i], o.items[i]));
    i = i + 1;
  }
  return out;
}

pub function (o: JsonObject) filter(pred: (string, JsonValue) => boolean): JsonObject {
  let keys: string[] = [];
  let values: JsonValue[] = [];
  let i: i32 = 0;
  while (i < o.names.len()) {
    if (pred(o.names[i], o.items[i])) {
      keys = keys.append(o.names[i]);
      values = values.append(o.items[i]);
    }
    i = i + 1;
  }
  return object_of(keys, values);
}

pub function (o: JsonObject) fold[A](init: A, f: (A, string, JsonValue) => A): A {
  let acc: A = init;
  let i: i32 = 0;
  while (i < o.names.len()) {
    acc = f(acc, o.names[i], o.items[i]);
    i = i + 1;
  }
  return acc;
}

pub function (o: JsonObject) map_values(f: (JsonValue) => JsonValue): JsonObject {
  let values: JsonValue[] = [];
  let i: i32 = 0;
  while (i < o.items.len()) {
    values = values.append(f(o.items[i]));
    i = i + 1;
  }
  return JsonObject { ...o, items: values };
}
```

### `8 pub function (o: JsonObject) for_each(f: (string, JsonValue)`

```fern
<<8 pub function (o: JsonObject) for_each(f: (string, JsonValue)>>=
pub function (o: JsonObject) for_each(f: (string, JsonValue) => void): void {
  let i: i32 = 0;
  while (i < o.names.len()) {
    f(o.names[i], o.items[i]);
    i = i + 1;
  }
}
```

### `9 function __json_hex_lower(d: i32): i32`

```fern
<<9 function __json_hex_lower(d: i32): i32>>=
function __json_hex_lower(d: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + 87;
}
```

### `10 const JSON_ESCAPES: u8[]`

```fern
<<10 const JSON_ESCAPES: u8[]>>=
const JSON_ESCAPES: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8,
];
```

### `11 pub function json_escape(s: string): string`

```fern
<<11 pub function json_escape(s: string): string>>=
pub function json_escape(s: string): string {
  let n: i32 = s.len();
  let i: i32 = __scan_set(s, 0, JSON_ESCAPES);
  if (i == n) {
    return s;
  }
  let total: i32 = n;
  while (i < n) {
    total = total + __json_escape_width(s[i] as i32) - 1;
    i = __scan_set(s, i + 1, JSON_ESCAPES);
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  let from: i32 = 0;
  while (from < n) {
    let stop: i32 = __scan_set(s, from, JSON_ESCAPES);
    while (from < stop) {
      buf = buf.with(pos, s[from] as u8);
      pos = pos + 1;
      from = from + 1;
    }
    if (stop < n) {
      buf = __json_put_escape(buf, pos, s[stop] as i32);
      pos = pos + __json_escape_width(s[stop] as i32);
      from = stop + 1;
    }
  }
  return string_from_bytes_unchecked(buf);
}
```

### `12 function __json_escape_width(b: i32): i32`

```fern
<<12 function __json_escape_width(b: i32): i32>>=
function __json_escape_width(b: i32): i32 {
  if (b == 34 || b == 92 || b == 10 || b == 13 || b == 9) {
    return 2;
  }
  return 6;
}

function __json_put_escape(buf: u8[], pos: i32, b: i32): u8[] {
  let out: u8[] = buf.with(pos, 92 as u8);
  if (b == 34 || b == 92) {
    return out.with(pos + 1, b as u8);
  }
  if (b == 10) {
    return out.with(pos + 1, 110 as u8);
  }
  if (b == 13) {
    return out.with(pos + 1, 114 as u8);
  }
  if (b == 9) {
    return out.with(pos + 1, 116 as u8);
  }
  out = out.with(pos + 1, 117 as u8);
  out = out.with(pos + 2, 48 as u8);
  out = out.with(pos + 3, 48 as u8);
  out = out.with(pos + 4, __json_hex_lower(b >> 4 & 15) as u8);
  return out.with(pos + 5, __json_hex_lower(b & 15) as u8);
}
```

### `13 pub function json_encode(v: JsonValue): string`

```fern
<<13 pub function json_encode(v: JsonValue): string>>=
pub function json_encode(v: JsonValue): string {
  let buf: u8[] = [];
  let i: i32 = 0;
  match (v) {
    JNull => {
      return "null";
    },
    JBool(b) => {
      if (b) {
        return "true";
      }
      return "false";
    },
    JNumber(s) => {
      return s;
    },
    JString(s) => {
      return "\"" + json_escape(s) + "\"";
    },
    JArray(arr) => {
      buf = buf.append(91 as u8);
      let n: i32 = arr.len();
      while (i < n) {
        if (i > 0) {
          buf = buf.append(44 as u8);
        }
        let enc: string = json_encode(arr[i]);
        let el: i32 = enc.len();
        let k: i32 = 0;
        while (k < el) {
          buf = buf.append(enc[k] as u8);
          k = k + 1;
        }
        i = i + 1;
      }
      buf = buf.append(93 as u8);
      return string_from_bytes_unchecked(buf);
    },
    JObject(o) => {
      buf = buf.append(123 as u8);
      let mi: i32 = 0;
      while (mi < o.names.len()) {
        if (mi > 0) {
          buf = buf.append(44 as u8);
        }
        buf = buf.append(34 as u8);
        let ek: string = json_escape(o.names[mi]);
        let ekl: i32 = ek.len();
        let kk: i32 = 0;
        while (kk < ekl) {
          buf = buf.append(ek[kk] as u8);
          kk = kk + 1;
        }
        buf = buf.append(34 as u8);
        buf = buf.append(58 as u8);
        let ev: string = json_encode(o.items[mi]);
        let evl: i32 = ev.len();
        let vk: i32 = 0;
        while (vk < evl) {
          buf = buf.append(ev[vk] as u8);
          vk = vk + 1;
        }
        mi = mi + 1;
      }
      buf = buf.append(125 as u8);
      return string_from_bytes_unchecked(buf);
    }
  }
}
```

### `14 function __json_spaces(n: i32): string`

```fern
<<14 function __json_spaces(n: i32): string>>=
function __json_spaces(n: i32): string {
  let b: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    b = b.append(32 as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(b);
}

function __json_append_str(buf: u8[], s: string): u8[] {
  let i: i32 = 0;
  let out: u8[] = buf;
  while (i < s.len()) {
    out = out.append(s[i] as u8);
    i = i + 1;
  }
  return out;
}

function __json_pretty(v: JsonValue, indent: i32, depth: i32): string {
  match (v) {
    JNull => {
      return "null";
    },
    JBool(b) => {
      if (b) {
        return "true";
      }
      return "false";
    },
    JNumber(s) => {
      return s;
    },
    JString(s) => {
      return "\"" + json_escape(s) + "\"";
    },
    JArray(arr) => {
      let n: i32 = arr.len();
      if (n == 0) {
        return "[]";
      }
      let inner: string = __json_spaces((depth + 1) * indent);
      let close: string = __json_spaces(depth * indent);
      let buf: u8[] = [];
      buf = buf.append(91 as u8);
      buf = __json_append_str(buf, "\n");
      let i: i32 = 0;
      while (i < n) {
        if (i > 0) {
          buf = __json_append_str(buf, ",\n");
        }
        buf = __json_append_str(buf, inner);
        buf = __json_append_str(buf, __json_pretty(arr[i], indent, depth + 1));
        i = i + 1;
      }
      buf = __json_append_str(buf, "\n");
      buf = __json_append_str(buf, close);
      buf = buf.append(93 as u8);
      return string_from_bytes_unchecked(buf);
    },
    JObject(o) => {
      if (o.len() == 0) {
        return "{}";
      }
      let inner: string = __json_spaces((depth + 1) * indent);
      let close: string = __json_spaces(depth * indent);
      let buf: u8[] = [];
      buf = buf.append(123 as u8);
      buf = __json_append_str(buf, "\n");
      let mi: i32 = 0;
      while (mi < o.names.len()) {
        if (mi > 0) {
          buf = __json_append_str(buf, ",\n");
        }
        buf = __json_append_str(buf, inner);
        buf = buf.append(34 as u8);
        buf = __json_append_str(buf, json_escape(o.names[mi]));
        buf = buf.append(34 as u8);
        buf = __json_append_str(buf, ": ");
        buf = __json_append_str(buf, __json_pretty(o.items[mi], indent, depth + 1));
        mi = mi + 1;
      }
      buf = __json_append_str(buf, "\n");
      buf = __json_append_str(buf, close);
      buf = buf.append(125 as u8);
      return string_from_bytes_unchecked(buf);
    }
  }
}
```

### `15 pub function json_encode_pretty(v: JsonValue, indent: i32): string`

```fern
<<15 pub function json_encode_pretty(v: JsonValue, indent: i32): string>>=
pub function json_encode_pretty(v: JsonValue, indent: i32): string {
  return __json_pretty(v, indent, 0);
}
```

### `16 const JSON_NOT_WS: u8[]`

```fern
<<16 const JSON_NOT_WS: u8[]>>=
const JSON_NOT_WS: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 0u8, 1u8, 1u8, 0u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];
```

### `17 function __json_ws(s: string, i: i32): i32`

```fern
<<17 function __json_ws(s: string, i: i32): i32>>=
function __json_ws(s: string, i: i32): i32 {
  if (i < s.len()) {
    let b: i32 = s[i] as i32;
    if (b == 32 || b == 9 || b == 10 || b == 13) {
      return __scan_set(s, i + 1, JSON_NOT_WS);
    }
  }
  return i;
}

function __json_byte(s: string, i: i32): i32 {
  if (i >= s.len()) {
    return 0;
  }
  return s[i] as i32;
}

function __json_fail(at: Cell[i32], pos: i32): void {
  if (at.get() >= 0) {
    at.set(-1 - pos);
  }
}

function __json_hex_value(c: i32): i32 {
  if (c >= 48 && c <= 57) {
    return c - 48;
  }
  if (c >= 97 && c <= 102) {
    return c - 87;
  }
  if (c >= 65 && c <= 70) {
    return c - 55;
  }
  return -1;
}

function __json_number_end(s: string, start: i32): (i32, boolean) {
  let n: i32 = s.len();
  let i: i32 = start;
  if (i < n && s[i] == 45) {
    i = i + 1;
  }
  let intStart: i32 = i;
  i = __scan_set(s, i, JSON_NOT_DIGIT);
  if (i == intStart) {
    return (-1 - i, false);
  }
  if (s[intStart] == 48 && i - intStart > 1) {
    return (-2 - intStart, false);
  }
  let whole: boolean = true;
  if (i < n && s[i] == 46) {
    let fracStart: i32 = i + 1;
    i = __scan_set(s, fracStart, JSON_NOT_DIGIT);
    if (i == fracStart) {
      return (-1 - i, false);
    }
    whole = false;
  }
  if (i < n && (s[i] == 101 || s[i] == 69)) {
    i = i + 1;
    if (i < n && (s[i] == 43 || s[i] == 45)) {
      i = i + 1;
    }
    let expStart: i32 = i;
    i = __scan_set(s, expStart, JSON_NOT_DIGIT);
    if (i == expStart) {
      return (-1 - i, false);
    }
    whole = false;
  }
  return (i, whole);
}

function __json_p_number(s: string, start: i32, at: Cell[i32]): JsonValue {
  let (end, _) = __json_number_end(s, start);
  if (end < 0) {
    __json_fail(at, -1 - end);
    return JNull;
  }
  at.set(end);
  return JNumber(slice_unchecked(s, start, end) + "");
}
```

### `18 const JSON_NOT_DIGIT: u8[]`

```fern
<<18 const JSON_NOT_DIGIT: u8[]>>=
const JSON_NOT_DIGIT: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];
```

### `19 function __json_cp_bytes(cp: i32): u8[]`

```fern
<<19 function __json_cp_bytes(cp: i32): u8[]>>=
function __json_cp_bytes(cp: i32): u8[] {
  if (cp < 128) {
    return [cp as u8];
  }
  if (cp < 2048) {
    return [
      (cp >> 6 | 192) as u8,
      (cp & 63 | 128) as u8,
    ];
  }
  if (cp < 65536) {
    return [
      (cp >> 12 | 224) as u8,
      (cp >> 6 & 63 | 128) as u8,
      (cp & 63 | 128) as u8,
    ];
  }
  return [
    (cp >> 18 | 240) as u8,
    (cp >> 12 & 63 | 128) as u8,
    (cp >> 6 & 63 | 128) as u8,
    (cp & 63 | 128) as u8,
  ];
}

function __json_p_uhex(s: string, i: i32, at: Cell[i32]): i32 {
  if (i + 4 > s.len()) {
    __json_fail(at, i);
    return -1;
  }
  let u: i32 = 0;
  let k: i32 = 0;
  while (k < 4) {
    let hv: i32 = __json_hex_value(s[i + k] as i32);
    if (hv < 0) {
      __json_fail(at, i + k);
      return -1;
    }
    u = u << 4 | hv;
    k = k + 1;
  }
  return u;
}
```

### `20 function __json_p_string(s: string, start: i32, at: Cell[i32]): string`

```fern
<<20 function __json_p_string(s: string, start: i32, at: Cell[i32]): string>>=
function __json_p_string(s: string, start: i32, at: Cell[i32]): string {
  if (__json_byte(s, start) != 34) {
    __json_fail(at, start);
    return "";
  }
  let i: i32 = start + 1;
  let plain: i32 = __scan_set(s, i, JSON_STRING_STOPS);
  if (plain < s.len() && s[plain] == 34) {
    at.set(plain + 1);
    return slice_unchecked(s, i, plain) + "";
  }
  let out: u8[] = [];
  let n: i32 = s.len();
  let b: i32 = 0;
  let cb: u8[] = [];
  let ci: i32 = 0;
  while (true) {
    b = __json_byte(s, i);
    if (b < 32) {
      __json_fail(at, i);
      return "";
    }
    if (b == 34) {
      at.set(i + 1);
      return string_from_bytes_unchecked(out);
    }
    if (b == 92) {
      i = i + 1;
      let esc: i32 = __json_byte(s, i);
      if (esc == 0) {
        __json_fail(at, i);
        return "";
      }
      if (esc == 117) {
        i = i + 1;
        let u: i32 = __json_p_uhex(s, i, at);
        if (u < 0) {
          return "";
        }
        i = i + 4;
        let cp1: i32 = 65533;
        let cp2: i32 = -1;
        if (u >= 55296 && u <= 56319) {
          if (i + 6 > n
            || s[i] != 92
            || s[i + 1] != 117) {
            cp1 = 65533;
          } else {
            i = i + 2;
            let low: i32 = __json_p_uhex(s, i, at);
            if (low < 0) {
              return "";
            }
            i = i + 4;
            if (low < 56320 || low > 57343) {
              cp1 = 65533;
              cp2 = low;
            } else {
              cp1 = 65536
                + (u - 55296 << 10)
                + (low - 56320);
            }
          }
        } else if (u >= 56320 && u <= 57343) {
          cp1 = 65533;
        } else {
          cp1 = u;
        }
        cb = __json_cp_bytes(cp1);
        ci = 0;
        while (ci < cb.len()) {
          out = out.append(cb[ci] as u8);
          ci = ci + 1;
        }
        if (cp2 >= 0) {
          cb = __json_cp_bytes(cp2);
          ci = 0;
          while (ci < cb.len()) {
            out = out.append(cb[ci] as u8);
            ci = ci + 1;
          }
        }
      } else {
        let ch: i32 = -1;
        if (esc == 34 || esc == 92 || esc == 47) {
          ch = esc;
        } else if (esc == 110) {
          ch = 10;
        } else if (esc == 114) {
          ch = 13;
        } else if (esc == 116) {
          ch = 9;
        } else if (esc == 98) {
          ch = 8;
        } else if (esc == 102) {
          ch = 12;
        } else {
          __json_fail(at, i);
          return "";
        }
        out = out.append(ch as u8);
        i = i + 1;
      }
    } else {
      let stop: i32 = __scan_set(s, i, JSON_STRING_STOPS);
      out = __json_append_run(out, s, i, stop);
      i = stop;
    }
  }
  return "";
}
```

### `21 const JSON_STRING_STOPS: u8[]`

```fern
<<21 const JSON_STRING_STOPS: u8[]>>=
const JSON_STRING_STOPS: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
];
```

### `22 function __json_append_run(out: u8[], s: string, from: i32, stop: i32): u8[]`

```fern
<<22 function __json_append_run(out: u8[], s: string, from: i32, stop: i32): u8[]>>=
function __json_append_run(out: u8[], s: string, from: i32, stop: i32): u8[] {
  let acc: u8[] = out;
  let i: i32 = from;
  while (i < stop) {
    acc = acc.append(s[i] as u8);
    i = i + 1;
  }
  return acc;
}

function __json_p_array(s: string, start: i32, at: Cell[i32], depth: i32): JsonValue {
  let i: i32 = __json_ws(s, start + 1);
  if (__json_byte(s, i) == 93) {
    at.set(i + 1);
    let empty: JsonValue[] = [];
    return JArray(empty);
  }
  let arr: JsonValue[] = [];
  at.set(i);
  while (true) {
    let elem: JsonValue = __json_p_value(s, at, depth);
    i = at.get();
    if (i < 0) {
      return JNull;
    }
    arr = arr.append(elem);
    i = __json_ws(s, i);
    let b: i32 = __json_byte(s, i);
    if (b == 44) {
      at.set(i + 1);
      continue;
    }
    if (b == 93) {
      at.set(i + 1);
      return JArray(arr);
    }
    __json_fail(at, i);
    return JNull;
  }
  return JNull;
}

function __json_p_object(s: string, start: i32, at: Cell[i32], depth: i32): JsonValue {
  let i: i32 = __json_ws(s, start + 1);
  if (__json_byte(s, i) == 125) {
    at.set(i + 1);
    return JObject(object());
  }
  let keys: string[] = [];
  let values: JsonValue[] = [];
  while (true) {
    let key: string = __json_p_string(s, i, at);
    i = at.get();
    if (i < 0) {
      return JNull;
    }
    i = __json_ws(s, i);
    if (__json_byte(s, i) != 58) {
      __json_fail(at, i);
      return JNull;
    }
    at.set(i + 1);
    let val: JsonValue = __json_p_value(s, at, depth);
    i = at.get();
    if (i < 0) {
      return JNull;
    }
    keys = keys.append(key);
    values = values.append(val);
    i = __json_ws(s, i);
    let b: i32 = __json_byte(s, i);
    if (b == 44) {
      i = __json_ws(s, i + 1);
      continue;
    }
    if (b == 125) {
      at.set(i + 1);
      return JObject(object_of(keys, values));
    }
    __json_fail(at, i);
    return JNull;
  }
  return JNull;
}

function __json_p_value(s: string, at: Cell[i32], depth: i32): JsonValue {
  let i: i32 = __json_ws(s, at.get());
  let b: i32 = __json_byte(s, i);
  if (b == 34) {
    let str: string = __json_p_string(s, i, at);
    if (at.get() < 0) {
      return JNull;
    }
    return JString(str);
  }
  if (b == 45 || b >= 48 && b <= 57) {
    return __json_p_number(s, i, at);
  }
  if (b == 91 || b == 123) {
    if (depth >= 128) {
      __json_fail(at, i);
      return JNull;
    }
    if (b == 91) {
      return __json_p_array(s, i, at, depth + 1);
    }
    return __json_p_object(s, i, at, depth + 1);
  }
  if (b == 110 && __json_word(s, i, "null")) {
    at.set(i + 4);
    return JNull;
  }
  if (b == 116 && __json_word(s, i, "true")) {
    at.set(i + 4);
    return JBool(true);
  }
  if (b == 102 && __json_word(s, i, "false")) {
    at.set(i + 5);
    return JBool(false);
  }
  __json_fail(at, i);
  return JNull;
}
```

### `23 function __json_word(s: string, i: i32, word: string): boolean`

```fern
<<23 function __json_word(s: string, i: i32, word: string): boolean>>=
function __json_word(s: string, i: i32, word: string): boolean {
  if (i + word.len() > s.len()) {
    return false;
  }
  let k: i32 = 1;
  while (k < word.len()) {
    if (s[i + k] != word[k]) {
      return false;
    }
    k = k + 1;
  }
  return true;
}

function __json_line_col(s: string, off: i32): (i32, i32) {
  let line: i32 = 1;
  let col: i32 = 1;
  let i: i32 = 0;
  let stop: i32 = off;
  if (stop > s.len()) {
    stop = s.len();
  }
  while (i < stop) {
    if (s[i] as i32 == 10) {
      line = line + 1;
      col = 1;
    } else {
      col = col + 1;
    }
    i = i + 1;
  }
  return (line, col);
}
```

### `24 pub struct JsonError`

```fern
<<24 pub struct JsonError>>=
pub struct JsonError {
  message: string,
  offset: i32,
  line: i32,
  col: i32,
}
```

### `25 pub function (e: JsonError) message(): string`

```fern
<<25 pub function (e: JsonError) message(): string>>=
pub function (e: JsonError) message(): string {
  return e.to_string();
}
```

### `26 impl error.Error for JsonError`

```fern
<<26 impl error.Error for JsonError>>=
impl error.Error for JsonError {}

impl cmp.Display for JsonError {}
```

### `27 pub function (e: JsonError) to_string(): string`

```fern
<<27 pub function (e: JsonError) to_string(): string>>=
pub function (e: JsonError) to_string(): string {
  return e.line.to_string() + ":" + e.col.to_string() + ": " + e.message;
}
```

### `28 function __json_message(s: string, off: i32): string`

```fern
<<28 function __json_message(s: string, off: i32): string>>=
function __json_message(s: string, off: i32): string {
  if (off >= s.len()) {
    return "unexpected end of input";
  }
  let b: i32 = s[off] as i32;
  if (b < 32) {
    return "unexpected control byte in input";
  }
  if (b < 128) {
    return "unexpected byte '" + string_from_bytes_unchecked([s[off]]) + "'";
  }
  return "unexpected byte '\\x" + b.to_hex().pad_start(2, '0') + "'";
}
```

### `29 pub function json_parse_result(s: string): Result[JsonValue, JsonError]`

```fern
<<29 pub function json_parse_result(s: string): Result[JsonValue, JsonError]>>=
pub function json_parse_result(s: string): Result[JsonValue, JsonError] {
  let at: Cell[i32] = cell_new(0);
  let v: JsonValue = __json_p_value(s, at, 0);
  let end: i32 = at.get();
  if (end < 0) {
    let off: i32 = -1 - end;
    let (ln, cl) = __json_line_col(s, off);
    return Err(JsonError {
      message: __json_message(s, off),
      offset: off,
      line: ln,
      col: cl,
    });
  }
  end = __json_ws(s, end);
  if (end < s.len()) {
    let (ln2, cl2) = __json_line_col(s, end);
    return Err(JsonError {
      message: "trailing content after the top-level value",
      offset: end,
      line: ln2,
      col: cl2,
    });
  }
  return Ok(v);
}

pub function json_parse(s: string): Option[JsonValue] {
  match (json_parse_result(s)) {
    Ok(v) => {
      return Some(v);
    },
    Err(_) => {
      return None;
    }
  }
}
```

### `30 pub struct Document`

```fern
<<30 pub struct Document>>=
pub struct Document { text: string, tape: u64[] }

const JSON_T_NULL: i32 = 1;

const JSON_T_TRUE: i32 = 2;

const JSON_T_FALSE: i32 = 3;

const JSON_T_NUMBER: i32 = 4;

const JSON_T_STRING: i32 = 5;

const JSON_T_ARRAY: i32 = 6;

const JSON_T_OBJECT: i32 = 7;
```

### `31 function __json_t_word(kind: i32, flags: i32, a: i32): u64`

```fern
<<31 function __json_t_word(kind: i32, flags: i32, a: i32): u64>>=
function __json_t_word(kind: i32, flags: i32, a: i32): u64 {
  return kind as u64 << 56 as u64 | flags as u64 << 32 as u64 | (a as u32) as u64;
}

function __json_t_node(own tape: u64[], kind: i32, flags: i32, a: i32, b: i32): u64[] {
  return tape.append(__json_t_word(kind, flags, a)).append((b as u32) as u64);
}

function __json_t_close(own tape: u64[], node: i32, kind: i32, count: i32): u64[] {
  let next: i32 = tape.len() / 2;
  return tape.with(2 * node, __json_t_word(kind, 0, next)).with(2 * node + 1, (count as u32) as u64);
}

function __json_string_end(s: string, plain: i32): i32 {
  let i: i32 = plain;
  while (true) {
    let b: i32 = __json_byte(s, i);
    if (b == 34) {
      return i + 1;
    }
    if (b == 92) {
      i = __json_escape_end(s, i + 1);
    } else if (b < 32) {
      return -1 - i;
    } else {
      i = __scan_set(s, i, JSON_STRING_STOPS);
    }
    if (i < 0) {
      return i;
    }
  }
  return -1;
}

function __json_short_escape(esc: i32): boolean {
  return esc == 34 || esc == 92 || esc == 47 || esc == 110 || esc == 114 || esc == 116 || esc == 98 || esc == 102;
}

function __json_escape_end(s: string, i: i32): i32 {
  let esc: i32 = __json_byte(s, i);
  if (__json_short_escape(esc)) {
    return i + 1;
  }
  if (esc != 117) {
    return -1 - i;
  }
  let h: i32 = i + 1;
  if (h + 4 > s.len()) {
    return -1 - h;
  }
  let k: i32 = 0;
  while (k < 4) {
    if (__json_hex_value(s[h + k] as i32) < 0) {
      return -1 - (h + k);
    }
    k = k + 1;
  }
  return h + 4;
}

function __json_t_scalar(s: string, i: i32, own tape: u64[]): (u64[], i32) {
  let b: i32 = __json_byte(s, i);
  if (b == 34) {
    return __json_t_string(s, i, tape);
  }
  if (b == 45 || b >= 48 && b <= 57) {
    let (end, whole) = __json_number_end(s, i);
    if (end < 0) {
      return (tape, end);
    }
    let flag: i32 = 0;
    if (whole) {
      flag = 1;
    }
    return (__json_t_node(tape, JSON_T_NUMBER, flag, i, end), end);
  }
  return __json_t_literal(s, i, b, tape);
}

function __json_t_string(s: string, i: i32, own tape: u64[]): (u64[], i32) {
  let plain: i32 = __scan_set(s, i + 1, JSON_STRING_STOPS);
  if (plain < s.len() && s[plain] == 34) {
    return (__json_t_node(tape, JSON_T_STRING, 0, i + 1, plain), plain + 1);
  }
  let end: i32 = __json_string_end(s, plain);
  if (end < 0) {
    return (tape, end);
  }
  return (__json_t_node(tape, JSON_T_STRING, 1, i + 1, end - 1), end);
}

function __json_t_literal(s: string, i: i32, b: i32, own tape: u64[]): (u64[], i32) {
  if (b == 110 && __json_word(s, i, "null")) {
    return (__json_t_node(tape, JSON_T_NULL, 0, 0, 0), i + 4);
  }
  if (b == 116 && __json_word(s, i, "true")) {
    return (__json_t_node(tape, JSON_T_TRUE, 0, 0, 0), i + 4);
  }
  if (b == 102 && __json_word(s, i, "false")) {
    return (__json_t_node(tape, JSON_T_FALSE, 0, 0, 0), i + 5);
  }
  return (tape, -1 - i);
}

function __json_t_key(s: string, i: i32, own tape: u64[]): (u64[], i32) {
  if (__json_byte(s, i) != 34) {
    return (tape, -1 - i);
  }
  let (t, end) = __json_t_string(s, i, tape);
  if (end < 0) {
    return (t, end);
  }
  let k: i32 = __json_ws(s, end);
  if (__json_byte(s, k) != 58) {
    return (t, -1 - k);
  }
  return (t, k + 1);
}

function __json_t_kind(tape: u64[], node: i32): i32 {
  return (tape[2 * node] >> 56 as u64) as i32;
}
```

### `32 function __json_t_value(s: string, i: i32, own tape: u64[], depth: i32): (u64[], i32)`

```fern
<<32 function __json_t_value(s: string, i: i32, own tape: u64[], depth: i32): (u64[], i32)>>=
function __json_t_value(s: string, i: i32, own tape: u64[], depth: i32): (u64[], i32) {
  let b: i32 = __json_byte(s, i);
  if (b != 91 && b != 123) {
    return __json_t_scalar(s, i, tape);
  }
  if (depth >= 128) {
    return (tape, -1 - i);
  }
  if (b == 91) {
    return __json_t_array(s, i, tape, depth + 1);
  }
  return __json_t_object(s, i, tape, depth + 1);
}

function __json_t_array(s: string, start: i32, own tape: u64[], depth: i32): (u64[], i32) {
  let node: i32 = tape.len() / 2;
  tape = __json_t_node(tape, JSON_T_ARRAY, 0, 0, 0);
  let i: i32 = __json_ws(s, start + 1);
  let count: i32 = 0;
  if (__json_byte(s, i) == 93) {
    return (__json_t_close(tape, node, JSON_T_ARRAY, 0), i + 1);
  }
  while (true) {
    let (t, end) = __json_t_value(s, i, tape, depth);
    tape = t;
    if (end < 0) {
      return (tape, end);
    }
    count = count + 1;
    i = __json_ws(s, end);
    let b: i32 = __json_byte(s, i);
    if (b == 93) {
      return (__json_t_close(tape, node, JSON_T_ARRAY, count), i + 1);
    }
    if (b != 44) {
      return (tape, -1 - i);
    }
    i = __json_ws(s, i + 1);
  }
  return (tape, -1);
}

function __json_t_object(s: string, start: i32, own tape: u64[], depth: i32): (u64[], i32) {
  let node: i32 = tape.len() / 2;
  tape = __json_t_node(tape, JSON_T_OBJECT, 0, 0, 0);
  let i: i32 = __json_ws(s, start + 1);
  let count: i32 = 0;
  if (__json_byte(s, i) == 125) {
    return (__json_t_close(tape, node, JSON_T_OBJECT, 0), i + 1);
  }
  while (true) {
    let (t, at) = __json_t_key(s, i, tape);
    tape = t;
    if (at < 0) {
      return (tape, at);
    }
    let (t2, end) = __json_t_value(s, __json_ws(s, at), tape, depth);
    tape = t2;
    if (end < 0) {
      return (tape, end);
    }
    count = count + 1;
    i = __json_ws(s, end);
    let b: i32 = __json_byte(s, i);
    if (b == 125) {
      return (__json_t_close(tape, node, JSON_T_OBJECT, count), i + 1);
    }
    if (b != 44) {
      return (tape, -1 - i);
    }
    i = __json_ws(s, i + 1);
  }
  return (tape, -1);
}
```

### `33 pub function parse_document(s: string): Result[Document, JsonError]`

```fern
<<33 pub function parse_document(s: string): Result[Document, JsonError]>>=
pub function parse_document(s: string): Result[Document, JsonError] {
  let empty: u64[] = [];
  let (tape, end) = __json_t_value(s, __json_ws(s, 0), empty, 0);
  let fail: i32 = -1 - end;
  if (end >= 0) {
    fail = __json_ws(s, end);
    if (fail == s.len()) {
      return Ok(Document { text: s, tape: tape });
    }
  }
  let (ln, cl) = __json_line_col(s, fail);
  if (end >= 0) {
    return Err(JsonError { message: "trailing content after the top-level value", offset: fail, line: ln, col: cl });
  }
  return Err(JsonError { message: __json_message(s, fail), offset: fail, line: ln, col: cl });
}
```

### `34 function __json_t_a(d: Document, n: i32): i32`

```fern
<<34 function __json_t_a(d: Document, n: i32): i32>>=
function __json_t_a(d: Document, n: i32): i32 {
  return (d.tape[2 * n] & 4294967295u64) as i32;
}

function __json_t_b(d: Document, n: i32): i32 {
  return d.tape[2 * n + 1] as i32;
}

function __json_t_flag(d: Document, n: i32): boolean {
  return (d.tape[2 * n] >> 32u64 & 1u64) == 1u64;
}

function __json_t_after(d: Document, n: i32): i32 {
  let k: i32 = __json_t_kind(d.tape, n);
  if (k == JSON_T_ARRAY || k == JSON_T_OBJECT) {
    return __json_t_a(d, n);
  }
  return n + 1;
}
```

### `35 pub function (d: Document) root(): i32`

```fern
<<35 pub function (d: Document) root(): i32>>=
pub function (d: Document) root(): i32 {
  return 0;
}

pub function (d: Document) is_null(n: i32): boolean {
  return __json_t_kind(d.tape, n) == JSON_T_NULL;
}

pub function (d: Document) is_bool(n: i32): boolean {
  let k: i32 = __json_t_kind(d.tape, n);
  return k == JSON_T_TRUE || k == JSON_T_FALSE;
}

pub function (d: Document) is_number(n: i32): boolean {
  return __json_t_kind(d.tape, n) == JSON_T_NUMBER;
}

pub function (d: Document) is_string(n: i32): boolean {
  return __json_t_kind(d.tape, n) == JSON_T_STRING;
}

pub function (d: Document) is_array(n: i32): boolean {
  return __json_t_kind(d.tape, n) == JSON_T_ARRAY;
}

pub function (d: Document) is_object(n: i32): boolean {
  return __json_t_kind(d.tape, n) == JSON_T_OBJECT;
}

pub function (d: Document) len(n: i32): i32 {
  let k: i32 = __json_t_kind(d.tape, n);
  if (k == JSON_T_ARRAY || k == JSON_T_OBJECT) {
    return __json_t_b(d, n);
  }
  return 0;
}

pub function (d: Document) first(n: i32): i32 {
  if (d.len(n) == 0) {
    return -1;
  }
  return n + 1;
}

pub function (d: Document) next(parent: i32, c: i32): i32 {
  let after: i32 = __json_t_after(d, c);
  if (__json_t_kind(d.tape, parent) == JSON_T_OBJECT) {
    after = __json_t_after(d, after);
  }
  if (after >= __json_t_a(d, parent)) {
    return -1;
  }
  return after;
}

pub function (d: Document) value(key: i32): i32 {
  return key + 1;
}

pub function (d: Document) at(n: i32, k: i32): Option[i32] {
  if (__json_t_kind(d.tape, n) != JSON_T_ARRAY || k < 0 || k >= __json_t_b(d, n)) {
    return None;
  }
  let c: i32 = n + 1;
  let j: i32 = 0;
  while (j < k) {
    c = __json_t_after(d, c);
    j = j + 1;
  }
  return Some(c);
}

pub function (d: Document) get(n: i32, key: string): Option[i32] {
  if (__json_t_kind(d.tape, n) != JSON_T_OBJECT) {
    return None;
  }
  let found: i32 = -1;
  let c: i32 = d.first(n);
  while (c >= 0) {
    if (__json_t_key_is(d, c, key)) {
      found = c + 1;
    }
    c = d.next(n, c);
  }
  if (found < 0) {
    return None;
  }
  return Some(found);
}
```

### `36 function __json_t_key_is(d: Document, k: i32, key: string): boolean`

```fern
<<36 function __json_t_key_is(d: Document, k: i32, key: string): boolean>>=
function __json_t_key_is(d: Document, k: i32, key: string): boolean {
  if (__json_t_flag(d, k)) {
    return __json_t_decode(d, k) == key;
  }
  let a: i32 = __json_t_a(d, k);
  let b: i32 = __json_t_b(d, k);
  return b - a == key.len() && slice_unchecked(d.text, a, b) == key;
}

function __json_t_slice(d: Document, n: i32): string {
  return slice_unchecked(d.text, __json_t_a(d, n), __json_t_b(d, n)) + "";
}

function __json_t_text(d: Document, n: i32): string {
  if (__json_t_flag(d, n)) {
    return __json_t_decode(d, n);
  }
  return __json_t_slice(d, n);
}

function __json_t_decode(d: Document, n: i32): string {
  return __json_p_string(d.text, __json_t_a(d, n) - 1, cell_new(0));
}
```

### `37 pub function (d: Document) as_bool(n: i32): Option[boolean]`

```fern
<<37 pub function (d: Document) as_bool(n: i32): Option[boolean]>>=
pub function (d: Document) as_bool(n: i32): Option[boolean] {
  let k: i32 = __json_t_kind(d.tape, n);
  if (k == JSON_T_TRUE) {
    return Some(true);
  }
  if (k == JSON_T_FALSE) {
    return Some(false);
  }
  return None;
}

pub function (d: Document) as_string(n: i32): Option[string] {
  if (__json_t_kind(d.tape, n) != JSON_T_STRING) {
    return None;
  }
  return Some(__json_t_text(d, n));
}

pub function (d: Document) number_text(n: i32): Option[string] {
  if (__json_t_kind(d.tape, n) != JSON_T_NUMBER) {
    return None;
  }
  return Some(__json_t_slice(d, n));
}

pub function (d: Document) as_f64(n: i32): Option[f64] {
  if (__json_t_kind(d.tape, n) != JSON_T_NUMBER) {
    return None;
  }
  return slice_unchecked(d.text, __json_t_a(d, n), __json_t_b(d, n)).parse_float();
}

pub function (d: Document) as_i64(n: i32): Option[i64] {
  if (__json_t_kind(d.tape, n) != JSON_T_NUMBER || !__json_t_flag(d, n)) {
    return None;
  }
  let i: i32 = __json_t_a(d, n);
  let end: i32 = __json_t_b(d, n);
  let neg: boolean = d.text[i] == 45;
  if (neg) {
    i = i + 1;
  }
  let acc: i64 = 0;
  let floor: i64 = -922337203685477580;
  while (i < end) {
    let digit: i64 = (d.text[i] - 48) as i64;
    if (acc < floor || acc == floor && digit > 8) {
      return None;
    }
    acc = acc * 10 - digit;
    i = i + 1;
  }
  if (neg) {
    return Some(acc);
  }
  if (acc == -9223372036854775807 - 1) {
    return None;
  }
  return Some(0 - acc);
}

pub function (d: Document) to_value(n: i32): JsonValue {
  let k: i32 = __json_t_kind(d.tape, n);
  if (k == JSON_T_NULL) {
    return JNull;
  }
  if (k == JSON_T_TRUE || k == JSON_T_FALSE) {
    return JBool(k == JSON_T_TRUE);
  }
  if (k == JSON_T_NUMBER) {
    return JNumber(__json_t_slice(d, n));
  }
  if (k == JSON_T_STRING) {
    return JString(__json_t_text(d, n));
  }
  if (k == JSON_T_ARRAY) {
    return JArray(__json_t_elements(d, n));
  }
  return JObject(__json_t_members(d, n));
}
```

### `38 function __json_t_elements(d: Document, n: i32): JsonValue[]`

```fern
<<38 function __json_t_elements(d: Document, n: i32): JsonValue[]>>=
function __json_t_elements(d: Document, n: i32): JsonValue[] {
  let arr: JsonValue[] = [];
  let c: i32 = d.first(n);
  while (c >= 0) {
    arr = arr.append(d.to_value(c));
    c = d.next(n, c);
  }
  return arr;
}

function __json_t_members(d: Document, n: i32): JsonObject {
  let keys: string[] = [];
  let values: JsonValue[] = [];
  let c: i32 = d.first(n);
  while (c >= 0) {
    keys = keys.append(__json_t_text(d, c));
    values = values.append(d.to_value(c + 1));
    c = d.next(n, c);
  }
  return object_of(keys, values);
}
```

### `39 pub function json_get(obj: JsonValue, key: string): Option[JsonValue]`

```fern
<<39 pub function json_get(obj: JsonValue, key: string): Option[JsonValue]>>=
pub function json_get(obj: JsonValue, key: string): Option[JsonValue] {
  match (obj) {
    JObject(m) => {
      match (m.get(key)) {
        Some(v) => {
          return Some(v);
        },
        None => {
          return None;
        }
      }
    },
    _ => {
      return None;
    }
  }
  return None;
}

pub function json_get_string(obj: JsonValue, key: string): Option[string] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JString(s) => {
          return Some(s);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}

pub function json_get_i32(obj: JsonValue, key: string): Option[i32] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JNumber(s) => {
          return __json_number_i32(s);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `40 function __json_number_i32(s: string): Option[i32]`

```fern
<<40 function __json_number_i32(s: string): Option[i32]>>=
function __json_number_i32(s: string): Option[i32] {
  let acc: i64 = 0;
  let i: i32 = 0;
  let neg: boolean = false;
  let nlen: i32 = s.len();
  if (nlen == 0) {
    return None;
  }
  if (s[0] == 45) {
    neg = true;
    i = 1;
  }
  if (i >= nlen) {
    return None;
  }
  while (i < nlen) {
    let b: i32 = s[i] as i32;
    if (b < 48 || b > 57) {
      return None;
    }
    acc = acc * 10 + (b - 48) as i64;
    if (acc > 2147483648) {
      return None;
    }
    i = i + 1;
  }
  if (neg) {
    return Some((0 - acc) as i32);
  }
  if (acc > 2147483647) {
    return None;
  }
  return Some(acc as i32);
}
```

### `41 pub function json_get_i64(obj: JsonValue, key: string): Option[i64]`

```fern
<<41 pub function json_get_i64(obj: JsonValue, key: string): Option[i64]>>=
pub function json_get_i64(obj: JsonValue, key: string): Option[i64] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JNumber(s) => {
          return __json_number_i64(s);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `42 function __json_number_i64(s: string): Option[i64]`

```fern
<<42 function __json_number_i64(s: string): Option[i64]>>=
function __json_number_i64(s: string): Option[i64] {
  let n: i64 = 0;
  let i: i32 = 0;
  let sign: i64 = 1;
  let nlen: i32 = s.len();
  let digits: i32 = 0;
  if (nlen == 0) {
    return None;
  }
  if (s[0] == 45) {
    sign = 0 - 1;
    i = 1;
  }
  if (i >= nlen) {
    return None;
  }
  while (i < nlen) {
    let b: i32 = s[i] as i32;
    if (b < 48 || b > 57) {
      return None;
    }
    n = n * 10 + (b - 48) as i64;
    digits = digits + 1;
    if (n < 0 || digits > 19) {
      return None;
    }
    i = i + 1;
  }
  return Some(n * sign);
}

function __json_number_u64(s: string): Option[u64] {
  let n: u64 = 0;
  let i: i32 = 0;
  let nlen: i32 = s.len();
  if (nlen == 0) {
    return None;
  }
  while (i < nlen) {
    let b: i32 = s[i] as i32;
    if (b < 48 || b > 57) {
      return None;
    }
    let d: u64 = (b - 48) as u64;
    if (n > 1844674407370955161) {
      return None;
    }
    if (n == 1844674407370955161 && d > 5) {
      return None;
    }
    n = n * 10 + d;
    i = i + 1;
  }
  return Some(n);
}
```

### `43 pub function json_get_f64(obj: JsonValue, key: string): Option[f64]`

```fern
<<43 pub function json_get_f64(obj: JsonValue, key: string): Option[f64]>>=
pub function json_get_f64(obj: JsonValue, key: string): Option[f64] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JNumber(s) => {
          return s.parse_float();
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}

pub function json_get_bool(obj: JsonValue, key: string): Option[boolean] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JBool(b) => {
          return Some(b);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}

pub function json_get_array(obj: JsonValue, key: string): Option[JsonValue[]] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JArray(arr) => {
          return Some(arr);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}

pub function json_get_object(obj: JsonValue, key: string): Option[JsonValue] {
  match (json_get(obj, key)) {
    Some(v) => {
      match (v) {
        JObject(_) => {
          return Some(v);
        },
        _ => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `44 function __json_ptr_unescape(tok: string): string`

```fern
<<44 function __json_ptr_unescape(tok: string): string>>=
function __json_ptr_unescape(tok: string): string {
  let n: i32 = tok.len();
  let has: boolean = false;
  let i: i32 = 0;
  while (i < n) {
    if (tok[i] == 126) {
      has = true;
      i = n;
    } else {
      i = i + 1;
    }
  }
  if (!has) {
    return tok;
  }
  let out: u8[] = [];
  i = 0;
  while (i < n) {
    let c: i32 = tok[i] as i32;
    if (c == 126 && i + 1 < n) {
      let d: i32 = tok[i + 1] as i32;
      if (d == 49) {
        out = out.append(47 as u8);
        i = i + 2;
      } else if (d == 48) {
        out = out.append(126 as u8);
        i = i + 2;
      } else {
        out = out.append(tok[i]);
        i = i + 1;
      }
    } else {
      out = out.append(tok[i]);
      i = i + 1;
    }
  }
  return string_from_bytes_unchecked(out);
}

function __json_ptr_index(tok: string): i32 {
  let n: i32 = tok.len();
  if (n == 0) {
    return 0 - 1;
  }
  if (tok[0] == 48 && n > 1) {
    return 0 - 1;
  }
  let acc: i64 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = tok[i] as i32;
    if (b < 48 || b > 57) {
      return 0 - 1;
    }
    acc = acc * 10 + (b - 48) as i64;
    if (acc > 2147483647) {
      return 0 - 1;
    }
    i = i + 1;
  }
  return acc as i32;
}
```

### `45 pub function json_pointer(doc: JsonValue, pointer: string): Option[JsonValue]`

```fern
<<45 pub function json_pointer(doc: JsonValue, pointer: string): Option[JsonValue]>>=
pub function json_pointer(doc: JsonValue, pointer: string): Option[JsonValue] {
  let n: i32 = pointer.len();
  if (n == 0) {
    return Some(doc);
  }
  if (pointer[0] != 47) {
    return None;
  }
  let cur: JsonValue = doc;
  let i: i32 = 1;
  while (i <= n) {
    let j: i32 = i;
    while (j < n && pointer[j] != 47) {
      j = j + 1;
    }
    let tok: string = __json_ptr_unescape(slice_unchecked(pointer, i, j));
    match (cur) {
      JObject(m) => {
        match (m.get(tok)) {
          Some(v) => {
            cur = v;
          },
          None => {
            return None;
          }
        }
      },
      JArray(arr) => {
        let idx: i32 = __json_ptr_index(tok);
        if (idx < 0 || idx >= arr.len()) {
          return None;
        }
        cur = arr[idx];
      },
      _ => {
        return None;
      }
    }
    i = j + 1;
  }
  return Some(cur);
}

pub function json_is_null(v: JsonValue): boolean {
  match (v) {
    JNull => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}
```

### `46 pub trait Json`

```fern
<<46 pub trait Json>>=
pub trait Json {
  function to_json(self: Self): string;
}

impl Json for i32 {
  function to_json(self: i32): string {
    return self.to_string();
  }
}

impl Json for i64 {
  function to_json(self: i64): string {
    return self.to_string();
  }
}

impl Json for u32 {
  function to_json(self: u32): string {
    return self.to_string();
  }
}

impl Json for u64 {
  function to_json(self: u64): string {
    return self.to_string();
  }
}

impl Json for boolean {
  function to_json(self: boolean): string {
    if (self) {
      return "true";
    }
    return "false";
  }
}

impl Json for f64 {
  function to_json(self: f64): string {
    return self.to_string();
  }
}

impl Json for f32 {
  function to_json(self: f32): string {
    return self.to_string();
  }
}

impl Json for string {
  function to_json(self: string): string {
    return "\"" + json_escape(self) + "\"";
  }
}
```

### `47 pub function (xs: T[]) to_json[T: Json](): string`

```fern
<<47 pub function (xs: T[]) to_json[T: Json](): string>>=
pub function (xs: T[]) to_json[T: Json](): string {
  let out: u8[] = [];
  out = out.append(91 as u8);
  let i: i32 = 0;
  while (i < xs.len()) {
    if (i > 0) {
      out = out.append(44 as u8);
    }
    let e: string = xs[i].to_json();
    let el: i32 = e.len();
    let k: i32 = 0;
    while (k < el) {
      out = out.append(e[k] as u8);
      k = k + 1;
    }
    i = i + 1;
  }
  out = out.append(93 as u8);
  return string_from_bytes_unchecked(out);
}
```

### Optional values

An absent value encodes as JSON null. A present value uses the codec of its element type.

```fern
<<Optional JSON values>>=
// An absent value maps to JSON null. Present values keep their own codec.
impl[T: Json] Json for Option[T] {
  function to_json(self: Option[T]): string {
    match (self) {
      Some(inner) => {
        return inner.to_json();
      },
      None => {
        return "null";
      }
    }
  }
}
```

### `48 pub trait FromJson`

```fern
<<48 pub trait FromJson>>=
pub trait FromJson {
  function from_json_value(v: JsonValue): Result[Self, string];
}
```

### `49 pub function json_kind(v: JsonValue): string`

```fern
<<49 pub function json_kind(v: JsonValue): string>>=
pub function json_kind(v: JsonValue): string {
  match (v) {
    JNull => {
      return "null";
    },
    JBool(b) => {
      return "a boolean";
    },
    JNumber(s) => {
      return "a number";
    },
    JString(s) => {
      return "a string";
    },
    JArray(xs) => {
      return "an array";
    },
    JObject(m) => {
      return "an object";
    }
  }
  return "";
}
```

### `50 impl FromJson for i32`

```fern
<<50 impl FromJson for i32>>=
impl FromJson for i32 {
  function from_json_value(v: JsonValue): Result[i32, string] {
    match (v) {
      JNumber(s) => {
        match (__json_number_i32(s)) {
          Some(n) => {
            return Ok(n);
          },
          None => {
            return Err("expected an integer in the i32 range, got " + s);
          }
        }
      },
      _ => {
        return Err("expected an integer, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for i64 {
  function from_json_value(v: JsonValue): Result[i64, string] {
    match (v) {
      JNumber(s) => {
        match (__json_number_i64(s)) {
          Some(n) => {
            return Ok(n);
          },
          None => {
            return Err("expected an integer in the i64 range, got " + s);
          }
        }
      },
      _ => {
        return Err("expected an integer, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for u32 {
  function from_json_value(v: JsonValue): Result[u32, string] {
    match (v) {
      JNumber(s) => {
        match (__json_number_i64(s)) {
          Some(n) => {
            if (n < 0 || n > 4294967295) {
              return Err("expected an integer in the u32 range, got " + s);
            }
            return Ok(n as u32);
          },
          None => {
            return Err("expected an integer in the u32 range, got " + s);
          }
        }
      },
      _ => {
        return Err("expected an integer, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for u64 {
  function from_json_value(v: JsonValue): Result[u64, string] {
    match (v) {
      JNumber(s) => {
        match (__json_number_u64(s)) {
          Some(n) => {
            return Ok(n);
          },
          None => {
            return Err("expected an integer in the u64 range, got " + s);
          }
        }
      },
      _ => {
        return Err("expected an integer, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for f64 {
  function from_json_value(v: JsonValue): Result[f64, string] {
    match (v) {
      JNumber(s) => {
        match (s.parse_float()) {
          Some(f) => {
            return Ok(f);
          },
          None => {
            return Err("expected a number, got " + s);
          }
        }
      },
      _ => {
        return Err("expected a number, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for f32 {
  function from_json_value(v: JsonValue): Result[f32, string] {
    match (v) {
      JNumber(s) => {
        match (s.parse_float()) {
          Some(f) => {
            return Ok(f as f32);
          },
          None => {
            return Err("expected a number, got " + s);
          }
        }
      },
      _ => {
        return Err("expected a number, got " + json_kind(v));
      }
    }
    return Err("");
  }
}
```

### `51 impl FromJson for boolean`

```fern
<<51 impl FromJson for boolean>>=
impl FromJson for boolean {
  function from_json_value(v: JsonValue): Result[boolean, string] {
    match (v) {
      JBool(b) => {
        return Ok(b);
      },
      _ => {
        return Err("expected a boolean, got " + json_kind(v));
      }
    }
    return Err("");
  }
}

impl FromJson for string {
  function from_json_value(v: JsonValue): Result[string, string] {
    match (v) {
      JString(s) => {
        return Ok(s);
      },
      _ => {
        return Err("expected a string, got " + json_kind(v));
      }
    }
    return Err("");
  }
}
```

### `52 pub function decode[T: FromJson](text: string): Result[T, string]`

```fern
<<52 pub function decode[T: FromJson](text: string): Result[T, string]>>=
pub function decode[T: FromJson](text: string): Result[T, string] {
  match (json_parse_result(text)) {
    Ok(v) => {
      return T.from_json_value(v);
    },
    Err(e) => {
      return Err("invalid JSON: " + e.to_string());
    }
  }
  return Err("");
}

pub function from_json_elem[T: FromJson](v: JsonValue): Result[T, string] {
  return T.from_json_value(v);
}

pub function from_json_array[T: FromJson](v: JsonValue): Result[T[], string] {
  match (v) {
    JArray(xs) => {
      let out: T[] = [];
      let i: i32 = 0;
      while (i < xs.len()) {
        match (T.from_json_value(xs[i])) {
          Ok(x) => {
            out = out.append(x);
          },
          Err(why) => {
            return Err(from_json_index(i, why));
          }
        }
        i = i + 1;
      }
      return Ok(out);
    },
    _ => {
      return Err("expected an array, got " + json_kind(v));
    }
  }
  return Err("");
}

pub function from_json_option[T: FromJson](v: Option[JsonValue]): Result[Option[T], string] {
  match (v) {
    Some(jv) => {
      match (T.from_json_value(jv)) {
        Ok(x) => {
          return Ok(Some(x));
        },
        Err(why) => {
          return Err(why);
        }
      }
    },
    None => {
      return Ok(None);
    }
  }
  return Err("");
}

pub function from_json_field(v: JsonValue, name: string): Result[JsonValue, string] {
  match (v) {
    JObject(m) => {
      match (m.get(name)) {
        Some(fv) => {
          return Ok(fv);
        },
        None => {
          return Err("missing field \"" + name + "\"");
        }
      }
    },
    _ => {
      return Err("expected an object, got " + json_kind(v));
    }
  }
  return Err("");
}

pub function from_json_optional_field(v: JsonValue, name: string): Result[Option[JsonValue], string] {
  match (v) {
    JObject(m) => {
      match (m.get(name)) {
        Some(fv) => {
          match (fv) {
            JNull => {
              return Ok(None);
            },
            _ => {
              return Ok(Some(fv));
            }
          }
        },
        None => {
          return Ok(None);
        }
      }
    },
    _ => {
      return Err("expected an object, got " + json_kind(v));
    }
  }
  return Err("");
}

pub function from_json_at(name: string, why: string): string {
  return "field \"" + name + "\": " + why;
}

pub function from_json_index(i: i32, why: string): string {
  return "[" + i.to_string() + "]: " + why;
}

pub function from_json_variant(v: JsonValue): Result[string, string] {
  match (v) {
    JString(s) => {
      return Ok(s);
    },
    JObject(m) => {
      if (m.len() != 1) {
        return Err("expected an object with one key, got " + m.len().to_string());
      }
      return Ok(m.names[0]);
    },
    _ => {
      return Err("expected a variant name or a one-key object, got " + json_kind(v));
    }
  }
  return Err("");
}
```

### `53 pub function from_json_payload(v: JsonValue): JsonValue`

```fern
<<53 pub function from_json_payload(v: JsonValue): JsonValue>>=
pub function from_json_payload(v: JsonValue): JsonValue {
  match (v) {
    JObject(m) => {
      if (m.len() == 1) {
        return m.items[0];
      }
    },
    _ => {}
  }
  return JNull;
}

pub function from_json_in(variant: string, why: string): string {
  return "variant \"" + variant + "\": " + why;
}

pub function from_json_unknown_variant(name: string): string {
  return "unknown variant \"" + name + "\"";
}

pub function from_json_item(p: JsonValue, i: i32, n: i32): Result[JsonValue, string] {
  if (n == 1) {
    return Ok(p);
  }
  match (p) {
    JArray(xs) => {
      if (xs.len() != n) {
        return Err("expected an array of " + n.to_string() + ", got " + xs.len().to_string());
      }
      return Ok(xs[i]);
    },
    _ => {
      return Err("expected an array, got " + json_kind(p));
    }
  }
  return Err("");
}

pub function from_json_optional_item(p: JsonValue, i: i32, n: i32): Result[Option[JsonValue], string] {
  match (from_json_item(p, i, n)) {
    Ok(x) => {
      match (x) {
        JNull => {
          return Ok(None);
        },
        _ => {
          return Ok(Some(x));
        }
      }
    },
    Err(why) => {
      return Err(why);
    }
  }
  return Err("");
}
```
