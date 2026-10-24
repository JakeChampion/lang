# The map runtime

This module implements generic maps.

## Model

This module implements the storage operations behind generic `Map` values. Table headers record the key and value representations.

## Contracts

Updates use copy-on-write when storage has more than one owner. Lookup helpers distinguish an absent key from a stored zero value.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function __map_pow2_ceil(n: i32): i32>>

<<3 function __map_hdr(buf: usize): i32>>

<<4 pub function __map_hash(k: i32, keyKind: i32): i32>>

<<5 function __map_own_copied_cols(buf: usize): void>>

<<6 pub function __map_clone(m: usize): usize>>

<<7 function __map_hash_scalar(k: usize): i32>>

<<8 pub function __map_lookup(m: usize, k: usize): i32>>

<<9 function __map_val_kind(buf: usize): i32>>

<<10 pub function __map_get_impl(m: usize, k: usize): Option[usize]>>

<<11 pub function __map_cell_f64_column(m: usize, kvOffset: i32): f64[]>>

<<12 pub function __map_drop_values(m: usize): usize>>

<<13 function __map_free_table(m: usize): usize>>

<<14 pub function __map_key_of(m: usize, k: usize): usize>>

<<15 function __map_lookup_keyed(m: usize, k: usize, hash_fn: (usize)>>

<<16 pub function __map_has_keyed_impl(m: usize, k: usize, hash_fn: (usize)>>

<<17 function __map_grow_keyed(m: usize, hash_fn: (usize)>>

<<18 pub function __map_set_keyed_impl(m: usize, k: usize, v: usize, hash_fn: (usize)>>

<<19 pub function __map_delete_keyed_impl(m: usize, k: usize, hash_fn: (usize)>>

<<20 Map entry snapshots>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements generic maps.

import "core/iter";
```

### `2 pub function __map_pow2_ceil(n: i32): i32`

```fern
<<2 pub function __map_pow2_ceil(n: i32): i32>>=
pub function __map_pow2_ceil(n: i32): i32 {
  if (n <= 4) {
    return 4;
  }
  let p: i32 = 4;
  while (p < n) {
    if (p >= 1073741824) {
      return p;
    }
    p = p << 1;
  }
  return p;
}

pub function __map_hdr_bytes(keyKind: i32): i32 {
  return 24;
}
```

### `3 function __map_hdr(buf: usize): i32`

```fern
<<3 function __map_hdr(buf: usize): i32>>=
function __map_hdr(buf: usize): i32 {
  return __map_hdr_bytes(__load_i32(buf + 8));
}

function __map_buf_bytes(cap: i32, hdr: i32): i32 {
  return hdr + cap * (4 + 2 * __ptr_width()) + cap + 8;
}

function __map_ctrl_base(buf: usize, cap: i32): usize {
  return buf + __map_hdr(buf) + cap * (4 + 2 * __ptr_width());
}

function __map_h2(h: i32): i32 {
  return (h as u32 >> 25 as u32) as i32;
}

function __map_swar_lo(): u64 {
  return 72340172838076673 as u64;
}

function __map_swar_hi(): u64 {
  return 9259542123273814144 as u64;
}

function __map_swar_all(): u64 {
  return 18446744073709551615 as u64;
}

function __map_ctrl_set(ctrlBase: usize, cap: i32, i: i32, v: i32): void {
  let p: i32 = i;
  while (p < cap + 8) {
    let wordOff: i32 = p & 0 - 4;
    let shift: i32 = (p & 3) * 8;
    let word: i32 = __load_i32(ctrlBase + wordOff);
    let keep: i32 = 255 << shift ^ 0 - 1;
    __store_i32(ctrlBase + wordOff, word & keep | v << shift);
    p = p + cap;
  }
}

function __map_seed(buf: usize): i32 {
  return __load_i32(buf + 16);
}

function __map_wang_mix(k: i32): i32 {
  let h: i32 = k ^ 61;
  h = h ^ (h as u32 >> 16 as u32) as i32;
  h = h + (h << 3);
  h = h ^ (h as u32 >> 4 as u32) as i32;
  h = h * 668265261;
  h = h ^ (h as u32 >> 15 as u32) as i32;
  return h;
}

function __map_fnv_str_seeded(s: string, seed: i32): i32 {
  let h: u32 = 2166136261 as u32 ^ seed as u32;
  let prime: u32 = 16777619 as u32;
  let sLen: i32 = s.len();
  let i: i32 = 0;
  while (i + 4 <= sLen) {
    let w: u32 = s[i] as u32
      | s[i + 1] as u32 << 8 as u32
      | s[i + 2] as u32 << 16 as u32
      | s[i + 3] as u32 << 24 as u32;
    h = (h ^ w) * prime;
    i = i + 4;
  }
  while (i < sLen) {
    h = (h ^ s[i] as u32) * prime;
    i = i + 1;
  }
  h = h ^ h >> 16 as u32;
  h = h * (2246822507 as u32);
  h = h ^ h >> 13 as u32;
  h = h * (3266489909 as u32);
  h = h ^ h >> 16 as u32;
  return h as i32;
}
```

### `4 pub function __map_hash(k: i32, keyKind: i32): i32`

```fern
<<4 pub function __map_hash(k: i32, keyKind: i32): i32>>=
pub function __map_hash(k: i32, keyKind: i32): i32 {
  return __map_wang_mix(k);
}

pub function map_new_impl(cap_hint: i32, keyTag: i32, valKind: i32): usize {
  let keyKind: i32 = keyTag & 255;
  let cap: i32 = __map_pow2_ceil(cap_hint);
  let hdr: i32 = __map_hdr_bytes(keyKind);
  let buf: usize = __alloc(__map_buf_bytes(cap, hdr));
  __store_i32(buf, cap);
  __store_i32(buf + 4, 0);
  __store_i32(buf + 8, keyKind);
  __store_i32(buf + 12, valKind);
  let seed: i32 = 0;
  if (keyKind == 1) {
    seed = __map_hash_seed();
  }
  __store_i32(buf + 16, seed);
  __store_i32(buf + 20, keyTag >> 8);
  __memset(buf + hdr, 255, cap * 4);
  __memset(__map_ctrl_base(buf, cap), 128, cap + 8);
  let base: usize = __alloc(16);
  __store_i32(base, 1);
  let m: usize = base + 8;
  __store_ptr(m, buf);
  return m;
}

pub function __map_cow_inplace(m: usize): usize {
  let rc: i32 = __load_i32(m - 8);
  if (rc <= 1) {
    return m;
  }
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let bufBytes: i32 = __map_buf_bytes(cap, __map_hdr(buf));
  let newBuf: usize = __alloc(bufBytes);
  __memcpy(newBuf, buf, bufBytes);
  __map_own_copied_cols(newBuf);
  let base: usize = __alloc(16);
  __store_i32(base, 1);
  let nm: usize = base + 8;
  __store_ptr(nm, newBuf);
  return nm;
}
```

### `5 function __map_own_copied_cols(buf: usize): void`

```fern
<<5 function __map_own_copied_cols(buf: usize): void>>=
function __map_own_copied_cols(buf: usize): void {
  let ownKeys: boolean = __load_i32(buf + 8) == 1;
  let retainKeys: boolean = __load_i32(buf + 8) == 4;
  let keyCellBytes: i32 = 0;
  if (__load_i32(buf + 8) == 2) {
    keyCellBytes = __map_key_cell_bytes(buf);
  }
  let valKind: i32 = __map_val_kind(buf);
  let ownVals: boolean = valKind == 5;
  let retainVals: boolean = valKind >= 2 && !ownVals;
  let cellBytes: i32 = __map_val_cell_bytes(buf);
  if (!ownKeys && !retainKeys && !ownVals && !retainVals && cellBytes == 0 && keyCellBytes == 0) {
    return;
  }
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let i: i32 = 0;
  while (i < len_) {
    let slot: usize = entriesBase + i * entryStride;
    if (ownKeys) {
      __store_ptr(slot, __map_own_str_slot(__load_ptr(slot)));
    }
    if (retainKeys) {
      __fern_rc_inc(__load_ptr(slot));
    }
    if (ownVals) {
      __store_ptr(slot + ptrW, __map_own_str_slot(__load_ptr(slot + ptrW)));
    }
    if (retainVals) {
      __map_retain_val(buf, __load_ptr(slot + ptrW));
    }
    if (cellBytes != 0) {
      let fresh: usize = __alloc(cellBytes);
      __memcpy(fresh, __load_ptr(slot + ptrW), cellBytes);
      __store_ptr(slot + ptrW, fresh);
    }
    if (keyCellBytes != 0) {
      let freshKey: usize = __alloc(keyCellBytes);
      __memcpy(freshKey, __load_ptr(slot), keyCellBytes);
      __store_ptr(slot, freshKey);
    }
    i = i + 1;
  }
}

function __map_own_str_slot(slot: usize): usize {
  let boxed: usize = (slot as string) as usize;
  if (boxed == slot) {
    return __fern_rc_inc(slot);
  }
  if (__load_i32(boxed + (2 * __ptr_width() - 4)) < 0) {
    return boxed;
  }
  __fern_rc_inc(__load_ptr(boxed));
  return boxed;
}
```

### `6 pub function __map_clone(m: usize): usize`

```fern
<<6 pub function __map_clone(m: usize): usize>>=
pub function __map_clone(m: usize): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let bufBytes: i32 = __map_buf_bytes(cap, __map_hdr(buf));
  let newBuf: usize = __alloc(bufBytes);
  __memcpy(newBuf, buf, bufBytes);
  __map_own_copied_cols(newBuf);
  let base: usize = __alloc(16);
  __store_i32(base, 1);
  let nm: usize = base + 8;
  __store_ptr(nm, newBuf);
  return nm;
}

pub function __map_len_impl(m: usize): i32 {
  let buf: usize = __load_ptr(m);
  return __load_i32(buf + 4);
}
```

### `7 function __map_hash_scalar(k: usize): i32`

```fern
<<7 function __map_hash_scalar(k: usize): i32>>=
function __map_hash_scalar(k: usize): i32 {
  return __map_wang_mix(k as i32);
}

function __map_eq_scalar(a: usize, b: usize): boolean {
  return a == b;
}

function __map_hash_wide(k: usize): i32 {
  let w: i64 = __load_i64(k);
  let lo: i32 = w as i32;
  let hi: i32 = (w >> 32 as i64) as i32;
  return __map_wang_mix(lo ^ hi);
}

function __map_eq_wide(a: usize, b: usize): boolean {
  return __load_i64(a) == __load_i64(b);
}

function __map_hash_str(k: usize): i32 {
  return __map_fnv_str_seeded(k as string, 0);
}

function __map_eq_str(a: usize, b: usize): boolean {
  return a as string == b as string;
}

function __map_bucket_hash(k: usize, hash_fn: (usize) => i32, seed: i32): i32 {
  if (seed != 0) {
    return __map_fnv_str_seeded(k as string, seed);
  }
  return hash_fn(k);
}
```

### `8 pub function __map_lookup(m: usize, k: usize): i32`

```fern
<<8 pub function __map_lookup(m: usize, k: usize): i32>>=
pub function __map_lookup(m: usize, k: usize): i32 {
  let keyKind: i32 = __load_i32(__load_ptr(m) + 8);
  if (keyKind == 0) {
    return __map_lookup_keyed(m, k, __map_hash_scalar, __map_eq_scalar);
  }
  if (keyKind == 2) {
    return __map_lookup_keyed(m, k, __map_hash_wide, __map_eq_wide);
  }
  return __map_lookup_keyed(m, k, __map_hash_str, __map_eq_str);
}

pub function __map_has_impl(m: usize, k: usize): boolean {
  return __map_lookup(m, k) != 0 - 1;
}
```

### `9 function __map_val_kind(buf: usize): i32`

```fern
<<9 function __map_val_kind(buf: usize): i32>>=
function __map_val_kind(buf: usize): i32 {
  return __load_i32(buf + 12) & 255;
}

function __map_val_stride(buf: usize): i32 {
  return __load_i32(buf + 12) >> 8;
}

function __map_val_cell_bytes(buf: usize): i32 {
  if (__map_val_kind(buf) != 0) {
    return 0;
  }
  return __load_i32(buf + 12) >> 8;
}

function __map_key_cell_bytes(buf: usize): i32 {
  return __load_i32(buf + 20);
}

function __map_free_key_cell(buf: usize, k: usize): void {
  let cellBytes: i32 = __map_key_cell_bytes(buf);
  if (cellBytes == 0) {
    return;
  }
  if (k == 0) {
    return;
  }
  if (__load_i32(buf + 8) == 1) {
    __fern_str_dec(k as string);
  }
  __free(k, cellBytes);
}

function __map_free_val_cell(buf: usize, v: usize): void {
  let cellBytes: i32 = __map_val_cell_bytes(buf);
  if (cellBytes == 0) {
    return;
  }
  if (v == 0) {
    return;
  }
  __free(v, cellBytes);
}

function __map_str_val_cell_bytes(buf: usize): i32 {
  if (__map_val_kind(buf) != 5) {
    return 0;
  }
  return __load_i32(buf + 12) >> 8;
}

function __map_dec_value(buf: usize, v: usize): void {
  let vk: i32 = __map_val_kind(buf);
  if (vk == 0) {
    __map_free_val_cell(buf, v);
    return;
  }
  if (vk == 5) {
    if (v == 0) {
      return;
    }
    __fern_str_dec(v as string);
    let cellBytes: i32 = __map_str_val_cell_bytes(buf);
    if (cellBytes != 0) {
      __free(v, cellBytes);
    }
    return;
  }
  if (vk != 2 && vk != 3) {
    return;
  }
  let stride: i32 = __map_val_stride(buf);
  if (vk == 3) {
    __fern_drop_arr_ptr(v, stride);
  } else {
    __fern_arr_dec(v, stride);
  }
}

function __map_retain_val(buf: usize, v: usize): usize {
  let vk: i32 = __map_val_kind(buf);
  if (vk >= 2 && vk != 5) {
    return __fern_rc_inc(v);
  }
  return v;
}

function __map_val_at(m: usize, idx: i32): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let ptrW: i32 = __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  return __load_ptr(entriesBase + idx * (2 * ptrW) + ptrW);
}
```

### `10 pub function __map_get_impl(m: usize, k: usize): Option[usize]`

```fern
<<10 pub function __map_get_impl(m: usize, k: usize): Option[usize]>>=
pub function __map_get_impl(m: usize, k: usize): Option[usize] {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(__map_retain_val(__load_ptr(m), __map_val_at(m, idx)));
}

pub function __map_get_or_impl(m: usize, k: usize, fallback: usize): usize {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return __map_retain_val(__load_ptr(m), fallback);
  }
  return __map_retain_val(__load_ptr(m), __map_val_at(m, idx));
}

pub function __map_lookup_val(m: usize, k: usize): usize {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return 0;
  }
  return __map_val_at(m, idx);
}

pub function __map_grow(m: usize): usize {
  let keyKind: i32 = __load_i32(__load_ptr(m) + 8);
  if (keyKind == 0) {
    return __map_grow_keyed(m, __map_hash_scalar);
  }
  if (keyKind == 2) {
    return __map_grow_keyed(m, __map_hash_wide);
  }
  return __map_grow_keyed(m, __map_hash_str);
}

pub function __map_set_impl(m: usize, k: usize, v: usize): usize {
  let keyKind: i32 = __load_i32(__load_ptr(m) + 8);
  if (keyKind == 0) {
    return __map_set_keyed_impl(m, k, v, __map_hash_scalar, __map_eq_scalar);
  }
  if (keyKind == 2) {
    return __map_set_keyed_impl(m, k, v, __map_hash_wide, __map_eq_wide);
  }
  return __map_set_keyed_impl(m, k, v, __map_hash_str, __map_eq_str);
}

pub function __map_put_impl(m: usize, k: usize, v: usize): usize {
  let keyKind: i32 = __load_i32(__load_ptr(m) + 8);
  if (keyKind == 0) {
    return __map_put_keyed(m, k, v, __map_hash_scalar, __map_eq_scalar);
  }
  if (keyKind == 2) {
    return __map_put_keyed(m, k, v, __map_hash_wide, __map_eq_wide);
  }
  return __map_put_keyed(m, k, v, __map_hash_str, __map_eq_str);
}

pub function __map_i32_column(m: usize, kvOffset: i32): i32[] {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < len_) {
    out = out.append(__load_i32(entriesBase + i * entryStride + kvOffset));
    i = i + 1;
  }
  return out;
}

