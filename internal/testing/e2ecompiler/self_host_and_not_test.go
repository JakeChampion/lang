package e2ecompiler

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const andNotProgram = `import "std/u64";
const ALL: u64 = 0xffffffffffffffffu64;
@noinline function right(x: u64, y: u64): u64 { return x & (y ^ ALL); }
@noinline function left(x: u64, y: u64): u64 { return (y ^ ALL) & x; }
@noinline function commuted(x: u64, y: u64): u64 { return x & (ALL ^ y); }
@noinline function different_mask(x: u64, y: u64): u64 { return x & (y ^ 0xfffffffffffffffeu64); }
@noinline function shared(x: u64, y: u64): u64 { let n = y ^ ALL; return n + (x & n); }
@noinline function pressure(a: u64,b: u64,c: u64,d: u64,e: u64,f: u64,g: u64,h: u64,i: u64,j: u64,k: u64,l: u64,m: u64): u64 {
 let r = a & (b ^ ALL); r = r + (c & (d ^ ALL)); r = r + (e & (f ^ ALL)); r = r + (g & (h ^ ALL));
 return r + a+b+c+d+e+f+g+h+i+j+k+l+m;
}
@noinline function signed(x: i32, y: i32): i32 { return x & (y ^ -1); }
@noinline function narrow(x: u32, y: u32): u32 { return x & (y ^ 0xffffffffu32); }
function main(): i32 {
 let edges: u64[] = [0u64,1u64,2u64,0x7fffffffffffffffu64,0x8000000000000000u64,0xfffffffffffffffeu64,ALL,0x123456789abcdef0u64,0xedcba98765432110u64];
 let acc = 0u64;
 for x in edges { for y in edges {
   let r = right(x,y) + 2u64*left(x,y) + 4u64*commuted(x,y) + 8u64*different_mask(x,y) + 16u64*shared(x,y);
   r = r + pressure(x,y,x+1u64,y+2u64,x^y,x-y,x*3u64,y*5u64,x+9u64,y+7u64,x*11u64,y*13u64,x^ALL);
   r = r + (signed(x as i32,y as i32) as i64 as u64) + (narrow(x as u32,y as u32) as u64);
   acc = acc*1000003u64+r;
   if (signed(x as i32,y as i32) != ((x as i32) & ((y as i32) ^ -1))) { return 1; }
   if (narrow(x as u32,y as u32) as u64 != ((x & (y ^ ALL)) & 0xffffffffu64)) { return 2; }
 } }
 print(acc.to_string()); return 0;
}
`

func andNotExpected() string {
	edges := []uint64{0, 1, 2, 0x7fffffffffffffff, 0x8000000000000000, 0xfffffffffffffffe, 0xffffffffffffffff, 0x123456789abcdef0, 0xedcba98765432110}
	var acc uint64
	for _, x := range edges {
		for _, y := range edges {
			n := ^y
			r := 7*(x&^y) + 8*(x&(y^uint64(0xfffffffffffffffe))) + 16*(n+(x&n))
			v := []uint64{x, y, x + 1, y + 2, x ^ y, x - y, x * 3, y * 5, x + 9, y + 7, x * 11, y * 13, ^x}
			r += v[0]&^v[1] + v[2]&^v[3] + v[4]&^v[5] + v[6]&^v[7]
			for _, n := range v {
				r += n
			}
			r += uint64(int64(int32(x)&^int32(y))) + uint64(uint32(x)&^uint32(y))
			acc = acc*1000003 + r
		}
	}
	return strconv.FormatUint(acc, 10) + "\n"
}

func TestSelfHostAndNot(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(andNotProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if target != "wasm32-wasi" {
				raw, err := os.ReadFile(cli.emit(t, src, target))
				if err != nil {
					t.Fatal(err)
				}
				op := `(?m)^\s+andnq\s`
				if target == "arm64-linux" {
					op = `(?m)^\s+bic\s`
				}
				for _, fn := range []string{"right", "left", "commuted", "shared", "pressure"} {
					body := tailCallBody(t, string(raw), fn)
					if !regexp.MustCompile(op).MatchString(body) {
						t.Errorf("%s has no fused and-not:\n%s", fn, body)
					}
				}
				if regexp.MustCompile(op).MatchString(tailCallBody(t, string(raw), "different_mask")) {
					t.Error("partial inverse was treated as all bits")
				}
			}
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
			default:
				cmd = exec.Command("wasmtime", "run", cli.emit(t, src, target))
			}
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != andNotExpected() {
				t.Fatalf("%s: %v: %s, want %s", target, err, out, andNotExpected())
			}
		})
	}
}

// Every register field and every aliasing pattern, at both widths, is pinned
// against GNU as. This also drives the in-process assembler's mnemonic path.
func TestSelfHostX86GasAndNot(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	var asm strings.Builder
	asm.WriteString(".text\n_start:\n")
	regs64 := []string{"%rax", "%rcx", "%rdx", "%rbx", "%rsp", "%rbp", "%rsi", "%rdi", "%r8", "%r9", "%r10", "%r11", "%r12", "%r13", "%r14", "%r15"}
	regs32 := []string{"%eax", "%ecx", "%edx", "%ebx", "%esp", "%ebp", "%esi", "%edi", "%r8d", "%r9d", "%r10d", "%r11d", "%r12d", "%r13d", "%r14d", "%r15d"}
	for _, regs := range [][]string{regs32, regs64} {
		suffix := "l"
		if regs[0] == "%rax" {
			suffix = "q"
		}
		for d := 0; d < 16; d++ {
			for a := 0; a < 16; a++ {
				for b := 0; b < 16; b++ {
					fmt.Fprintf(&asm, " andn%s %s, %s, %s\n", suffix, regs[a], regs[b], regs[d])
				}
			}
		}
	}
	asm.WriteString(" andnl -80(%rbp), %r11d, %r9d\n andnq 8(%r12,%r13,4), %r14, %r15\n andnq _start(%rip), %rdx, %rcx\n andnl -264(%r8), %r8d, %r8d\n")
	dir := t.TempDir()
	src := filepath.Join(dir, "all.s")
	obj := filepath.Join(dir, "all.o")
	if err := os.WriteFile(src, []byte(asm.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gcc, "-c", "-o", obj, src).CombinedOutput(); err != nil {
		t.Fatalf("GNU as: %v: %s", err, out)
	}
	object, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	section := object.Section(".text")
	if section == nil {
		t.Fatal("GNU object has no text section")
	}
	expected, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	main := fmt.Sprintf(`function main(): i32 {
 for bad in ["andnq %%rax, %%ecx, %%rdx", "andnq %%rax, (%%rcx), %%rdx", "andnq %%rax, %%rdx", "andnl %%eax, %%ecx, %%rdx"] {
  if (x86_gas_assemble(".text\n_start:\n " + bad + "\n").unknown.len() == 0) { return 93; }
 }
 let a: X86Asm = x86_gas_assemble(%q);
 if (a.unknown.len() > 0) { return 90; }
 let exp: string = "%x";
 if (a.text.len()*2 != exp.len()) { return 91; }
 let digits: string = "0123456789abcdef";
 let i=0; while (i<a.text.len()) {
   let b: i32 = a.text[i] as i32;
   if (digits[b >> 4] != exp[i*2] || digits[b & 15] != exp[i*2+1]) { return 92; }
   i=i+1;
 }
 return 0;
}`, asm.String(), expected)
	runX86GasWasmSelfTest(t, "x86_gas_andnot", main)
}
