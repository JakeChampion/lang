# WebAssembly control and value instructions

This module encodes WebAssembly control, constant, and variable instructions.

## Model

Instruction helpers append control, constant, and variable operations to a byte buffer. Constant immediates use the wire representation for their value type.

## Contracts

Block types and variable indices require a valid function. These helpers construct bytes without a stack-type validation pass.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function blocktype_empty(): u8>>

<<4 pub function inst_br_if(buf: u8[], labelidx: u32): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes WebAssembly control, constant, and variable instructions.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/wasm/leb128";
import "std/wasm/encode";
```

### `3 pub function blocktype_empty(): u8`

```fern
<<3 pub function blocktype_empty(): u8>>=
pub function blocktype_empty(): u8 {
  return 64u8;
}

pub function put_blocktype_typeidx(buf: u8[], idx: i32): u8[] {
  return leb128.sleb_i32(buf, idx);
}

pub function inst_i32_const(buf: u8[], v: i32): u8[] {
  let out: u8[] = buf;
  out = out.append(65u8);
  return leb128.sleb_i32(out, v);
}

pub function inst_i64_const(buf: u8[], v: i64): u8[] {
  let out: u8[] = buf;
  out = out.append(66u8);
  return leb128.sleb_i64(out, v);
}

pub function inst_f32_const(buf: u8[], bits: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(67u8);
  return encode.put_u32_le(out, bits);
}

pub function inst_f64_const(buf: u8[], bits: u64): u8[] {
  let out: u8[] = buf;
  out = out.append(68u8);
  out = out.append((bits & 255u64) as u8);
  out = out.append((bits >> 8 & 255u64) as u8);
  out = out.append((bits >> 16 & 255u64) as u8);
  out = out.append((bits >> 24 & 255u64) as u8);
  out = out.append((bits >> 32 & 255u64) as u8);
  out = out.append((bits >> 40 & 255u64) as u8);
  out = out.append((bits >> 48 & 255u64) as u8);
  out = out.append((bits >> 56 & 255u64) as u8);
  return out;
}

pub function inst_drop(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(26u8);
  return out;
}

pub function inst_select(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(27u8);
  return out;
}

pub function inst_local_get(buf: u8[], idx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(32u8);
  return leb128.uleb_u32(out, idx);
}

pub function inst_local_set(buf: u8[], idx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(33u8);
  return leb128.uleb_u32(out, idx);
}

pub function inst_local_tee(buf: u8[], idx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(34u8);
  return leb128.uleb_u32(out, idx);
}

pub function inst_global_get(buf: u8[], idx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(35u8);
  return leb128.uleb_u32(out, idx);
}

pub function inst_global_set(buf: u8[], idx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(36u8);
  return leb128.uleb_u32(out, idx);
}

pub function inst_unreachable(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(0u8);
  return out;
}

pub function inst_nop(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(1u8);
  return out;
}

pub function inst_block_start(buf: u8[], blocktype: u8): u8[] {
  let out: u8[] = buf;
  out = out.append(2u8);
  out = out.append(blocktype);
  return out;
}

pub function inst_loop_start(buf: u8[], blocktype: u8): u8[] {
  let out: u8[] = buf;
  out = out.append(3u8);
  out = out.append(blocktype);
  return out;
}

pub function inst_if_start(buf: u8[], blocktype: u8): u8[] {
  let out: u8[] = buf;
  out = out.append(4u8);
  out = out.append(blocktype);
  return out;
}

pub function inst_else(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(5u8);
  return out;
}

pub function inst_end(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(11u8);
  return out;
}

pub function inst_br(buf: u8[], labelidx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(12u8);
  return leb128.uleb_u32(out, labelidx);
}
```

### `4 pub function inst_br_if(buf: u8[], labelidx: u32): u8[]`

```fern
<<4 pub function inst_br_if(buf: u8[], labelidx: u32): u8[]>>=
pub function inst_br_if(buf: u8[], labelidx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(13u8);
  return leb128.uleb_u32(out, labelidx);
}

pub function inst_return(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(15u8);
  return out;
}

pub function inst_call(buf: u8[], funcidx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(16u8);
  return leb128.uleb_u32(out, funcidx);
}

pub function inst_call_indirect(buf: u8[], typeidx: u32, tableidx: u32): u8[] {
  let out: u8[] = buf;
  out = out.append(17u8);
  out = leb128.uleb_u32(out, typeidx);
  return leb128.uleb_u32(out, tableidx);
}

pub function put_locals_empty(buf: u8[]): u8[] {
  return leb128.uleb_u32(buf, 0u32);
}

pub function put_locals_one_group(buf: u8[], count: u32, vt: u8): u8[] {
  let out: u8[] = leb128.uleb_u32(buf, 1u32);
  out = leb128.uleb_u32(out, count);
  out = out.append(vt);
  return out;
}

pub function put_function_body(buf: u8[], locals_bytes: u8[], body_bytes: u8[]): u8[] {
  let inner: u8[] = [];
  inner = encode.put_bytes(inner, locals_bytes);
  inner = encode.put_bytes(inner, body_bytes);
  inner = inner.append(11u8);
  let out: u8[] = leb128.uleb_u32(buf, inner.len() as u32);
  return encode.put_bytes(out, inner);
}
```
