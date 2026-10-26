package e2e

import "testing"

// countBelowSrc checks __count_below_u64 against a scalar count for every
// length from 0 to 69, with words at 0, near 2^63 and at the maximum, so that
// each vector width and each tail is reached.
const countBelowSrc = `function slow(ws: u64[], w: u64): i32 {
  let c: i32 = 0;
  for x in ws {
    if (x < w) {
      c = c + 1;
    }
  }
  return c;
}

function word(i: i32, seed: i32): u64 {
  let x: u64 = (i as u64) * 11400714819323198485u64 + (seed as u64) * 40503u64;
  let k: i32 = (i + seed) % 7;
  if (k == 0) {
    return 0u64;
  }
  if (k == 1) {
    return 18446744073709551615u64;
  }
  if (k == 2) {
    return 9223372036854775808u64 + (i as u64);
  }
  if (k == 3) {
    return 9223372036854775807u64 - (i as u64);
  }
  return x;
}

function main(): i32 {
  let bad: i32 = 0;
  let checks: i32 = 0;
  let n: i32 = 0;
  while (n < 70) {
    let ws: u64[] = [];
    let i: i32 = 0;
    while (i < n) {
      ws = ws.append(word(i, n));
      i = i + 1;
    }
    let probes: u64[] = [0u64, 1u64, 18446744073709551615u64, 9223372036854775808u64, 9223372036854775807u64];
    for x in ws {
      probes = probes.append(x);
      probes = probes.append(x + 1u64);
    }
    for p in probes {
      checks = checks + 1;
      if (__count_below_u64(ws, p) != slow(ws, p)) {
        bad = bad + 1;
      }
    }
    n = n + 1;
  }
  if (checks != 5180) {
    return 100;
  }
  return 42 + bad;
}
`

func TestCountBelowU64(t *testing.T) {
	runs := map[string]func(*testing.T, string) int{
		"interp":           runInterpExit,
		"self-host-interp": func(t *testing.T, src string) int { _, code := runSelfHostInterp(t, src); return code },
		"x86-64":           func(t *testing.T, src string) int { _, code := compileAndRunX86_64(t, src); return code },
		"arm64":            func(t *testing.T, src string) int { _, code := compileAndRunArm64(t, src); return code },
		"wasm":             compileAndRunWasmbinMain,
	}
	for name, run := range runs {
		t.Run(name, func(t *testing.T) {
			if got := run(t, countBelowSrc); got != 42 {
				t.Fatalf("%s: got %d, want 42 (42 + the number of wrong counts, 100 when a check was skipped)", name, got)
			}
		})
	}
}
