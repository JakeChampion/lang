# Numeric parse errors and kernels

This module defines numeric parse errors and the shared integer and float parser kernels. Scalar modules expose the public parser functions.

## Executable example

```fern test name=number_parse-example
import "std/i64";
import "std/number_parse";

function main(): i32 {
  let Err(why) = i64.parse_i64("9x") else { return 1; };
  if (why.message() != "invalid digit at byte 1") { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<errors and integer parsing>>
<<binary32 parsing>>
<<exact decimal conversion>>
<<binary64 parsing>>
```

### Errors and integer parsing

```fern
<<errors and integer parsing>>=
// std/number_parse — typed numeric parse errors and shared parser kernels.

import "std/error";
import "core/bigint";
import "core/int";
import "core/cmp";

// A nonnegative code is an invalid byte offset. Negative codes identify other failures.
pub(package) const SUCCESS: i32 = -4;

pub(package) function _error(code: i32, radix: i32): ParseNumberError {
  if (code >= 0) {
    return InvalidDigit(code);
  }
  if (code == -1) {
    return EmptyInput;
  }
  if (code == -2) {
    return OutOfRange;
  }
  return InvalidRadix(radix);
}

function _error_code(error: ParseNumberError): i32 {
  match (error) {
    EmptyInput => {
      return -1;
    },
    InvalidDigit(offset) => {
      return offset;
    },
    OutOfRange => {
      return -2;
    },
    InvalidRadix(_) => {
      return -3;
    }
  }
  return -1;
}

pub enum ParseNumberError {
  EmptyInput,
  InvalidDigit(i32),
  OutOfRange,
  InvalidRadix(i32),
}

pub function (e: ParseNumberError) message(): string {
  match (e) {
    EmptyInput => {
      return "number input is empty";
    },
    InvalidDigit(offset) => {
      return "invalid digit at byte " + offset.to_string();
    },
    OutOfRange => {
      return "number is out of range";
    },
    InvalidRadix(radix) => {
      return "invalid radix " + radix.to_string() + ": expected 2 through 36";
    }
  }
  return "";
}

pub function (e: ParseNumberError) to_string(): string {
  return e.message();
}

impl error.Error for ParseNumberError {}

impl cmp.Display for ParseNumberError {}

// Return the integer bits after a range check before each multiply.
// Signed callers cast these bits to their own result type.
pub(package) function _integer(s: str, radix: i32, signed: boolean, positive_limit: u64, negative_limit: u64): (u64, i32) {
  if (radix < 2 || radix > 36) {
    return (0 as u64, -3);
  }
  let n: i32 = s.len();
  if (n == 0) {
    return (0 as u64, -1);
  }
  let i: i32 = 0;
  let negative: boolean = false;
  if (s[0] == 45) {
    if (!signed) {
      return (0 as u64, 0);
    }
    negative = true;
    i = 1;
  } else if (s[0] == 43) {
    i = 1;
  }
  if (i == n) {
    return (0 as u64, i);
  }
  let limit: u64 = positive_limit;
  if (negative) {
    limit = negative_limit;
  }
  if (limit <= 4294967295 as u64) {
    return _small_integer_value(s, i, radix, limit, negative);
  }
  let base: u64 = radix as u64;
  let cutoff: u64 = limit / base;
  let last_digit: u64 = limit % base;
  let value: u64 = 0 as u64;
  while (i < n) {
    let digit: i32 = s[i] as i32 - 48;
    if (radix != 10) {
      digit = int.__radix_digit(s[i] as i32);
    }
    if (digit < 0 || digit >= radix) {
      return (0 as u64, i);
    }
    let d: u64 = digit as u64;
    if (value > cutoff || value == cutoff && d > last_digit) {
      return (0 as u64, -2);
    }
    value = value * base + d;
    i = i + 1;
  }
  if (negative) {
    return (0 as u64 - value, SUCCESS);
  }
  return (value, SUCCESS);
}

// A 32-bit limit leaves room for the next radix multiply in a 64-bit value.
function _small_integer_value(s: str, from: i32, radix: i32, limit: u64, negative: boolean): (u64, i32) {
  let value: u64 = 0 as u64;
  let i: i32 = from;
  while (i < s.len()) {
    let digit: i32 = s[i] as i32 - 48;
    if (radix != 10) {
      digit = int.__radix_digit(s[i] as i32);
    }
    if (digit < 0 || digit >= radix) {
      return (0 as u64, i);
    }
    value = value * (radix as u64) + digit as u64;
    if (value > limit) {
      return (0 as u64, -2);
    }
    i = i + 1;
  }
  if (negative) {
    return (0 as u64 - value, SUCCESS);
  }
  return (value, SUCCESS);
}

// Shared decimal conversion kernels.
```

### Binary32 parsing

```fern
<<binary32 parsing>>=
struct DecimalParts { digits: string, exponent: i32, negative: boolean }

function _signed64(value: f64, negative: boolean): f64 {
  let bits: i64 = f64_bits(value);
  if (negative) {
    bits = bits | 1 as i64 << 63;
  }
  return f64_from_bits(bits);
}

function _signed32(bits_in: i32, negative: boolean): f32 {
  let bits: i32 = bits_in;
  if (negative) {
    bits = bits | 1 << 31;
  }
  return f32_from_bits(bits);
}

function _word_is(s: str, from: i32, word: string): boolean {
  if (s.len() - from != word.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < word.len()) {
    let c: i32 = s[from + i] as i32;
    if (c >= 65 && c <= 90) {
      c = c + 32;
    }
    if (c != word[i] as i32) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function _special_value(s: str, from: i32): i32 {
  if (from == s.len()) {
    return 0;
  }
  let first: i32 = s[from] as i32 | 32;
  if (first == 110 && _word_is(s, from, "nan")) {
    return 1;
  }
  if (first == 105 && (_word_is(s, from, "inf") || _word_is(s, from, "infinity"))) {
    return 2;
  }
  return 0;
}

function _float_exact(s: str): (f64, i32) {
  match (_decimal_parts(s)) {
    Ok(parts) => {
      let bits: i64 = _el_parse(parts.digits, parts.exponent);
      let value: f64 = 0.0;
      if (bits >= 0) {
        value = f64_from_bits(bits);
      } else {
        value = __decimal_to_f64(parts.digits, parts.exponent);
      }
      if (f64_bits(value) >= 9218868437227405312) {
        return (0.0, -2);
      }
      return (_signed64(value, parts.negative), SUCCESS);
    },
    Err(e) => {
      return (0.0, _error_code(e));
    }
  }
  return (0.0, -1);
}

// Interpret the next binary32 pattern after the maximum as the finite 2^128.
// This supplies the exact overflow midpoint without an infinity operand.
function _f32_boundary_value(bits: i32): f64 {
  let exponent: i32 = bits >> 23;
  let significand: i32 = bits & 8388607;
  let power: i32 = -149;
  if (exponent != 0) {
    significand = significand + 8388608;
    power = exponent - 150;
  }
  return significand as f64 * f64_from_bits((power + 1023) as i64 << 52);
}

pub(package) function _float32(s: str): (f32, i32) {
  let (value, code) = _float64(s);
  if (code != SUCCESS) {
    return (0.0 as f32, code);
  }
  let raw: i64 = f64_bits(value);
  let negative: boolean = raw < 0 as i64;
  let absolute_bits: i64 = raw & 9223372036854775807;
  if (absolute_bits > 9218868437227405312) {
    return (_signed32(2143289344, negative), SUCCESS);
  }
  if (absolute_bits == 9218868437227405312) {
    return (_signed32(2139095040, negative), SUCCESS);
  }
  let absolute: f64 = f64_from_bits(absolute_bits);
  let candidate: i32 = f32_bits(absolute as f32);
  let represented: f64 = _f32_boundary_value(candidate);
  if (candidate < 2139095040 && absolute == represented) {
    return (_signed32(candidate, negative), SUCCESS);
  }
  let lower: i32 = candidate;
  let upper: i32 = candidate + 1;
  if (candidate == 2139095040 || absolute < represented) {
    lower = candidate - 1;
    upper = candidate;
  }
  let midpoint: f64 = (_f32_boundary_value(lower) + _f32_boundary_value(upper)) * 0.5;
  if (absolute != midpoint) {
    if (candidate == 2139095040) {
      return (0.0 as f32, -2);
    }
    return (_signed32(candidate, negative), SUCCESS);
  }
  return _float32_midpoint(s, midpoint, lower, upper, negative);
}

// Compare the full decimal input when its binary64 estimate equals a binary32 midpoint.
function _float32_midpoint(s: str, midpoint: f64, lower: i32, upper: i32, negative: boolean): (f32, i32) {
  match (_decimal_parts(s)) {
    Ok(parts) => {
      let mid_bits: i64 = f64_bits(midpoint);
      let relation: i32 = __cmp_big(__bn_of_digits(parts.digits), parts.exponent, __f64_sig(mid_bits), __f64_bexp(mid_bits));
      let chosen: i32 = lower;
      if (relation > 0 || relation == 0 && (lower & 1) != 0) {
        chosen = upper;
      }
      if (chosen == 2139095040) {
        return (0.0 as f32, -2);
      }
      return (_signed32(chosen, negative), SUCCESS);
    },
    Err(e) => {
      return (0.0 as f32, _error_code(e));
    }
  }
  return (0.0 as f32, -1);
}
```

### Exact decimal conversion