pub function __map_cell(v: i64): usize {
  let c: usize = __alloc(8);
  __store_i64(c, v);
  return c;
}

pub function __map_cell_load(c: usize): i64 {
  return __load_i64(c);
}

pub function __map_cell_free(c: usize): usize {
  __free(c, 8);
  return c;
}

pub function __map_get_cell_impl(m: usize, k: usize): Option[i64] {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(__load_i64(__map_val_at(m, idx)));
}

pub function __map_get_cell_f64_impl(m: usize, k: usize): Option[f64] {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(f64_from_bits(__load_i64(__map_val_at(m, idx))));
}

pub function __map_get_cell_keyed_impl(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): Option[i64] {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(__load_i64(__map_val_at(m, idx)));
}

pub function __map_get_cell_f64_keyed_impl(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): Option[f64] {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(f64_from_bits(__load_i64(__map_val_at(m, idx))));
}

pub function __map_cell_column(m: usize, kvOffset: i32): i64[] {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: i64[] = [];
  let i: i32 = 0;
  while (i < len_) {
    out = out.append(__load_i64(__load_ptr(entriesBase + i * entryStride + kvOffset)));
    i = i + 1;
  }
  return out;
}
```

### `11 pub function __map_cell_f64_column(m: usize, kvOffset: i32): f64[]`

```fern
<<11 pub function __map_cell_f64_column(m: usize, kvOffset: i32): f64[]>>=
pub function __map_cell_f64_column(m: usize, kvOffset: i32): f64[] {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: f64[] = [];
  let i: i32 = 0;
  while (i < len_) {
    out = out.append(f64_from_bits(__load_i64(__load_ptr(entriesBase + i * entryStride + kvOffset))));
    i = i + 1;
  }
  return out;
}

