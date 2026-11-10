package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const simdZeroSelectFunctions = `
@noinline function right64(a: f64x2, m: f64x2): f64x2 { return __f64x2_select(a,__f64x2_splat(0.0),m); }
@noinline function left64(a: f64x2, m: f64x2): f64x2 { return __f64x2_select(__f64x2_splat(0.0),a,m); }
@noinline function negative64(a: f64x2, m: f64x2): f64x2 { return __f64x2_select(a,__f64x2_splat(-0.0),m); }
@noinline function right32(a: f32x4, m: f32x4): f32x4 { return __f32x4_select(a,__f32x4_splat(0.0f32),m); }
@noinline function left32(a: f32x4, m: f32x4): f32x4 { return __f32x4_select(__f32x4_splat(0.0f32),a,m); }
@noinline function negative32(a: f32x4, m: f32x4): f32x4 { return __f32x4_select(a,__f32x4_splat(-0.0f32),m); }
`

func simdZeroSelectProgram() string {
	var src strings.Builder
	src.WriteString(simdZeroSelectFunctions)
	src.WriteString("function main(): i32 { let words: i64[] = [")
	rows := [][4]uint64{{0, 0, ^uint64(0), 0}, {0x8000000000000000, 0x7ff0000000000000, 0xaaaaaaaaaaaaaaaa, 0x5555555555555555}, {0x7ff0000000000001, 0xfff8123456789abc, 0x7fffffff80000000, 0xff00ff00ff00ff00}}
	rng := rand.New(rand.NewSource(15168))
	for i := 0; i < 128; i++ {
		rows = append(rows, [4]uint64{rng.Uint64(), rng.Uint64(), rng.Uint64(), rng.Uint64()})
	}
	for _, row := range rows {
		for _, v := range row {
			fmt.Fprintf(&src, "%di64,", int64(v))
		}
	}
	src.WriteString("];let expected: i64[] = [")
	for _, row := range rows {
		for _, mode := range []int{0, 1, 2, 3, 4, 5} {
			for i := 0; i < 2; i++ {
				a, m := row[i], row[2+i]
				v := a & m
				if mode == 1 || mode == 4 {
					v = a &^ m
				}
				if mode == 2 {
					v |= uint64(0x8000000000000000) &^ m
				}
				if mode == 5 {
					v |= uint64(0x8000000080000000) &^ m
				}
				fmt.Fprintf(&src, "%di64,", int64(v))
			}
		}
	}
	src.WriteString(`];
 for i in 0..words.len()/4 {
 let at=i*4; let a=__f64x2_of(f64_from_bits(words[at]),f64_from_bits(words[at+1]));
 let m=__f64x2_of(f64_from_bits(words[at+2]),f64_from_bits(words[at+3]));
 let raw=__alloc_u8(32);
 unsafe {
 let p=raw as usize+8 as usize;
 for k in 0..4 { __store_i64(p+(k*8) as usize,words[at+k]); }
 let a32=__f32x4_load_ptr(p);
 let m32=__f32x4_load_ptr(p+16 as usize);
 for mode in 0..3 {
 let v=if (mode==0) {right64(a,m)} else if (mode==1) {left64(a,m)} else {negative64(a,m)};
 if (__f64x2_lane_bits(v,0)!=expected[i*12+mode*2] as u64 || __f64x2_lane_bits(v,1)!=expected[i*12+mode*2+1] as u64) {return 10+mode;}
 let w=if (mode==0) {right32(a32,m32)} else if (mode==1) {left32(a32,m32)} else {negative32(a32,m32)};
 let low=__f32x4_lane_bits(w,0) as u64 | (__f32x4_lane_bits(w,1) as u64 << 32u64);
 let high=__f32x4_lane_bits(w,2) as u64 | (__f32x4_lane_bits(w,3) as u64 << 32u64);
 if (low!=expected[i*12+6+mode*2] as u64 || high!=expected[i*12+7+mode*2] as u64) {return 20+mode;}
 }
 }
 }
 return 0;
}`)
	return src.String()
}

func TestSelfHostSIMDZeroSelect(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := simdZeroSelectProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("zero select differs from Go: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostSIMDZeroSelectShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "select.fern")
	if err := os.WriteFile(src, []byte(simdZeroSelectProgram()), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"right64", "left64", "right32", "left32"} {
				body := asmFuncBody(t, string(data), "__fn_"+name)
				op := "vand"
				forbidden := "orp"
				if target == "arm64-linux" {
					op = "and v"
					if strings.HasPrefix(name, "left") {
						op = "bic v"
					}
					forbidden = "bsl "
				}
				if !strings.Contains(body, op) || strings.Contains(body, forbidden) {
					t.Errorf("%s lacks exact zero-select fold:\n%s", name, body)
				}
			}
		})
	}
}
