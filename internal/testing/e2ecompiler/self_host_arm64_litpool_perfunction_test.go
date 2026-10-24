package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostArm64LitPoolPerFunction(t *testing.T) {
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	const src = `@noinline
function twice(n: i32): i32 { return n * 2; }
@noinline
function big(n: i32): i32 { return n + 1000007; }
@noinline
function masked(n: i32): i32 { return n & 0xfffff0; }
function main(): i32 { let k: i32 = args().len(); return twice(big(k) - 999999) + masked(k + 0x7f0000); }
`
	asm, _ := compileFilesModload(t, x86runner, driverBin,
		map[string]string{"main.fern": src}, "-target", "arm64-linux")
	if asm == "" {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}

	pending := 0
	flushes := 0
	literals := 0
	curFn := "<top>"
	prevInsn := ""
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "_start:":
			if pending > 0 {
				t.Errorf("%d literal load(s) in %s still pending at _start", pending, curFn)
			}
			if literals == 0 || flushes == 0 {
				t.Fatalf("nothing to check: %d literal loads, %d .ltorg flushes", literals, flushes)
			}
			return
		case strings.HasPrefix(line, "__fn_") && strings.HasSuffix(line, ":"):
			if pending > 0 {
				t.Errorf("%d literal load(s) in %s still pending at %s — asm_arm64_ir "+
					"must emit .ltorg at every function boundary", pending, curFn, line)
			}
			pending = 0
			curFn = strings.TrimSuffix(line, ":")
		case line == ".ltorg":
			if prevInsn != "ret" && !strings.HasPrefix(prevInsn, "b ") {
				t.Errorf("%s: .ltorg follows %q, not an unconditional branch — the pool "+
					"has to sit off the execution path or its bytes are executed", curFn, prevInsn)
			}
			flushes++
			pending = 0
		case strings.HasPrefix(line, "ldr") && strings.Contains(line, ", ="):
			pending++
			literals++
			prevInsn = line
		case line == "" || strings.HasPrefix(line, ".") || strings.HasSuffix(line, ":"):
		default:
			prevInsn = line
		}
	}
	t.Fatal("emitted asm has no _start")
}