pub function __map_bool_column(m: usize, kvOffset: i32): boolean[] {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: boolean[] = [];
  let i: i32 = 0;
  while (i < len_) {
    out = out.append(__load_i32(entriesBase + i * entryStride + kvOffset) != 0);
    i = i + 1;
  }
  return out;
}

pub function __map_u8_column(m: usize, kvOffset: i32): u8[] {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: u8[] = __alloc_u8(len_);
  let i: i32 = 0;
  while (i < len_) {
    let v32: i32 = __load_i32(entriesBase + i * entryStride + kvOffset);
    out = out.with(i, v32 as u8);
    i = i + 1;
  }
  return out;
}

pub function __map_keys_impl(m: usize): usize {
  let buf: usize = __load_ptr(m);
  let keyKind: i32 = __load_i32(buf + 8);
  if (keyKind == 1) {
    return __map_string_column(m, 0);
  }
  if (keyKind == 3) {
    return __map_ptr_column(m, 0, 1);
  }
  let col: i32[] = __map_i32_column(m, 0);
  return __fern_rc_inc(col as usize);
}

pub function __map_values_impl(m: usize): usize {
  let buf: usize = __load_ptr(m);
  let valKind: i32 = __map_val_kind(buf);
  let ptrW: i32 = __ptr_width();
  if (valKind >= 2 && valKind != 5) {
    return __map_ptr_column(m, ptrW, 1);
  }
  if (valKind == 1 || valKind == 5) {
    return __map_string_column(m, ptrW);
  }
  let col: i32[] = __map_i32_column(m, ptrW);
  return __fern_rc_inc(col as usize);
}

