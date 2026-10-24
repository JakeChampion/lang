# WebAssembly imports and globals

This module constructs WebAssembly import and global sections.

## Model

Import descriptors distinguish functions, tables, memories, and globals. Global descriptors retain their value type and mutability.

## Contracts

Descriptor kind and payload must agree. Function descriptors refer to type indices, while global descriptors describe value type and mutability directly.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function import_func(): u8>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module constructs WebAssembly import and global sections.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/wasm/leb128";
import "std/wasm/encode";
```

### `3 pub function import_func(): u8`

```fern
<<3 pub function import_func(): u8>>=
pub function import_func(): u8 {
  return 0u8;
}

pub function import_table(): u8 {
  return 1u8;
}

pub function import_memory(): u8 {
  return 2u8;
}

pub function import_global(): u8 {
  return 3u8;
}

pub function mut_const(): u8 {
  return 0u8;
}

pub function mut_var(): u8 {
  return 1u8;
}

pub function import_desc_func(typeidx: u32): u8[] {
  return leb128.uleb_u32([], typeidx);
}

pub function import_desc_global(vt: u8, is_mut: u8): u8[] {
  let out: u8[] = [];
  out = out.append(vt);
  out = out.append(is_mut);
  return out;
}

pub function import_desc_memory(min_pages: u32, max_pages: i32): u8[] {
  let out: u8[] = [];
  if (max_pages < 0) {
    out = out.append(0u8);
    out = leb128.uleb_u32(out, min_pages);
  } else {
    out = out.append(1u8);
    out = leb128.uleb_u32(out, min_pages);
    out = leb128.uleb_u32(out, max_pages as u32);
  }
  return out;
}

pub function reftype_funcref(): u8 {
  return 112u8;
}

pub function reftype_externref(): u8 {
  return 111u8;
}

pub function import_desc_table(reftype: u8, min: u32, max: i32): u8[] {
  let out: u8[] = [];
  out = out.append(reftype);
  if (max < 0) {
    out = out.append(0u8);
    out = leb128.uleb_u32(out, min);
  } else {
    out = out.append(1u8);
    out = leb128.uleb_u32(out, min);
    out = leb128.uleb_u32(out, max as u32);
  }
  return out;
}

pub function encode_import_section(buf: u8[], modules: string[], names: string[], kinds: u8[], desc_bodies: u8[][]): u8[] {
  let n: i32 = modules.len();
  let body: u8[] = leb128.uleb_u32([], n as u32);
  let i: i32 = 0;
  while (i < n) {
    body = encode.put_name(body, modules[i]);
    body = encode.put_name(body, names[i]);
    body = body.append(kinds[i]);
    body = encode.put_bytes(body, desc_bodies[i]);
    i = i + 1;
  }
  return encode.put_section(buf, encode.section_import(), body);
}

pub function encode_global_section(buf: u8[], valtypes: u8[], muts: u8[], init_exprs: u8[][]): u8[] {
  let n: i32 = valtypes.len();
  let body: u8[] = leb128.uleb_u32([], n as u32);
  let i: i32 = 0;
  while (i < n) {
    body = body.append(valtypes[i]);
    body = body.append(muts[i]);
    body = encode.put_bytes(body, init_exprs[i]);
    i = i + 1;
  }
  return encode.put_section(buf, encode.section_global(), body);
}
```
