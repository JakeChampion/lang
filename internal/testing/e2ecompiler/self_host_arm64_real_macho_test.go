package e2ecompiler

import (
	"bytes"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"strings"
	"testing"
)

func TestSelfHostArm64DarwinMachORealAsm(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"return42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { let x = 6; let y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 42},
		{"concat", `function main(): i32 { let s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = Box { v: 4 }; return x.scale(3); }`, 12},
		{"array_sum", `function main(): i32 { let a = [1, 2, 3, 4, 5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		{"closure", `function main(): i32 { let k = 40; let f = (x: i32): i32 => { return x + k; }; return f(2); }`, 42},
		{"higher_order", `function apply(g: (i32) => i32, n: i32): i32 { return g(n); } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { return apply(dbl, 21); }`, 42},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := mustWrite(t, t.TempDir(), c.name+".fern", c.src)
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.Stage0Compiler(t), e2eharness.TargetArm64Darwin, srcPath)

			bin := selfHostMachOBytes(t, c.name+"_real", arm64NativeSrc(t)+"\n"+asmToMachoDriver(asm))
			if bytes.HasPrefix(bin, []byte("UNKNOWN:")) {
				t.Fatalf("arm64_gas dropped unhandled mnemonic(s): %s\n--- asm ---\n%s", bin, asm)
			}
			assertMachOBytesRun(t, c.name, bin, c.wantExit, false)
		})
	}
}

func asmToMachoDriver(asm string) string {
	var b strings.Builder
	b.WriteString("\nfunction main(): i32 {\n")
	b.WriteString("    let asm: string = \"\";\n")
	for _, line := range strings.Split(asm, "\n") {
		b.WriteString("    asm = asm + \"" + fernEscapeAsmLine(line) + "\\n\";\n")
	}
	b.WriteString("    let p: Arm64GasProg = arm64_gas_program(asm);\n")
	b.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\"); let ui: i32 = 0; while (ui < p.unknown.len()) { if (ui > 0) { write(\",\"); } write(p.unknown[ui]); ui = ui + 1; } return 0; }\n")
	b.WriteString("    let pa: Arm64Asm = p.asm;\n")
	b.WriteString("    let ehlen: i32 = arm64_eh_frame_darwin_len(p);\n")
	b.WriteString("    let tv: i64 = macho_text_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
	b.WriteString("    let ev: i64 = macho_eh_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
	b.WriteString("    let dv: i64 = macho_data_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
	b.WriteString("    let eh: i32[] = arm64_eh_frame_darwin(p, tv, ev);\n")
	b.WriteString("    p = arm64_gas_link(p, tv, dv);\n")
	b.WriteString("    let pa2: Arm64Asm = p.asm;\n")
	b.WriteString("    let bin: i32[] = macho_executable(pa2.text, eh, p.data, \"fern\", macho_entry_off(pa2), p.bss_size, arm64_gas_rebase_offs(p));\n")
	b.WriteString("    write(string_from_bytes_unchecked(to_u8(bin)));\n")
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

func fernEscapeAsmLine(line string) string {
	line = strings.ReplaceAll(line, "\\", "\\\\")
	line = strings.ReplaceAll(line, "\"", "\\\"")
	line = strings.ReplaceAll(line, "\t", "\\t")
	return line
}