pub function __map_ptr_column(m: usize, kvOffset: i32, retainVals: i32): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let base: usize = __alloc(16 + len_ * ptrW);
  __store_i32(base + 4, len_);
  __store_i32(base + 8, 1);
  __store_i32(base + 12, len_);
  let data: usize = base + 16;
  let i: i32 = 0;
  while (i < len_) {
    let vp: usize = __load_ptr(entriesBase + i * entryStride + kvOffset);
    if (retainVals == 1) {
      vp = __fern_rc_inc(vp);
    }
    __store_ptr(data + i * ptrW, vp);
    i = i + 1;
  }
  return data;
}

pub function __map_string_column(m: usize, kvOffset: i32): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: string[] = [];
  let i: i32 = 0;
  while (i < len_) {
    let cellPtr: usize = __load_ptr(entriesBase + i * entryStride + kvOffset);
    let s: string = cellPtr as string;
    out = out.append(s);
    i = i + 1;
  }
  return __fern_rc_inc(out as usize);
}

pub function __map_box_column(m: usize, kvOffset: i32, retain: boolean): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let out: usize[] = [];
  let i: i32 = 0;
  while (i < len_) {
    let slot: usize = __load_ptr(entriesBase + i * entryStride + kvOffset);
    if (retain) {
      slot = __fern_rc_inc(slot);
    }
    out = out.append(slot);
    i = i + 1;
  }
  return __fern_rc_inc(out as usize);
}
```

### `12 pub function __map_drop_values(m: usize): usize`

```fern
<<12 pub function __map_drop_values(m: usize): usize>>=
pub function __map_drop_values(m: usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) != 1) {
    return m;
  }
  let buf: usize = __load_ptr(m);
  let vk0: i32 = __map_val_kind(buf);
  if (vk0 != 2 && vk0 != 3 && __map_val_cell_bytes(buf) == 0) {
    return m;
  }
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let i: i32 = 0;
  while (i < len_) {
    __map_dec_value(buf, __load_ptr(entriesBase + i * entryStride + ptrW));
    i = i + 1;
  }
  return m;
}

