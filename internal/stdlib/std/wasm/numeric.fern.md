# WebAssembly numeric opcodes

This module encodes WebAssembly numeric instructions.

## Model

Numeric helpers append comparison, arithmetic, bit, and floating-point instructions. Each named helper selects one WebAssembly opcode.

## Contracts

The caller supplies operands with the instruction types. Integer signedness and floating-point behavior follow the selected opcode.

## Executable example

```fern test name=std-wasm-numeric-example
import "std/wasm/numeric";

function main(): i32 {
  let bytes = numeric.inst_i32_add([]);
  if (bytes.len() != 1 || bytes[0] != 106 as u8) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function inst_i32_clz(buf: u8[]): u8[]>>

<<3 pub function inst_i64_ne(buf: u8[]): u8[]>>

<<4 pub function inst_f32_trunc(buf: u8[]): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes WebAssembly numeric instructions.
```

### `2 pub function inst_i32_clz(buf: u8[]): u8[]`

```fern
<<2 pub function inst_i32_clz(buf: u8[]): u8[]>>=
pub function inst_i32_clz(buf: u8[]): u8[] {
  return buf.append(103u8);
}

pub function inst_i32_ctz(buf: u8[]): u8[] {
  return buf.append(104u8);
}

pub function inst_i32_popcnt(buf: u8[]): u8[] {
  return buf.append(105u8);
}

pub function inst_i32_eqz(buf: u8[]): u8[] {
  return buf.append(69u8);
}

pub function inst_i32_eq(buf: u8[]): u8[] {
  return buf.append(70u8);
}

pub function inst_i32_ne(buf: u8[]): u8[] {
  return buf.append(71u8);
}

pub function inst_i32_lt_s(buf: u8[]): u8[] {
  return buf.append(72u8);
}

pub function inst_i32_lt_u(buf: u8[]): u8[] {
  return buf.append(73u8);
}

pub function inst_i32_gt_s(buf: u8[]): u8[] {
  return buf.append(74u8);
}

pub function inst_i32_gt_u(buf: u8[]): u8[] {
  return buf.append(75u8);
}

pub function inst_i32_le_s(buf: u8[]): u8[] {
  return buf.append(76u8);
}

pub function inst_i32_le_u(buf: u8[]): u8[] {
  return buf.append(77u8);
}

pub function inst_i32_ge_s(buf: u8[]): u8[] {
  return buf.append(78u8);
}

pub function inst_i32_ge_u(buf: u8[]): u8[] {
  return buf.append(79u8);
}

pub function inst_i32_add(buf: u8[]): u8[] {
  return buf.append(106u8);
}

pub function inst_i32_sub(buf: u8[]): u8[] {
  return buf.append(107u8);
}

pub function inst_i32_mul(buf: u8[]): u8[] {
  return buf.append(108u8);
}

pub function inst_i32_div_s(buf: u8[]): u8[] {
  return buf.append(109u8);
}

pub function inst_i32_div_u(buf: u8[]): u8[] {
  return buf.append(110u8);
}

pub function inst_i32_rem_s(buf: u8[]): u8[] {
  return buf.append(111u8);
}

pub function inst_i32_rem_u(buf: u8[]): u8[] {
  return buf.append(112u8);
}

pub function inst_i32_and(buf: u8[]): u8[] {
  return buf.append(113u8);
}

pub function inst_i32_or(buf: u8[]): u8[] {
  return buf.append(114u8);
}

pub function inst_i32_xor(buf: u8[]): u8[] {
  return buf.append(115u8);
}

pub function inst_i32_shl(buf: u8[]): u8[] {
  return buf.append(116u8);
}

pub function inst_i32_shr_s(buf: u8[]): u8[] {
  return buf.append(117u8);
}

pub function inst_i32_shr_u(buf: u8[]): u8[] {
  return buf.append(118u8);
}

pub function inst_i32_rotl(buf: u8[]): u8[] {
  return buf.append(119u8);
}

pub function inst_i32_rotr(buf: u8[]): u8[] {
  return buf.append(120u8);
}

pub function inst_i64_clz(buf: u8[]): u8[] {
  return buf.append(121u8);
}

pub function inst_i64_ctz(buf: u8[]): u8[] {
  return buf.append(122u8);
}

pub function inst_i64_popcnt(buf: u8[]): u8[] {
  return buf.append(123u8);
}

pub function inst_i64_eqz(buf: u8[]): u8[] {
  return buf.append(80u8);
}

pub function inst_i64_eq(buf: u8[]): u8[] {
  return buf.append(81u8);
}
```

### `3 pub function inst_i64_ne(buf: u8[]): u8[]`

