package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const narrowRawStoreFunctions = `
@noinline unsafe function store32(p: usize, x: i64, y: i64): void { unsafe { __store_i32(p, ((x as i32) + (y as i32)) ^ ((x as i32) * 3)); } }
@noinline unsafe function store8(p: usize, x: i64, y: i64): void { unsafe { __store_u8(p, ((x as i32) ^ ((y as i32) << 3)) as u8); } }
@noinline unsafe function store64(p: usize, x: i64): void { unsafe { __store_i64(p, (x as i32) as i64); } }
@noinline unsafe function store_unsigned64(p: usize, x: i64): void { unsafe { __store_i64(p, (x as u32) as i64); } }
@noinline unsafe function mixed(p: usize, x: i64): i64 { let v=x as i32; unsafe { __store_i32(p,v); } return v as i64; }
@noinline unsafe function addressed(p: usize, x: i64, expected: i64): void { let offset=((x as i32) as i64)-expected; unsafe { __store_u8(p + offset as usize,99); } }
@noinline unsafe function loop_store(p: usize, x: i64, y: i64): void {
 let v = x as i32;
 for i in 0..4 {
  v = if ((i & 1)==0) { v+(y as i32) } else { v*3 };
  unsafe { __store_i32(p + (4*i) as usize,v); }
 }
}
`

func narrowRawStoreProgram() string {
	inputs := []int64{0, 1, -1, 127, 128, 255, 256, 2147483647, 2147483648, 4294967295, 4294967296, -2147483648, -9223372036854775808, 9223372036854775807}
	rng := rand.New(rand.NewSource(15066))
	for range 32 {
		inputs = append(inputs, int64(rng.Uint64()))
	}
	var src strings.Builder
	src.WriteString(narrowRawStoreFunctions)
	src.WriteString("function main(): i32 { let cases: i64[] = [")
	for _, x := range inputs {
		for _, y := range inputs {
			a, b := int32(x), int32(y)
			row := []int64{x, y, int64((a + b) ^ (a * 3)), int64(uint8(a ^ (b << 3))), int64(a), int64(uint32(x))}
			v := a
			for i := 0; i < 4; i++ {
				if i&1 == 0 {
					v += b
				} else {
					v *= 3
				}
				row = append(row, int64(v))
			}
			for _, n := range row {
				fmt.Fprintf(&src, "%di64,", n)
			}
		}
	}
	src.WriteString(`];
 let bytes=__alloc_u8(40);
 unsafe {
  let base=bytes as usize+8 as usize;
  let p=base+8 as usize;
  for i in 0..cases.len()/10 {
   let at=i*10;let x=cases[at];let y=cases[at+1];
   __store_u8(base+7 as usize,71);__store_u8(base+12 as usize,73);
   store32(p,x,y);
   if ((__load_i32(p) as i64)!=cases[at+2] || __load_u8(base+7 as usize)!=71 || __load_u8(base+12 as usize)!=73) { return 1; }
   store8(p+1 as usize,x,y);
   if ((__load_u8(p+1 as usize) as i64)!=cases[at+3] || __load_u8(base+7 as usize)!=71 || __load_u8(base+12 as usize)!=73) { return 2; }
   store64(p,x);if (__load_i64(p)!=cases[at+4]) { return 3; }
   store_unsigned64(p,x);if (__load_i64(p)!=cases[at+5]) { return 4; }
   if (mixed(p,x)!=cases[at+4] || (__load_i32(p) as i64)!=cases[at+4]) { return 5; }
   addressed(p,x,cases[at+4]);if (__load_u8(p)!=99) { return 6; }
   loop_store(p,x,y);
   for j in 0..4 { if ((__load_i32(p+(4*j) as usize) as i64)!=cases[at+6+j]) { return 7; } }
  }
 }
 return 0;
}
`)
	return src.String()
}

func TestSelfHostNarrowRawStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := narrowRawStoreProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("narrow store differs from Go: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostNarrowRawStoreShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "stores.fern")
	if err := os.WriteFile(src, []byte(narrowRawStoreProgram()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			extension := "movslq"
			if target == "arm64-linux" {
				extension = "sxtw"
			}
			for _, name := range []string{"store32", "store8"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if strings.Contains(body, extension) {
					t.Errorf("%s retains a sign extension before a narrow store:\n%s", name, body)
				}
			}
			for _, name := range []string{"store64", "mixed", "addressed"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if !strings.Contains(body, extension) {
					t.Errorf("%s lost a required sign extension:\n%s", name, body)
				}
			}
		})
	}
}
