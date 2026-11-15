package e2e

import "testing"

// scanMasksSrc checks __scan_masks and __scan_masks_bytes against a scan
// written in Fern. It tries one, two and three probes at offsets up to 40, on
// texts of each length from 0 to 99 and from each start, so that each vector
// block and each tail is reached. Masks with a bad count or too few bytes
// find nothing.
const scanMasksSrc = `function next(x: i32): i32 {
  return (x * 1103515245 + 12345) & 2147483647;
}

function slow(text: string, from: i32, m: u8[]): i32 {
  let k = m[0] as i32;
  let reach = 0;
  for j in 0..k {
    if (m[1 + j] as i32 > reach) {
      reach = m[1 + j] as i32;
    }
  }
  let at = if (from < 0) { 0 } else { from };
  while (at + reach < text.len()) {
    let acc = 255;
    for j in 0..k {
      let c = text[at + m[1 + j] as i32] as i32;
      acc = acc & (m[4 + 32 * j + (c & 15)] as i32) & (m[20 + 32 * j + (c >> 4)] as i32);
    }
    if (acc != 0) {
      return at;
    }
    at += 1;
  }
  return -1;
}

function masks(k: i32, seed: i32): u8[] {
  let out: u8[] = [k as u8];
  let x = seed;
  for j in 0..3 {
    x = next(x);
    out = out.append((x % 41) as u8);
  }
  for j in 0..k {
    for t in 0..32 {
      x = next(x);
      let v = if (x % 5 == 0) { x >> 8 & 255 } else { 0 };
      out = out.append(v as u8);
    }
  }
  return out;
}

function bytes_of(n: i32, seed: i32): u8[] {
  let b: u8[] = [];
  let x = seed;
  for i in 0..n {
    x = next(x);
    b = b.append((x >> 7 & 255) as u8);
  }
  return b;
}

// Masks that accept one byte at each probe, as the masks of a literal do.
function exact(k: i32, offs: i32[], vals: i32[]): u8[] {
  let out: u8[] = [k as u8, offs[0] as u8, offs[1] as u8, offs[2] as u8];
  for j in 0..k {
    for t in 0..32 {
      let hit = t < 16 && t == (vals[j] & 15) || t >= 16 && t - 16 == vals[j] >> 4;
      out = out.append(if (hit) { 1u8 } else { 0u8 });
    }
  }
  return out;
}

// Plants the bytes of the probes at a random start in a text of n bytes, and checks
// each start around them.
function planted(n: i32, k: i32, seed: i32): i32 {
  let x = next(seed);
  let offs = [x % 23, (x >> 5) % 23, (x >> 10) % 23];
  let vals = [(x >> 3) & 255, (x >> 11) & 255, (x >> 19) & 255];
  let m = exact(k, offs, vals);
  let b = bytes_of(n, seed);
  let reach = 0;
  for j in 0..k {
    reach = if (offs[j] > reach) { offs[j] } else { reach };
  }
  let at = if (n > reach) { (x >> 4) % (n - reach) } else { 0 };
  if (n > reach) {
    for j in 0..k {
      b = b.with(at + offs[j], vals[j] as u8);
    }
  }
  let text = unsafe { string_from_bytes_unchecked(b) };
  let bad = 0;
  for from in [0, at - 33, at - 1, at, at + 1, n] {
    let want = slow(text, from, m);
    if (__scan_masks(text, from, m) != want || __scan_masks_bytes(b, from, m) != want) {
      bad += 1;
    }
  }
  return bad;
}

function main(): i32 {
  let bad = 0;
  let checks = 0;
  let found = 0;
  for n in 0..100 {
    let raw = bytes_of(n, n * 7 + 1);
    let text = unsafe { string_from_bytes_unchecked(raw) };
    for k in 1..4 {
      let m = masks(k, n * 13 + k);
      for from in -1..n + 2 {
        let got = __scan_masks(text, from, m);
        let want = slow(text, from, m);
        checks += 1;
        if (got >= 0) {
          found += 1;
        }
        if (got != want || __scan_masks_bytes(raw, from, m) != want) {
          bad += 1;
        }
      }
    }
  }
  let n = 0;
  while (n < 400) {
    for k in 1..4 {
      bad += planted(n, k, n * 31 + k);
    }
    n += 3;
  }
  let short: u8[] = [1, 0, 0, 0];
  let none: u8[] = [];
  let four = masks(3, 5).with(0, 4);
  let zero = masks(1, 5).with(0, 0);
  let probe_bytes = bytes_of(64, 3);
  let probe = unsafe { string_from_bytes_unchecked(probe_bytes) };
  if (__scan_masks(probe, 0, short) != -1 || __scan_masks(probe, 0, none) != -1 || __scan_masks(probe, 0, four) != -1 || __scan_masks(probe, 0, zero) != -1 || __scan_masks_bytes(probe_bytes, 0, short) != -1 || __scan_masks_bytes(probe_bytes, 0, zero) != -1) {
    bad += 1;
  }
  if (checks != 15750 || found < 1000) {
    return 100;
  }
  return 42 + bad;
}
`

func TestScanMasks(t *testing.T) {
	runs := map[string]func(*testing.T, string) int{
		"interp":           runInterpExit,
		"self-host-interp": func(t *testing.T, src string) int { _, code := runSelfHostInterp(t, src); return code },
		"x86-64":           func(t *testing.T, src string) int { _, code := compileAndRunX86_64(t, src); return code },
		"arm64":            func(t *testing.T, src string) int { _, code := compileAndRunArm64(t, src); return code },
		"wasm":             compileAndRunWasmbinMain,
	}
	for name, run := range runs {
		t.Run(name, func(t *testing.T) {
			if got := run(t, scanMasksSrc); got != 42 {
				t.Fatalf("%s: got %d, want 42 (42 + the number of wrong scans, 100 when the scans did not run)", name, got)
			}
		})
	}
}
