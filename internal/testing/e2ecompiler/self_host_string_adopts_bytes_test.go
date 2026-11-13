package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// exact fills an array whose capacity is its length. pushed grows one by
// append, so its capacity is larger than its length.
const stringAdoptsBytesProgram = `@noinline function exact(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    b = b.with(i, (97 + i % 26) as u8);
    i = i + 1;
  }
  return b;
}

@noinline function pushed(n: i32): u8[] {
  let b: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    b = b.append((65 + i % 26) as u8);
    i = i + 1;
  }
  return b;
}

@noinline function unique_exact(): string {
  let b: u8[] = exact(20);
  let before: i64 = __heap_alloc_count();
  let s: string = unsafe { string_from_bytes_unchecked(b) };
  return (__heap_alloc_count() - before).to_string() + " " + s;
}

@noinline function unique_spare(): string {
  let b: u8[] = pushed(13);
  let before: i64 = __heap_alloc_count();
  let s: string = unsafe { string_from_bytes_unchecked(b) };
  return (__heap_alloc_count() - before).to_string() + " " + s;
}

@noinline function shared(): string {
  let b: u8[] = exact(5);
  let before: i64 = __heap_alloc_count();
  let s: string = unsafe { string_from_bytes_unchecked(b) };
  let n: i64 = __heap_alloc_count() - before;
  b = b.with(0, 90 as u8);
  return n.to_string() + " " + s + " " + b.len().to_string() + " " + unsafe { string_from_bytes_unchecked(b) };
}

@noinline function literal(): string {
  let before: i64 = __heap_alloc_count();
  let s: string = unsafe { string_from_bytes_unchecked([104, 105]) };
  let n: i64 = __heap_alloc_count() - before;
  let b: u8[] = [33, 63];
  return n.to_string() + " " + s + unsafe { string_from_bytes_unchecked(b) } + unsafe { string_from_bytes_unchecked(b) };
}

@noinline function empty(): string {
  let b: u8[] = [];
  let c: u8[] = __alloc_u8(0);
  let d: u8[] = pushed(9).truncate(0);
  let s: string = unsafe { string_from_bytes_unchecked(b) } + unsafe { string_from_bytes_unchecked(c) } + unsafe { string_from_bytes_unchecked(d) };
  return "[" + s + "]" + s.len().to_string();
}

@noinline function large(): string {
  let b: u8[] = exact(1048576);
  let before: i64 = __heap_bump_bytes();
  let s: string = unsafe { string_from_bytes_unchecked(b) };
  let adopted: boolean = __heap_bump_bytes() - before < 4096;
  let c: u8[] = exact(1048576);
  before = __heap_bump_bytes();
  let t: string = unsafe { string_from_bytes_unchecked(c) };
  let copied: boolean = __heap_bump_bytes() - before >= 1048576;
  return adopted.to_string() + " " + copied.to_string() + " " + s.len().to_string() + " " + c.len().to_string() + " " + (s == t).to_string();
}

@noinline function appended(): string {
  let s: string = unsafe { string_from_bytes_unchecked(pushed(13)) };
  let i: i32 = 0;
  while (i < 40) {
    s += "x";
    i = i + 1;
  }
  return s;
}

@noinline function kept(): string {
  let xs: string[] = [];
  let i: i32 = 0;
  while (i < 6) {
    let s: string = unsafe { string_from_bytes_unchecked(pushed(i * 5)) };
    xs = xs.append(s);
    xs = xs.append(s);
    i = i + 1;
  }
  let out: string = "";
  for x in xs {
    out = out + x.len().to_string() + ",";
  }
  return out;
}

function main(): i32 {
  print(unique_exact());
  print(unique_spare());
  print(shared());
  print(literal());
  print(empty());
  print(large());
  print(appended());
  print(kept());
  return 0;
}
`

// TestSelfHostStringAdoptsUniqueBytes checks that string_from_bytes_unchecked
// takes over a unique array with no new allocation, and copies a shared or
// immortal one. The shared array stays usable after the copy. The census must
// balance on each target and under the sanitizer.
func TestSelfHostStringAdoptsUniqueBytes(t *testing.T) {
	want := "0 abcdefghijklmnopqrst\n" +
		"0 ABCDEFGHIJKLM\n" +
		"1 abcde 5 Zbcde\n" +
		"1 hi!?!?\n" +
		"[]0\n" +
		"true true 1048576 1048576 true\n" +
		"ABCDEFGHIJKLM" + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n" +
		"0,0,5,5,10,10,15,15,20,20,25,25,\n"
	runSemanticProgram(t, "stringadopt", stringAdoptsBytesProgram,
		[]string{"exact", "pushed", "unique_exact", "unique_spare", "shared", "literal", "empty", "large", "appended", "kept"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

const stringAdoptsFileBytesSrc = `function same(path: string): i32 {
  if let Ok(text) = read_file(path) {
    if let Ok(b) = read_file_bytes(path) {
      let before: i64 = __heap_alloc_count();
      let s: string = unsafe { string_from_bytes_unchecked(b) };
      if (__heap_alloc_count() != before) {
        return 1;
      }
      if (s != text) {
        return 2;
      }
      return 0;
    }
    return 3;
  }
  return 4;
}

function main(): i32 {
  let r = same(%q);
  if (r != 0) {
    return 10 + r;
  }
  r = same(%q);
  if (r != 0) {
    return 20 + r;
  }
  r = same("/proc/self/mounts");
  if (r != 0) {
    return 30 + r;
  }
  return 0;
}
`

// TestSelfHostStringAdoptsFileBytes checks that a string takes over the array
// read_file_bytes returns, both for a file whose size is known and for a
// procfs file, whose array has spare capacity.
func TestSelfHostStringAdoptsFileBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no procfs on " + runtime.GOOS)
	}
	dir := t.TempDir()
	data := make([]byte, 70001)
	for i := range data {
		data[i] = byte(32 + i%90)
	}
	big := filepath.Join(dir, "big")
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(big, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(fmt.Sprintf(stringAdoptsFileBytesSrc, big, empty)), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