```fern
<<3 pub function inst_i64_ne(buf: u8[]): u8[]>>=
pub function inst_i64_ne(buf: u8[]): u8[] {
  return buf.append(82u8);
}

pub function inst_i64_lt_s(buf: u8[]): u8[] {
  return buf.append(83u8);
}

pub function inst_i64_lt_u(buf: u8[]): u8[] {
  return buf.append(84u8);
}

pub function inst_i64_gt_s(buf: u8[]): u8[] {
  return buf.append(85u8);
}

pub function inst_i64_gt_u(buf: u8[]): u8[] {
  return buf.append(86u8);
}

pub function inst_i64_le_s(buf: u8[]): u8[] {
  return buf.append(87u8);
}

pub function inst_i64_le_u(buf: u8[]): u8[] {
  return buf.append(88u8);
}

pub function inst_i64_ge_s(buf: u8[]): u8[] {
  return buf.append(89u8);
}

pub function inst_i64_ge_u(buf: u8[]): u8[] {
  return buf.append(90u8);
}

pub function inst_i64_add(buf: u8[]): u8[] {
  return buf.append(124u8);
}

pub function inst_i64_sub(buf: u8[]): u8[] {
  return buf.append(125u8);
}

pub function inst_i64_mul(buf: u8[]): u8[] {
  return buf.append(126u8);
}

pub function inst_i64_div_s(buf: u8[]): u8[] {
  return buf.append(127u8);
}

pub function inst_i64_div_u(buf: u8[]): u8[] {
  return buf.append(128u8);
}

pub function inst_i64_rem_s(buf: u8[]): u8[] {
  return buf.append(129u8);
}

pub function inst_i64_rem_u(buf: u8[]): u8[] {
  return buf.append(130u8);
}

pub function inst_i64_and(buf: u8[]): u8[] {
  return buf.append(131u8);
}

pub function inst_i64_or(buf: u8[]): u8[] {
  return buf.append(132u8);
}

pub function inst_i64_xor(buf: u8[]): u8[] {
  return buf.append(133u8);
}

pub function inst_i64_shl(buf: u8[]): u8[] {
  return buf.append(134u8);
}

pub function inst_i64_shr_s(buf: u8[]): u8[] {
  return buf.append(135u8);
}

pub function inst_i64_shr_u(buf: u8[]): u8[] {
  return buf.append(136u8);
}

pub function inst_i64_rotl(buf: u8[]): u8[] {
  return buf.append(137u8);
}

pub function inst_i64_rotr(buf: u8[]): u8[] {
  return buf.append(138u8);
}

pub function inst_f32_eq(buf: u8[]): u8[] {
  return buf.append(91u8);
}

pub function inst_f32_ne(buf: u8[]): u8[] {
  return buf.append(92u8);
}

pub function inst_f32_lt(buf: u8[]): u8[] {
  return buf.append(93u8);
}

pub function inst_f32_gt(buf: u8[]): u8[] {
  return buf.append(94u8);
}

pub function inst_f32_le(buf: u8[]): u8[] {
  return buf.append(95u8);
}

pub function inst_f32_ge(buf: u8[]): u8[] {
  return buf.append(96u8);
}

pub function inst_f32_abs(buf: u8[]): u8[] {
  return buf.append(139u8);
}

pub function inst_f32_neg(buf: u8[]): u8[] {
  return buf.append(140u8);
}

pub function inst_f32_ceil(buf: u8[]): u8[] {
  return buf.append(141u8);
}

pub function inst_f32_floor(buf: u8[]): u8[] {
  return buf.append(142u8);
}
```

### `4 pub function inst_f32_trunc(buf: u8[]): u8[]`

```fern
<<4 pub function inst_f32_trunc(buf: u8[]): u8[]>>=
pub function inst_f32_trunc(buf: u8[]): u8[] {
  return buf.append(143u8);
}

pub function inst_f32_nearest(buf: u8[]): u8[] {
  return buf.append(144u8);
}

pub function inst_f32_sqrt(buf: u8[]): u8[] {
  return buf.append(145u8);
}

pub function inst_f32_add(buf: u8[]): u8[] {
  return buf.append(146u8);
}

pub function inst_f32_sub(buf: u8[]): u8[] {
  return buf.append(147u8);
}

pub function inst_f32_mul(buf: u8[]): u8[] {
  return buf.append(148u8);
}

pub function inst_f32_div(buf: u8[]): u8[] {
  return buf.append(149u8);
}

pub function inst_f32_min(buf: u8[]): u8[] {
  return buf.append(150u8);
}

pub function inst_f32_max(buf: u8[]): u8[] {
  return buf.append(151u8);
}

pub function inst_f32_copysign(buf: u8[]): u8[] {
  return buf.append(152u8);
}

pub function inst_f64_eq(buf: u8[]): u8[] {
  return buf.append(97u8);
}

pub function inst_f64_ne(buf: u8[]): u8[] {
  return buf.append(98u8);
}

pub function inst_f64_lt(buf: u8[]): u8[] {
  return buf.append(99u8);
}

pub function inst_f64_gt(buf: u8[]): u8[] {
  return buf.append(100u8);
}

pub function inst_f64_le(buf: u8[]): u8[] {
  return buf.append(101u8);
}

pub function inst_f64_ge(buf: u8[]): u8[] {
  return buf.append(102u8);
}

pub function inst_f64_abs(buf: u8[]): u8[] {
  return buf.append(153u8);
}

pub function inst_f64_neg(buf: u8[]): u8[] {
  return buf.append(154u8);
}

pub function inst_f64_ceil(buf: u8[]): u8[] {
  return buf.append(155u8);
}

pub function inst_f64_floor(buf: u8[]): u8[] {
  return buf.append(156u8);
}

pub function inst_f64_trunc(buf: u8[]): u8[] {
  return buf.append(157u8);
}

pub function inst_f64_nearest(buf: u8[]): u8[] {
  return buf.append(158u8);
}

pub function inst_f64_sqrt(buf: u8[]): u8[] {
  return buf.append(159u8);
}

pub function inst_f64_add(buf: u8[]): u8[] {
  return buf.append(160u8);
}

pub function inst_f64_sub(buf: u8[]): u8[] {
  return buf.append(161u8);
}

pub function inst_f64_mul(buf: u8[]): u8[] {
  return buf.append(162u8);
}

pub function inst_f64_div(buf: u8[]): u8[] {
  return buf.append(163u8);
}

pub function inst_f64_min(buf: u8[]): u8[] {
  return buf.append(164u8);
}

pub function inst_f64_max(buf: u8[]): u8[] {
  return buf.append(165u8);
}

pub function inst_f64_copysign(buf: u8[]): u8[] {
  return buf.append(166u8);
}
```
