# WebAssembly binary containers

This module encodes WebAssembly binary containers.

## Model

Container helpers supply value types, section identifiers, and binary wrappers. Length prefixes use the WebAssembly variable-length integer representation.

## Contracts

Payload sizes describe encoded bytes. The caller must preserve section order and valid index relationships when it assembles a module.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const VALTYPE_I32: u8>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes WebAssembly binary containers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/wasm/leb128";
```

### `3 pub const VALTYPE_I32: u8`

```fern
<<3 pub const VALTYPE_I32: u8>>=
pub const VALTYPE_I32: u8 = 127u8;

pub const VALTYPE_I64: u8 = 126u8;

pub const VALTYPE_F32: u8 = 125u8;

pub const VALTYPE_F64: u8 = 124u8;

pub const SECTION_CUSTOM: u8 = 0u8;

pub const SECTION_TYPE: u8 = 1u8;

pub const SECTION_IMPORT: u8 = 2u8;

pub const SECTION_FUNCTION: u8 = 3u8;

pub const SECTION_TABLE: u8 = 4u8;

pub const SECTION_MEMORY: u8 = 5u8;

pub const SECTION_GLOBAL: u8 = 6u8;

pub const SECTION_EXPORT: u8 = 7u8;

pub const SECTION_START: u8 = 8u8;

pub const SECTION_ELEMENT: u8 = 9u8;

pub const SECTION_CODE: u8 = 10u8;

pub const SECTION_DATA: u8 = 11u8;

pub function put_bytes(buf: u8[], src: u8[]): u8[] {
  let n: i32 = src.len();
  let out: u8[] = buf;
  let i: i32 = 0;
  while (i < n) {
    out = out.append(src[i]);
    i = i + 1;
  }
  return out;
}

pub function put_u32_le(buf: u8[], v: u32): u8[] {
  let out: u8[] = buf;
  out = out.append((v & 255u32) as u8);
  out = out.append((v >> 8 & 255u32) as u8);
  out = out.append((v >> 16 & 255u32) as u8);
  out = out.append((v >> 24 & 255u32) as u8);
  return out;
}

pub function put_module_header(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(0u8);
  out = out.append(97u8);
  out = out.append(115u8);
  out = out.append(109u8);
  out = put_u32_le(out, 1u32);
  return out;
}

pub function put_name(buf: u8[], s: string): u8[] {
  let n: i32 = s.len();
  let out: u8[] = leb128.uleb_u32(buf, n as u32);
  let i: i32 = 0;
  while (i < n) {
    out = out.append(s[i] as u8);
    i = i + 1;
  }
  return out;
}

pub function put_section(buf: u8[], id: u8, body: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(id);
  out = leb128.uleb_u32(out, body.len() as u32);
  out = put_bytes(out, body);
  return out;
}

pub function put_func_type(buf: u8[], params: u8[], results: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(96u8);
  out = leb128.uleb_u32(out, params.len() as u32);
  out = put_bytes(out, params);
  out = leb128.uleb_u32(out, results.len() as u32);
  out = put_bytes(out, results);
  return out;
}
```
