package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var permutationNames = []string{"reverse32", "swap32", "ziplo32", "ziphi32", "reverse64", "swap64", "ziplo64", "ziphi64"}

func permutationExpected(row [4]uint64, mode int) [2]uint64 {
	if mode >= 4 {
		switch mode {
		case 4, 5:
			return [2]uint64{row[1], row[0]}
		case 6:
			return [2]uint64{row[0], row[2]}
		default:
			return [2]uint64{row[1], row[3]}
		}
	}
	a := [4]uint32{uint32(row[0]), uint32(row[0] >> 32), uint32(row[1]), uint32(row[1] >> 32)}
	b := [4]uint32{uint32(row[2]), uint32(row[2] >> 32), uint32(row[3]), uint32(row[3] >> 32)}
	var out [4]uint32
	switch mode {
	case 0:
		out = [4]uint32{a[3], a[2], a[1], a[0]}
	case 1:
		out = [4]uint32{a[2], a[3], a[0], a[1]}
	case 2:
		out = [4]uint32{a[0], b[0], a[1], b[1]}
	case 3:
		out = [4]uint32{a[2], b[2], a[3], b[3]}
	}
	return [2]uint64{uint64(out[0]) | uint64(out[1])<<32, uint64(out[2]) | uint64(out[3])<<32}
}

func permutationSource() string {
	var s strings.Builder
	operations := []string{"reverse", "swap_halves", "zip_lo", "zip_hi"}
	for mode, name := range permutationNames {
		lane := "f32x4"
		if mode >= 4 {
			lane = "f64x2"
		}
		fmt.Fprintf(&s, "@noinline function %s(p: usize, out: usize): void { unsafe { let a = __%s_load_ptr(p);", name, lane)
		args := "a"
		if mode%4 >= 2 {
			fmt.Fprintf(&s, "let b = __%s_load_ptr(p + 16 as usize);", lane)
			args = "a, b"
		}
		fmt.Fprintf(&s, "__%s_store(out, __%s_%s(%s)); } }\n", lane, lane, operations[mode%4], args)
	}
	s.WriteString("function invoke(mode: i32, p: usize, out: usize): void {\n")
	for mode, name := range permutationNames {
		fmt.Fprintf(&s, "if (mode == %d) { %s(p, out); return; }\n", mode, name)
	}
	s.WriteString("}\n@noinline function pressure(p: usize, out: usize): void { unsafe {\n")
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "let v%d = __f32x4_load_ptr(p + %d as usize);\n", i, 16*i)
	}
	for i := 0; i < 32; i++ {
		args := fmt.Sprintf("v%d", i)
		if i%4 >= 2 {
			args += fmt.Sprintf(", v%d", (i+1)%32)
		}
		fmt.Fprintf(&s, "let z%d = __f32x4_%s(%s);\n", i, operations[i%4], args)
	}
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "__f32x4_store(out + %d as usize, z%d);\n", 16*i, i)
	}
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "__f32x4_store(out + %d as usize, v%d);\n", 512+16*i, i)
	}
	s.WriteString("} }\nfunction main(): i32 { let words: i64[] = [")
	rows := [][4]uint64{{0, 0, 0, 0}, {0x8000000000000000, 0x7ff0000000000001, 0xfff8123456789abc, 0x7ff0000000000000}, {0x7f800001ff800001, 0x800000007fc12345, 0x0123456789abcdef, 0xfedcba9876543210}}
	rng := rand.New(rand.NewSource(15191))
	for i := 0; i < 128; i++ {
		rows = append(rows, [4]uint64{rng.Uint64(), rng.Uint64(), rng.Uint64(), rng.Uint64()})
	}
	for _, row := range rows {
		for _, v := range row {
			fmt.Fprintf(&s, "%di64,", int64(v))
		}
	}
	s.WriteString("]; let expected: i64[] = [")
	for _, row := range rows {
		for mode := 0; mode < 8; mode++ {
			for _, v := range permutationExpected(row, mode) {
				fmt.Fprintf(&s, "%di64,", int64(v))
			}
		}
		for mode := 0; mode < 4; mode++ {
			shifted := row
			if mode%2 != 0 {
				shifted = [4]uint64{row[2], row[3], row[0], row[1]}
			}
			for _, v := range permutationExpected(shifted, mode) {
				fmt.Fprintf(&s, "%di64,", int64(v))
			}
		}
	}
	s.WriteString(`];
 let raw = __alloc_u8(64);
 let input = __alloc_u8(512);
 let output = __alloc_u8(1056);
 unsafe {
  let p = raw as usize + 8 as usize;
  let ip = input as usize + 8 as usize;
  let op = output as usize + 24 as usize;
  for row in 0..words.len()/4 {
   for mode in 0..8 {
    for offset in [0,8,16,32] {
     for i in 0..8 { __store_i64(p + (8*i) as usize, if (i<4) {words[row*4+i]} else {-6510615555426900571i64}); }
     invoke(mode,p,p+offset as usize);
     for i in 0..2 {
      if (__load_i64(p+(offset+8*i) as usize)!=expected[row*24+mode*2+i]) {return 1;}
     }
     for i in 0..64 {
      if (i<offset || i>=offset+16) {
       let word=if (i/8<4) {words[row*4+i/8]} else {-6510615555426900571i64};
       if (raw[i]!=(word as u64 >> (8*(i%8)) as u64) as u8) {return 2;}
      }
     }
    }
   }
   for i in 0..64 {__store_i64(ip+(8*i) as usize,words[row*4+i%4]);}
   pressure(ip,op);
   for i in 0..64 {
    if (__load_i64(op+(8*i) as usize)!=expected[row*24+16+(i/2%4)*2+i%2]) {return 3;}
    if (__load_i64(op+(512+8*i) as usize)!=words[row*4+i%4]) {return 4;}
   }
   for i in 0..16 {if (output[i]!=0u8 || output[1040+i]!=0u8) {return 5;}}
  }
 }
 return 0;
}`)
	return s.String()
}

func TestSelfHostSIMDPermutationRegisters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := permutationSource()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("permutation differs from Go: exit %d\n%s", code, stderr)
			}
		})
	}
	src := filepath.Join(t.TempDir(), "permute.fern")
	if err := os.WriteFile(src, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target+"/native-assembler", func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "permute")
			if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := runX86_64Bin(cli.runner, bin)
			if target == "arm64-linux" {
				_, qemu := arm64Tooling(t)
				run = runArm64Bin(qemu, bin)
			}
			if out, err := run.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
		t.Run(target+"/shape", func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for mode, name := range permutationNames {
				body := asmFuncBody(t, string(asm), "__fn_"+name)
				if target == "x86-64-linux" {
					if strings.Contains(body, "movaps ") || strings.Contains(body, "movapd ") {
						t.Errorf("%s copies vectors through scratch:\n%s", name, body)
					}
					if mode%4 >= 2 && !strings.Contains(body, "vpunpck") {
						t.Errorf("%s lacks a three-operand zip:\n%s", name, body)
					}
				} else if strings.Contains(body, "mov v") {
					t.Errorf("%s copies vectors through scratch:\n%s", name, body)
				}
			}
		})
	}
}
