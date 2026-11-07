# Flattened device trees

This module reads a flattened device tree (FDT). Firmware, a boot loader or a VMM gives a kernel this blob to tell it what hardware the machine has.

## Model

`parse` reads the header and walks the structure block once. It gives a `Tree` with a flat list of nodes in the order of the blob, so the root is node 0. Each node has its name, the index of its parent and its properties. Each property gives the range of the blob that holds its value.

A node finds its devices by `compatible` with `find_compatible`, or by name with `find_name`. A name matches with or without its unit address, so `memory` matches `memory@40000000`. `property_string`, `property_strings` and `property_u32` read a value. `reg` reads one address and size pair of a `reg` property, with the `#address-cells` and `#size-cells` of the parent.

## Contracts

`parse` refuses a blob that does not start with the magic `0xd00dfeed`, a version below 16, and a structure that runs past the end of the blob or does not close each node. The error gives the byte offset of the problem. The functions that read a value give None for a property that is not there or that is too short.

## Executable example

```fern test name=std-fdt-example
import "std/binary";
import "std/fdt";

function header(structs: i32, strings: i32): u8[] {
  let b = binary.append_u32_be([], 0xd00dfeed as u32);
  b = binary.append_u32_be(b, (40 + structs + strings) as u32);
  b = binary.append_u32_be(b, 40 as u32);
  b = binary.append_u32_be(b, (40 + structs) as u32);
  b = binary.append_u32_be(b, 40 as u32);
  b = binary.append_u32_be(b, 17 as u32);
  b = binary.append_u32_be(b, 16 as u32);
  b = binary.append_u32_be(b, 0 as u32);
  b = binary.append_u32_be(b, strings as u32);
  return binary.append_u32_be(b, structs as u32);
}

function main(): i32 {
  // The root node, with no name and no properties.
  let s = binary.append_u32_be([], 1 as u32);
  s = binary.append_u32_be(s, 0 as u32);
  s = binary.append_u32_be(s, 2 as u32);
  s = binary.append_u32_be(s, 9 as u32);
  let blob = header(s.len(), 0);
  for x in s {
    blob = blob.append(x);
  }
  let Ok(tree) = fdt.parse(blob) else {
    return 1;
  };
  if (tree.nodes.len() != 1 || tree.nodes[0].parent != -1) {
    return 2;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<model>>

<<reading the blob>>

<<the walk>>

<<finding a node>>

<<reading a value>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads a flattened device tree: the description of the hardware
// that firmware or a VMM gives a kernel.
import "core/cmp";
import "core/error";
import "std/binary";
import "std/utf8";
```

### `model`

```fern
<<model>>=
pub struct FdtError { offset: i32, reason: string }

impl error.Error for FdtError {}

impl cmp.Display for FdtError {}

pub function (e: FdtError) message(): string {
  return "device tree: " + e.reason + " at byte " + e.offset.to_string();
}

pub function (e: FdtError) to_string(): string {
  return e.message();
}

// A property names the range of the blob that holds its value.
pub struct Property { name: string, start: i32, len: i32 }

// A node of the tree. `parent` is the index of the parent node, or -1 for the
// root. `address_cells` and `size_cells` are the node's own #address-cells and
// #size-cells, which give the shape of the `reg` of each child.
pub struct Node { name: string, parent: i32, address_cells: i32, size_cells: i32, properties: Property[] }

pub struct Tree { blob: u8[], nodes: Node[] }

// One address and size pair of a `reg` property.
pub struct Region { address: u64, size: u64 }
```

### `reading the blob`

Each field of the blob is a big-endian 32-bit word. A name ends with a NUL byte, and the next token starts at the next multiple of four.

```fern
<<reading the blob>>=
function word(b: [u8], at: i32): Result[i64, FdtError] {
  let Ok(v) = binary.read_u32_be(b, at) else {
    return Err(FdtError { offset: at, reason: "the blob ends inside a field" });
  };
  return Ok(v as i64);
}

// The index of the NUL byte that ends the string at `at`.
function nul(b: [u8], at: i32): Result[i32, FdtError] {
  for i in at..b.len() {
    if (b[i] == 0 as u8) {
      return Ok(i);
    }
  }
  return Err(FdtError { offset: at, reason: "a string has no end" });
}

function text(b: [u8], start: i32, end: i32): Result[string, FdtError] {
  let bytes: u8[] = [];
  for i in start..end {
    bytes = bytes.append(b[i]);
  }
  let Ok(s) = utf8.from_bytes(bytes) else {
    return Err(FdtError { offset: start, reason: "a name is not UTF-8" });
  };
  return Ok(s);
}

function align4(n: i32): i32 {
  return (n + 3) / 4 * 4;
}
```

### `the walk`

The structure block is a sequence of tokens: 1 opens a node, 2 closes it, 3 is a property, 4 is a no-op, and 9 ends the block. The walk keeps the index of the innermost open node, so the parent of each node is the node that was open when it started.

