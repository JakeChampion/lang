package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const simdByteStoreFunctions = `
@noinline unsafe function packed[const MODE: i32](p: usize, a: f64x2, b: f64x2): void {
 let a0=__f64x2_lane_bits(a,0) as i32; let a1=__f64x2_lane_bits(a,1) as i32;
 let b0=__f64x2_lane_bits(b,0) as i32; let b1=__f64x2_lane_bits(b,1) as i32;
 let x0=a0;let x1=if (MODE==0) { a1 } else { b0 };let x2=if (MODE==0) { b0 } else { a1 };let x3=b1;
 unsafe {
  __store_i32(p,x0&16777215 | x1<<24);
  __store_i32(p+4 as usize,x1>>8&65535 | x2<<16);
  __store_i32(p+8 as usize,x2>>16&255 | x3<<8);
 }
}
@noinline unsafe function packed8(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_i32(p,(__f64x2_lane_bits(a,1)>>16u64) as i32);
  __store_i32(p+4 as usize,(__f64x2_lane_bits(b,0)>>8u64) as i32);
 }
}
@noinline unsafe function packed16(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_i32(p,__f64x2_lane_bits(a,0) as i32);
  __store_i32(p+4 as usize,__f64x2_lane_bits(a,1) as i32);
  __store_i32(p+8 as usize,__f64x2_lane_bits(b,0) as i32);
  __store_i32(p+12 as usize,__f64x2_lane_bits(b,1) as i32);
 }
}
@noinline unsafe function packed_high(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_u8(p,(__f64x2_lane_bits(a,0)>>56u64) as u8);
  __store_u8(p+1 as usize,(__f64x2_lane_bits(a,1)>>48u64) as u8);
  __store_u8(p+2 as usize,(__f64x2_lane_bits(b,0)>>40u64) as u8);
  __store_u8(p+3 as usize,(__f64x2_lane_bits(b,1)>>32u64) as u8);
 }
}
@noinline unsafe function packed_zero(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_u8(p,__f64x2_lane_bits(a,0) as u8); __store_u8(p+1 as usize,0);
  __store_u8(p+2 as usize,(__f64x2_lane_bits(b,1)>>56u64) as u8); __store_u8(p+3 as usize,0);
 }
}
@noinline unsafe function gaps(p: usize, a: f64x2, b: f64x2): void {
 unsafe { __store_i32(p,__f64x2_lane_bits(a,0) as i32); __store_i32(p+5 as usize,__f64x2_lane_bits(b,0) as i32); }
}
@noinline unsafe function overlap(p: usize, a: f64x2, b: f64x2): void {
 unsafe { __store_i32(p,__f64x2_lane_bits(a,0) as i32); __store_i32(p+2 as usize,__f64x2_lane_bits(b,0) as i32); }
}
@noinline unsafe function read_fence(p: usize, a: f64x2, b: f64x2): i32 {
 unsafe {
  __store_i32(p,__f64x2_lane_bits(a,0) as i32);
  let old=__load_i32(p+4 as usize);
  __store_i32(p+4 as usize,__f64x2_lane_bits(b,0) as i32);
  return old;
 }
}
@noinline unsafe function observe(p: usize): i32 { unsafe { return __load_i32(p); } }
@noinline unsafe function call_fence(p: usize, a: f64x2, b: f64x2): i32 {
 unsafe {
  __store_i32(p,__f64x2_lane_bits(a,0) as i32);
  let old=observe(p+4 as usize);
  __store_i32(p+4 as usize,__f64x2_lane_bits(b,0) as i32);
  return old;
 }
}
@noinline unsafe function nonbyte(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_i32(p,(__f64x2_lane_bits(a,0)>>7u64) as i32);
  __store_i32(p+4 as usize,(__f64x2_lane_bits(b,0)>>7u64) as i32);
 }
}
@noinline unsafe function partial_mask(p: usize, a: f64x2, b: f64x2): void {
 unsafe {
  __store_i32(p,(__f64x2_lane_bits(a,0)&2147483647u64) as i32);
  __store_i32(p+4 as usize,__f64x2_lane_bits(b,0) as i32);
 }
}
@noinline unsafe function three(p: usize, a: f64x2, b: f64x2, c: f64x2): void {
 unsafe {
  __store_i32(p,__f64x2_lane_bits(a,0) as i32);
  __store_i32(p+4 as usize,__f64x2_lane_bits(b,0) as i32);
  __store_i32(p+8 as usize,__f64x2_lane_bits(c,0) as i32);
 }
}
@noinline unsafe function liveout(p: usize, a: f64x2, b: f64x2): u64 {
 let a0=__f64x2_lane_bits(a,0) as i32; let a1=__f64x2_lane_bits(a,1) as i32;
 let b0=__f64x2_lane_bits(b,0) as i32; let b1=__f64x2_lane_bits(b,1) as i32;
 unsafe {
  __store_i32(p,a0&16777215 | b0<<24);
  __store_i32(p+4 as usize,b0>>8&65535 | a1<<16);
  __store_i32(p+8 as usize,a1>>16&255 | b1<<8);
 }
 return __f64x2_lane_bits(a,0);
}
`

