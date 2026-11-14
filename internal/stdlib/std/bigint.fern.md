# Arbitrary-precision integers

This module provides arbitrary-precision integers.

## Model

`BigInt` stores a sign and an unsigned magnitude. Each magnitude limb carries 32 bits in a `u64` slot. Zero has no negative sign.

## Contracts

Arithmetic methods return a new value. `parse` refuses invalid text with the `ParseNumberError` the integer parsers use. The shift and bit methods operate on the magnitude.

## Executable example

```fern test name=bigint-example
import "std/bigint";

function main(): i32 {
  let n = bigint.from_i64(40).add(bigint.from_i64(2));
  if (n.to_string() != "42") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct BigInt>>

<<3 function __bi_mask(): u64>>

<<4 function __bi_mul_mag(a: u64[], b: u64[]): u64[]>>

<<5 pub function zero(): BigInt>>

<<6 pub function (a: BigInt) sub(b: BigInt): BigInt>>

<<7 pub function (a: BigInt) shr(k: i32): BigInt>>

<<8 function __bi_divmod_mag(a: u64[], b: u64[]): (u64[], u64[])>>

<<9 pub function (a: BigInt) to_string(): string>>

<<comparison traits>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides arbitrary-precision integers.
import "core/cmp";
import "std/number_parse";
```

### `2 pub struct BigInt`

```fern
<<2 pub struct BigInt>>=
pub struct BigInt {
  neg: boolean,
  mag: u64[],
}
```

### `3 function __bi_mask(): u64`

```fern
<<3 function __bi_mask(): u64>>=
function __bi_mask(): u64 {
  return 4294967295 as u64;
}

function __bi_base(): u64 {
  return 4294967296 as u64;
}

function __bi_sh32(): u64 {
  return 32 as u64;
}

function __bi_trim(a: u64[]): u64[] {
  let n = a.len();
  while (n > 1 && a[n - 1] == 0 as u64) {
    n -= 1;
  }
  if (n == a.len()) {
    return a;
  }
  let out: u64[] = [];
  let i = 0;
  while (i < n) {
    out = out.append(a[i]);
    i += 1;
  }
  return out;
}

function __bi_is_zero_mag(a: u64[]): boolean {
  return a.len() == 1 && a[0] == 0 as u64;
}

function __bi_make(neg: boolean, mag: u64[]): BigInt {
  let m = __bi_trim(mag);
  if (__bi_is_zero_mag(m)) {
    return BigInt { neg: false, mag: m };
  }
  return BigInt { neg: neg, mag: m };
}

function __bi_cmp_mag(a: u64[], b: u64[]): i32 {
  if (a.len() < b.len()) {
    return -1;
  }
  if (a.len() > b.len()) {
    return 1;
  }
  let i = a.len() - 1;
  while (i >= 0) {
    if (a[i] < b[i]) {
      return -1;
    }
    if (a[i] > b[i]) {
      return 1;
    }
    i -= 1;
  }
  return 0;
}

function __bi_add_mag(a: u64[], b: u64[]): u64[] {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let la = a.len();
  let lb = b.len();
  let n = la;
  if (lb > n) {
    n = lb;
  }
  let out: u64[] = [];
  let carry = 0 as u64;
  let i = 0;
  while (i < n) {
    let s = carry;
    if (i < la) {
      s += a[i];
    }
    if (i < lb) {
      s += b[i];
    }
    out = out.append(s & mask);
    carry = s >> sh;
    i += 1;
  }
  if (carry > 0 as u64) {
    out = out.append(carry);
  }
  return __bi_trim(out);
}

function __bi_sub_mag(a: u64[], b: u64[]): u64[] {
  let base = __bi_base();
  let la = a.len();
  let lb = b.len();
  let out: u64[] = [];
  let borrow = 0 as u64;
  let i = 0;
  while (i < la) {
    let d = borrow;
    if (i < lb) {
      d += b[i];
    }
    if (a[i] >= d) {
      out = out.append(a[i] - d);
      borrow = 0 as u64;
    } else {
      out = out.append(a[i] + base - d);
      borrow = 1 as u64;
    }
    i += 1;
  }
  return __bi_trim(out);
}
```

### `4 function __bi_mul_mag(a: u64[], b: u64[]): u64[]`

