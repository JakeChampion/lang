# Measured workloads: JSON and compression

The JSON lab and the compression bench compare three engines on the same bytes.
Native Fern runs on the server. Fern compiled to WebAssembly and the browser's own implementation run in the visitor's browser.

A comparison is fair only when each engine reads identical input. A deterministic generator therefore produces each corpus from a seed and a size.
The server and the browser kernel compile this same module, so both produce the same bytes. A checksum confirms it.

This document is the source program. The server imports it natively, and the build compiles it to the `measure.wasm` kernel.

```fern
<<*>>=
<<Imports>>
<<Deterministic corpora>>
<<JSON rounds>>
<<Codec rounds>>
<<Command entry>>
```

## Imports

```fern
<<Imports>>=
import "std/json";
import "std/array";
import "std/deflate";
import "std/brotli";
import "std/zstd";
```

## Deterministic corpora

A xorshift step supplies every random choice. The JSON corpus is an array of social-media records with nested objects, strings, numbers, booleans, and nulls.
The text corpus is an access log, the usual input for a log compressor. Decimal values come from integer parts, so no float formatting differs between targets.

```fern
<<Deterministic corpora>>=
const WORDS: string[] = ["fern", "frond", "spore", "moss", "lichen", "cedar", "river", "stone", "light", "shade", "root", "leaf", "bark", "seed", "rain", "field"];

fip function rand(state: u32): u32 {
  let x: u32 = state ^ state << 13;
  x = x ^ x >> 17;
  return x ^ x << 5;
}

fip function start(seed: i32): u32 {
  return seed as u32 * 2654435761 | 1;
}

function word(state: u32): string {
  return WORDS[(state % (WORDS.len() as u32)) as i32];
}

function decimal(state: u32, whole: u32): string {
  let fraction: string = (state % 1000).to_string();
  while (fraction.len() < 3) {
    fraction = "0" + fraction;
  }
  return ((state >> 10) % whole).to_string() + "." + fraction;
}

@export("fern:showcase/measure", "json-document")
pub function json_document(seed: i32, size: i32): string {
  let s: u32 = start(seed);
  let parts: string[] = ["["];
  let bytes = 1;
  let id = 0;
  while (bytes < size) {
    let text: string[] = [];
    for i in 0..8 {
      s = rand(s);
      text = text.append(word(s));
    }
    s = rand(s);
    let followers: u32 = s % 100000;
    s = rand(s);
    let geo: string = "null";
    if (s % 3 == 0) {
      s = rand(s);
      geo = "{\"lat\":" + decimal(s, 90) + ",\"lon\":" + decimal(rand(s), 180) + "}";
    }
    s = rand(s);
    let record: string = "{\"id\":" + (100000 + id).to_string() + ",\"user\":{\"name\":\"" + word(s) + "_" + (s % 997).to_string()
      + "\",\"followers\":" + followers.to_string() + ",\"verified\":" + (s % 5 == 0).to_string() + "},\"text\":\"" + text.join(" ")
      + "\",\"tags\":[\"" + word(rand(s)) + "\",\"" + word(rand(rand(s))) + "\"],\"score\":" + decimal(rand(s), 100) + ",\"geo\":" + geo + "}";
    if (id > 0) {
      record = "," + record;
    }
    parts = parts.append(record);
    bytes = bytes + record.len();
    id = id + 1;
  }
  return parts.append("]").join("");
}

@export("fern:showcase/measure", "access-log")
pub function access_log(seed: i32, size: i32): u8[] {
  let s: u32 = start(seed);
  let parts: string[] = [];
  let bytes = 0;
  let methods: string[] = ["GET", "GET", "GET", "POST", "PUT", "DELETE"];
  let statuses: string[] = ["200", "200", "200", "201", "304", "404", "500"];
  while (bytes < size) {
    s = rand(s);
    let seconds: u32 = s % 86400;
    s = rand(s);
    let line: string = "2026-10-25T" + two(seconds / 3600) + ":" + two(seconds / 60 % 60) + ":" + two(seconds % 60) + "." + (s % 1000).to_string()
      + "Z " + methods[(s % 6) as i32] + " /api/" + word(rand(s)) + "/" + (s % 5000).to_string() + " " + statuses[(rand(rand(s)) % 7) as i32]
      + " " + (s % 20000).to_string() + "b " + decimal(rand(s), 250) + "ms ip=10." + (s % 256).to_string() + "." + (s >> 8 & 255).to_string()
      + " ua=\"fern-client/1." + (s % 9).to_string() + "\"\n";
    parts = parts.append(line);
    bytes = bytes + line.len();
  }
  return parts.join("").bytes();
}

function two(value: u32): string {
  if (value < 10) {
    return "0" + value.to_string();
  }
  return value.to_string();
}

@export("fern:showcase/measure", "checksum")
pub function checksum(data: u8[]): i32 {
  let hash: u32 = 2166136261;
  for b in data {
    hash = (hash ^ b as u32) * 16777619;
  }
  return hash as i32;
}
```