func simdByteStoreProgram() string {
	var src strings.Builder
	src.WriteString(simdByteStoreFunctions)
	// Distinct vector loads stay live beyond the packed stores. There are
	// more than either native backend's available vector registers.
	src.WriteString("@noinline unsafe function pressure(p: usize, input: usize): void { unsafe {\n")
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&src, "let v%d=__f64x2_load_ptr(input+%d as usize);\n", i, 16*i)
	}
	src.WriteString(`
 let a0=__f64x2_lane_bits(v0,0) as i32;let a1=__f64x2_lane_bits(v0,1) as i32;
 let b0=__f64x2_lane_bits(v31,0) as i32;let b1=__f64x2_lane_bits(v31,1) as i32;
 unsafe {
 __store_i32(p,a0&16777215 | a1<<24);
 __store_i32(p+4 as usize,a1>>8&65535 | b0<<16);
 __store_i32(p+8 as usize,b0>>16&255 | b1<<8);
 `)
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&src, "__f64x2_store(p+%d as usize,v%d);\n", 16+16*i, i)
	}
	src.WriteString("} } }\nfunction main(): i32 { let cases: i64[] = [")
	rng := rand.New(rand.NewSource(15085))
	inputs := [][4]uint64{{}, {^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}, {0x8000000000000000, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000123}, {0x0123456789abcdef, 0xfedcba9876543210, 0x8081828384858687, 0xff00ff00ff00ff00}}
	for range 128 {
		inputs = append(inputs, [4]uint64{rng.Uint64(), rng.Uint64(), rng.Uint64(), rng.Uint64()})
	}
	var expected []byte
	lengths := []int{12, 12, 8, 16, 4, 4, 9, 6, 8, 8, 8, 8, 12, 12, 12}
	for _, x := range inputs {
		for _, v := range x {
			fmt.Fprintf(&src, "%di64,", int64(v))
		}
		words := make([][]byte, 4)
		for i, v := range x {
			words[i] = binary.LittleEndian.AppendUint64(nil, v)
		}
		join := func(parts ...[]byte) []byte {
			var b []byte
			for _, p := range parts {
				b = append(b, p...)
			}
			return b
		}
		mono := join(words[0][:3], words[1][:3], words[2][:3], words[3][:3])
		stereo := join(words[0][:3], words[2][:3], words[1][:3], words[3][:3])
		shifted := join(binary.LittleEndian.AppendUint32(nil, uint32(x[0]>>7)), binary.LittleEndian.AppendUint32(nil, uint32(x[2]>>7)))
		partial := join(binary.LittleEndian.AppendUint32(nil, uint32(x[0])&0x7fffffff), words[2][:4])
		layouts := [][]byte{mono, stereo, join(words[1][2:6], words[2][1:5]), join(words[0][:4], words[1][:4], words[2][:4], words[3][:4]), {words[0][7], words[1][6], words[2][5], words[3][4]}, {words[0][0], 0, words[3][7], 0}, join(words[0][:4], []byte{101}, words[2][:4]), join(words[0][:2], words[2][:4]), join(words[0][:4], words[2][:4]), join(words[0][:4], words[2][:4]), shifted, partial, join(words[0][:4], words[2][:4], words[1][:4]), stereo, mono}
		for i, b := range layouts {
			if len(b) != lengths[i] {
				panic("layout size")
			}
			expected = append(expected, b...)
		}
	}
	src.WriteString("];let expected: u8[] = [")
	for _, b := range expected {
		fmt.Fprintf(&src, "%d,", b)
	}
	src.WriteString("];let sizes: i32[] = [")
	stride := 0
	for _, n := range lengths {
		fmt.Fprintf(&src, "%d,", n)
		stride += n
	}
	src.WriteString(`];let bytes=__alloc_u8(560);let input=__alloc_u8(512);
 unsafe {
 let base=bytes as usize+8 as usize;let p=base+8 as usize;
 for i in 0..cases.len()/4 {
 let at=i*4;
 let a=__f64x2_of(f64_from_bits(cases[at]),f64_from_bits(cases[at+1]));
 let b=__f64x2_of(f64_from_bits(cases[at+2]),f64_from_bits(cases[at+3]));
 let c=__f64x2_of(f64_from_bits(cases[at+1]),f64_from_bits(cases[at+3]));
 let offset=0;
 for mode in 0..sizes.len() {
 __memset(base,101,560);
 if (mode==0) { packed[0](p,a,b); }
 else if (mode==1) { packed[1](p,a,b); }
 else if (mode==2) { packed8(p,a,b); }
 else if (mode==3) { packed16(p,a,b); }
 else if (mode==4) { packed_high(p,a,b); }
 else if (mode==5) { packed_zero(p,a,b); }
 else if (mode==6) { gaps(p,a,b); }
 else if (mode==7) { overlap(p,a,b); }
 else if (mode==8) { if (read_fence(p,a,b)!=1701143909) { return 81; } }
 else if (mode==9) { if (call_fence(p,a,b)!=1701143909) { return 82; } }
 else if (mode==10) { nonbyte(p,a,b); }
 else if (mode==11) { partial_mask(p,a,b); }
 else if (mode==12) { three(p,a,b,c); }
 else if (mode==13) { if (liveout(p,a,b)!=cases[at] as u64) { return 83; } }
 else {
  let ip=input as usize+8 as usize;
  for k in 0..32 {
   let offset=at+(if ((k&1)==0) { 0 } else { 2 });
   __store_i64(ip+(16*k) as usize,cases[offset]);
   __store_i64(ip+(16*k+8) as usize,cases[offset+1]);
  }
  pressure(p,ip);
 }
`)
	fmt.Fprintf(&src, `for j in 0..sizes[mode] { if (bytes[8+j]!=expected[i*%d+offset+j]) { return 10+mode; } }
 if (bytes[7]!=101 || bytes[8+sizes[mode]]!=101) { return 40+mode; }
 offset+=sizes[mode];
 }
 }
 }
 return 0;
}`, stride)
	return src.String()
}

