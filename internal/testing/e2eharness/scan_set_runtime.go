package e2eharness

// ScanSetRuntimeSource checks __scan_set and __scan_set_bytes with sets that
// are not constants against a scalar scan. The sets are picked at run time from
// several, or built at run time. It exits 0 when every scan agrees.
const ScanSetRuntimeSource = `function member(b: u8, set: u8[]): boolean {
  return b as i32 < set.len() && set[b as i32] != 0u8;
}

function slow_scan(s: u8[], from: i32, set: u8[]): i32 {
  let i = from;
  if (i < 0) {
    i = 0;
  }
  while (i < s.len()) {
    if (member(s[i], set)) {
      return i;
    }
    i += 1;
  }
  return s.len();
}

fip function scan_bytes(s: u8[], from: i32, set: u8[]): i32 {
  return __scan_set_bytes(s, from, set);
}

function scan_text(s: u8[], from: i32, set: u8[]): i32 {
  let text = unsafe { string_from_bytes_unchecked(s) };
  return __scan_set(text, from, set);
}

// Both scans of s from from against the scalar answer.
function agrees(s: u8[], from: i32, set: u8[]): boolean {
  let want = slow_scan(s, from, set);
  return scan_text(s, from, set) == want && scan_bytes(s, from, set) == want;
}

// A set of n entries. The members get the entries 1, 0x80 and 0xff in turn.
function set_of(members: i32[], n: i32): u8[] {
  let set = __alloc_u8(n);
  let k = 0;
  for m in members {
    if (m < n) {
      set = set.with(m, [1u8, 128u8, 255u8][k % 3]);
    }
    k += 1;
  }
  return set;
}

// A byte that is not a member of set, picked from seed.
function plain(seed: i32, set: u8[]): u8 {
  let b = seed & 255;
  let tries = 0;
  while (member(b as u8, set) && tries < 256) {
    b = (b + 1) & 255;
    tries += 1;
  }
  return b as u8;
}

function haystack(n: i32, seed: i32, set: u8[]): u8[] {
  let s = __alloc_u8(n);
  for i in 0..n {
    s = s.with(i, plain(seed + i * 37, set));
  }
  return s;
}

// A member at each position of strings up to 100 bytes long, from several
// starts: the scalar head, each lane of a block, and the bytes after the last
// full block.
function positions(set: u8[], m: u8): i32 {
  for n in 0..100 {
    let s = haystack(n, n, set);
    if (!agrees(s, 0, set)) {
      return 1;
    }
    for at in 0..n {
      let t = s.with(at, m);
      for from in [0 - 3, 0, 1, at - 1, at, at + 1, 17, n, n + 5] {
        if (!agrees(t, from, set)) {
          return 2;
        }
      }
    }
  }
  return 0;
}

function long_scans(set: u8[], m: u8): i32 {
  let s = haystack(4099, 7, set);
  for at in [0, 15, 16, 31, 32, 33, 47, 48, 63, 64, 1000, 4000, 4080, 4083, 4084, 4095, 4098] {
    let t = s.with(at, m);
    for from in [0, 1, 5, 16, 30, 999, 4000] {
      if (!agrees(t, from, set)) {
        return 3;
      }
    }
  }
  return 0;
}

function dense(set: u8[], seed: i32): i32 {
  let s = __alloc_u8(600);
  let x = seed;
  for i in 0..600 {
    x = (x * 1103 + 12345) & 65535;
    s = s.with(i, (x >> 4) as u8);
  }
  for from in 0..600 {
    if (!agrees(s, from, set)) {
      return 4;
    }
  }
  return 0;
}

function check(set: u8[], seed: i32): i32 {
  for m in [0, 128, 255, 10, 92] {
    if (member(m as u8, set)) {
      let r = positions(set, m as u8);
      if (r != 0) {
        return r;
      }
      r = long_scans(set, m as u8);
      if (r != 0) {
        return r;
      }
    }
  }
  if (!agrees(__alloc_u8(0), 0, set) || !agrees(haystack(300, seed, set), 0, set)) {
    return 5;
  }
  return dense(set, seed);
}

function main(): i32 {
  let all = __alloc_u8(256);
  for b in 0..256 {
    all = all.with(b, 1u8);
  }
  let sets: u8[][] = [
    set_of([0, 128, 255, 10, 92], 256),
    set_of([0, 10, 92, 128, 255, 13, 34, 60, 62, 38], 300),
    set_of([0, 10, 92], 93),
    set_of([0, 128, 255], 256),
    set_of([10], 14),
    all,
  ];
  let k = 0;
  while (k < sets.len()) {
    let r = check(sets[(k * 5 + 1) % sets.len()], k);
    if (r != 0) {
      return 10 * k + r;
    }
    k += 1;
  }
  let built = __alloc_u8(256);
  for b in 0..256 {
    if (b % 7 == 3 || b >= 250 || b == 0) {
      built = built.with(b, (b + 1) as u8);
    }
  }
  let r = check(built, 99);
  if (r != 0) {
    return 100 + r;
  }
  let none = __alloc_u8(256);
  if (!agrees(haystack(100, 1, none), 0, none) || !agrees(haystack(100, 1, __alloc_u8(0)), 3, __alloc_u8(0))) {
    return 120;
  }
  return 0;
}
`
