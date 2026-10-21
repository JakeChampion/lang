package e2ecompiler

import "testing"

// concatKernelProgram concatenates arrays of every scalar element width, a
// packed u8[] (one long enough to cross the copy's word loop), i32, i64, f64
// and boolean, with an empty operand on either side, and a string[], whose
// counted elements keep std/array's body. allocs counts what 100 byte concats
// onto a receiver the frame keeps allocate: one box each, where building the
// result an element at a time grew it seven times. chain and field concat
// onto a receiver the frame holds the only count of — a local reassigned, and
// a record's field rebuilt around the join — 200 times each, and report
// whether that cost fewer than 20 boxes: the receiver's block grows in place
// and doubles, as a chain of appends does. shared keeps a receiver another
// binding still reads.
const concatKernelProgram = `import "std/array";

struct S { buf: u8[], n: i32 }

@noinline function bytes(n: i32, base: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append((base + i) as u8);
    i = i + 1;
  }
  return out;
}

@noinline function sum_u8(xs: u8[]): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t * 31 + x as i32;
    t = t % 1000003;
  }
  return t;
}

@noinline function joined(): i32 {
  let a: u8[] = bytes(5, 10);
  let b: u8[] = bytes(1200, 3);
  let c: u8[] = a.concat(b);
  let none: u8[] = [];
  let d: u8[] = c.concat(none);
  let e: u8[] = none.concat(a);
  return c.len() * 1000000 + sum_u8(c) + d.len() + e.len() + a.len() + b.len();
}

@noinline function words(): string {
  let a: i32[] = [1, -2, 3];
  let b: i32[] = [40, 50];
  let c: i32[] = a.concat(b);
  let w: i64[] = [9000000000, -1].concat([7]);
  let f: f64[] = [1.5].concat([2.25, -3.0]);
  let z: boolean[] = [true].concat([false, true]);
  let s: string[] = ["x", "y"].concat(["z"]);
  let out: string = "";
  for x in c {
    out = out + x.to_string() + ",";
  }
  for x in w {
    out = out + x.to_string() + ",";
  }
  for x in f {
    out = out + x.to_string() + ",";
  }
  for x in z {
    out = out + x.to_string() + ",";
  }
  for x in s {
    out = out + x + ",";
  }
  return out + a.len().to_string() + b.len().to_string();
}

@noinline function allocs(): i32 {
  let a: u8[] = bytes(100, 0);
  let b: u8[] = bytes(1000, 1);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 100) {
    let c: u8[] = a.concat(b);
    t = t + c.len();
    i = i + 1;
  }
  return ((__heap_alloc_count() - before) as i32) * 1000000 + t;
}

@noinline function chain(): i32 {
  let chunk: u8[] = bytes(16, 7);
  let before: i64 = __heap_alloc_count();
  let x: u8[] = [];
  let i: i32 = 0;
  while (i < 200) {
    x = x.concat(chunk);
    i = i + 1;
  }
  let few: i32 = 0;
  if ((__heap_alloc_count() - before) as i32 < 20) {
    few = 1;
  }
  return few * 100000000 + x.len() * 1000 + sum_u8(x) % 1000;
}

@noinline function field(): i32 {
  let chunk: u8[] = bytes(4, 9);
  let before: i64 = __heap_alloc_count();
  let s: S = S { buf: [], n: 0 };
  let i: i32 = 0;
  while (i < 200) {
    s = S { ...s, buf: s.buf.concat(chunk), n: s.n + 1 };
    i = i + 1;
  }
  let few: i32 = 0;
  if ((__heap_alloc_count() - before) as i32 < 20) {
    few = 1;
  }
  return few * 100000000 + s.buf.len() * 1000 + s.n + (sum_u8(s.buf) % 100);
}

@noinline function shared(): i32 {
  let a: u8[] = bytes(8, 1);
  let keep: u8[] = a;
  let b: u8[] = a.concat(bytes(3, 100));
  let c: u8[] = keep.concat(bytes(2, 200));
  return keep.len() * 1000000 + b.len() * 10000 + c.len() * 100 + (sum_u8(keep) % 97);
}

function main(): i32 {
  print(joined().to_string());
  print(words());
  print(allocs().to_string());
  print(chain().to_string());
  print(field().to_string());
  print(shared().to_string());
  return 0;
}
`

func TestSelfHostArrayConcatKernel(t *testing.T) {
	want := "1205565925\n1,-2,3,40,50,9000000000,-1,7,1.5,2.25,-3,true,false,true,x,y,z,32\n100110000\n103200652\n100800297\n8111068\n"
	runSemanticProgram(t, "concatkernel", concatKernelProgram, []string{"joined", "words", "allocs", "chain", "field", "shared"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
