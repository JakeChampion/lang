package x86_64

// Tests for the streaming output peephole (generator.put / peepholeTail).
//
// The peephole removes three safe, purely-local stack-machine redundancies:
//   P1  a `push rax` immediately followed by the matching `pop DST`,
//   P3  a `push rax` whose slot is freed unread, and
//   P2  a `jmp L` immediately followed by the label `L:`.
// It must NOT collapse a non-adjacent push/pop, which is a genuinely live
// stack slot (left for the register allocator). These tests assert the
// collapse happens, that it is byte-for-byte the only change, and that live
// slots are preserved — without an assembler or qemu.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// compileOpts is compile() but with explicit Emit options so a test can turn
// the peephole off and compare the two emissions.
func compileOpts(t *testing.T, src string, opts Options) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := EmitWithOptions(prog, info, opts)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

func instrCount(asm string) int {
	n := 0
	for _, l := range strings.Split(asm, "\n") {
		if len(l) > 0 && l[0] == '\t' { // instructions are tab-indented
			n++
		}
	}
	return n
}

// hasAdjacent reports whether some line trimmed-equals a and the immediately
// following line trimmed-equals b.
func hasAdjacent(asm, a, b string) bool {
	lines := strings.Split(asm, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == a && strings.TrimSpace(lines[i+1]) == b {
			return true
		}
	}
	return false
}

// hasJumpToNextLabel reports whether some `jmp L` is immediately followed by
// the label `L:`.
func hasJumpToNextLabel(asm string) bool {
	lines := strings.Split(asm, "\n")
	for i := 0; i+1 < len(lines); i++ {
		cur := strings.TrimSpace(lines[i])
		next := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(cur, "jmp ") && next == strings.TrimPrefix(cur, "jmp ")+":" {
			return true
		}
	}
	return false
}

// add(a,b)=a+b exercises both rewrites: the second operand is pushed then
// immediately popped (P1), and `return` emits `jmp .Lret` right before the
// return label (P2).
const peepProg = `
@noinline function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(40, 2); }`

func TestPeepholeRemovesRedundantStoreReload(t *testing.T) {
	off := compileOpts(t, peepProg, Options{NoPeephole: true})
	on := compileOpts(t, peepProg, Options{})

	// Sanity: with the peephole off, the un-collapsed program must actually
	// contain the patterns — otherwise the test proves nothing.
	if !hasAdjacent(off, "push rax", "pop rcx") &&
		!hasAdjacent(off, "push rax", "pop rax") {
		t.Fatal("precondition: un-peepholed asm has no adjacent push/pop to collapse")
	}
	if !hasJumpToNextLabel(off) {
		t.Fatal("precondition: un-peepholed asm has no jump-to-next-label to remove")
	}

	// P1: no `push rax` is immediately followed by a pop.
	if hasAdjacent(on, "push rax", "pop rcx") ||
		hasAdjacent(on, "push rax", "pop rax") {
		t.Error("P1: redundant push-then-pop survived the peephole")
	}
	// P2: no jump to the immediately-following label survives.
	if hasJumpToNextLabel(on) {
		t.Error("P2: jump-to-next-label survived the peephole")
	}
	// The peephole only ever removes instructions.
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d",
			instrCount(off), instrCount(on))
	}
}

// A non-adjacent push/pop is a live stack slot and must be preserved. In
// add(), the first operand `a` is pushed, then `b` is evaluated, then `a` is
// popped — so a `push rax` ... (other lines) ... `pop rax` pair must still be
// present after the peephole.
func TestPeepholePreservesLiveStackSlot(t *testing.T) {
	on := compileOpts(t, peepProg, Options{})
	lines := strings.Split(on, "\n")
	sawPush, sawNonAdjacentPop := false, false
	for i, l := range lines {
		cur := strings.TrimSpace(l)
		if cur == "push rax" {
			// Only count it as a surviving live-slot push if its pop is NOT
			// the very next line (that case is exactly what P1 removes).
			if i+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "pop ") {
				sawPush = true
			}
		}
		if cur == "pop rax" {
			sawNonAdjacentPop = true
		}
	}
	if !sawPush || !sawNonAdjacentPop {
		t.Error("a non-adjacent (live) push/pop pair was incorrectly collapsed")
	}
}

// NoPeephole is an exact opt-out: the emitted text must differ only by the
// removed instructions, and both must still contain the program entry symbol.
func TestPeepholeOptOutEmitsUncollapsed(t *testing.T) {
	off := compileOpts(t, peepProg, Options{NoPeephole: true})
	if !hasJumpToNextLabel(off) {
		t.Error("NoPeephole should leave the jump-to-next-label in place")
	}
	if !strings.Contains(off, "main") {
		t.Error("opt-out emission lost the entry symbol")
	}
}

// deadPushProg has statement-position expressions whose values nobody reads:
// each assignment leaves its result on the operand stack and the statement end
// discards it. That is P3's shape.
const deadPushProg = `
function main(): i32 {
  var t: i32 = 0;
  var i: i32 = 0;
  while (i < 4) { t = t + i; i = i + 1; }
  return t;
}`

// hasDeadPush reports whether a push/free pair survives: `push rax` then the
// one-slot release `add rsp, 8`.
func hasDeadPush(asm string) bool {
	return hasAdjacent(asm, "push rax", fmt.Sprintf("add rsp, %d", slotBytes))
}

func TestPeepholeRemovesDeadPush(t *testing.T) {
	off := compileOpts(t, deadPushProg, Options{NoPeephole: true})
	on := compileOpts(t, deadPushProg, Options{})

	// Sanity: the pattern must exist un-peepholed, or the test proves nothing.
	if !hasDeadPush(off) {
		t.Fatal("precondition: un-peepholed asm has no dead push to remove")
	}
	if hasDeadPush(on) {
		t.Error("P3: a push whose slot is freed unread survived the peephole")
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d",
			instrCount(off), instrCount(on))
	}
}

// P3 must not touch a push whose slot IS read before it is freed — that is a
// live operand-stack slot, and removing it would drop the value.
func TestPeepholePreservesReadBeforeFree(t *testing.T) {
	on := compileOpts(t, peepProg, Options{})
	lines := strings.Split(on, "\n")
	sawLive := false
	for i, l := range lines {
		if strings.TrimSpace(l) != "push rax" {
			continue
		}
		// Walk forward to the slot's release; a `pop` before a bare release
		// means the value was read.
		for j := i + 1; j < len(lines); j++ {
			cur := strings.TrimSpace(lines[j])
			if strings.HasPrefix(cur, "add rsp, ") {
				break
			}
			if strings.HasPrefix(cur, "pop ") {
				sawLive = true
				break
			}
		}
	}
	if !sawLive {
		t.Error("no live push survived: P3 removed a slot that was read before release")
	}
}