```fern
<<4 function __bi_mul_mag(a: u64[], b: u64[]): u64[]>>=
function __bi_mul_mag(a: u64[], b: u64[]): u64[] {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let la = a.len();
  let lb = b.len();
  if (__bi_is_zero_mag(a) || __bi_is_zero_mag(b)) {
    let z: u64[] = [];
    return z.append(0 as u64);
  }
  let out: u64[] = [];
  let i = 0;
  while (i < la + lb) {
    out = out.append(0 as u64);
    i += 1;
  }
  i = 0;
  while (i < la) {
    let carry = 0 as u64;
    let ai = a[i];
    let j = 0;
    while (j < lb) {
      let cur = out[i + j] + ai * b[j] + carry;
      out = out.with(i + j, cur & mask);
      carry = cur >> sh;
      j += 1;
    }
    let k = i + lb;
    while (carry > 0 as u64) {
      let c2 = out[k] + carry;
      out = out.with(k, c2 & mask);
      carry = c2 >> sh;
      k += 1;
    }
    i += 1;
  }
  return __bi_trim(out);
}

function __bi_mul_small(a: u64[], k: u64): u64[] {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let out = a;
  let carry = 0 as u64;
  let i = 0;
  while (i < a.len()) {
    let prod = a[i] * k + carry;
    out = out.with(i, prod & mask);
    carry = prod >> sh;
    i += 1;
  }
  if (carry > 0 as u64) {
    out = out.append(carry);
  }
  return __bi_trim(out);
}

function __bi_add_small(a: u64[], k: u64): u64[] {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let out: u64[] = [];
  let carry = k;
  let i = 0;
  while (i < a.len()) {
    let cur = a[i] + carry;
    out = out.append(cur & mask);
    carry = cur >> sh;
    i += 1;
  }
  while (carry > 0 as u64) {
    out = out.append(carry & mask);
    carry = carry >> sh;
  }
  return __bi_trim(out);
}

function __bi_divmod_small(a: u64[], d: u64): (u64[], u64) {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let n = a.len();
  let q: u64[] = [];
  let i = 0;
  while (i < n) {
    q = q.append(0 as u64);
    i += 1;
  }
  let rem = 0 as u64;
  i = n - 1;
  while (i >= 0) {
    let cur = rem << sh | a[i] & mask;
    q = q.with(i, cur / d);
    rem = cur % d;
    i -= 1;
  }
  return (__bi_trim(q), rem);
}
```

### `5 pub function zero(): BigInt`

```fern
<<5 pub function zero(): BigInt>>=
pub function zero(): BigInt {
  let z: u64[] = [];
  return BigInt { neg: false, mag: z.append(0 as u64) };
}

pub function from_i64(v: i64): BigInt {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let neg = v < 0;
  let u = v as u64;
  if (neg) {
    u = 0 as u64 - u;
  }
  let m: u64[] = [];
  m = m.append(u & mask);
  m = m.append(u >> sh & mask);
  return __bi_make(neg, m);
}

pub function parse(s: string): Result[BigInt, number_parse.ParseNumberError] {
  let n = s.len();
  if (n == 0) {
    return Err(number_parse.EmptyInput);
  }
  let i = 0;
  let neg = false;
  if (s[0] == 45) {
    neg = true;
    i = 1;
  } else {
    if (s[0] == 43) {
      i = 1;
    }
  }
  if (i >= n) {
    return Err(number_parse.InvalidDigit(i));
  }
  let mag: u64[] = [];
  mag = mag.append(0 as u64);
  while (i < n) {
    let chunk = 0 as u64;
    let scale = 1 as u64;
    let taken = 0;
    while (i < n && taken < 9) {
      let c = s[i] as i32;
      if (c < 48 || c > 57) {
        return Err(number_parse.InvalidDigit(i));
      }
      chunk = chunk * (10 as u64) + (c - 48) as u64;
      scale *= 10 as u64;
      taken += 1;
      i += 1;
    }
    mag = __bi_mul_small(mag, scale);
    mag = __bi_add_small(mag, chunk);
  }
  return Ok(__bi_make(neg, mag));
}

pub function (a: BigInt) is_zero(): boolean {
  return __bi_is_zero_mag(a.mag);
}

pub function (a: BigInt) is_negative(): boolean {
  return a.neg;
}

pub function (a: BigInt) bit_length(): i32 {
  if (__bi_is_zero_mag(a.mag)) {
    return 0;
  }
  let top = a.mag.len() - 1;
  let v = a.mag[top];
  let bits = 0;
  while (v > 0 as u64) {
    bits += 1;
    v >>= 1 as u64;
  }
  return top * 32 + bits;
}

pub function (a: BigInt) cmp(b: BigInt): i32 {
  if (a.neg && !b.neg) {
    return -1;
  }
  if (!a.neg && b.neg) {
    return 1;
  }
  let c = __bi_cmp_mag(a.mag, b.mag);
  if (a.neg) {
    return -c;
  }
  return c;
}

pub function (a: BigInt) eq(b: BigInt): boolean {
  return a.cmp(b) == 0;
}

pub function (a: BigInt) hash(): i32 {
  let h = 17;
  if (a.neg) {
    h = 31;
  }
  let i = 0;
  while (i < a.mag.len()) {
    h = h * 31 + (a.mag[i] & __bi_mask()) as i32;
    i += 1;
  }
  return h;
}

pub function (a: BigInt) negate(): BigInt {
  return __bi_make(!a.neg, a.mag);
}

pub function (a: BigInt) abs(): BigInt {
  return __bi_make(false, a.mag);
}

pub function (a: BigInt) add(b: BigInt): BigInt {
  if (a.neg == b.neg) {
    return __bi_make(a.neg, __bi_add_mag(a.mag, b.mag));
  }
  let c = __bi_cmp_mag(a.mag, b.mag);
  if (c == 0) {
    return zero();
  }
  if (c > 0) {
    return __bi_make(a.neg, __bi_sub_mag(a.mag, b.mag));
  }
  return __bi_make(b.neg, __bi_sub_mag(b.mag, a.mag));
}
```

