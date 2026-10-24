# WebAssembly memory instructions

This module encodes WebAssembly memory instructions.

## Model

Load and store helpers append an opcode, alignment exponent, and address offset. Width and signedness distinguish the narrow load forms.

## Contracts

Alignment is an exponent rather than a byte count. Memory indices and operand types must match the module and instruction context.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function put_memarg(buf: u8[], align: u32, offset: u32): u8[]>>

<<4 pub function inst_i32_load(buf: u8[], align: u32, offset: u32): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes WebAssembly memory instructions.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/wasm/leb128";
```

### `3 function put_memarg(buf: u8[], align: u32, offset: u32): u8[]`

```fern
<<3 function put_memarg(buf: u8[], align: u32, offset: u32): u8[]>>=
function put_memarg(buf: u8[], align: u32, offset: u32): u8[] {
  let out: u8[] = leb128.uleb_u32(buf, align);
  return leb128.uleb_u32(out, offset);
}
```

### `4 pub function inst_i32_load(buf: u8[], align: u32, offset: u32): u8[]`

```fern
<<4 pub function inst_i32_load(buf: u8[], align: u32, offset: u32): u8[]>>=
pub function inst_i32_load(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(40u8), align, offset);
}

pub function inst_i64_load(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(41u8), align, offset);
}

pub function inst_f32_load(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(42u8), align, offset);
}

pub function inst_f64_load(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(43u8), align, offset);
}

pub function inst_i32_load8_s(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(44u8), align, offset);
}

pub function inst_i32_load8_u(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(45u8), align, offset);
}

pub function inst_i32_load16_s(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(46u8), align, offset);
}

pub function inst_i32_load16_u(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(47u8), align, offset);
}

pub function inst_i64_load8_s(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(48u8), align, offset);
}

pub function inst_i64_load8_u(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(49u8), align, offset);
}

pub function inst_i64_load16_s(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(50u8), align, offset);
}

pub function inst_i64_load16_u(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(51u8), align, offset);
}

pub function inst_i64_load32_s(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(52u8), align, offset);
}

pub function inst_i64_load32_u(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(53u8), align, offset);
}

pub function inst_i32_store(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(54u8), align, offset);
}

pub function inst_i64_store(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(55u8), align, offset);
}

pub function inst_f32_store(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(56u8), align, offset);
}

pub function inst_f64_store(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(57u8), align, offset);
}

pub function inst_i32_store8(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(58u8), align, offset);
}

pub function inst_i32_store16(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(59u8), align, offset);
}

pub function inst_i64_store8(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(60u8), align, offset);
}

pub function inst_i64_store16(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(61u8), align, offset);
}

pub function inst_i64_store32(buf: u8[], align: u32, offset: u32): u8[] {
  return put_memarg(buf.append(62u8), align, offset);
}

pub function inst_memory_size(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(63u8);
  out = out.append(0u8);
  return out;
}

pub function inst_memory_grow(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(64u8);
  out = out.append(0u8);
  return out;
}

pub function inst_memory_copy(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(252u8);
  out = out.append(10u8);
  out = out.append(0u8);
  out = out.append(0u8);
  return out;
}

pub function inst_memory_fill(buf: u8[]): u8[] {
  let out: u8[] = buf;
  out = out.append(252u8);
  out = out.append(11u8);
  out = out.append(0u8);
  return out;
}
```