pub function __map_drop_impl(m: usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) == 1) {
    let buf: usize = __load_ptr(m);
    if (buf != 0 && __load_i32(buf + 8) != 1 && __map_key_cell_bytes(buf) != 0) {
      let len_: i32 = __load_i32(buf + 4);
      let entryStride: i32 = 2 * __ptr_width();
      let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
      let i: i32 = 0;
      while (i < len_) {
        __map_free_key_cell(buf, __load_ptr(entriesBase + i * entryStride));
        i = i + 1;
      }
    }
  }
  return __map_free_table(m);
}
```

### `13 function __map_free_table(m: usize): usize`

```fern
<<13 function __map_free_table(m: usize): usize>>=
function __map_free_table(m: usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) != 1) {
    __fern_rc_dec(m);
    return m;
  }
  let buf: usize = __load_ptr(m);
  if (buf != 0) {
    __free(buf, __map_buf_bytes(__load_i32(buf), __map_hdr(buf)));
  }
  __fern_box_free(m, 8);
  return m;
}
```

### `14 pub function __map_key_of(m: usize, k: usize): usize`

```fern
<<14 pub function __map_key_of(m: usize, k: usize): usize>>=
pub function __map_key_of(m: usize, k: usize): usize {
  let idx: i32 = __map_lookup(m, k);
  if (idx == 0 - 1) {
    return 0;
  }
  let buf: usize = __load_ptr(m);
  let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
  return __load_ptr(entriesBase + idx * (2 * __ptr_width()));
}

pub function __map_key_of_keyed(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): usize {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return 0;
  }
  let buf: usize = __load_ptr(m);
  let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
  return __load_ptr(entriesBase + idx * (2 * __ptr_width()));
}

pub function __map_drop_keyed_impl(m: usize, key_release: (usize) => usize, val_release: (usize) => usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) == 1) {
    let buf: usize = __load_ptr(m);
    let len_: i32 = __load_i32(buf + 4);
    let ptrW: i32 = __ptr_width();
    let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
    let i: i32 = 0;
    while (i < len_) {
      let slot: usize = entriesBase + i * (2 * ptrW);
      key_release(__load_ptr(slot));
      val_release(__load_ptr(slot + ptrW));
      i = i + 1;
    }
  }
  return __map_free_table(m);
}

pub function __map_drop_strcols_impl(m: usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) == 1) {
    let buf: usize = __load_ptr(m);
    let keys: boolean = __load_i32(buf + 8) == 1;
    let vals: boolean = __map_val_kind(buf) == 5;
    let len_: i32 = __load_i32(buf + 4);
    let ptrW: i32 = __ptr_width();
    let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
    let i: i32 = 0;
    while (i < len_) {
      let slot: usize = entriesBase + i * (2 * ptrW);
      if (keys) {
        __fern_str_dec(__load_ptr(slot) as string);
      } else {
        __map_free_key_cell(buf, __load_ptr(slot));
      }
      if (vals) {
        __fern_str_dec(__load_ptr(slot + ptrW) as string);
      } else {
        __map_free_val_cell(buf, __load_ptr(slot + ptrW));
      }
      i = i + 1;
    }
  }
  return __map_free_table(m);
}

pub function __map_drop_boxes_impl(m: usize, release: (usize) => usize): usize {
  if (m == 0) {
    return m;
  }
  if (__load_i32(m - 8) == 1) {
    let buf: usize = __load_ptr(m);
    let keys: boolean = __load_i32(buf + 8) == 1;
    let len_: i32 = __load_i32(buf + 4);
    let ptrW: i32 = __ptr_width();
    let entriesBase: usize = buf + __map_hdr(buf) + __load_i32(buf) * 4;
    let i: i32 = 0;
    while (i < len_) {
      let slot: usize = entriesBase + i * (2 * ptrW);
      if (keys) {
        __fern_str_dec(__load_ptr(slot) as string);
      } else {
        __map_free_key_cell(buf, __load_ptr(slot));
      }
      release(__load_ptr(slot + ptrW));
      i = i + 1;
    }
  }
  return __map_free_table(m);
}

pub function __map_delete_impl(m: usize, k: usize): boolean {
  let keyKind: i32 = __load_i32(__load_ptr(m) + 8);
  if (keyKind == 0) {
    return __map_delete_keyed_impl(m, k, __map_hash_scalar, __map_eq_scalar);
  }
  if (keyKind == 2) {
    return __map_delete_keyed_impl(m, k, __map_hash_wide, __map_eq_wide);
  }
  return __map_delete_keyed_impl(m, k, __map_hash_str, __map_eq_str);
}

