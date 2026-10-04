package e2e

import "testing"

// A read of a `const` array allocates nothing (#11471).
//
// constfold substitutes a copy of the literal for every use of a constant, and
// each copy lowered to a fresh heap array: 100 reads of a four-element u8[]
// constant cost 100 allocations, and std/http's byte-set scans paid it per
// header. A constant is now one static array under the immortal rc, so a read
// is its address, and every write path has to see it as shared: a push grows
// into a fresh buffer, a `.with` copies, and the constant reads the same
// afterwards.
//
// Built by the Go compiler: the self-host places constant literals in its
// static constant pool and never allocated them.
func TestConstArrayReadsDoNotAllocate(t *testing.T) {
	shapes := []struct{ name, src string }{
		{"reads", `
const SET: u8[] = [1u8, 0u8, 1u8, 1u8];
const WIDE: i32[] = [10, 0 - 20, 30, 2147483647];
const STOP: u8[] = [0u8, 0u8, 1u8];

@noinline
function pick(i: i32): u8 { return SET[i % 4]; }

@noinline
function pick_wide(i: i32): i32 { return WIDE[i % 4]; }

@noinline
function scan(buf: u8[]): i32 { return __scan_set_bytes(buf, 0, STOP); }

function main(): i32 {
  let buf: u8[] = [0u8, 1u8, 2u8, 1u8];
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let narrow: i32 = 0;
  let wide: i64 = 0;
  let scans: i32 = 0;
  while (i < 100) {
    narrow = narrow + pick(i) as i32;
    wide = wide + pick_wide(i) as i64;
    scans = scans + scan(buf);
    i = i + 1;
  }
  let reads: i64 = __heap_alloc_count() - before;
  if (narrow != 75 || wide != 53687091675 || scans != 200) {
    eprint("wrong value read from a constant");
    return 1;
  }
  if (reads != 0) {
    eprint("reads of a constant allocated");
    return 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  return 0;
}`},
		{"writes_copy", `
const SET: u8[] = [1u8, 0u8, 1u8, 1u8];
const WIDE: i32[] = [10, 0 - 20, 30, 40];

@noinline
function grown(): u8[] { return SET.append(9u8); }

@noinline
function changed(): i32[] { return WIDE.with(0, 5); }

@noinline
function bump(own xs: i32[]): i32[] {
  let i: i32 = 0;
  while (i < xs.len()) {
    xs = xs.with(i, xs[i] + 1);
    i = i + 1;
  }
  return xs;
}

@noinline
function intact(): boolean {
  return SET.len() == 4 && SET[0] == 1u8 && SET[1] == 0u8 && SET[3] == 1u8
    && WIDE.len() == 4 && WIDE[0] == 10 && WIDE[1] == 0 - 20 && WIDE[3] == 40;
}

function main(): i32 {
  let bad: i32 = 0;
  let round: i32 = 0;
  while (round < 3) {
    let g: u8[] = grown();
    if (g.len() != 5 || g[4] != 9u8 || g[0] != 1u8) { bad = bad + 1; }
    let c: i32[] = changed();
    if (c[0] != 5 || c[1] != 0 - 20) { bad = bad + 1; }
    let b: i32[] = bump(WIDE);
    if (b[0] != 11 || b[3] != 41) { bad = bad + 1; }
    let acc: u8[] = SET;
    let i: i32 = 0;
    while (i < 6) {
      acc = acc.append(i as u8);
      acc = acc.with(0, 7u8);
      i = i + 1;
    }
    if (acc.len() != 10 || acc[0] != 7u8 || acc[9] != 5u8) { bad = bad + 1; }
    if (!intact()) { bad = bad + 1; }
    round = round + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (bad != 0) {
    eprint("a write through a constant was not copied");
    return 1;
  }
  return 0;
}`},
	}

	checkGoCompilerCensusOnEveryTarget(t, shapes,
		"a copy made from a constant array leaks (#11471)")
}
