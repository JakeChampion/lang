# WebAssembly conversion opcodes

This module encodes WebAssembly conversion instructions.

## Model

Each helper appends the binary opcode for a numeric conversion. Signed, unsigned, trap, saturation, and reinterpret operations have distinct names.

## Contracts

The caller supplies a valid instruction context and operand types. The returned buffer contains the appended instruction.

## Executable example

```fern test name=std-wasm-convert-example
import "std/wasm/convert";

function main(): i32 {
  let bytes = convert.inst_i32_wrap_i64([]);
  if (bytes.len() != 1 || bytes[0] != 167 as u8) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function inst_i32_wrap_i64(buf: u8[]): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes WebAssembly conversion instructions.
```

### `2 pub function inst_i32_wrap_i64(buf: u8[]): u8[]`

```fern
<<2 pub function inst_i32_wrap_i64(buf: u8[]): u8[]>>=
pub function inst_i32_wrap_i64(buf: u8[]): u8[] {
  return buf.append(167u8);
}

pub function inst_i64_extend_i32_s(buf: u8[]): u8[] {
  return buf.append(172u8);
}

pub function inst_i64_extend_i32_u(buf: u8[]): u8[] {
  return buf.append(173u8);
}

pub function inst_i32_trunc_f32_s(buf: u8[]): u8[] {
  return buf.append(168u8);
}

pub function inst_i32_trunc_f32_u(buf: u8[]): u8[] {
  return buf.append(169u8);
}

pub function inst_i32_trunc_f64_s(buf: u8[]): u8[] {
  return buf.append(170u8);
}

pub function inst_i32_trunc_f64_u(buf: u8[]): u8[] {
  return buf.append(171u8);
}

pub function inst_i64_trunc_f32_s(buf: u8[]): u8[] {
  return buf.append(174u8);
}

pub function inst_i64_trunc_f32_u(buf: u8[]): u8[] {
  return buf.append(175u8);
}

pub function inst_i64_trunc_f64_s(buf: u8[]): u8[] {
  return buf.append(176u8);
}

pub function inst_i64_trunc_f64_u(buf: u8[]): u8[] {
  return buf.append(177u8);
}

pub function inst_f32_convert_i32_s(buf: u8[]): u8[] {
  return buf.append(178u8);
}

pub function inst_f32_convert_i32_u(buf: u8[]): u8[] {
  return buf.append(179u8);
}

pub function inst_f32_convert_i64_s(buf: u8[]): u8[] {
  return buf.append(180u8);
}

pub function inst_f32_convert_i64_u(buf: u8[]): u8[] {
  return buf.append(181u8);
}

pub function inst_f64_convert_i32_s(buf: u8[]): u8[] {
  return buf.append(183u8);
}

pub function inst_f64_convert_i32_u(buf: u8[]): u8[] {
  return buf.append(184u8);
}

pub function inst_f64_convert_i64_s(buf: u8[]): u8[] {
  return buf.append(185u8);
}

pub function inst_f64_convert_i64_u(buf: u8[]): u8[] {
  return buf.append(186u8);
}

pub function inst_f32_demote_f64(buf: u8[]): u8[] {
  return buf.append(182u8);
}

pub function inst_f64_promote_f32(buf: u8[]): u8[] {
  return buf.append(187u8);
}

pub function inst_i32_reinterpret_f32(buf: u8[]): u8[] {
  return buf.append(188u8);
}

pub function inst_i64_reinterpret_f64(buf: u8[]): u8[] {
  return buf.append(189u8);
}

pub function inst_f32_reinterpret_i32(buf: u8[]): u8[] {
  return buf.append(190u8);
}

pub function inst_f64_reinterpret_i64(buf: u8[]): u8[] {
  return buf.append(191u8);
}

pub function inst_i32_extend8_s(buf: u8[]): u8[] {
  return buf.append(192u8);
}

pub function inst_i32_extend16_s(buf: u8[]): u8[] {
  return buf.append(193u8);
}

pub function inst_i64_extend8_s(buf: u8[]): u8[] {
  return buf.append(194u8);
}

pub function inst_i64_extend16_s(buf: u8[]): u8[] {
  return buf.append(195u8);
}

pub function inst_i64_extend32_s(buf: u8[]): u8[] {
  return buf.append(196u8);
}
```