### `6 pub function (a: BigInt) sub(b: BigInt): BigInt`

```fern
<<6 pub function (a: BigInt) sub(b: BigInt): BigInt>>=
pub function (a: BigInt) sub(b: BigInt): BigInt {
  return a.add(b.negate());
}

pub function (a: BigInt) mul(b: BigInt): BigInt {
  let neg = a.neg != b.neg;
  return __bi_make(neg, __bi_mul_mag(a.mag, b.mag));
}

pub function (a: BigInt) mul_pow10(k: i32): BigInt {
  if (k <= 0) {
    return a;
  }
  let mag = a.mag;
  let left = k;
  while (left > 0) {
    let step = left;
    if (step > 9) {
      step = 9;
    }
    let scale = 1 as u64;
    let i = 0;
    while (i < step) {
      scale *= 10 as u64;
      i += 1;
    }
    mag = __bi_mul_small(mag, scale);
    left -= step;
  }
  return __bi_make(a.neg, mag);
}

pub function (a: BigInt) mul_pow5(k: i32): BigInt {
  if (k <= 0) {
    return a;
  }
  let mag = a.mag;
  let left = k;
  while (left > 0) {
    let step = left;
    if (step > 13) {
      step = 13;
    }
    let scale = 1 as u64;
    let i = 0;
    while (i < step) {
      scale *= 5 as u64;
      i += 1;
    }
    mag = __bi_mul_small(mag, scale);
    left -= step;
  }
  return __bi_make(a.neg, mag);
}

pub function (a: BigInt) shl(k: i32): BigInt {
  if (k <= 0 || __bi_is_zero_mag(a.mag)) {
    return a;
  }
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let limbShift = k / 32;
  let bitShift = (k % 32) as u64;
  let out: u64[] = [];
  let i = 0;
  while (i < limbShift) {
    out = out.append(0 as u64);
    i += 1;
  }
  let carry = 0 as u64;
  i = 0;
  while (i < a.mag.len()) {
    let cur = a.mag[i] << bitShift | carry;
    out = out.append(cur & mask);
    carry = cur >> sh;
    i += 1;
  }
  if (carry > 0 as u64) {
    out = out.append(carry);
  }
  return __bi_make(a.neg, out);
}

pub function from_u64(v: u64): BigInt {
  let m: u64[] = [];
  m = m.append(v & __bi_mask());
  m = m.append(v >> __bi_sh32() & __bi_mask());
  return __bi_make(false, m);
}

pub function (a: BigInt) low_u64(): u64 {
  return a.word64(0);
}

pub function (a: BigInt) word64(i: i32): u64 {
  if (i < 0 || i >= a.mag.len()) {
    return 0 as u64;
  }
  let lo = 2 * i;
  let v = 0 as u64;
  if (lo < a.mag.len()) {
    v = a.mag[lo];
  }
  if (lo + 1 < a.mag.len()) {
    v |= a.mag[lo + 1] << __bi_sh32();
  }
  return v;
}

pub function (a: BigInt) has_low_bits(k: i32): boolean {
  if (k <= 0) {
    return false;
  }
  let limbs = k / 32;
  let i = 0;
  while (i < limbs && i < a.mag.len()) {
    if (a.mag[i] != 0 as u64) {
      return true;
    }
    i += 1;
  }
  if (limbs >= a.mag.len() || k % 32 == 0) {
    return false;
  }
  let mask = (1 as u64 << (k % 32) as u64) - 1 as u64;
  return (a.mag[limbs] & mask) != 0 as u64;
}

pub function (a: BigInt) bit(i: i32): boolean {
  if (i < 0) {
    return false;
  }
  let limb = i / 32;
  if (limb >= a.mag.len()) {
    return false;
  }
  return (a.mag[limb] >> (i % 32) as u64 & 1 as u64) == 1 as u64;
}
```