## JSON rounds

Each export parses the same text `rounds` times and counts the values of the last result.
The tree parser builds `JsonValue` objects. The tape parser records each value as two 64-bit words, with no object per value.
A count of −1 reports a parse error. Every engine must report the same count for the same text.

```fern
<<JSON rounds>>=
function tree_count(value: json.JsonValue): i32 {
  match (value) {
    JArray(items) => {
      let total = 1;
      for item in items {
        total = total + tree_count(item);
      }
      return total;
    },
    JObject(object) => {
      let total = 1;
      for item in object.items {
        total = total + tree_count(item);
      }
      return total;
    },
    _ => {
      return 1;
    }
  }
}

function tape_count(doc: json.Document, node: i32): i32 {
  let total = 1;
  if (doc.is_array(node) || doc.is_object(node)) {
    let child = doc.first(node);
    while (child >= 0) {
      if (doc.is_object(node)) {
        total = total + tape_count(doc, doc.value(child));
      } else {
        total = total + tape_count(doc, child);
      }
      child = doc.next(node, child);
    }
  }
  return total;
}

@export("fern:showcase/measure", "parse-tree")
pub function parse_tree(text: string, rounds: i32): i32 {
  let count = -1;
  for round in 0..rounds {
    match (json.parse(text)) {
      Ok(value) => {
        if (round == rounds - 1) {
          count = tree_count(value);
        }
      },
      Err(error) => {
        return -1;
      }
    }
  }
  return count;
}

@export("fern:showcase/measure", "parse-tape")
pub function parse_tape(text: string, rounds: i32): i32 {
  let count = -1;
  for round in 0..rounds {
    match (json.parse_document(text)) {
      Ok(doc) => {
        if (round == rounds - 1) {
          count = tape_count(doc, doc.root());
        }
      },
      Err(error) => {
        return -1;
      }
    }
  }
  return count;
}
```

## Codec rounds

Codec 0 is gzip, 1 is Brotli, and 2 is zstd. These are the standard library's own encoders and decoders, with no level setting.
A decode returns the checksum of its output, so the caller can confirm a lossless round trip. It returns 0 for a decode error.

```fern
<<Codec rounds>>=
@export("fern:showcase/measure", "compress")
pub function compress(codec: i32, data: u8[], rounds: i32): u8[] {
  let packed: u8[] = [];
  for round in 0..rounds {
    if (codec == 0) {
      packed = deflate.gzip(data);
    } else if (codec == 1) {
      packed = brotli.compress(data);
    } else {
      packed = zstd.compress(data);
    }
  }
  return packed;
}

@export("fern:showcase/measure", "decompress")
pub function decompress(codec: i32, packed: u8[], size: i32, rounds: i32): i32 {
  let sum = 0;
  for round in 0..rounds {
    if (codec == 0) {
      let Ok(plain) = deflate.gunzip(packed, size) else {
        return 0;
      };
      sum = checksum(plain);
    } else if (codec == 1) {
      let Ok(plain) = brotli.decompress(packed, size) else {
        return 0;
      };
      sum = checksum(plain);
    } else {
      let Ok(plain) = zstd.decompress(packed, size) else {
        return 0;
      };
      sum = checksum(plain);
    }
  }
  return sum;
}
```

## Command entry

The browser instantiates the core module and calls its exports. It never calls `_start`.

```fern
<<Command entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

The generators are deterministic, each parser counts the same values, and each codec round-trips.

```fern test name=measure-example
<<Imports>>
<<Deterministic corpora>>
<<JSON rounds>>
<<Codec rounds>>
function main(): i32 {
  let text = json_document(7, 4096);
  if (text != json_document(7, 4096) || text.len() < 4096) { return 1; }
  let values = parse_tree(text, 1);
  if (values < 100 || values != parse_tape(text, 2)) { return 2; }
  let log = access_log(3, 8192);
  for codec in 0..3 {
    if (decompress(codec, compress(codec, log, 1), log.len(), 1) != checksum(log)) { return 3 + codec; }
  }
  return 0;
}
```