pub function __map_clear_impl(m: usize): void {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  if (__map_val_cell_bytes(buf) != 0 || __map_key_cell_bytes(buf) != 0) {
    let len_: i32 = __load_i32(buf + 4);
    let ptrW: i32 = __ptr_width();
    let entryStride: i32 = 2 * ptrW;
    let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
    let i: i32 = 0;
    while (i < len_) {
      __map_free_val_cell(buf, __load_ptr(entriesBase + i * entryStride + ptrW));
      __map_free_key_cell(buf, __load_ptr(entriesBase + i * entryStride));
      i = i + 1;
    }
  }
  __store_i32(buf + 4, 0);
  __memset(buf + __map_hdr(buf), 255, cap * 4);
  __memset(__map_ctrl_base(buf, cap), 128, cap + 8);
}
```

### `15 function __map_lookup_keyed(m: usize, k: usize, hash_fn: (usize)`

```fern
<<15 function __map_lookup_keyed(m: usize, k: usize, hash_fn: (usize)>>=
function __map_lookup_keyed(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): i32 {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let mask: i32 = cap - 1;
  let entryStride: i32 = 2 * __ptr_width();
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;

  let len_: i32 = __load_i32(buf + 4);
  if (len_ <= 8) {
    let j: i32 = 0;
    while (j < len_) {
      let scanK: usize = __load_ptr(entriesBase + j * entryStride);
      if (eq_fn(scanK, k)) {
        return j;
      }
      j = j + 1;
    }
    return 0 - 1;
  }

  let h: i32 = __map_bucket_hash(k, hash_fn, __map_seed(buf));
  let bucket: i32 = h & mask;
  let homeB: i32 = __load_i32(buf + __map_hdr(buf) + bucket * 4);
  if (homeB == 0 - 1) {
    return 0 - 1;
  }
  if (homeB >= 0) {
    if (eq_fn(__load_ptr(entriesBase + homeB * entryStride), k)) {
      return homeB;
    }
  }
  let ctrlBase: usize = __map_ctrl_base(buf, cap);
  let lo: u64 = __map_swar_lo();
  let hi: u64 = __map_swar_hi();
  let all: u64 = __map_swar_all();
  let h2b: u64 = __map_h2(h) as u64 * lo;
  let eb: u64 = 128 as u64 * lo;
  let probed: i32 = 0;
  while (probed < cap) {
    let w: u64 = __load_i64(ctrlBase + bucket) as u64;
    let x: u64 = w ^ h2b;
    let hits: u64 = x - lo & (x ^ all) & hi;
    while (hits != 0 as u64) {
      let pos: i32 = bucket + (__ctz64(hits) >> 3) & mask;
      let b: i32 = __load_i32(buf + __map_hdr(buf) + pos * 4);
      let entryK: usize = __load_ptr(entriesBase + b * entryStride);
      if (eq_fn(entryK, k)) {
        return b;
      }
      hits = hits & hits - 1 as u64;
    }
    let y: u64 = w ^ eb;
    if ((y - lo & (y ^ all) & hi) != 0 as u64) {
      return 0 - 1;
    }
    bucket = bucket + 8 & mask;
    probed = probed + 8;
  }
  return 0 - 1;
}
```

### `16 pub function __map_has_keyed_impl(m: usize, k: usize, hash_fn: (usize)`

```fern
<<16 pub function __map_has_keyed_impl(m: usize, k: usize, hash_fn: (usize)>>=
pub function __map_has_keyed_impl(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): boolean {
  return __map_lookup_keyed(m, k, hash_fn, eq_fn) != 0 - 1;
}

pub function __map_get_keyed_impl(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): Option[usize] {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return None;
  }
  return Some(__map_retain_val(__load_ptr(m), __map_val_at(m, idx)));
}

pub function __map_get_or_keyed_impl(m: usize, k: usize, fallback: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): usize {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return __map_retain_val(__load_ptr(m), fallback);
  }
  return __map_retain_val(__load_ptr(m), __map_val_at(m, idx));
}

pub function __map_lookup_val_keyed(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): usize {
  let idx: i32 = __map_lookup_keyed(m, k, hash_fn, eq_fn);
  if (idx == 0 - 1) {
    return 0;
  }
  return __map_val_at(m, idx);
}
```

### `17 function __map_grow_keyed(m: usize, hash_fn: (usize)`

```fern
<<17 function __map_grow_keyed(m: usize, hash_fn: (usize)>>=
function __map_grow_keyed(m: usize, hash_fn: (usize) => i32): usize {
  let oldBuf: usize = __load_ptr(m);
  let seed: i32 = __map_seed(oldBuf);
  let oldCap: i32 = __load_i32(oldBuf);
  let oldLen: i32 = __load_i32(oldBuf + 4);
  let oldKK: i32 = __load_i32(oldBuf + 8);
  let oldVK: i32 = __load_i32(oldBuf + 12);
  let hdr: i32 = __map_hdr_bytes(oldKK);
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let oldEntries: usize = oldBuf + hdr + oldCap * 4;
  let newCap: i32 = 4;
  if (oldCap != 0) {
    newCap = oldCap << 1;
  }
  let newMask: i32 = newCap - 1;
  let newBuf: usize = __alloc(__map_buf_bytes(newCap, hdr));
  __store_i32(newBuf, newCap);
  __store_i32(newBuf + 4, oldLen);
  __store_i32(newBuf + 8, oldKK);
  __store_i32(newBuf + 12, oldVK);
  __store_i32(newBuf + 16, seed);
  __store_i32(newBuf + 20, __load_i32(oldBuf + 20));
  __memset(newBuf + hdr, 255, newCap * 4);
  let newCtrl: usize = __map_ctrl_base(newBuf, newCap);
  __memset(newCtrl, 128, newCap + 8);
  let newEntries: usize = newBuf + hdr + newCap * 4;
  let i: i32 = 0;
  while (i < oldLen) {
    let key: usize = __load_ptr(oldEntries + i * entryStride);
    let val: usize = __load_ptr(oldEntries + i * entryStride + ptrW);
    __store_ptr(newEntries + i * entryStride, key);
    __store_ptr(newEntries + i * entryStride + ptrW, val);
    let h: i32 = __map_bucket_hash(key, hash_fn, seed);
    let bucket: i32 = h & newMask;
    while (true) {
      let b: i32 = __load_i32(newBuf + hdr + bucket * 4);
      if (b == 0 - 1) {
        __store_i32(newBuf + hdr + bucket * 4, i);
        __map_ctrl_set(newCtrl, newCap, bucket, __map_h2(h));
        break;
      }
      bucket = bucket + 1 & newMask;
    }
    i = i + 1;
  }
  __store_ptr(m, newBuf);
  __free(oldBuf, __map_buf_bytes(oldCap, hdr));
  return newBuf;
}
```

### `18 pub function __map_set_keyed_impl(m: usize, k: usize, v: usize, hash_fn: (usize)`

```fern
<<18 pub function __map_set_keyed_impl(m: usize, k: usize, v: usize, hash_fn: (usize)>>=
pub function __map_set_keyed_impl(m: usize, k: usize, v: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): usize {
  m = __map_cow_inplace(m);
  __map_put_keyed(m, k, v, hash_fn, eq_fn);
  return m;
}

pub function __map_put_keyed(m: usize, k: usize, v: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): usize {
  let buf: usize = __load_ptr(m);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  if ((len_ + 1) * 4 >= cap * 3) {
    __map_grow_keyed(m, hash_fn);
    buf = __load_ptr(m);
    cap = __load_i32(buf);
    len_ = __load_i32(buf + 4);
  }
  let mask: i32 = cap - 1;
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let h: i32 = __map_bucket_hash(k, hash_fn, __map_seed(buf));
  let bucket: i32 = h & mask;
  let h2: i32 = __map_h2(h);
  let ctrlBase: usize = __map_ctrl_base(buf, cap);
  let insertSlot: i32 = 0 - 1;
  let homeB: i32 = __load_i32(buf + __map_hdr(buf) + bucket * 4);
  if (homeB == 0 - 1) {
    insertSlot = bucket;
  } else if (homeB >= 0 && eq_fn(__load_ptr(entriesBase + homeB * entryStride), k)) {
    let old: usize = __load_ptr(entriesBase + homeB * entryStride + ptrW);
    __map_dec_value(buf, old);
    __store_ptr(entriesBase + homeB * entryStride + ptrW, v);
    return old;
  }
  let lo: u64 = __map_swar_lo();
  let hi: u64 = __map_swar_hi();
  let all: u64 = __map_swar_all();
  let h2b: u64 = h2 as u64 * lo;
  let eb: u64 = 128 as u64 * lo;
  let tb: u64 = 254 as u64 * lo;
  let probed: i32 = 0;
  let done: boolean = insertSlot != 0 - 1;
  while (!done && probed < cap) {
    let w: u64 = __load_i64(ctrlBase + bucket) as u64;
    let x: u64 = w ^ h2b;
    let hits: u64 = x - lo & (x ^ all) & hi;
    while (hits != 0 as u64) {
      let pos: i32 = bucket + (__ctz64(hits) >> 3) & mask;
      let b: i32 = __load_i32(buf + __map_hdr(buf) + pos * 4);
      let entryK: usize = __load_ptr(entriesBase + b * entryStride);
      if (eq_fn(entryK, k)) {
        let old: usize = __load_ptr(entriesBase + b * entryStride + ptrW);
        __map_dec_value(buf, old);
        __store_ptr(entriesBase + b * entryStride + ptrW, v);
        return old;
      }
      hits = hits & hits - 1 as u64;
    }
    let y: u64 = w ^ eb;
    let empty: u64 = y - lo & (y ^ all) & hi;
    if (insertSlot == 0 - 1) {
      let z: u64 = w ^ tb;
      let dead: u64 = empty | z - lo & (z ^ all) & hi;
      if (dead != 0 as u64) {
        insertSlot = bucket + (__ctz64(dead) >> 3) & mask;
      }
    }
    if (empty != 0 as u64) {
      done = true;
    } else {
      bucket = bucket + 8 & mask;
      probed = probed + 8;
    }
  }
  __store_ptr(entriesBase + len_ * entryStride, k);
  __store_ptr(entriesBase + len_ * entryStride + ptrW, v);
  __store_i32(buf + __map_hdr(buf) + insertSlot * 4, len_);
  __map_ctrl_set(ctrlBase, cap, insertSlot, h2);
  __store_i32(buf + 4, len_ + 1);
  return 0;
}
```

### `19 pub function __map_delete_keyed_impl(m: usize, k: usize, hash_fn: (usize)`

```fern
<<19 pub function __map_delete_keyed_impl(m: usize, k: usize, hash_fn: (usize)>>=
pub function __map_delete_keyed_impl(m: usize, k: usize, hash_fn: (usize) => i32, eq_fn: (usize, usize) => boolean): boolean {
  let buf: usize = __load_ptr(m);
  let seed: i32 = __map_seed(buf);
  let cap: i32 = __load_i32(buf);
  let len_: i32 = __load_i32(buf + 4);
  let mask: i32 = cap - 1;
  let ptrW: i32 = __ptr_width();
  let entryStride: i32 = 2 * ptrW;
  let entriesBase: usize = buf + __map_hdr(buf) + cap * 4;
  let h: i32 = __map_bucket_hash(k, hash_fn, seed);
  let bucket: i32 = h & mask;
  let ctrlBase: usize = __map_ctrl_base(buf, cap);
  let foundBucket: i32 = 0 - 1;
  let idx: i32 = 0;
  let homeB: i32 = __load_i32(buf + __map_hdr(buf) + bucket * 4);
  if (homeB == 0 - 1) {
    return false;
  }
  let done: boolean = false;
  if (homeB >= 0) {
    if (eq_fn(__load_ptr(entriesBase + homeB * entryStride), k)) {
      foundBucket = bucket;
      idx = homeB;
      done = true;
    }
  }
  let lo: u64 = __map_swar_lo();
  let hi: u64 = __map_swar_hi();
  let all: u64 = __map_swar_all();
  let h2b: u64 = __map_h2(h) as u64 * lo;
  let eb: u64 = 128 as u64 * lo;
  let probed: i32 = 0;
  while (!done && probed < cap) {
    let w: u64 = __load_i64(ctrlBase + bucket) as u64;
    let x: u64 = w ^ h2b;
    let hits: u64 = x - lo & (x ^ all) & hi;
    while (hits != 0 as u64) {
      let pos: i32 = bucket + (__ctz64(hits) >> 3) & mask;
      let b: i32 = __load_i32(buf + __map_hdr(buf) + pos * 4);
      let entryK: usize = __load_ptr(entriesBase + b * entryStride);
      if (eq_fn(entryK, k)) {
        foundBucket = pos;
        idx = b;
        done = true;
        hits = 0 as u64;
      } else {
        hits = hits & hits - 1 as u64;
      }
    }
    if (!done) {
      let y: u64 = w ^ eb;
      if ((y - lo & (y ^ all) & hi) != 0 as u64) {
        done = true;
      } else {
        bucket = bucket + 8 & mask;
        probed = probed + 8;
      }
    }
  }
  if (foundBucket == 0 - 1) {
    return false;
  }
  __map_free_val_cell(buf, __load_ptr(entriesBase + idx * entryStride + ptrW));
  __map_free_key_cell(buf, __load_ptr(entriesBase + idx * entryStride));
  __store_i32(buf + __map_hdr(buf) + foundBucket * 4, 0 - 2);
  __map_ctrl_set(ctrlBase, cap, foundBucket, 254);
  let last: i32 = len_ - 1;
  if (idx != last) {
    let lastKey: usize = __load_ptr(entriesBase + last * entryStride);
    let lastBucket: i32 = __map_bucket_hash(lastKey, hash_fn, seed) & mask;
    while (__load_i32(buf + __map_hdr(buf) + lastBucket * 4) != last) {
      lastBucket = lastBucket + 1 & mask;
    }
    let lastVal: usize = __load_ptr(entriesBase + last * entryStride + ptrW);
    __store_ptr(entriesBase + idx * entryStride, lastKey);
    __store_ptr(entriesBase + idx * entryStride + ptrW, lastVal);
    __store_i32(buf + __map_hdr(buf) + lastBucket * 4, idx);
  }
  __store_i32(buf + 4, last);
  return true;
}
```

### `20 Map entry snapshots`

```fern
<<20 Map entry snapshots>>=
// Return an owned snapshot of all key-value pairs.
pub function (m: Map[K, V]) entries(): (K, V)[] {
  let out: (K, V)[] = [];
  // Read both columns in storage order without a hash lookup per entry.
  for (k, v) in m {
    out = out.append((k, v));
  }
  return out;
}

pub function (m: Map[K, V]) merge(other: Map[K, V]): Map[K, V] {
  let out: Map[K, V] = m;
  for (k, v) in other {
    out = out.insert(k, v);
  }
  return out;
}

pub function from[K, V](pairs: (K, V)[]): Map[K, V] {
  let m: Map[K, V] = map_new(pairs.len());
  for p in pairs {
    m = m.insert(p.0, p.1);
  }
  return m;
}

pub function (m: Map[K, V]) get_or_insert(k: K, fallback: V): (Map[K, V], V) {
  match (m.get(k)) {
    Some(v) => {
      return (m, v);
    },
    None => {
      let m2: Map[K, V] = m.insert(k, fallback);
      return (m2, fallback);
    }
  }
}

pub function (m: Map[K, V]) update(k: K, init: V, fn: (V) => V): Map[K, V] {
  let cur: V = m.get_or(k, init);
  return m.insert(k, fn(cur));
}

pub function (m: Map[K, V]) is_empty(): boolean {
  return m.len() == 0;
}

pub function (m: Map[K, V]) fold[K, V, A](init: A, f: (A, K, V) => A): A {
  let acc: A = init;
  for (k, v) in m {
    acc = f(acc, k, v);
  }
  return acc;
}

pub function (m: Map[K, V]) for_each(f: (K, V) => void): void {
  for (k, v) in m {
    f(k, v);
  }
}

pub function (m: Map[K, V]) map_values[K, V, W](f: (V) => W): Map[K, W] {
  let out: Map[K, W] = map_new(m.len());
  for (k, v) in m {
    out = out.insert(k, f(v));
  }
  return out;
}

pub function (m: Map[K, V]) filter(pred: (K, V) => boolean): Map[K, V] {
  let out: Map[K, V] = map_new(m.len());
  for (k, v) in m {
    if (pred(k, v)) {
      out = out.insert(k, v);
    }
  }
  return out;
}

pub function (m: Map[K, V]) contains_value(target: V): boolean {
  for v in m.values() {
    if (v == target) {
      return true;
    }
  }
  return false;
}

// The pin reserves Map.iter, so its source provider has a different name.
pub function (m: Map[K, V]) __iterator(): iter.ArrayIter[(K, V)] {
  return iter.of(m.entries());
}
```