### `7 pub function (a: BigInt) shr(k: i32): BigInt`

```fern
<<7 pub function (a: BigInt) shr(k: i32): BigInt>>=
pub function (a: BigInt) shr(k: i32): BigInt {
  if (k <= 0) {
    return a;
  }
  let mask = __bi_mask();
  let limbShift = k / 32;
  let bitShift = (k % 32) as u64;
  let n = a.mag.len() - limbShift;
  if (n <= 0) {
    return zero();
  }
  let out: u64[] = [];
  let i = 0;
  while (i < n) {
    let cur = a.mag[i + limbShift] >> bitShift;
    if (bitShift > 0 as u64 && i + limbShift + 1 < a.mag.len()) {
      cur |= a.mag[i + limbShift + 1] << 32 as u64 - bitShift & mask;
    }
    out = out.append(cur);
    i += 1;
  }
  return __bi_make(a.neg, out);
}

pub function (a: BigInt) pow(n: i32): BigInt {
  let result = from_i64(1 as i64);
  let base = a;
  let e = n;
  while (e > 0) {
    if (e % 2 == 1) {
      result = result.mul(base);
    }
    e /= 2;
    if (e > 0) {
      base = base.mul(base);
    }
  }
  return result;
}

// A division by zero answers the quotient 0 and the remainder `a`.
pub function (a: BigInt) divmod(b: BigInt): (BigInt, BigInt) {
  if (__bi_is_zero_mag(b.mag)) {
    return (zero(), a);
  }
  let qr = __bi_divmod_mag(a.mag, b.mag);
  return (__bi_make(a.neg != b.neg, qr.0), __bi_make(a.neg, qr.1));
}
```

### `8 function __bi_divmod_mag(a: u64[], b: u64[]): (u64[], u64[])`

