package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostArm64LitPoolPerFunction pins the arm64 IR selector's literal-pool
// flush structurally: `asm_arm64_ir.emit_function_via_ir` emits `.ltorg` after
// each function's final `ret`, so a `ldr Xd, =N` reaches its pool within one
// function's text rather than across the whole program. Without the flush the
// assembler holds every pending literal to the END of .text and the reach — a
// signed 19-bit word offset, ±1 MiB — has to span the module: the self-host
// checker driver crossed that at ~264k instructions and GNU `as` rejected its
// first ~700 `ldr` lines with "pc-relative load offset out of range".
//
// TestSelfHostArm64LitPoolRange covers the same fix from the other side, by
// assembling, linking and RUNNING a fixture deliberately past the reach. That
// catches a wrapped imm19 the in-process assemblers might mask; this catches a
// missing flush on a three-function program with no toolchain, so the two are
// complementary and this one is the cheaper first line of defence.
//
// Two properties, both of which the fix has to hold:
//
//   - no literal load is still pending when the next function starts (the
//     invariant that BOUNDS the reach — the reach itself is unobservable on a
//     program this small); and
//   - every pool sits behind an unconditional `ret`, i.e. off the execution
//     path. A flush placed mid-function would satisfy the first property while
//     executing its own pool bytes as instructions.
//
// Scanning stops at `_start`: the hand-written runtime helpers after it are one
// block that legitimately rides the end-of-.text flush.
func TestSelfHostArm64LitPoolPerFunction(t *testing.T) {
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	// Each of big/masked/main holds a literal the pool must carry, so a missing
	// flush shows up as a load pending across a function boundary. `0xff` is
	// there to keep the fixture honest if the movz fast path widens again: it
	// admits only the canonical decimal of a value in [0, 65535], so a hex
	// literal reaches the pool whatever the threshold does.
	const src = `function twice(n: i32): i32 { return n * 2; }
function big(): i32 { return 1000000 + 7; }
function masked(n: i32): i32 { return n & 0xff; }
function main(): i32 { return twice(big() - 999999) + masked(0x7f); }
`
	asm, _ := compileFilesModload(t, x86runner, driverBin,
		map[string]string{"main.fern": src}, "-target", "arm64")
	if asm == "" {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}

	pending := 0     // `ldr Xd, =N` seen since the last `.ltorg`
	flushes := 0     // `.ltorg` directives seen
	literals := 0    // `ldr Xd, =N` seen in total
	curFn := "<top>" // function the pending literals belong to
	prevInsn := ""   // last instruction before the line being scanned
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
			// `b <label>` is unconditional; `b.<cond>` is not, and does not match.
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
			// directive, label or blank — not an instruction
		default:
			prevInsn = line
		}
	}
	t.Fatal("emitted asm has no _start")
}
