package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var unzipLanes = [][4]int{{0, 2, 4, 6}, {1, 3, 5, 7}, {6, 4, 2, 0}, {7, 5, 3, 1}, {0, 6, 4, 2}, {1, 7, 5, 3}}

func unzipExpression(a, b string, mode int) string {
	highA, highB := a, b
	if mode >= 4 {
		highA, highB = b, a
	}
	op := "lo"
	if mode%2 != 0 {
		op = "hi"
	}
	expr := fmt.Sprintf("__f32x4_zip_%s(__f32x4_zip_lo(%s,%s),__f32x4_zip_hi(%s,%s))", op, a, b, highA, highB)
	if mode == 2 || mode == 3 {
		expr = "__f32x4_reverse(" + expr + ")"
	}
	return expr
}

func unzipSource() string {
	var s strings.Builder
	for mode := range unzipLanes {
		fmt.Fprintf(&s, "@noinline function unzip%d(p: usize, out: usize): void { unsafe { let a=__f32x4_load_ptr(p); let b=__f32x4_load_ptr(p+16 as usize); __f32x4_store(out,%s); } }\n", mode, unzipExpression("a", "b", mode))
	}
	s.WriteString("function invoke(mode: i32,p: usize,out: usize): void {\n")
	for mode := range unzipLanes {
		fmt.Fprintf(&s, "if (mode==%d) {unzip%d(p,out);return;}\n", mode, mode)
	}
	s.WriteString("}\n@noinline function pressure(p: usize,out: usize): void { unsafe {\n")
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "let v%d=__f32x4_load_ptr(p+%d as usize);\n", i, i*16)
	}
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "let z%d=%s;\n", i, unzipExpression(fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", (i+1)%32), i%4))
	}
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "__f32x4_store(out+%d as usize,z%d);\n", i*16, i)
	}
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "__f32x4_store(out+%d as usize,v%d);\n", 512+i*16, i)
	}
	s.WriteString("} }\nfunction main(): i32 { let words: i64[]=[")
	rows := [][4]uint64{{0x8000000000000000, 0x7f800001ff800001, 0x7fc1234580000000, 0xffc1234500000001}, {0, 0, 0, 0}, {0x0102030405060708, 0x1112131415161718, 0x2122232425262728, 0x3132333435363738}}
	rng := rand.New(rand.NewSource(15223))
	for i := 0; i < 128; i++ {
		rows = append(rows, [4]uint64{rng.Uint64(), rng.Uint64(), rng.Uint64(), rng.Uint64()})
	}
	for _, row := range rows {
		for _, v := range row {
			fmt.Fprintf(&s, "%di64,", int64(v))
		}
	}
	s.WriteString("]; let expected: i64[]=[")
	for _, row := range rows {
		for mode := 0; mode < 10; mode++ {
			indices := unzipLanes[mode%6]
			if mode >= 6 {
				indices = unzipLanes[mode-6]
			}
			var out [4]uint32
			for j, lane := range indices {
				if mode >= 6 && mode%2 != 0 {
					lane = (lane + 4) % 8
				}
				out[j] = uint32(row[lane/2] >> uint(32*(lane%2)))
			}
			for j := 0; j < 4; j += 2 {
				fmt.Fprintf(&s, "%di64,", int64(uint64(out[j])|uint64(out[j+1])<<32))
			}
		}
	}
	s.WriteString(`];
 let raw=__alloc_u8(80);
 let input=__alloc_u8(520);
 let output=__alloc_u8(1040);
 unsafe {
  let p=raw as usize+8 as usize;
  let ip=input as usize+8 as usize;
  let op=output as usize+8 as usize;
  for row in 0..words.len()/4 {
   for mode in 0..6 {
    for offset in [0,8,16,32] {
     for i in 0..8 {__store_i64(p+(8*i) as usize,if (i<4) {words[row*4+i]} else {123456789i64});}
     invoke(mode,p,p+offset as usize);
     for i in 0..2 {if (__load_i64(p+(offset+8*i) as usize)!=expected[row*20+mode*2+i]) {return 1;}}
     for i in 0..64 {
      if (i<offset || i>=offset+16) {
       let word=if (i/8<4) {words[row*4+i/8]} else {123456789i64};
       if (raw[i]!=(word as u64>>(8*(i%8)) as u64) as u8) {return 2;}
      }
     }
    }
   }
   for i in 0..64 {__store_i64(ip+(8*i) as usize,words[row*4+i%4]);}
   pressure(ip,op);
   for i in 0..64 {
    if (__load_i64(op+(8*i) as usize)!=expected[row*20+12+(i/2%4)*2+i%2]) {return 3;}
    if (__load_i64(op+(512+8*i) as usize)!=words[row*4+i%4]) {return 4;}
   }
  }
 }
 return 0;
}`)
	return s.String()
}

func TestSelfHostSIMDUnzipFusion(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := unzipSource()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("unzip differs from Go: exit %d\n%s", code, stderr)
			}
		})
	}
	src := filepath.Join(t.TempDir(), "unzip.fern")
	if err := os.WriteFile(src, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target+"/native-assembler", func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "unzip")
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
			for mode := 0; mode < 4; mode++ {
				body := asmFuncBody(t, string(asm), fmt.Sprintf("__fn_unzip%d", mode))
				if target == "x86-64-linux" {
					if strings.Count(body, "vshufps ") != 1 || strings.Contains(body, "vpunpck") || strings.Contains(body, "pshufd") {
						t.Errorf("unzip%d requires one shuffle:\n%s", mode, body)
					}
				} else if strings.Count(body, "uzp") != 1 || strings.Contains(body, "    zip") {
					t.Errorf("unzip%d requires one unzip:\n%s", mode, body)
				}
			}
		})
	}
	t.Run("wasm32-wasi/shape", func(t *testing.T) {
		asm, err := os.ReadFile(cli.emit(t, src, "wasm32-wasi"))
		if err != nil {
			t.Fatal(err)
		}
		wat := string(asm)
		for mode := 0; mode < 4; mode++ {
			name := fmt.Sprintf("  (func $unzip%d ", mode)
			start := strings.Index(wat, name)
			if start < 0 {
				t.Fatalf("missing %s", name)
			}
			body := wat[start:]
			if end := strings.Index(body[1:], "\n  (func "); end >= 0 {
				body = body[:end+1]
			}
			if strings.Count(body, "i8x16.shuffle") != 1 {
				t.Errorf("unzip%d requires one shuffle:\n%s", mode, body)
			}
		}
	})
}
