package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostArm64LitPoolPerFunction pins the arm64 IR selector's literal-pool
// flush: every `ldr Xd, =N` must reach its pool through a 19-bit PC-relative
// offset (±1 MiB), so asm_arm64_ir emits `.ltorg` after each function's final
// `ret`. Without it the assembler holds every pending literal to the END of
// .text and the reach becomes the whole PROGRAM: the self-host checker driver
// crossed 1 MiB at 264,380 instructions and GNU `as` rejected its first ~700
// `ldr` lines with "pc-relative load offset out of range" (the in-process
// assemblers do not range-check imm19 at all — they would have loaded from the
// wrong address silently).
//
// The reach itself is unobservable on a small program, so this asserts the
// structural invariant that bounds it: no literal load is still pending when
// the next function starts. Scanning stops at `_start`, since the hand-written
// runtime helpers after it are one block that rides the end-of-.text flush.
func TestSelfHostArm64LitPoolPerFunction(t *testing.T) {
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	const src = `function twice(n: i32): i32 { return n * 2; }
function big(): i32 { return 1000000 + 7; }
function main(): i32 { return twice(big() - 999999); }
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
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "_start:":
			// The runtime block below is not per-function flushed.
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
			flushes++
			pending = 0
		case strings.HasPrefix(line, "ldr ") && strings.Contains(line, ", ="):
			pending++
			literals++
		}
	}
	t.Fatal("emitted asm has no _start")
}
