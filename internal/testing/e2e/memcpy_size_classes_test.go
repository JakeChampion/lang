package e2e

import "testing"

const memcpySizeClassesProgram = `
function check(n: i32, soff: i32, doff: i32): i32 {
  unsafe {
      let src: usize = __alloc(160);
      let dst: usize = __alloc(160);
      let i: i32 = 0;
      while (i < 160) {
          __store_u8(src + i as usize, (i + 1) % 256);
          __store_u8(dst + i as usize, 0xEE);
          i = i + 1;
      }
      __memcpy(dst + doff as usize, src + soff as usize, n);
      let bad: i32 = 0;
      i = 0;
      while (i < 160) {
          let want: i32 = 0xEE;
          if (i >= doff && i < doff + n) { want = __load_u8(src + (soff + i - doff) as usize); }
          if (__load_u8(dst + i as usize) != want) { bad = bad + 1; }
          i = i + 1;
      }
      __free(src, 160);
      __free(dst, 160);
      return bad;
  }
}

function main(): i32 {
    let bad: i32 = 0;
    let n: i32 = 0;
    while (n <= 136) {
        bad = bad + check(n, 0, 0);
        bad = bad + check(n, 1, 0);
        bad = bad + check(n, 0, 3);
        bad = bad + check(n, 7, 5);
        n = n + 1;
    }
    if (bad > 255) { return 255; }
    return bad;
}
`

const bulkMemoryProgram = `function main(): i32 {
  unsafe {
      let base: usize = __alloc(16);
      let i: i32 = 0;
      while (i < 16) { __store_u8(base + i as usize, 65 + i); i = i + 1; }
      __memset(base + 8, 0, 8);
      if (__load_u8(base) != 65) { return 1; }
      if (__load_u8(base + 7) != 72) { return 2; }
      if (__load_u8(base + 8) != 0) { return 3; }
      if (__load_u8(base + 15) != 0) { return 4; }
      __memcpy(base + 8, base, 4);
      if (__load_u8(base + 8) != 65) { return 5; }
      if (__load_u8(base + 9) != 66) { return 6; }
      if (__load_u8(base + 10) != 67) { return 7; }
      if (__load_u8(base + 11) != 68) { return 8; }
      if (__load_u8(base + 12) != 0) { return 9; }
      __free(base, 16);
      return 0;
  }
}`

const memsetSizeClassesProgram = `
function check(n: i32, off: i32, byte: i32): i32 {
  unsafe {
      let p: usize = __alloc(200);
      let i: i32 = 0;
      while (i < 200) {
          __store_u8(p + i as usize, 0xEE);
          i = i + 1;
      }
      __memset(p + off as usize, byte, n);
      let bad: i32 = 0;
      i = 0;
      while (i < 200) {
          let want: i32 = 0xEE;
          if (i >= off && i < off + n) { want = byte; }
          if (__load_u8(p + i as usize) != want) { bad = bad + 1; }
          i = i + 1;
      }
      __free(p, 200);
      return bad;
  }
}

function main(): i32 {
    let bad: i32 = 0;
    let n: i32 = 0;
    while (n <= 150) {
        bad = bad + check(n, 0, 0);
        bad = bad + check(n, 1, 255);
        bad = bad + check(n, 7, 128);
        bad = bad + check(n, 33, 65);
        n = n + 1;
    }
    if (bad > 255) { return 255; }
    return bad;
}
`

func TestWASMMemsetSizeClasses(t *testing.T) {
	if code := runWasm(t, memsetSizeClassesProgram); code != 0 {
		t.Errorf("wasm __memset size classes: main = %d, want 0 (bad byte count)", code)
	}
}

func TestX86_64MemsetSizeClasses(t *testing.T) {
	if _, code := compileAndRunX86_64(t, memsetSizeClassesProgram); code != 0 {
		t.Errorf("x86-64 __memset size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestArm64MemsetSizeClasses(t *testing.T) {
	if _, code := compileAndRunArm64(t, memsetSizeClassesProgram); code != 0 {
		t.Errorf("arm64 __memset size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestWASMMemcpySizeClasses(t *testing.T) {
	if code := runWasm(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("wasm __memcpy size classes: main = %d, want 0 (bad byte count)", code)
	}
}

func TestX86_64MemcpySizeClasses(t *testing.T) {
	if _, code := compileAndRunX86_64(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("x86-64 __memcpy size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestArm64MemcpySizeClasses(t *testing.T) {
	if _, code := compileAndRunArm64(t, memcpySizeClassesProgram); code != 0 {
		t.Errorf("arm64 __memcpy size classes: exit = %d, want 0 (bad byte count)", code)
	}
}

func TestWASMBulkMemoryPrimitives(t *testing.T) {
	if code := runWasm(t, bulkMemoryProgram); code != 0 {
		t.Errorf("wasm __memset / __memcpy: main = %d, want 0 (the code names the byte)", code)
	}
}

func TestX86_64BulkMemoryPrimitives(t *testing.T) {
	if _, code := compileAndRunX86_64(t, bulkMemoryProgram); code != 0 {
		t.Errorf("x86-64 __memset / __memcpy: exit = %d, want 0 (the code names the byte)", code)
	}
}