func TestSelfHostSIMDByteStores(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := simdByteStoreProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("byte stores differ from Go: exit %d\n%s", code, stderr)
			}
		})
		if target != "wasm32-wasi" {
			t.Run(target+"-native", func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, "bytes.fern")
				bin := filepath.Join(dir, "bytes")
				if err := os.WriteFile(src, []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
				if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
					t.Fatalf("native compile: %v\n%s", err, out)
				}
				run := runX86_64Bin(cli.runner, bin)
				if target == "arm64-linux" {
					_, qemu := arm64Tooling(t)
					run = runArm64Bin(qemu, bin)
				}
				if out, err := run.CombinedOutput(); err != nil {
					t.Fatalf("native byte stores differ from Go: %v\n%s", err, out)
				}
			})
		}
	}
}

func TestSelfHostSIMDByteStoresShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := filepath.Join(t.TempDir(), "bytes.fern")
	if err := os.WriteFile(source, []byte(simdByteStoreProgram()), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, source, target))
			if err != nil {
				t.Fatal(err)
			}
			op := "pshufb"
			if target == "arm64-linux" {
				op = "tbl "
			}
			for _, name := range []string{"packed__0", "packed__1", "packed8", "packed16", "packed_high", "packed_zero", "liveout", "pressure"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if !strings.Contains(body, op) {
					t.Errorf("%s lacks vector byte packing:\n%s", name, body)
				}
			}
			for _, name := range []string{"gaps", "overlap", "read_fence", "call_fence", "nonbyte", "partial_mask", "three"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				if strings.Contains(body, op) {
					t.Errorf("%s unexpectedly combines stores:\n%s", name, body)
				}
			}
		})
	}
}
