package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const narrowRightShiftFunctions = `
@noinline function signed_shift(x: i64, n: i32): i64 { return ((x as i32) >> n) as i64; }
@noinline function unsigned_shift(x: i64, n: i32): i64 { return ((x as u32) >> n as u32) as i64; }
@noinline function byte_shift(bytes: u8[], at: i32, n: i32): i64 { return (bytes[at] as i32 >> n) as i64; }
@noinline function self_shift(x: i64): i64 { let n=x as i32; return (n >> n) as i64; }
@noinline function wide_signed(x: i64, n: i32): i64 { return x >> n as i64; }
@noinline function wide_unsigned(x: i64, n: i32): i64 { return (x as u64 >> n as u64) as i64; }
@noinline function wide_known_signed(x: i64, n: i32): i64 { return ((x as i32) as i64) >> n as i64; }
@noinline function wide_known_unsigned(x: i64, n: i32): i64 { return (((x as u32) as u64) >> n as u64) as i64; }
@noinline function unproven(x: i32, n: i32): i64 { return (x >> n) as i64; }
@noinline function pressure(x0:i64,x1:i64,x2:i64,x3:i64,x4:i64,x5:i64,x6:i64,x7:i64,x8:i64,x9:i64,x10:i64,x11:i64,n:i32):i64 {
 let a0=x0 as i32;let a1=x1 as i32;let a2=x2 as i32;let a3=x3 as i32;
 let a4=x4 as i32;let a5=x5 as i32;let a6=x6 as i32;let a7=x7 as i32;
 let a8=x8 as i32;let a9=x9 as i32;let a10=x10 as i32;let a11=x11 as i32;
 let shifted=((a0>>n) as i64)^((a1>>n) as i64)^((a2>>n) as i64)^((a3>>n) as i64)
 ^((a4>>n) as i64)^((a5>>n) as i64)^((a6>>n) as i64)^((a7>>n) as i64)
 ^((a8>>n) as i64)^((a9>>n) as i64)^((a10>>n) as i64)^((a11>>n) as i64);
 let sum=(a0 as i64)+(a1 as i64)+(a2 as i64)+(a3 as i64)+(a4 as i64)+(a5 as i64)
 +(a6 as i64)+(a7 as i64)+(a8 as i64)+(a9 as i64)+(a10 as i64)+(a11 as i64);
 return shifted^sum;
}
`

func narrowRightShiftProgram() string {
	inputs := []int64{0, 1, -1, 127, 128, 255, 256, 2147483647, 2147483648, 4294967295, 4294967296, -2147483648, -9223372036854775808, 9223372036854775807}
	rng := rand.New(rand.NewSource(417))
	for range 32 {
		inputs = append(inputs, int64(rng.Uint64()))
	}
	counts := []int32{-2147483648, 2147483647}
	for n := int32(-65); n <= 65; n++ {
		counts = append(counts, n)
	}
	var src strings.Builder
	src.WriteString(narrowRightShiftFunctions)
	src.WriteString("function main(): i32 {\nlet inputs: i64[] = [")
	for _, x := range inputs {
		fmt.Fprintf(&src, "%di64,", x)
	}
	src.WriteString("];\nlet counts: i32[] = [")
	for _, n := range counts {
		fmt.Fprintf(&src, "%d,", n)
	}
	src.WriteString("];\nlet expected: i64[] = [")
	for _, x := range inputs {
		for _, n := range counts {
			shift := uint32(n) & 31
			sum, xor := int64(0), int64(0)
			for j := int64(0); j < 12; j++ {
				a := int32(x + j)
				sum += int64(a)
				xor ^= int64(a >> shift)
			}
			for _, value := range []int64{int64(int32(x) >> shift), int64(uint32(x) >> shift), int64(uint8(x) >> shift), x >> (uint32(n) & 63), int64(uint64(x) >> (uint32(n) & 63)), xor ^ sum, int64(int32(x)) >> (uint32(n) & 63), int64(uint64(uint32(x)) >> (uint32(n) & 63))} {
				fmt.Fprintf(&src, "%di64,", value)
			}
		}
	}
	src.WriteString("];\nlet bytes: u8[] = [")
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&src, "%du8,", i)
	}
	src.WriteString(`];
 let at=0;
 for x in inputs {
  for n in counts {
   if (signed_shift(x,n)!=expected[at]) { return 1; }
   if (unsigned_shift(x,n)!=expected[at+1]) { return 2; }
   if (byte_shift(bytes,(x & 255i64) as i32,n)!=expected[at+2]) { return 3; }
   if (wide_signed(x,n)!=expected[at+3] || wide_unsigned(x,n)!=expected[at+4]) { return 4; }
   if (pressure(x,x+1i64,x+2i64,x+3i64,x+4i64,x+5i64,x+6i64,x+7i64,x+8i64,x+9i64,x+10i64,x+11i64,n)!=expected[at+5]) { return 5; }
   if (unproven(x as i32,n)!=expected[at]) { return 6; }
   if (wide_known_signed(x,n)!=expected[at+6] || wide_known_unsigned(x,n)!=expected[at+7]) { return 8; }
   at+=8;
  }
 }
`)
	for _, x := range inputs {
		fmt.Fprintf(&src, "if (self_shift(%di64)!=%di64) { return 7; }\n", x, int64(int32(x)>>(uint32(x)&31)))
	}
	src.WriteString("return 0;\n}\n")
	return src.String()
}

func TestSelfHostNarrowRightShift(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := narrowRightShiftProgram()
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("shift differs from Go reference: exit %d\n%s", code, stderr)
			}
		})
	}
	t.Run("direct-native", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "shifts.fern")
		bin := filepath.Join(dir, "shifts")
		if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("native assembler compile: %v\n%s", err, out)
		}
		if out, err := runX86_64Bin(cli.runner, bin).CombinedOutput(); err != nil {
			t.Fatalf("native assembler shift reference: %v\n%s", err, out)
		}
	})

}

func TestSelfHostNarrowRightShiftShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "narrow_shifts.fern")
	if err := os.WriteFile(src, []byte(narrowRightShiftProgram()), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range []string{"signed_shift", "unsigned_shift", "byte_shift", "self_shift", "pressure"} {
		body := asmFuncBody(t, text, "__fn_"+name)
		op := "sarxl"
		if name == "unsigned_shift" {
			op = "shrxl"
		}
		if !strings.Contains(body, op) || strings.Contains(body, "andl $31") {
			t.Errorf("%s did not use narrow hardware count masking:\n%s", name, body)
		}
	}
	pressureBody := asmFuncBody(t, text, "__fn_pressure")
	spilled := false
	for _, line := range strings.Split(pressureBody, "\n") {
		spilled = spilled || strings.Contains(line, "sarxl ") && strings.Contains(line, "(%rbp)")
	}
	if !spilled {
		t.Fatalf("pressure test does not exercise a spilled shift source:\n%s", pressureBody)
	}
	for _, name := range []string{"wide_signed", "wide_unsigned", "wide_known_signed", "wide_known_unsigned", "unproven"} {
		body := asmFuncBody(t, text, "__fn_"+name)
		op := "sarxq"
		if name == "wide_unsigned" || name == "wide_known_unsigned" {
			op = "shrxq"
		}
		if !strings.Contains(body, op) {
			t.Errorf("%s lost its full-width shift:\n%s", name, body)
		}
	}
}
