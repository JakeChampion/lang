package e2ecompiler

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64NativeViaGoBackend(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("native x86-64 run required")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src string }{
		{"exit42", `function main(): i32 { return 42; }`},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
		{"print", `function main(): i32 { print("hi"); return 0; }`},
		{"concat", `function main(): i32 { let s: string = "a"; s = s + "b"; print(s); return 0; }`},
		{"i64math", `function main(): i32 { let a: i64 = 1000000; let b: i64 = 7; let c: i64 = a*b + a/b; return (c % 256) as i32; }`},
		{"bitwise", `function main(): i32 { let a: i32 = 240; let b: i32 = 15; let c: i32 = (a & b) | (a << 2); let d: i32 = c >> 1; return d % 256; }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, c.name+".bin")
			if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit (arm64-darwin binary) failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("emitted output is not a parseable Mach-O: %v (len=%d)", err, len(raw))
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
		})
	}
}