```fern
<<8 function __bi_divmod_mag(a: u64[], b: u64[]): (u64[], u64[])>>=
function __bi_divmod_mag(a: u64[], b: u64[]): (u64[], u64[]) {
  let mask = __bi_mask();
  let base = __bi_base();
  let sh = __bi_sh32();
  if (__bi_cmp_mag(a, b) < 0) {
    let z: u64[] = [];
    return (z.append(0 as u64), a);
  }
  if (b.len() == 1) {
    let dm: (u64[], u64) = __bi_divmod_small(a, b[0]);
    let r1: u64[] = [];
    return (dm.0, r1.append(dm.1));
  }
  let s = 0;
  let top = b[b.len() - 1];
  while ((top & 2147483648 as u64) == 0 as u64) {
    top = top << 1 as u64;
    s += 1;
  }
  let v = __bi_shl_limbs(b, s, false);
  let u = __bi_shl_limbs(a, s, true);
  let n = v.len();
  let m = u.len() - n;
  let q: u64[] = [];
  let k = 0;
  while (k < m) {
    q = q.append(0 as u64);
    k += 1;
  }
  let j = m - 1;
  while (j >= 0) {
    let num = u[j + n] << sh | u[j + n - 1];
    let qhat = num / v[n - 1];
    let rhat = num % v[n - 1];
    while (qhat >= base || qhat * v[n - 2] > (rhat << sh | u[j + n - 2])) {
      qhat = qhat - 1 as u64;
      rhat += v[n - 1];
      if (rhat >= base) {
        break;
      }
    }
    let carry = 0 as u64;
    let borrow = 0 as u64;
    let i = 0;
    while (i < n) {
      let p = qhat * v[i] + carry;
      carry = p >> sh;
      let sub = (p & mask) + borrow;
      if (u[i + j] >= sub) {
        u = u.with(i + j, u[i + j] - sub);
        borrow = 0 as u64;
      } else {
        u = u.with(i + j, u[i + j] + base - sub);
        borrow = 1 as u64;
      }
      i += 1;
    }
    let subTop = carry + borrow;
    if (u[j + n] >= subTop) {
      u = u.with(j + n, u[j + n] - subTop);
      q = q.with(j, qhat);
    } else {
      q = q.with(j, qhat - 1 as u64);
      let c2 = 0 as u64;
      i = 0;
      while (i < n) {
        let sum = u[i + j] + v[i] + c2;
        u = u.with(i + j, sum & mask);
        c2 = sum >> sh;
        i += 1;
      }
      u = u.with(j + n, u[j + n] + base - subTop + c2 & mask);
    }
    j -= 1;
  }
  let rem: u64[] = [];
  k = 0;
  while (k < n) {
    rem = rem.append(u[k]);
    k += 1;
  }
  return (__bi_trim(q), __bi_shr_limbs(__bi_trim(rem), s));
}

function __bi_shl_limbs(a: u64[], s: i32, extend: boolean): u64[] {
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let bs = s as u64;
  let out: u64[] = [];
  let carry = 0 as u64;
  let i = 0;
  while (i < a.len()) {
    let cur = a[i] << bs | carry;
    out = out.append(cur & mask);
    carry = cur >> sh;
    i += 1;
  }
  if (extend || carry > 0 as u64) {
    out = out.append(carry);
  }
  return out;
}

function __bi_shr_limbs(a: u64[], s: i32): u64[] {
  if (s == 0) {
    return a;
  }
  let mask = __bi_mask();
  let bs = s as u64;
  let out: u64[] = [];
  let i = 0;
  while (i < a.len()) {
    let cur = a[i] >> bs;
    if (i + 1 < a.len()) {
      cur = cur | a[i + 1] << 32 as u64 - bs & mask;
    }
    out = out.append(cur);
    i += 1;
  }
  return __bi_trim(out);
}
```

### `9 pub function (a: BigInt) to_string(): string`

```fern
<<9 pub function (a: BigInt) to_string(): string>>=
pub function (a: BigInt) to_string(): string {
  if (__bi_is_zero_mag(a.mag)) {
    return "0";
  }
  let mask = __bi_mask();
  let sh = __bi_sh32();
  let ten9 = 1000000000 as u64;
  let cur = a.mag;
  let n = cur.len();
  let chunks: u64[] = [];
  while (n > 0) {
    let rem = 0 as u64;
    let i = n - 1;
    while (i >= 0) {
      let c = rem << sh | cur[i] & mask;
      cur = cur.with(i, c / ten9);
      rem = c % ten9;
      i -= 1;
    }
    chunks = chunks.append(rem);
    while (n > 0 && cur[n - 1] == 0 as u64) {
      n -= 1;
    }
  }
  let top = chunks[chunks.len() - 1];
  let lead = 1;
  let t = top / (10 as u64);
  while (t > 0 as u64) {
    lead += 1;
    t /= 10 as u64;
  }
  let start = 0;
  if (a.neg) {
    start = 1;
  }
  let total = start + lead + 9 * (chunks.len() - 1);
  let out = __alloc_u8(total);
  if (a.neg) {
    out = out.with(0, 45 as u8);
  }
  let pos = total - 1;
  let k = 0;
  while (k < chunks.len()) {
    let v = chunks[k];
    let width = 9;
    if (k == chunks.len() - 1) {
      width = lead;
    }
    let d = 0;
    while (d < width) {
      out = out.with(pos, (v % (10 as u64) + 48 as u64) as u8);
      v /= 10 as u64;
      pos -= 1;
      d += 1;
    }
    k += 1;
  }
  unsafe {
    return string_from_bytes_unchecked(out);
  }
}
```

### `comparison traits`

```fern
<<comparison traits>>=
impl cmp.Display for BigInt {}

impl cmp.Eq for BigInt {}

impl cmp.Ord for BigInt {}

impl cmp.Hash for BigInt {}

impl cmp.Default for BigInt {
  function default(): BigInt {
    return zero();
  }
}

impl cmp.Debug for BigInt {
  function to_debug(self: BigInt): string {
    return self.to_string();
  }
}
```
