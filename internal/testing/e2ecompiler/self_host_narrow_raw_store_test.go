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
@noinline unsafe function masked_store(p: usize, x: i64): void { unsafe { __store_i32(p, ((x as i32) >> 8) & 65535); } }
@noinline function masked_return(x: i64): i64 { return (((x as i32) >> 16) & 255) as i64; }
@noinline function byte_return(x: i64): i64 { return (x as u8) as i64; }
@noinline unsafe function far_store(p: usize, x: i64): void { unsafe { __store_i32(p, ((((x as i32) as i64) >> 40i64) & 255i64) as i32); } }
@noinline unsafe function loop_byte(p: usize, x: i64, y: i64): void {
 let v=x as u8;
 for i in 0..4 { v=(v >> 1u8)+(y as u8); unsafe { __store_u8(p+i as usize,v); } }
}
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
			row = append(row, int64(uint32(x)>>8&65535), int64(uint32(x)>>16&255), int64(uint8(x)), (int64(int32(x))>>40)&255)
			bv := uint8(x)
			for range 4 {
				bv = (bv >> 1) + uint8(y)
				row = append(row, int64(bv))
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
  for i in 0..cases.len()/18 {
   let at=i*18;let x=cases[at];let y=cases[at+1];
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
   masked_store(p,x);if ((__load_i32(p) as i64)!=cases[at+10]) { return 8; }
   if (masked_return(x)!=cases[at+11] || byte_return(x)!=cases[at+12]) { return 9; }
   far_store(p,x);if ((__load_i32(p) as i64)!=cases[at+13]) { return 10; }
   loop_byte(p,x,y);
   for j in 0..4 { if ((__load_u8(p+j as usize) as i64)!=cases[at+14+j]) { return 11; } }
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
			for _, name := range []string{"store32", "store8", "masked_store", "masked_return"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if strings.Contains(body, extension) {
					t.Errorf("%s retains a sign extension before a narrow store:\n%s", name, body)
				}
			}
			for _, name := range []string{"store64", "mixed", "addressed", "far_store"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if !strings.Contains(body, extension) {
					t.Errorf("%s lost a required sign extension:\n%s", name, body)
				}
			}
		})
	}
}

// Constant shifts and masks exercise demand propagation rather than the
// conservative variable-shift path. Each expected result is computed in Go.
func maskDemandProgram() string {
	inputs := []int64{0, 1, -1, 127, 255, 256, 2147483647, 2147483648, 4294967295, 4294967296, -2147483648, -9223372036854775808, 9223372036854775807}
	rng := rand.New(rand.NewSource(15066))
	for range 16 {
		inputs = append(inputs, int64(rng.Uint64()))
	}
	masks := []int64{0, 1, 127, 255, 256, 65535, 65536, 16777215, 2147483647, 4294967295, 4294967296, 9223372036854775807}
	counts := []int32{-65, -1, 0, 8, 24, 32, 40, 63, 64, 65}
	var src strings.Builder
	at := 0
	for _, mask := range masks {
		for _, n := range counts {
			for mode := 0; mode < 4; mode++ {
				var expr string
				switch mode {
				case 0:
					expr = fmt.Sprintf("(((x as i32)>>%d) as i64)", n)
				case 1:
					expr = fmt.Sprintf("(((x as u32)>>(%di64 as u32)) as i64)", n)
				case 2:
					expr = fmt.Sprintf("(((x as i32) as i64)>>%di64)", n)
				case 3:
					expr = fmt.Sprintf("((((x as u32) as u64)>>(%di64 as u64)) as i64)", n)
				}
				fmt.Fprintf(&src, "@noinline function md%d(x:i64):i64 { return %s & %di64; }\n", at, expr, mask)
				at++
			}
		}
	}
	fmt.Fprint(&src, "function main():i32 { let inputs:i64[]=[")
	for _, x := range inputs {
		fmt.Fprintf(&src, "%di64,", x)
	}
	fmt.Fprint(&src, "];let expected:i64[]=[")
	for _, x := range inputs {
		for _, mask := range masks {
			for _, n := range counts {
				for _, v := range []int64{int64(int32(x) >> (uint32(n) & 31)), int64(uint32(x) >> (uint32(n) & 31)), int64(int32(x)) >> (uint32(n) & 63), int64(uint64(uint32(x)) >> (uint32(n) & 63))} {
					fmt.Fprintf(&src, "%di64,", v&mask)
				}
			}
		}
	}
	fmt.Fprint(&src, "];let at=0;for x in inputs {\n")
	for i := 0; i < at; i++ {
		fmt.Fprintf(&src, "if (md%d(x)!=expected[at]) { return %d; } at+=1;\n", i, 1+i%250)
	}
	fmt.Fprint(&src, "}return 0;}\n")
	return src.String()
}

func TestSelfHostMaskDemand(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := maskDemandProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("masked shift differs from Go: exit %d\n%s", code, stderr)
			}
		})
	}
}