```fern
<<the walk>>=
const MAGIC: i64 = 3490578157;

struct Walk { at: i32, open: i32, nodes: Node[], done: boolean }

// Reads the blob that starts at byte 0 of `blob`.
pub function parse(blob: u8[]): Result[Tree, FdtError] {
  if (word(blob, 0)? != MAGIC) {
    return Err(FdtError { offset: 0, reason: "no device-tree magic" });
  }
  if (word(blob, 20)? < 16) {
    return Err(FdtError { offset: 20, reason: "the version is below 16" });
  }
  let strings = word(blob, 12)? as i32;
  let w = Walk { at: word(blob, 8)? as i32, open: -1, nodes: [], done: false };
  while (!w.done) {
    w = step(blob, strings, w)?;
  }
  if (w.open >= 0) {
    return Err(FdtError { offset: w.at, reason: "a node is not closed" });
  }
  return Ok(Tree { blob: blob, nodes: w.nodes });
}

function step(b: [u8], strings: i32, w: Walk): Result[Walk, FdtError] {
  let token = word(b, w.at)?;
  let at = w.at + 4;
  if (token == 1) {
    return begin_node(b, Walk { ...w, at: at });
  }
  if (token == 3) {
    return property(b, strings, Walk { ...w, at: at });
  }
  if (token == 2 && w.open >= 0) {
    return Ok(Walk { ...w, at: at, open: w.nodes[w.open].parent });
  }
  if (token == 4) {
    return Ok(Walk { ...w, at: at });
  }
  if (token == 9) {
    return Ok(Walk { ...w, at: at, done: true });
  }
  return Err(FdtError { offset: w.at, reason: "an unknown token" });
}

function begin_node(b: [u8], w: Walk): Result[Walk, FdtError] {
  let end = nul(b, w.at)?;
  let node = Node { name: text(b, w.at, end)?, parent: w.open, address_cells: 2, size_cells: 1, properties: [] };
  let nodes = w.nodes.append(node);
  return Ok(Walk { ...w, at: align4(end + 1), open: nodes.len() - 1, nodes: nodes });
}

function property(b: [u8], strings: i32, w: Walk): Result[Walk, FdtError] {
  if (w.open < 0) {
    return Err(FdtError { offset: w.at, reason: "a property is outside a node" });
  }
  let len = word(b, w.at)? as i32;
  let name_at = strings + word(b, w.at + 4)? as i32;
  let start = w.at + 8;
  if (len < 0 || start + len > b.len()) {
    return Err(FdtError { offset: w.at, reason: "a property runs past the end of the blob" });
  }
  let name = text(b, name_at, nul(b, name_at)?)?;
  let n = w.nodes[w.open];
  n = Node { ...n, properties: n.properties.append(Property { name: name, start: start, len: len }) };
  if (name == "#address-cells" && len == 4) {
    n = Node { ...n, address_cells: word(b, start)? as i32 };
  }
  if (name == "#size-cells" && len == 4) {
    n = Node { ...n, size_cells: word(b, start)? as i32 };
  }
  return Ok(Walk { ...w, at: align4(start + len), nodes: w.nodes.with(w.open, n) });
}
```

### `finding a node`

```fern
<<finding a node>>=
// The first node whose `compatible` list holds `c`.
pub function (t: Tree) find_compatible(c: string): Option[i32] {
  for i in 0..t.nodes.len() {
    for s in t.property_strings(i, "compatible") {
      if (s == c) {
        return Some(i);
      }
    }
  }
  return None;
}

// The first node named `name`, with or without a unit address.
pub function (t: Tree) find_name(name: string): Option[i32] {
  for i in 0..t.nodes.len() {
    let n = t.nodes[i].name;
    if (n == name || n.starts_with(name + "@")) {
      return Some(i);
    }
  }
  return None;
}

pub function (t: Tree) property(node: i32, name: string): Option[Property] {
  for p in t.nodes[node].properties {
    if (p.name == name) {
      return Some(p);
    }
  }
  return None;
}
```

### `reading a value`

A string list is a sequence of NUL-terminated strings. A `reg` property is a sequence of pairs. In each pair the address has `#address-cells` words and the size has `#size-cells` words, and the parent node gives both counts.

```fern
<<reading a value>>=
// Each string of a string list such as `compatible`.
pub function (t: Tree) property_strings(node: i32, name: string): string[] {
  let out: string[] = [];
  let Some(p) = t.property(node, name) else {
    return out;
  };
  let at = p.start;
  while (at < p.start + p.len) {
    let Ok(end) = nul(t.blob, at) else {
      return out;
    };
    let Ok(s) = text(t.blob, at, end) else {
      return out;
    };
    out = out.append(s);
    at = end + 1;
  }
  return out;
}

// The first string of a property such as the `method` of a PSCI node.
pub function (t: Tree) property_string(node: i32, name: string): Option[string] {
  let all = t.property_strings(node, name);
  if (all.len() == 0) {
    return None;
  }
  return Some(all[0]);
}

pub function (t: Tree) property_u32(node: i32, name: string): Option[u32] {
  let Some(p) = t.property(node, name) else {
    return None;
  };
  let Ok(v) = binary.read_u32_be(t.blob, p.start) else {
    return None;
  };
  if (p.len < 4) {
    return None;
  }
  return Some(v);
}

// The pair at `index` of the `reg` property of `node`.
pub function (t: Tree) reg(node: i32, index: i32): Option[Region] {
  let Some(p) = t.property(node, "reg") else {
    return None;
  };
  let parent = t.nodes[node].parent;
  let ac = 2;
  let sc = 1;
  if (parent >= 0) {
    ac = t.nodes[parent].address_cells;
    sc = t.nodes[parent].size_cells;
  }
  let at = p.start + index * (ac + sc) * 4;
  if (index < 0 || at + (ac + sc) * 4 > p.start + p.len) {
    return None;
  }
  let Some(address) = t.cells(at, ac) else {
    return None;
  };
  let Some(size) = t.cells(at + ac * 4, sc) else {
    return None;
  };
  return Some(Region { address: address, size: size });
}

// The value of `count` big-endian words at `at`, at most two of them.
function (t: Tree) cells(at: i32, count: i32): Option[u64] {
  if (count > 2) {
    return None;
  }
  let v: u64 = 0 as u64;
  for i in 0..count {
    let Ok(w) = binary.read_u32_be(t.blob, at + i * 4) else {
      return None;
    };
    v = v << 32 as u64 | w as u64;
  }
  return Some(v);
}
```