```fern
<<exact decimal conversion>>=
function __bn_of_digits(s: string): bigint.BigInt {
  // `s` carries only significant digits by __decimal_to_f64's contract,
  // and the empty string is handled before any caller reaches here — so
  // the None arm is unreachable. It returns zero rather than trapping to
  // keep the documented `"" is 0` behaviour total.
  match (bigint.parse(s)) {
    Some(v) => {
      return v;
    },
    None => {
      return bigint.zero();
    }
  }
}

// compare  Ldig * 10^p  vs  V * 2^b  → -1 / 0 / 1 (cross-multiply, no
// fractions). Ldig is a bignum; V fits an i64.
function __cmp_big(Ldig: bigint.BigInt, p: i32, V: i64, b: i32): i32 {
  let L: bigint.BigInt = Ldig;
  let R: bigint.BigInt = bigint.from_i64(V);
  if (p > 0) {
    L = L.mul_pow10(p);
  }
  if (p < 0) {
    R = R.mul_pow10(0 - p);
  }
  if (b > 0) {
    R = R.shl(b);
  }
  if (b < 0) {
    L = L.shl(0 - b);
  }
  return L.cmp(R);
}

// IEEE-754 f64 = sig * 2^bexp, sig in [2^52, 2^53) for normals (or the
// raw fraction for subnormals). These decode the fields of a bit pattern.
function __f64_sig(bits: i64): i64 {
  let e11: i64 = bits >> 52 & 2047;
  let frac: i64 = bits & 4503599627370495;
  if (e11 == 0) {
    return frac;
  }
  return frac | 4503599627370496;
}

function __f64_bexp(bits: i64): i32 {
  let e11: i64 = bits >> 52 & 2047;
  if (e11 == 0) {
    return 0 - 1074;
  }
  return (e11 - 1075) as i32;
}

// `(dig as integer) * 10^k` → the positive f64 NEAREST that value,
// round-half-to-even. `dig` carries only significant digits (no sign /
// point / leading / trailing zeros); "" is 0. A cheap f64 estimate seeds
// the answer, then exact bignum comparison against the two neighbouring
// ULP midpoints steps to — and pins the tie at — the correctly-rounded
// bit pattern. Neighbours come from ±1 on the bit pattern, so the ULP-
// spacing changes at powers of two and the subnormal boundary are handled
// automatically (IEEE bit patterns are monotone in magnitude).
function __decimal_to_f64(dig: string, k: i32): f64 {
  let dl: i32 = dig.len();
  if (dl == 0) {
    return 0.0;
  }
  let mag: i32 = dl + k;  // ~ floor(log10(value)) + 1
  if (mag > 310) {  // clearly overflows → +Inf
    return 1.0 / 0.0;
  }
  if (mag < 0 - 330) {  // clearly underflows → 0
    return 0.0;
  }
  // Seed: m0 = first <=15 digits (exact, < 2^53); Clinger's exact path
  // (single mul/div by an exact 10^e) when |e0| <= 22, else a drifting
  // estimate the refinement below corrects.
  let take: i32 = dl;
  if (take > 15) {
    take = 15;
  }
  let m0: i64 = 0;
  let di: i32 = 0;
  while (di < take) {
    m0 = m0 * 10 + (dig[di] as i64 - 48);
    di = di + 1;
  }
  let e0: i32 = k + (dl - take);
  let v: f64 = 0.0;
  if (e0 >= 0 && e0 <= 22) {
    let p: f64 = 1.0;
    let t: i32 = 0;
    while (t < e0) {
      p = p * 10.0;
      t = t + 1;
    }
    v = m0 as f64 * p;
  } else if (e0 < 0 && e0 >= 0 - 22) {
    let p2: f64 = 1.0;
    let t2: i32 = 0;
    let ne: i32 = 0 - e0;
    while (t2 < ne) {
      p2 = p2 * 10.0;
      t2 = t2 + 1;
    }
    v = m0 as f64 / p2;
  } else {
    v = m0 as f64;
    let ee: i32 = e0;
    while (ee > 0) {
      v = v * 10.0;
      ee = ee - 1;
    }
    while (ee < 0) {
      v = v / 10.0;
      ee = ee + 1;
    }
  }
  let INF: i64 = 9218868437227405312;  // 0x7FF0000000000000
  let bits: i64 = f64_bits(v);
  if (bits < 1) {  // estimate underflowed → smallest subnormal
    bits = 1;
  }
  if (bits >= INF) {  // estimate overflowed → max finite
    bits = INF - 1;
  }
  // Ld = 2 * mantissa: the midpoint tests compare 2*value against the
  // (integer) sum of two adjacent f64s.
  let Ld: bigint.BigInt = __bn_of_digits(dig).shl(1);
  let guard: i32 = 0;
  while (guard < 4000) {
    guard = guard + 1;
    let s: i64 = __f64_sig(bits);
    let e: i32 = __f64_bexp(bits);
    let up: i64 = bits + 1;
    let su: i64 = __f64_sig(up);
    let eu: i32 = __f64_bexp(up);
    let emin: i32 = e;
    if (eu < emin) {
      emin = eu;
    }
    let msum_up: i64 = (s << e - emin) + (su << eu - emin);
    let cu: i32 = __cmp_big(Ld, k, msum_up, emin);  // sign of (2*value - (v + v_up))
    if (cu > 0) {
      if (up >= INF) {  // rounds up past max finite → +Inf
        return f64_from_bits(INF);
      }
      bits = up;
    } else if (bits == 0) {
      return f64_from_bits(bits);
    } else {
      let dn: i64 = bits - 1;
      let sd: i64 = __f64_sig(dn);
      let ed: i32 = __f64_bexp(dn);
      let emin2: i32 = e;
      if (ed < emin2) {
        emin2 = ed;
      }
      let msum_dn: i64 = (s << e - emin2) + (sd << ed - emin2);
      let cd: i32 = __cmp_big(Ld, k, msum_dn, emin2);
      if (cd < 0) {
        bits = dn;
      } else {
        // mid_dn <= value <= mid_up: `bits` is nearest. Exact ties
        // (value on a midpoint) round to the even significand.
        if (cu == 0 && (s & 1) == 1) {
          bits = up;
        } else if (cd == 0 && (s & 1) == 1) {
          bits = dn;
        }
        return f64_from_bits(bits);
      }
    }
  }
  return f64_from_bits(bits);
}

// Eisel-Lemire: the fast path for decimal → binary64 (#6161 follow-on).
//
// `__decimal_to_f64` below is exact but slow: it seeds a float estimate and
// then refines it against big-integer midpoint comparisons, scaling a bignum
// by 10^k and 2^e on every iteration. Eisel-Lemire replaces that with a table
// lookup and one 128-bit multiply, and — by the Mushtak-Lemire result — is
// PROVABLY exact whenever the significand fits in 64 bits (at most 19 decimal
// digits). Longer inputs are truncated to 19 digits and computed twice, once
// with the truncated significand and once with it incremented; agreeing
// answers are exact, and only a disagreement falls back to the slow path.
// Measured over 9.5M inputs, 0.026% fall back.
```

### Binary64 parsing

