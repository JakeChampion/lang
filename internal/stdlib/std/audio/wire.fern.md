# Audio byte fields

These helpers read fields after the codec validates their byte ranges.
Do not call a reader with an offset outside the input.

```fern
<<*>>=
pub function be(input: [u8], at: i32, bytes: i32): u64 {
  let value = 0u64;
  for i in 0..bytes {
    value = value << 8 | input[at + i] as u64;
  }
  return value;
}

pub function le(input: [u8], at: i32, bytes: i32): u64 {
  let value = 0u64;
  for i in 0..bytes {
    value = value | input[at + i] as u64 << (i * 8) as u64;
  }
  return value;
}

pub function put_be(own out: u8[], value: u64, bytes: i32): u8[] {
  for i in 0..bytes {
    out = out.append((value >> ((bytes - 1 - i) * 8) as u64) as u8);
  }
  return out;
}

pub function put_le(own out: u8[], value: u64, bytes: i32): u8[] {
  for i in 0..bytes {
    out = out.append((value >> (i * 8) as u64) as u8);
  }
  return out;
}

pub function tag(input: [u8], at: i32, text: string): boolean {
  let bytes = text.as_bytes();
  if (at < 0 || at > input.len() || bytes.len() > input.len() - at) {
    return false;
  }
  for i in 0..bytes.len() {
    if (input[at + i] != bytes[i]) {
      return false;
    }
  }
  return true;
}
```