```fern
<<binary64 parsing>>=
// BEGIN GENERATED EISEL-LEMIRE TABLE (cmd/floattablegen) — do not edit by hand.
// The table covers powers of five from -342 through 308 with 651 entries.
// Each entry stores the high 64 bits before the low 64 bits.
const EL_POW5: u64[] = [
  0xeef453d6923bd65au64, 0x113faa2906a13b3fu64,
  0x9558b4661b6565f8u64, 0x4ac7ca59a424c507u64,
  0xbaaee17fa23ebf76u64, 0x5d79bcf00d2df649u64,
  0xe95a99df8ace6f53u64, 0xf4d82c2c107973dcu64,
  0x91d8a02bb6c10594u64, 0x79071b9b8a4be869u64,
  0xb64ec836a47146f9u64, 0x9748e2826cdee284u64,
  0xe3e27a444d8d98b7u64, 0xfd1b1b2308169b25u64,
  0x8e6d8c6ab0787f72u64, 0xfe30f0f5e50e20f7u64,
  0xb208ef855c969f4fu64, 0xbdbd2d335e51a935u64,
  0xde8b2b66b3bc4723u64, 0xad2c788035e61382u64,
  0x8b16fb203055ac76u64, 0x4c3bcb5021afcc31u64,
  0xaddcb9e83c6b1793u64, 0xdf4abe242a1bbf3du64,
  0xd953e8624b85dd78u64, 0xd71d6dad34a2af0du64,
  0x87d4713d6f33aa6bu64, 0x8672648c40e5ad68u64,
  0xa9c98d8ccb009506u64, 0x680efdaf511f18c2u64,
  0xd43bf0effdc0ba48u64, 0x0212bd1b2566def2u64,
  0x84a57695fe98746du64, 0x014bb630f7604b57u64,
  0xa5ced43b7e3e9188u64, 0x419ea3bd35385e2du64,
  0xcf42894a5dce35eau64, 0x52064cac828675b9u64,
  0x818995ce7aa0e1b2u64, 0x7343efebd1940993u64,
  0xa1ebfb4219491a1fu64, 0x1014ebe6c5f90bf8u64,
  0xca66fa129f9b60a6u64, 0xd41a26e077774ef6u64,
  0xfd00b897478238d0u64, 0x8920b098955522b4u64,
  0x9e20735e8cb16382u64, 0x55b46e5f5d5535b0u64,
  0xc5a890362fddbc62u64, 0xeb2189f734aa831du64,
  0xf712b443bbd52b7bu64, 0xa5e9ec7501d523e4u64,
  0x9a6bb0aa55653b2du64, 0x47b233c92125366eu64,
  0xc1069cd4eabe89f8u64, 0x999ec0bb696e840au64,
  0xf148440a256e2c76u64, 0xc00670ea43ca250du64,
  0x96cd2a865764dbcau64, 0x380406926a5e5728u64,
  0xbc807527ed3e12bcu64, 0xc605083704f5ecf2u64,
  0xeba09271e88d976bu64, 0xf7864a44c633682eu64,
  0x93445b8731587ea3u64, 0x7ab3ee6afbe0211du64,
  0xb8157268fdae9e4cu64, 0x5960ea05bad82964u64,
  0xe61acf033d1a45dfu64, 0x6fb92487298e33bdu64,
  0x8fd0c16206306babu64, 0xa5d3b6d479f8e056u64,
  0xb3c4f1ba87bc8696u64, 0x8f48a4899877186cu64,
  0xe0b62e2929aba83cu64, 0x331acdabfe94de87u64,
  0x8c71dcd9ba0b4925u64, 0x9ff0c08b7f1d0b14u64,
  0xaf8e5410288e1b6fu64, 0x07ecf0ae5ee44dd9u64,
  0xdb71e91432b1a24au64, 0xc9e82cd9f69d6150u64,
  0x892731ac9faf056eu64, 0xbe311c083a225cd2u64,
  0xab70fe17c79ac6cau64, 0x6dbd630a48aaf406u64,
  0xd64d3d9db981787du64, 0x092cbbccdad5b108u64,
  0x85f0468293f0eb4eu64, 0x25bbf56008c58ea5u64,
  0xa76c582338ed2621u64, 0xaf2af2b80af6f24eu64,
  0xd1476e2c07286faau64, 0x1af5af660db4aee1u64,
  0x82cca4db847945cau64, 0x50d98d9fc890ed4du64,
  0xa37fce126597973cu64, 0xe50ff107bab528a0u64,
  0xcc5fc196fefd7d0cu64, 0x1e53ed49a96272c8u64,
  0xff77b1fcbebcdc4fu64, 0x25e8e89c13bb0f7au64,
  0x9faacf3df73609b1u64, 0x77b191618c54e9acu64,
  0xc795830d75038c1du64, 0xd59df5b9ef6a2417u64,
  0xf97ae3d0d2446f25u64, 0x4b0573286b44ad1du64,
  0x9becce62836ac577u64, 0x4ee367f9430aec32u64,
  0xc2e801fb244576d5u64, 0x229c41f793cda73fu64,
  0xf3a20279ed56d48au64, 0x6b43527578c1110fu64,
  0x9845418c345644d6u64, 0x830a13896b78aaa9u64,
  0xbe5691ef416bd60cu64, 0x23cc986bc656d553u64,
  0xedec366b11c6cb8fu64, 0x2cbfbe86b7ec8aa8u64,
  0x94b3a202eb1c3f39u64, 0x7bf7d71432f3d6a9u64,
  0xb9e08a83a5e34f07u64, 0xdaf5ccd93fb0cc53u64,
  0xe858ad248f5c22c9u64, 0xd1b3400f8f9cff68u64,
  0x91376c36d99995beu64, 0x23100809b9c21fa1u64,
  0xb58547448ffffb2du64, 0xabd40a0c2832a78au64,
  0xe2e69915b3fff9f9u64, 0x16c90c8f323f516cu64,
  0x8dd01fad907ffc3bu64, 0xae3da7d97f6792e3u64,
  0xb1442798f49ffb4au64, 0x99cd11cfdf41779cu64,
  0xdd95317f31c7fa1du64, 0x40405643d711d583u64,
  0x8a7d3eef7f1cfc52u64, 0x482835ea666b2572u64,
  0xad1c8eab5ee43b66u64, 0xda3243650005eecfu64,
  0xd863b256369d4a40u64, 0x90bed43e40076a82u64,
  0x873e4f75e2224e68u64, 0x5a7744a6e804a291u64,
  0xa90de3535aaae202u64, 0x711515d0a205cb36u64,
  0xd3515c2831559a83u64, 0x0d5a5b44ca873e03u64,
  0x8412d9991ed58091u64, 0xe858790afe9486c2u64,
  0xa5178fff668ae0b6u64, 0x626e974dbe39a872u64,
  0xce5d73ff402d98e3u64, 0xfb0a3d212dc8128fu64,
  0x80fa687f881c7f8eu64, 0x7ce66634bc9d0b99u64,
  0xa139029f6a239f72u64, 0x1c1fffc1ebc44e80u64,
  0xc987434744ac874eu64, 0xa327ffb266b56220u64,
  0xfbe9141915d7a922u64, 0x4bf1ff9f0062baa8u64,
  0x9d71ac8fada6c9b5u64, 0x6f773fc3603db4a9u64,
  0xc4ce17b399107c22u64, 0xcb550fb4384d21d3u64,
  0xf6019da07f549b2bu64, 0x7e2a53a146606a48u64,
  0x99c102844f94e0fbu64, 0x2eda7444cbfc426du64,
  0xc0314325637a1939u64, 0xfa911155fefb5308u64,
  0xf03d93eebc589f88u64, 0x793555ab7eba27cau64,
  0x96267c7535b763b5u64, 0x4bc1558b2f3458deu64,
  0xbbb01b9283253ca2u64, 0x9eb1aaedfb016f16u64,
  0xea9c227723ee8bcbu64, 0x465e15a979c1cadcu64,
  0x92a1958a7675175fu64, 0x0bfacd89ec191ec9u64,
  0xb749faed14125d36u64, 0xcef980ec671f667bu64,
  0xe51c79a85916f484u64, 0x82b7e12780e7401au64,
  0x8f31cc0937ae58d2u64, 0xd1b2ecb8b0908810u64,
  0xb2fe3f0b8599ef07u64, 0x861fa7e6dcb4aa15u64,
  0xdfbdcece67006ac9u64, 0x67a791e093e1d49au64,
  0x8bd6a141006042bdu64, 0xe0c8bb2c5c6d24e0u64,
  0xaecc49914078536du64, 0x58fae9f773886e18u64,
  0xda7f5bf590966848u64, 0xaf39a475506a899eu64,
  0x888f99797a5e012du64, 0x6d8406c952429603u64,
  0xaab37fd7d8f58178u64, 0xc8e5087ba6d33b83u64,
  0xd5605fcdcf32e1d6u64, 0xfb1e4a9a90880a64u64,
  0x855c3be0a17fcd26u64, 0x5cf2eea09a55067fu64,
  0xa6b34ad8c9dfc06fu64, 0xf42faa48c0ea481eu64,
  0xd0601d8efc57b08bu64, 0xf13b94daf124da26u64,
  0x823c12795db6ce57u64, 0x76c53d08d6b70858u64,
  0xa2cb1717b52481edu64, 0x54768c4b0c64ca6eu64,
  0xcb7ddcdda26da268u64, 0xa9942f5dcf7dfd09u64,
  0xfe5d54150b090b02u64, 0xd3f93b35435d7c4cu64,
  0x9efa548d26e5a6e1u64, 0xc47bc5014a1a6dafu64,
  0xc6b8e9b0709f109au64, 0x359ab6419ca1091bu64,
  0xf867241c8cc6d4c0u64, 0xc30163d203c94b62u64,
  0x9b407691d7fc44f8u64, 0x79e0de63425dcf1du64,
  0xc21094364dfb5636u64, 0x985915fc12f542e4u64,
  0xf294b943e17a2bc4u64, 0x3e6f5b7b17b2939du64,
  0x979cf3ca6cec5b5au64, 0xa705992ceecf9c42u64,
  0xbd8430bd08277231u64, 0x50c6ff782a838353u64,
  0xece53cec4a314ebdu64, 0xa4f8bf5635246428u64,
  0x940f4613ae5ed136u64, 0x871b7795e136be99u64,
  0xb913179899f68584u64, 0x28e2557b59846e3fu64,
  0xe757dd7ec07426e5u64, 0x331aeada2fe589cfu64,
  0x9096ea6f3848984fu64, 0x3ff0d2c85def7621u64,
  0xb4bca50b065abe63u64, 0x0fed077a756b53a9u64,
  0xe1ebce4dc7f16dfbu64, 0xd3e8495912c62894u64,
  0x8d3360f09cf6e4bdu64, 0x64712dd7abbbd95cu64,
  0xb080392cc4349decu64, 0xbd8d794d96aacfb3u64,
  0xdca04777f541c567u64, 0xecf0d7a0fc5583a0u64,
  0x89e42caaf9491b60u64, 0xf41686c49db57244u64,
  0xac5d37d5b79b6239u64, 0x311c2875c522ced5u64,
  0xd77485cb25823ac7u64, 0x7d633293366b828bu64,
  0x86a8d39ef77164bcu64, 0xae5dff9c02033197u64,
  0xa8530886b54dbdebu64, 0xd9f57f830283fdfcu64,
  0xd267caa862a12d66u64, 0xd072df63c324fd7bu64,
  0x8380dea93da4bc60u64, 0x4247cb9e59f71e6du64,
  0xa46116538d0deb78u64, 0x52d9be85f074e608u64,
  0xcd795be870516656u64, 0x67902e276c921f8bu64,
  0x806bd9714632dff6u64, 0x00ba1cd8a3db53b6u64,
  0xa086cfcd97bf97f3u64, 0x80e8a40eccd228a4u64,
  0xc8a883c0fdaf7df0u64, 0x6122cd128006b2cdu64,
  0xfad2a4b13d1b5d6cu64, 0x796b805720085f81u64,
  0x9cc3a6eec6311a63u64, 0xcbe3303674053bb0u64,
  0xc3f490aa77bd60fcu64, 0xbedbfc4411068a9cu64,
  0xf4f1b4d515acb93bu64, 0xee92fb5515482d44u64,
  0x991711052d8bf3c5u64, 0x751bdd152d4d1c4au64,
  0xbf5cd54678eef0b6u64, 0xd262d45a78a0635du64,
  0xef340a98172aace4u64, 0x86fb897116c87c34u64,
  0x9580869f0e7aac0eu64, 0xd45d35e6ae3d4da0u64,
  0xbae0a846d2195712u64, 0x8974836059cca109u64,
  0xe998d258869facd7u64, 0x2bd1a438703fc94bu64,
  0x91ff83775423cc06u64, 0x7b6306a34627ddcfu64,
  0xb67f6455292cbf08u64, 0x1a3bc84c17b1d542u64,
  0xe41f3d6a7377eecau64, 0x20caba5f1d9e4a93u64,
  0x8e938662882af53eu64, 0x547eb47b7282ee9cu64,
  0xb23867fb2a35b28du64, 0xe99e619a4f23aa43u64,
  0xdec681f9f4c31f31u64, 0x6405fa00e2ec94d4u64,
  0x8b3c113c38f9f37eu64, 0xde83bc408dd3dd04u64,
  0xae0b158b4738705eu64, 0x9624ab50b148d445u64,
  0xd98ddaee19068c76u64, 0x3badd624dd9b0957u64,
  0x87f8a8d4cfa417c9u64, 0xe54ca5d70a80e5d6u64,
  0xa9f6d30a038d1dbcu64, 0x5e9fcf4ccd211f4cu64,
  0xd47487cc8470652bu64, 0x7647c3200069671fu64,
  0x84c8d4dfd2c63f3bu64, 0x29ecd9f40041e073u64,
  0xa5fb0a17c777cf09u64, 0xf468107100525890u64,
  0xcf79cc9db955c2ccu64, 0x7182148d4066eeb4u64,
  0x81ac1fe293d599bfu64, 0xc6f14cd848405530u64,
  0xa21727db38cb002fu64, 0xb8ada00e5a506a7cu64,
  0xca9cf1d206fdc03bu64, 0xa6d90811f0e4851cu64,
  0xfd442e4688bd304au64, 0x908f4a166d1da663u64,
  0x9e4a9cec15763e2eu64, 0x9a598e4e043287feu64,
  0xc5dd44271ad3cdbau64, 0x40eff1e1853f29fdu64,
  0xf7549530e188c128u64, 0xd12bee59e68ef47cu64,
  0x9a94dd3e8cf578b9u64, 0x82bb74f8301958ceu64,
  0xc13a148e3032d6e7u64, 0xe36a52363c1faf01u64,
  0xf18899b1bc3f8ca1u64, 0xdc44e6c3cb279ac1u64,
  0x96f5600f15a7b7e5u64, 0x29ab103a5ef8c0b9u64,
  0xbcb2b812db11a5deu64, 0x7415d448f6b6f0e7u64,
  0xebdf661791d60f56u64, 0x111b495b3464ad21u64,
  0x936b9fcebb25c995u64, 0xcab10dd900beec34u64,
  0xb84687c269ef3bfbu64, 0x3d5d514f40eea742u64,
  0xe65829b3046b0afau64, 0x0cb4a5a3112a5112u64,
  0x8ff71a0fe2c2e6dcu64, 0x47f0e785eaba72abu64,
  0xb3f4e093db73a093u64, 0x59ed216765690f56u64,
  0xe0f218b8d25088b8u64, 0x306869c13ec3532cu64,
  0x8c974f7383725573u64, 0x1e414218c73a13fbu64,
  0xafbd2350644eeacfu64, 0xe5d1929ef90898fau64,
  0xdbac6c247d62a583u64, 0xdf45f746b74abf39u64,
  0x894bc396ce5da772u64, 0x6b8bba8c328eb783u64,
  0xab9eb47c81f5114fu64, 0x066ea92f3f326564u64,
  0xd686619ba27255a2u64, 0xc80a537b0efefebdu64,
  0x8613fd0145877585u64, 0xbd06742ce95f5f36u64,
  0xa798fc4196e952e7u64, 0x2c48113823b73704u64,
  0xd17f3b51fca3a7a0u64, 0xf75a15862ca504c5u64,
  0x82ef85133de648c4u64, 0x9a984d73dbe722fbu64,
  0xa3ab66580d5fdaf5u64, 0xc13e60d0d2e0ebbau64,
  0xcc963fee10b7d1b3u64, 0x318df905079926a8u64,
  0xffbbcfe994e5c61fu64, 0xfdf17746497f7052u64,
  0x9fd561f1fd0f9bd3u64, 0xfeb6ea8bedefa633u64,
  0xc7caba6e7c5382c8u64, 0xfe64a52ee96b8fc0u64,
  0xf9bd690a1b68637bu64, 0x3dfdce7aa3c673b0u64,
  0x9c1661a651213e2du64, 0x06bea10ca65c084eu64,
  0xc31bfa0fe5698db8u64, 0x486e494fcff30a62u64,
  0xf3e2f893dec3f126u64, 0x5a89dba3c3efccfau64,
  0x986ddb5c6b3a76b7u64, 0xf89629465a75e01cu64,
  0xbe89523386091465u64, 0xf6bbb397f1135823u64,
  0xee2ba6c0678b597fu64, 0x746aa07ded582e2cu64,
  0x94db483840b717efu64, 0xa8c2a44eb4571cdcu64,
  0xba121a4650e4ddebu64, 0x92f34d62616ce413u64,
  0xe896a0d7e51e1566u64, 0x77b020baf9c81d17u64,
  0x915e2486ef32cd60u64, 0x0ace1474dc1d122eu64,
  0xb5b5ada8aaff80b8u64, 0x0d819992132456bau64,
  0xe3231912d5bf60e6u64, 0x10e1fff697ed6c69u64,
  0x8df5efabc5979c8fu64, 0xca8d3ffa1ef463c1u64,
  0xb1736b96b6fd83b3u64, 0xbd308ff8a6b17cb2u64,
  0xddd0467c64bce4a0u64, 0xac7cb3f6d05ddbdeu64,
  0x8aa22c0dbef60ee4u64, 0x6bcdf07a423aa96bu64,
  0xad4ab7112eb3929du64, 0x86c16c98d2c953c6u64,
  0xd89d64d57a607744u64, 0xe871c7bf077ba8b7u64,
  0x87625f056c7c4a8bu64, 0x11471cd764ad4972u64,
  0xa93af6c6c79b5d2du64, 0xd598e40d3dd89bcfu64,
  0xd389b47879823479u64, 0x4aff1d108d4ec2c3u64,
  0x843610cb4bf160cbu64, 0xcedf722a585139bau64,
  0xa54394fe1eedb8feu64, 0xc2974eb4ee658828u64,
  0xce947a3da6a9273eu64, 0x733d226229feea32u64,
  0x811ccc668829b887u64, 0x0806357d5a3f525fu64,
  0xa163ff802a3426a8u64, 0xca07c2dcb0cf26f7u64,
  0xc9bcff6034c13052u64, 0xfc89b393dd02f0b5u64,
  0xfc2c3f3841f17c67u64, 0xbbac2078d443ace2u64,
  0x9d9ba7832936edc0u64, 0xd54b944b84aa4c0du64,
  0xc5029163f384a931u64, 0x0a9e795e65d4df11u64,
  0xf64335bcf065d37du64, 0x4d4617b5ff4a16d5u64,
  0x99ea0196163fa42eu64, 0x504bced1bf8e4e45u64,
  0xc06481fb9bcf8d39u64, 0xe45ec2862f71e1d6u64,
  0xf07da27a82c37088u64, 0x5d767327bb4e5a4cu64,
  0x964e858c91ba2655u64, 0x3a6a07f8d510f86fu64,
  0xbbe226efb628afeau64, 0x890489f70a55368bu64,
  0xeadab0aba3b2dbe5u64, 0x2b45ac74ccea842eu64,
  0x92c8ae6b464fc96fu64, 0x3b0b8bc90012929du64,
  0xb77ada0617e3bbcbu64, 0x09ce6ebb40173744u64,
  0xe55990879ddcaabdu64, 0xcc420a6a101d0515u64,
  0x8f57fa54c2a9eab6u64, 0x9fa946824a12232du64,
  0xb32df8e9f3546564u64, 0x47939822dc96abf9u64,
  0xdff9772470297ebdu64, 0x59787e2b93bc56f7u64,
  0x8bfbea76c619ef36u64, 0x57eb4edb3c55b65au64,
  0xaefae51477a06b03u64, 0xede622920b6b23f1u64,
  0xdab99e59958885c4u64, 0xe95fab368e45ecedu64,
  0x88b402f7fd75539bu64, 0x11dbcb0218ebb414u64,
  0xaae103b5fcd2a881u64, 0xd652bdc29f26a119u64,
  0xd59944a37c0752a2u64, 0x4be76d3346f0495fu64,
  0x857fcae62d8493a5u64, 0x6f70a4400c562ddbu64,
  0xa6dfbd9fb8e5b88eu64, 0xcb4ccd500f6bb952u64,
  0xd097ad07a71f26b2u64, 0x7e2000a41346a7a7u64,
  0x825ecc24c873782fu64, 0x8ed400668c0c28c8u64,
  0xa2f67f2dfa90563bu64, 0x728900802f0f32fau64,
  0xcbb41ef979346bcau64, 0x4f2b40a03ad2ffb9u64,
  0xfea126b7d78186bcu64, 0xe2f610c84987bfa8u64,
  0x9f24b832e6b0f436u64, 0x0dd9ca7d2df4d7c9u64,
  0xc6ede63fa05d3143u64, 0x91503d1c79720dbbu64,
  0xf8a95fcf88747d94u64, 0x75a44c6397ce912au64,
  0x9b69dbe1b548ce7cu64, 0xc986afbe3ee11abau64,
  0xc24452da229b021bu64, 0xfbe85badce996168u64,
  0xf2d56790ab41c2a2u64, 0xfae27299423fb9c3u64,
  0x97c560ba6b0919a5u64, 0xdccd879fc967d41au64,
  0xbdb6b8e905cb600fu64, 0x5400e987bbc1c920u64,
  0xed246723473e3813u64, 0x290123e9aab23b68u64,
  0x9436c0760c86e30bu64, 0xf9a0b6720aaf6521u64,
  0xb94470938fa89bceu64, 0xf808e40e8d5b3e69u64,
  0xe7958cb87392c2c2u64, 0xb60b1d1230b20e04u64,
  0x90bd77f3483bb9b9u64, 0xb1c6f22b5e6f48c2u64,
  0xb4ecd5f01a4aa828u64, 0x1e38aeb6360b1af3u64,
  0xe2280b6c20dd5232u64, 0x25c6da63c38de1b0u64,
  0x8d590723948a535fu64, 0x579c487e5a38ad0eu64,
  0xb0af48ec79ace837u64, 0x2d835a9df0c6d851u64,
  0xdcdb1b2798182244u64, 0xf8e431456cf88e65u64,
  0x8a08f0f8bf0f156bu64, 0x1b8e9ecb641b58ffu64,
  0xac8b2d36eed2dac5u64, 0xe272467e3d222f3fu64,
  0xd7adf884aa879177u64, 0x5b0ed81dcc6abb0fu64,
  0x86ccbb52ea94baeau64, 0x98e947129fc2b4e9u64,
  0xa87fea27a539e9a5u64, 0x3f2398d747b36224u64,
  0xd29fe4b18e88640eu64, 0x8eec7f0d19a03aadu64,
  0x83a3eeeef9153e89u64, 0x1953cf68300424acu64,
  0xa48ceaaab75a8e2bu64, 0x5fa8c3423c052dd7u64,
  0xcdb02555653131b6u64, 0x3792f412cb06794du64,
  0x808e17555f3ebf11u64, 0xe2bbd88bbee40bd0u64,
  0xa0b19d2ab70e6ed6u64, 0x5b6aceaeae9d0ec4u64,
  0xc8de047564d20a8bu64, 0xf245825a5a445275u64,
  0xfb158592be068d2eu64, 0xeed6e2f0f0d56712u64,
  0x9ced737bb6c4183du64, 0x55464dd69685606bu64,
  0xc428d05aa4751e4cu64, 0xaa97e14c3c26b886u64,
  0xf53304714d9265dfu64, 0xd53dd99f4b3066a8u64,
  0x993fe2c6d07b7fabu64, 0xe546a8038efe4029u64,
  0xbf8fdb78849a5f96u64, 0xde98520472bdd033u64,
  0xef73d256a5c0f77cu64, 0x963e66858f6d4440u64,
  0x95a8637627989aadu64, 0xdde7001379a44aa8u64,
  0xbb127c53b17ec159u64, 0x5560c018580d5d52u64,
  0xe9d71b689dde71afu64, 0xaab8f01e6e10b4a6u64,
  0x9226712162ab070du64, 0xcab3961304ca70e8u64,
  0xb6b00d69bb55c8d1u64, 0x3d607b97c5fd0d22u64,
  0xe45c10c42a2b3b05u64, 0x8cb89a7db77c506au64,
  0x8eb98a7a9a5b04e3u64, 0x77f3608e92adb242u64,
  0xb267ed1940f1c61cu64, 0x55f038b237591ed3u64,
  0xdf01e85f912e37a3u64, 0x6b6c46dec52f6688u64,
  0x8b61313bbabce2c6u64, 0x2323ac4b3b3da015u64,
  0xae397d8aa96c1b77u64, 0xabec975e0a0d081au64,
  0xd9c7dced53c72255u64, 0x96e7bd358c904a21u64,
  0x881cea14545c7575u64, 0x7e50d64177da2e54u64,
  0xaa242499697392d2u64, 0xdde50bd1d5d0b9e9u64,
  0xd4ad2dbfc3d07787u64, 0x955e4ec64b44e864u64,
  0x84ec3c97da624ab4u64, 0xbd5af13bef0b113eu64,
  0xa6274bbdd0fadd61u64, 0xecb1ad8aeacdd58eu64,
  0xcfb11ead453994bau64, 0x67de18eda5814af2u64,
  0x81ceb32c4b43fcf4u64, 0x80eacf948770ced7u64,
  0xa2425ff75e14fc31u64, 0xa1258379a94d028du64,
  0xcad2f7f5359a3b3eu64, 0x096ee45813a04330u64,
  0xfd87b5f28300ca0du64, 0x8bca9d6e188853fcu64,
  0x9e74d1b791e07e48u64, 0x775ea264cf55347eu64,
  0xc612062576589ddau64, 0x95364afe032a819eu64,
  0xf79687aed3eec551u64, 0x3a83ddbd83f52205u64,
  0x9abe14cd44753b52u64, 0xc4926a9672793543u64,
  0xc16d9a0095928a27u64, 0x75b7053c0f178294u64,
  0xf1c90080baf72cb1u64, 0x5324c68b12dd6339u64,
  0x971da05074da7beeu64, 0xd3f6fc16ebca5e04u64,
  0xbce5086492111aeau64, 0x88f4bb1ca6bcf585u64,
  0xec1e4a7db69561a5u64, 0x2b31e9e3d06c32e6u64,
  0x9392ee8e921d5d07u64, 0x3aff322e62439fd0u64,
  0xb877aa3236a4b449u64, 0x09befeb9fad487c3u64,
  0xe69594bec44de15bu64, 0x4c2ebe687989a9b4u64,
  0x901d7cf73ab0acd9u64, 0x0f9d37014bf60a11u64,
  0xb424dc35095cd80fu64, 0x538484c19ef38c95u64,
  0xe12e13424bb40e13u64, 0x2865a5f206b06fbau64,
  0x8cbccc096f5088cbu64, 0xf93f87b7442e45d4u64,
  0xafebff0bcb24aafeu64, 0xf78f69a51539d749u64,
  0xdbe6fecebdedd5beu64, 0xb573440e5a884d1cu64,
  0x89705f4136b4a597u64, 0x31680a88f8953031u64,
  0xabcc77118461cefcu64, 0xfdc20d2b36ba7c3eu64,
  0xd6bf94d5e57a42bcu64, 0x3d32907604691b4du64,
  0x8637bd05af6c69b5u64, 0xa63f9a49c2c1b110u64,
  0xa7c5ac471b478423u64, 0x0fcf80dc33721d54u64,
  0xd1b71758e219652bu64, 0xd3c36113404ea4a9u64,
  0x83126e978d4fdf3bu64, 0x645a1cac083126eau64,
  0xa3d70a3d70a3d70au64, 0x3d70a3d70a3d70a4u64,
  0xccccccccccccccccu64, 0xcccccccccccccccdu64,
  0x8000000000000000u64, 0x0000000000000000u64,
  0xa000000000000000u64, 0x0000000000000000u64,
  0xc800000000000000u64, 0x0000000000000000u64,
  0xfa00000000000000u64, 0x0000000000000000u64,
  0x9c40000000000000u64, 0x0000000000000000u64,
  0xc350000000000000u64, 0x0000000000000000u64,
  0xf424000000000000u64, 0x0000000000000000u64,
  0x9896800000000000u64, 0x0000000000000000u64,
  0xbebc200000000000u64, 0x0000000000000000u64,
  0xee6b280000000000u64, 0x0000000000000000u64,
  0x9502f90000000000u64, 0x0000000000000000u64,
  0xba43b74000000000u64, 0x0000000000000000u64,
  0xe8d4a51000000000u64, 0x0000000000000000u64,
  0x9184e72a00000000u64, 0x0000000000000000u64,
  0xb5e620f480000000u64, 0x0000000000000000u64,
  0xe35fa931a0000000u64, 0x0000000000000000u64,
  0x8e1bc9bf04000000u64, 0x0000000000000000u64,
  0xb1a2bc2ec5000000u64, 0x0000000000000000u64,
  0xde0b6b3a76400000u64, 0x0000000000000000u64,
  0x8ac7230489e80000u64, 0x0000000000000000u64,
  0xad78ebc5ac620000u64, 0x0000000000000000u64,
  0xd8d726b7177a8000u64, 0x0000000000000000u64,
  0x878678326eac9000u64, 0x0000000000000000u64,
  0xa968163f0a57b400u64, 0x0000000000000000u64,
  0xd3c21bcecceda100u64, 0x0000000000000000u64,
  0x84595161401484a0u64, 0x0000000000000000u64,
  0xa56fa5b99019a5c8u64, 0x0000000000000000u64,
  0xcecb8f27f4200f3au64, 0x0000000000000000u64,
  0x813f3978f8940984u64, 0x4000000000000000u64,
  0xa18f07d736b90be5u64, 0x5000000000000000u64,
  0xc9f2c9cd04674edeu64, 0xa400000000000000u64,
  0xfc6f7c4045812296u64, 0x4d00000000000000u64,
  0x9dc5ada82b70b59du64, 0xf020000000000000u64,
  0xc5371912364ce305u64, 0x6c28000000000000u64,
  0xf684df56c3e01bc6u64, 0xc732000000000000u64,
  0x9a130b963a6c115cu64, 0x3c7f400000000000u64,
  0xc097ce7bc90715b3u64, 0x4b9f100000000000u64,
  0xf0bdc21abb48db20u64, 0x1e86d40000000000u64,
  0x96769950b50d88f4u64, 0x1314448000000000u64,
  0xbc143fa4e250eb31u64, 0x17d955a000000000u64,
  0xeb194f8e1ae525fdu64, 0x5dcfab0800000000u64,
  0x92efd1b8d0cf37beu64, 0x5aa1cae500000000u64,
  0xb7abc627050305adu64, 0xf14a3d9e40000000u64,
  0xe596b7b0c643c719u64, 0x6d9ccd05d0000000u64,
  0x8f7e32ce7bea5c6fu64, 0xe4820023a2000000u64,
  0xb35dbf821ae4f38bu64, 0xdda2802c8a800000u64,
  0xe0352f62a19e306eu64, 0xd50b2037ad200000u64,
  0x8c213d9da502de45u64, 0x4526f422cc340000u64,
  0xaf298d050e4395d6u64, 0x9670b12b7f410000u64,
  0xdaf3f04651d47b4cu64, 0x3c0cdd765f114000u64,
  0x88d8762bf324cd0fu64, 0xa5880a69fb6ac800u64,
  0xab0e93b6efee0053u64, 0x8eea0d047a457a00u64,
  0xd5d238a4abe98068u64, 0x72a4904598d6d880u64,
  0x85a36366eb71f041u64, 0x47a6da2b7f864750u64,
  0xa70c3c40a64e6c51u64, 0x999090b65f67d924u64,
  0xd0cf4b50cfe20765u64, 0xfff4b4e3f741cf6du64,
  0x82818f1281ed449fu64, 0xbff8f10e7a8921a4u64,
  0xa321f2d7226895c7u64, 0xaff72d52192b6a0du64,
  0xcbea6f8ceb02bb39u64, 0x9bf4f8a69f764490u64,
  0xfee50b7025c36a08u64, 0x02f236d04753d5b4u64,
  0x9f4f2726179a2245u64, 0x01d762422c946590u64,
  0xc722f0ef9d80aad6u64, 0x424d3ad2b7b97ef5u64,
  0xf8ebad2b84e0d58bu64, 0xd2e0898765a7deb2u64,
  0x9b934c3b330c8577u64, 0x63cc55f49f88eb2fu64,
  0xc2781f49ffcfa6d5u64, 0x3cbf6b71c76b25fbu64,
  0xf316271c7fc3908au64, 0x8bef464e3945ef7au64,
  0x97edd871cfda3a56u64, 0x97758bf0e3cbb5acu64,
  0xbde94e8e43d0c8ecu64, 0x3d52eeed1cbea317u64,
  0xed63a231d4c4fb27u64, 0x4ca7aaa863ee4bddu64,
  0x945e455f24fb1cf8u64, 0x8fe8caa93e74ef6au64,
  0xb975d6b6ee39e436u64, 0xb3e2fd538e122b44u64,
  0xe7d34c64a9c85d44u64, 0x60dbbca87196b616u64,
  0x90e40fbeea1d3a4au64, 0xbc8955e946fe31cdu64,
  0xb51d13aea4a488ddu64, 0x6babab6398bdbe41u64,
  0xe264589a4dcdab14u64, 0xc696963c7eed2dd1u64,
  0x8d7eb76070a08aecu64, 0xfc1e1de5cf543ca2u64,
  0xb0de65388cc8ada8u64, 0x3b25a55f43294bcbu64,
  0xdd15fe86affad912u64, 0x49ef0eb713f39ebeu64,
  0x8a2dbf142dfcc7abu64, 0x6e3569326c784337u64,
  0xacb92ed9397bf996u64, 0x49c2c37f07965404u64,
  0xd7e77a8f87daf7fbu64, 0xdc33745ec97be906u64,
  0x86f0ac99b4e8dafdu64, 0x69a028bb3ded71a3u64,
  0xa8acd7c0222311bcu64, 0xc40832ea0d68ce0cu64,
  0xd2d80db02aabd62bu64, 0xf50a3fa490c30190u64,
  0x83c7088e1aab65dbu64, 0x792667c6da79e0fau64,
  0xa4b8cab1a1563f52u64, 0x577001b891185938u64,
  0xcde6fd5e09abcf26u64, 0xed4c0226b55e6f86u64,
  0x80b05e5ac60b6178u64, 0x544f8158315b05b4u64,
  0xa0dc75f1778e39d6u64, 0x696361ae3db1c721u64,
  0xc913936dd571c84cu64, 0x03bc3a19cd1e38e9u64,
  0xfb5878494ace3a5fu64, 0x04ab48a04065c723u64,
  0x9d174b2dcec0e47bu64, 0x62eb0d64283f9c76u64,
  0xc45d1df942711d9au64, 0x3ba5d0bd324f8394u64,
  0xf5746577930d6500u64, 0xca8f44ec7ee36479u64,
  0x9968bf6abbe85f20u64, 0x7e998b13cf4e1ecbu64,
  0xbfc2ef456ae276e8u64, 0x9e3fedd8c321a67eu64,
  0xefb3ab16c59b14a2u64, 0xc5cfe94ef3ea101eu64,
  0x95d04aee3b80ece5u64, 0xbba1f1d158724a12u64,
  0xbb445da9ca61281fu64, 0x2a8a6e45ae8edc97u64,
  0xea1575143cf97226u64, 0xf52d09d71a3293bdu64,
  0x924d692ca61be758u64, 0x593c2626705f9c56u64,
  0xb6e0c377cfa2e12eu64, 0x6f8b2fb00c77836cu64,
  0xe498f455c38b997au64, 0x0b6dfb9c0f956447u64,
  0x8edf98b59a373fecu64, 0x4724bd4189bd5eacu64,
  0xb2977ee300c50fe7u64, 0x58edec91ec2cb657u64,
  0xdf3d5e9bc0f653e1u64, 0x2f2967b66737e3edu64,
  0x8b865b215899f46cu64, 0xbd79e0d20082ee74u64,
  0xae67f1e9aec07187u64, 0xecd8590680a3aa11u64,
  0xda01ee641a708de9u64, 0xe80e6f4820cc9495u64,
  0x884134fe908658b2u64, 0x3109058d147fdcddu64,
  0xaa51823e34a7eedeu64, 0xbd4b46f0599fd415u64,
  0xd4e5e2cdc1d1ea96u64, 0x6c9e18ac7007c91au64,
  0x850fadc09923329eu64, 0x03e2cf6bc604ddb0u64,
  0xa6539930bf6bff45u64, 0x84db8346b786151cu64,
  0xcfe87f7cef46ff16u64, 0xe612641865679a63u64,
  0x81f14fae158c5f6eu64, 0x4fcb7e8f3f60c07eu64,
  0xa26da3999aef7749u64, 0xe3be5e330f38f09du64,
  0xcb090c8001ab551cu64, 0x5cadf5bfd3072cc5u64,
  0xfdcb4fa002162a63u64, 0x73d9732fc7c8f7f6u64,
  0x9e9f11c4014dda7eu64, 0x2867e7fddcdd9afau64,
  0xc646d63501a1511du64, 0xb281e1fd541501b8u64,
  0xf7d88bc24209a565u64, 0x1f225a7ca91a4226u64,
  0x9ae757596946075fu64, 0x3375788de9b06958u64,
  0xc1a12d2fc3978937u64, 0x0052d6b1641c83aeu64,
  0xf209787bb47d6b84u64, 0xc0678c5dbd23a49au64,
  0x9745eb4d50ce6332u64, 0xf840b7ba963646e0u64,
  0xbd176620a501fbffu64, 0xb650e5a93bc3d898u64,
  0xec5d3fa8ce427affu64, 0xa3e51f138ab4cebeu64,
  0x93ba47c980e98cdfu64, 0xc66f336c36b10137u64,
  0xb8a8d9bbe123f017u64, 0xb80b0047445d4184u64,
  0xe6d3102ad96cec1du64, 0xa60dc059157491e5u64,
  0x9043ea1ac7e41392u64, 0x87c89837ad68db2fu64,
  0xb454e4a179dd1877u64, 0x29babe4598c311fbu64,
  0xe16a1dc9d8545e94u64, 0xf4296dd6fef3d67au64,
  0x8ce2529e2734bb1du64, 0x1899e4a65f58660cu64,
  0xb01ae745b101e9e4u64, 0x5ec05dcff72e7f8fu64,
  0xdc21a1171d42645du64, 0x76707543f4fa1f73u64,
  0x899504ae72497ebau64, 0x6a06494a791c53a8u64,
  0xabfa45da0edbde69u64, 0x0487db9d17636892u64,
  0xd6f8d7509292d603u64, 0x45a9d2845d3c42b6u64,
  0x865b86925b9bc5c2u64, 0x0b8a2392ba45a9b2u64,
  0xa7f26836f282b732u64, 0x8e6cac7768d7141eu64,
  0xd1ef0244af2364ffu64, 0x3207d795430cd926u64,
  0x8335616aed761f1fu64, 0x7f44e6bd49e807b8u64,
  0xa402b9c5a8d3a6e7u64, 0x5f16206c9c6209a6u64,
  0xcd036837130890a1u64, 0x36dba887c37a8c0fu64,
  0x802221226be55a64u64, 0xc2494954da2c9789u64,
  0xa02aa96b06deb0fdu64, 0xf2db9baa10b7bd6cu64,
  0xc83553c5c8965d3du64, 0x6f92829494e5acc7u64,
  0xfa42a8b73abbf48cu64, 0xcb772339ba1f17f9u64,
  0x9c69a97284b578d7u64, 0xff2a760414536efbu64,
  0xc38413cf25e2d70du64, 0xfef5138519684abau64,
  0xf46518c2ef5b8cd1u64, 0x7eb258665fc25d69u64,
  0x98bf2f79d5993802u64, 0xef2f773ffbd97a61u64,
  0xbeeefb584aff8603u64, 0xaafb550ffacfd8fau64,
  0xeeaaba2e5dbf6784u64, 0x95ba2a53f983cf38u64,
  0x952ab45cfa97a0b2u64, 0xdd945a747bf26183u64,
  0xba756174393d88dfu64, 0x94f971119aeef9e4u64,
  0xe912b9d1478ceb17u64, 0x7a37cd5601aab85du64,
  0x91abb422ccb812eeu64, 0xac62e055c10ab33au64,
  0xb616a12b7fe617aau64, 0x577b986b314d6009u64,
  0xe39c49765fdf9d94u64, 0xed5a7e85fda0b80bu64,
  0x8e41ade9fbebc27du64, 0x14588f13be847307u64,
  0xb1d219647ae6b31cu64, 0x596eb2d8ae258fc8u64,
  0xde469fbd99a05fe3u64, 0x6fca5f8ed9aef3bbu64,
  0x8aec23d680043beeu64, 0x25de7bb9480d5854u64,
  0xada72ccc20054ae9u64, 0xaf561aa79a10ae6au64,
  0xd910f7ff28069da4u64, 0x1b2ba1518094da04u64,
  0x87aa9aff79042286u64, 0x90fb44d2f05d0842u64,
  0xa99541bf57452b28u64, 0x353a1607ac744a53u64,
  0xd3fa922f2d1675f2u64, 0x42889b8997915ce8u64,
  0x847c9b5d7c2e09b7u64, 0x69956135febada11u64,
  0xa59bc234db398c25u64, 0x43fab9837e699095u64,
  0xcf02b2c21207ef2eu64, 0x94f967e45e03f4bbu64,
  0x8161afb94b44f57du64, 0x1d1be0eebac278f5u64,
  0xa1ba1ba79e1632dcu64, 0x6462d92a69731732u64,
  0xca28a291859bbf93u64, 0x7d7b8f7503cfdcfeu64,
  0xfcb2cb35e702af78u64, 0x5cda735244c3d43eu64,
  0x9defbf01b061adabu64, 0x3a0888136afa64a7u64,
  0xc56baec21c7a1916u64, 0x088aaa1845b8fdd0u64,
  0xf6c69a72a3989f5bu64, 0x8aad549e57273d45u64,
  0x9a3c2087a63f6399u64, 0x36ac54e2f678864bu64,
  0xc0cb28a98fcf3c7fu64, 0x84576a1bb416a7ddu64,
  0xf0fdf2d3f3c30b9fu64, 0x656d44a2a11c51d5u64,
  0x969eb7c47859e743u64, 0x9f644ae5a4b1b325u64,
  0xbc4665b596706114u64, 0x873d5d9f0dde1feeu64,
  0xeb57ff22fc0c7959u64, 0xa90cb506d155a7eau64,
  0x9316ff75dd87cbd8u64, 0x09a7f12442d588f2u64,
  0xb7dcbf5354e9beceu64, 0x0c11ed6d538aeb2fu64,
  0xe5d3ef282a242e81u64, 0x8f1668c8a86da5fau64,
  0x8fa475791a569d10u64, 0xf96e017d694487bcu64,
  0xb38d92d760ec4455u64, 0x37c981dcc395a9acu64,
  0xe070f78d3927556au64, 0x85bbe253f47b1417u64,
  0x8c469ab843b89562u64, 0x93956d7478ccec8eu64,
  0xaf58416654a6babbu64, 0x387ac8d1970027b2u64,
  0xdb2e51bfe9d0696au64, 0x06997b05fcc0319eu64,
  0x88fcf317f22241e2u64, 0x441fece3bdf81f03u64,
  0xab3c2fddeeaad25au64, 0xd527e81cad7626c3u64,
  0xd60b3bd56a5586f1u64, 0x8a71e223d8d3b074u64,
  0x85c7056562757456u64, 0xf6872d5667844e49u64,
  0xa738c6bebb12d16cu64, 0xb428f8ac016561dbu64,
  0xd106f86e69d785c7u64, 0xe13336d701beba52u64,
  0x82a45b450226b39cu64, 0xecc0024661173473u64,
  0xa34d721642b06084u64, 0x27f002d7f95d0190u64,
  0xcc20ce9bd35c78a5u64, 0x31ec038df7b441f4u64,
  0xff290242c83396ceu64, 0x7e67047175a15271u64,
  0x9f79a169bd203e41u64, 0x0f0062c6e984d386u64,
  0xc75809c42c684dd1u64, 0x52c07b78a3e60868u64,
  0xf92e0c3537826145u64, 0xa7709a56ccdf8a82u64,
  0x9bbcc7a142b17ccbu64, 0x88a66076400bb691u64,
  0xc2abf989935ddbfeu64, 0x6acff893d00ea435u64,
  0xf356f7ebf83552feu64, 0x0583f6b8c4124d43u64,
  0x98165af37b2153deu64, 0xc3727a337a8b704au64,
  0xbe1bf1b059e9a8d6u64, 0x744f18c0592e4c5cu64,
  0xeda2ee1c7064130cu64, 0x1162def06f79df73u64,
  0x9485d4d1c63e8be7u64, 0x8addcb5645ac2ba8u64,
  0xb9a74a0637ce2ee1u64, 0x6d953e2bd7173692u64,
  0xe8111c87c5c1ba99u64, 0xc8fa8db6ccdd0437u64,
  0x910ab1d4db9914a0u64, 0x1d9c9892400a22a2u64,
  0xb54d5e4a127f59c8u64, 0x2503beb6d00cab4bu64,
  0xe2a0b5dc971f303au64, 0x2e44ae64840fd61du64,
  0x8da471a9de737e24u64, 0x5ceaecfed289e5d2u64,
  0xb10d8e1456105dadu64, 0x7425a83e872c5f47u64,
  0xdd50f1996b947518u64, 0xd12f124e28f77719u64,
  0x8a5296ffe33cc92fu64, 0x82bd6b70d99aaa6fu64,
  0xace73cbfdc0bfb7bu64, 0x636cc64d1001550bu64,
  0xd8210befd30efa5au64, 0x3c47f7e05401aa4eu64,
  0x8714a775e3e95c78u64, 0x65acfaec34810a71u64,
  0xa8d9d1535ce3b396u64, 0x7f1839a741a14d0du64,
  0xd31045a8341ca07cu64, 0x1ede48111209a050u64,
  0x83ea2b892091e44du64, 0x934aed0aab460432u64,
  0xa4e4b66b68b65d60u64, 0xf81da84d5617853fu64,
  0xce1de40642e3f4b9u64, 0x36251260ab9d668eu64,
  0x80d2ae83e9ce78f3u64, 0xc1d72b7c6b426019u64,
  0xa1075a24e4421730u64, 0xb24cf65b8612f81fu64,
  0xc94930ae1d529cfcu64, 0xdee033f26797b627u64,
  0xfb9b7cd9a4a7443cu64, 0x169840ef017da3b1u64,
  0x9d412e0806e88aa5u64, 0x8e1f289560ee864eu64,
  0xc491798a08a2ad4eu64, 0xf1a6f2bab92a27e2u64,
  0xf5b5d7ec8acb58a2u64, 0xae10af696774b1dbu64,
  0x9991a6f3d6bf1765u64, 0xacca6da1e0a8ef29u64,
  0xbff610b0cc6edd3fu64, 0x17fd090a58d32af3u64,
  0xeff394dcff8a948eu64, 0xddfc4b4cef07f5b0u64,
  0x95f83d0a1fb69cd9u64, 0x4abdaf101564f98eu64,
  0xbb764c4ca7a4440fu64, 0x9d6d1ad41abe37f1u64,
  0xea53df5fd18d5513u64, 0x84c86189216dc5edu64,
  0x92746b9be2f8552cu64, 0x32fd3cf5b4e49bb4u64,
  0xb7118682dbb66a77u64, 0x3fbc8c33221dc2a1u64,
  0xe4d5e82392a40515u64, 0x0fabaf3feaa5334au64,
  0x8f05b1163ba6832du64, 0x29cb4d87f2a7400eu64,
  0xb2c71d5bca9023f8u64, 0x743e20e9ef511012u64,
  0xdf78e4b2bd342cf6u64, 0x914da9246b255416u64,
  0x8bab8eefb6409c1au64, 0x1ad089b6c2f7548eu64,
  0xae9672aba3d0c320u64, 0xa184ac2473b529b1u64,
  0xda3c0f568cc4f3e8u64, 0xc9e5d72d90a2741eu64,
  0x8865899617fb1871u64, 0x7e2fa67c7a658892u64,
  0xaa7eebfb9df9de8du64, 0xddbb901b98feeab7u64,
  0xd51ea6fa85785631u64, 0x552a74227f3ea565u64,
  0x8533285c936b35deu64, 0xd53a88958f87275fu64,
  0xa67ff273b8460356u64, 0x8a892abaf368f137u64,
  0xd01fef10a657842cu64, 0x2d2b7569b0432d85u64,
  0x8213f56a67f6b29bu64, 0x9c3b29620e29fc73u64,
  0xa298f2c501f45f42u64, 0x8349f3ba91b47b8fu64,
  0xcb3f2f7642717713u64, 0x241c70a936219a73u64,
  0xfe0efb53d30dd4d7u64, 0xed238cd383aa0110u64,
  0x9ec95d1463e8a506u64, 0xf4363804324a40aau64,
  0xc67bb4597ce2ce48u64, 0xb143c6053edcd0d5u64,
  0xf81aa16fdc1b81dau64, 0xdd94b7868e94050au64,
  0x9b10a4e5e9913128u64, 0xca7cf2b4191c8326u64,
  0xc1d4ce1f63f57d72u64, 0xfd1c2f611f63a3f0u64,
  0xf24a01a73cf2dccfu64, 0xbc633b39673c8cecu64,
  0x976e41088617ca01u64, 0xd5be0503e085d813u64,
  0xbd49d14aa79dbc82u64, 0x4b2d8644d8a74e18u64,
  0xec9c459d51852ba2u64, 0xddf8e7d60ed1219eu64,
  0x93e1ab8252f33b45u64, 0xcabb90e5c942b503u64,
  0xb8da1662e7b00a17u64, 0x3d6a751f3b936243u64,
  0xe7109bfba19c0c9du64, 0x0cc512670a783ad4u64,
  0x906a617d450187e2u64, 0x27fb2b80668b24c5u64,
  0xb484f9dc9641e9dau64, 0xb1f9f660802dedf6u64,
  0xe1a63853bbd26451u64, 0x5e7873f8a0396973u64,
  0x8d07e33455637eb2u64, 0xdb0b487b6423e1e8u64,
  0xb049dc016abc5e5fu64, 0x91ce1a9a3d2cda62u64,
  0xdc5c5301c56b75f7u64, 0x7641a140cc7810fbu64,
  0x89b9b3e11b6329bau64, 0xa9e904c87fcb0a9du64,
  0xac2820d9623bf429u64, 0x546345fa9fbdcd44u64,
  0xd732290fbacaf133u64, 0xa97c177947ad4095u64,
  0x867f59a9d4bed6c0u64, 0x49ed8eabcccc485du64,
  0xa81f301449ee8c70u64, 0x5c68f256bfff5a74u64,
  0xd226fc195c6a2f8cu64, 0x73832eec6fff3111u64,
  0x83585d8fd9c25db7u64, 0xc831fd53c5ff7eabu64,
  0xa42e74f3d032f525u64, 0xba3e7ca8b77f5e55u64,
  0xcd3a1230c43fb26fu64, 0x28ce1bd2e55f35ebu64,
  0x80444b5e7aa7cf85u64, 0x7980d163cf5b81b3u64,
  0xa0555e361951c366u64, 0xd7e105bcc332621fu64,
  0xc86ab5c39fa63440u64, 0x8dd9472bf3fefaa7u64,
  0xfa856334878fc150u64, 0xb14f98f6f0feb951u64,
  0x9c935e00d4b9d8d2u64, 0x6ed1bf9a569f33d3u64,
  0xc3b8358109e84f07u64, 0x0a862f80ec4700c8u64,
  0xf4a642e14c6262c8u64, 0xcd27bb612758c0fau64,
  0x98e7e9cccfbd7dbdu64, 0x8038d51cb897789cu64,
  0xbf21e44003acdd2cu64, 0xe0470a63e6bd56c3u64,
  0xeeea5d5004981478u64, 0x1858ccfce06cac74u64,
  0x95527a5202df0ccbu64, 0x0f37801e0c43ebc8u64,
  0xbaa718e68396cffdu64, 0xd30560258f54e6bau64,
  0xe950df20247c83fdu64, 0x47c6b82ef32a2069u64,
  0x91d28b7416cdd27eu64, 0x4cdc331d57fa5441u64,
  0xb6472e511c81471du64, 0xe0133fe4adf8e952u64,
  0xe3d8f9e563a198e5u64, 0x58180fddd97723a6u64,
  0x8e679c2f5e44ff8fu64, 0x570f09eaa7ea7648u64,
];

// END GENERATED EISEL-LEMIRE TABLE

// floor(log2(10^q)) + 63, the magic-multiply approximation, exact over
// q in [-342, 308].
function _el_log2_pow10(q: i32): i32 {
  return (217706 * q >> 16) + 63;
}

// _el_compute returns the binary64 bits of w * 10^q: the 11-bit exponent field
// above the 52-bit stored mantissa.
function _el_compute(w: u64, q: i32): u64 {
  if (w == 0 as u64 || q < 0 - 342) {  // zero
    return 0 as u64;
  }
  if (q > 308) {  // +Inf
    return 9218868437227405312 as u64;
  }

  // The 128-bit product of the normalised significand with 5^q, taking the
  // low table word only when the leading bits are too close to call (the low
  // 9 bits all set).
  let lz: i32 = __clz64(w);
  let x: u64 = w << lz as u64;
  let at: i32 = (q + 342) * 2;
  let hi: u64 = __mulhi_u64(x, EL_POW5[at]);
  let lo: u64 = x * EL_POW5[at];
  if ((hi & 511 as u64) == 511 as u64) {
    let carry: u64 = __mulhi_u64(x, EL_POW5[at + 1]);
    lo = lo + carry;
    if (carry > lo) {
      hi = hi + 1 as u64;
    }
  }
  let upperbit: i32 = (hi >> 63 as u64) as i32;
  let shift: i32 = upperbit + 9;  // upperbit + 64 - 52 - 3
  let mant: u64 = hi >> shift as u64;
  let pow2: i32 = _el_log2_pow10(q) + upperbit - lz + 1023;

  if (pow2 <= 0) {
    // Subnormal, or below the smallest one.
    if (1 - pow2 >= 64) {
      return 0 as u64;
    }
    mant = mant >> (1 - pow2) as u64;
    mant = mant + (mant & 1 as u64);
    // A carry into bit 52 is the exponent field's 1: rounding up lifted the
    // value into the normal range.
    return mant >> 1 as u64;
  }

  // A tie can only occur where 5^q fits in one 64-bit word; there `lo <= 1`
  // means the bits dropped by the shift were all zero, so the value sits
  // exactly on a midpoint and round-half-to-even applies.
  if (lo <= 1 as u64 && q >= 0 - 4 && q <= 23 && (mant & 3 as u64) == 1 as u64) {
    if (mant << shift as u64 == hi) {
      mant = mant - (mant & 1 as u64);
    }
  }
  mant = mant + (mant & 1 as u64);
  mant = mant >> 1 as u64;
  if (mant >= 9007199254740992 as u64) {  // carried out of 53 bits
    mant = 4503599627370496 as u64;
    pow2 = pow2 + 1;
  }
  if (pow2 >= 2047) {  // overflowed to +Inf
    return 9218868437227405312 as u64;
  }
  return pow2 as u64 << 52 as u64 | mant & 4503599627370495 as u64;
}

// _el_parse is the fast path for `dig * 10^k`, with `dig` already stripped of
// leading and trailing zeros. Returns the bits when the answer is proven
// exact, or -1 when the caller must use __decimal_to_f64.
function _el_parse(dig: string, k: i32): i64 {
  let nd: i32 = dig.len();
  if (nd == 0) {  // value is zero
    return 0;
  }
  let take: i32 = nd;
  if (take > 19) {
    take = 19;
  }
  let w: u64 = 0 as u64;
  let i: i32 = 0;
  while (i < take) {
    w = w * (10 as u64) + (dig[i] as i32 - 48) as u64;
    i = i + 1;
  }
  // Dropped digits only shift the exponent; they never change `w`.
  let q: i32 = k + (nd - take);
  let r: u64 = _el_compute(w, q);
  // Truncated: the true significand lies in (w, w+1). If both ends give the
  // same double, every value between them does too.
  if (nd > take && _el_compute(w + 1 as u64, q) != r) {
    return 0 - 1;
  }
  return r as i64;
}

// Decimal binary64 conversion. Grammar:
//   [+-]<digits>[.<digits>][(e|E)[+-]?<digits>]
// At least one of integer or fraction digits required; trailing
// garbage rejected. The result is the correctly-rounded
// (round-half-to-even) nearest f64 — bit-exact with strconv (#5566).
//
// Up to 19 significant digits are gathered into a u64 as they are scanned
// and go straight to Eisel-Lemire, allocating nothing. A number with a
// nonzero digit past the 19th takes `_float_exact`.
pub(package) function _float64(s: str): (f64, i32) {
  let n: i32 = s.len();
  if (n == 0) {
    return (0.0, -1);
  }
  let i: i32 = 0;
  let neg: boolean = false;
  if (s[0] == 45 || s[0] == 43) {
    neg = s[0] == 45;
    i = 1;
  }
  let special: i32 = _special_value(s, i);
  if (special == 1) {
    return (_signed64(f64_from_bits(9221120237041090560), neg), SUCCESS);
  }
  if (special == 2) {
    return (_signed64(f64_from_bits(9218868437227405312), neg), SUCCESS);
  }
  // w holds the significant digits read so far, kept of them; a digit past
  // the 19th only moves the exponent (shift), unless it is nonzero (lost).
  let w: u64 = 0 as u64;
  let kept: i32 = 0;
  let shift: i32 = 0;
  let lost: boolean = false;
  let digits: i32 = 0;
  let fraclen: i32 = 0;
  let b: i32 = 0;
  let in_frac: boolean = false;
  while (i < n) {
    b = s[i] as i32;
    if (b == 46 && !in_frac) {  // '.'
      in_frac = true;
      i = i + 1;
      continue;
    }
    if (b < 48 || b > 57) {
      break;
    }
    digits = digits + 1;
    if (in_frac) {
      fraclen = fraclen + 1;
    }
    if (kept < 19) {
      if (kept > 0 || b != 48) {
        w = w * (10 as u64) + (b - 48) as u64;
        kept = kept + 1;
      }
    } else {
      shift = shift + 1;
      lost = lost || b != 48;
    }
    i = i + 1;
  }
  if (digits == 0) {
    return (0.0, i);
  }
  let eexp: i32 = 0;
  if (i < n && (s[i] == 101 || s[i] == 69)) {  // 'e' / 'E'
    i = i + 1;
    let exp_neg: boolean = false;
    if (i < n && (s[i] == 43 || s[i] == 45)) {
      exp_neg = s[i] == 45;
      i = i + 1;
    }
    let exp_start: i32 = i;
    while (i < n) {
      b = s[i] as i32;
      if (b < 48 || b > 57) {
        break;
      }
      if (eexp < 1000000) {
        eexp = eexp * 10 + (b - 48);
      }
      i = i + 1;
    }
    if (i == exp_start) {
      return (0.0, i);
    }
    if (exp_neg) {
      eexp = 0 - eexp;
    }
  }
  if (i < n) {  // trailing garbage
    return (0.0, i);
  }
  if (lost) {
    return _float_exact(s);
  }
  let v: f64 = 0.0;
  if (kept > 0) {
    // Trailing zeros go to the exponent, giving Eisel-Lemire the same
    // (w, q) as the exact path's stripped digits.
    let q: i32 = eexp - fraclen + shift;
    while (w % (10 as u64) == 0 as u64) {
      w = w / (10 as u64);
      q = q + 1;
    }
    v = f64_from_bits(_el_compute(w, q) as i64);
  }
  if (f64_bits(v) >= 9218868437227405312) {
    return (0.0, -2);
  }
  return (_signed64(v, neg), SUCCESS);
}

// Collect the full decimal significand for exact midpoint comparisons.

function _decimal_parts(s: str): Result[DecimalParts, ParseNumberError] {
  let n: i32 = s.len();
  if (n == 0) {
    return Err(EmptyInput);
  }

  let i: i32 = 0;
  let neg: boolean = false;
  if (s[0] == 45 || s[0] == 43) {
    neg = s[0] == 45;
    i = 1;
  }
  if (i == n) {
    return Err(InvalidDigit(i));
  }

  // Collect every integer + fraction digit into one buffer; the base-10
  // exponent tracks the decimal point and the literal exponent.
  let db: u8[] = [];
  let fraclen: i32 = 0;
  let saw_int: boolean = false;
  let saw_frac: boolean = false;
  // Single `b` reused across the integer / fraction / exponent inner
  // loops — declaring the same name in multiple inner scopes triggers a
  // duplicate-local collision in the wasm emitter.
  let b: i32 = 0;

  while (i < n) {
    b = s[i] as i32;
    if (b < 48 || b > 57) {
      break;
    }
    db = db.append(b as u8);
    saw_int = true;
    i = i + 1;
  }

  if (i < n && s[i] == 46) {  // '.'
    i = i + 1;
    while (i < n) {
      b = s[i] as i32;
      if (b < 48 || b > 57) {
        break;
      }
      db = db.append(b as u8);
      fraclen = fraclen + 1;
      saw_frac = true;
      i = i + 1;
    }
  }

  if (!saw_int && !saw_frac) {
    return Err(InvalidDigit(i));
  }

  let eexp: i32 = 0;
  if (i < n && (s[i] == 101 || s[i] == 69)) {  // 'e' / 'E'
    i = i + 1;
    let exp_neg: boolean = false;
    if (i < n && s[i] == 43) {  // '+'
      i = i + 1;
    } else if (i < n && s[i] == 45) {  // '-'
      exp_neg = true;
      i = i + 1;
    }
    let exp: i32 = 0;
    let saw_exp: boolean = false;
    while (i < n) {
      b = s[i] as i32;
      if (b < 48 || b > 57) {
        break;
      }
      if (exp < 1000000) {
        exp = exp * 10 + (b - 48);
      }
      saw_exp = true;
      i = i + 1;
    }
    if (!saw_exp) {
      return Err(InvalidDigit(i));
    }
    if (exp_neg) {
      eexp = 0 - exp;
    } else {
      eexp = exp;
    }
  }

  if (i < n) {  // trailing garbage
    return Err(InvalidDigit(i));
  }

  // value = (db as integer) * 10^(eexp - fraclen). Strip leading zeros
  // (no value change) and trailing zeros (each bumps the exponent) so
  // the bignum stays small and `dig` is canonical.
  let m: i32 = db.len();
  let lo: i32 = 0;
  while (lo < m && db[lo] == 48) {
    lo = lo + 1;
  }
  let hi: i32 = m - 1;
  while (hi >= lo && db[hi] == 48) {
    hi = hi - 1;
  }
  let k: i32 = eexp - fraclen + (m - 1 - hi);
  let dig: string = "";
  if (hi >= lo) {
    let out: u8[] = [];
    let j: i32 = lo;
    while (j <= hi) {
      out = out.append(db[j]);
      j = j + 1;
    }
    dig = string_from_bytes_unchecked(out);
  }

  return Ok(DecimalParts { digits: dig, exponent: k, negative: neg });
}
```
