package x86_64ssa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The arena has to stay inside the address range the backend's arithmetic can
// carry: an i32-width add of a base and an offset is sign-extended back through
// maskFix (`movsxd`), so an object at or above 0x80000000 is reached through a
// truncated, negative pointer. That is also what makes exhaustion reachable at
// all — past the line a bump site faults on the rc header it writes before the
// guard runs, so the program dies by signal instead of reporting (#7329).
func TestHeapReservationFitsTheAddressRange(t *testing.T) {
	end := int64(heapHint) + heapBytes
	if end > 0x80000000 {
		t.Errorf("arena ends at %#x, past the %#x ceiling this backend's i32-width "+
			"address arithmetic can address", end, 0x80000000)
	}
	if heapBytes <= 1<<16 {
		t.Errorf("heapBytes = %d, no larger than the fixed .bss buffer it replaced", heapBytes)
	}
	if heapSlackBytes%4096 != 0 || heapSlackBytes == 0 {
		t.Errorf("heapSlackBytes = %d, want a non-zero whole number of pages",
			heapSlackBytes)
	}
	if heapSlackBytes >= heapBytes {
		t.Errorf("heapSlackBytes = %d leaves nothing of the %d-byte reservation to hand out",
			heapSlackBytes, heapBytes)
	}
}

// Every bump site publishes its new cursor and then calls the guard, which is
// what turns an exhausted arena into a diagnostic instead of a store into
// unmapped memory. A site that skips the call allocates past the end silently,
// and nothing else in the suite notices until a program large enough to reach
// the end runs — so check the emitted text of every runtime helper directly.
func TestEveryHeapBumpPublishesThroughTheGuard(t *testing.T) {
	names := make([]string, 0, len(runtimeHelperEmitters))
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var b strings.Builder
		w := func(format string, args ...any) {
			fmt.Fprintf(&b, format, args...)
			b.WriteByte('\n')
		}
		runtimeHelperEmitters[name](w)
		for _, line := range unguardedBumps(b.String()) {
			t.Errorf("%s: cursor published without the guard: %s", name, line)
		}
	}
}

// The inline bump sites — MemAlloc and the closure/env allocations — are not
// runtime helpers, so they are rendered through their own emitters and scanned
// the same way. Reaching them needs no SSA input: the rendered text is what the
// scan reads.
func TestInlineHeapBumpsPublishThroughTheGuard(t *testing.T) {
	memAlloc, err := asmInst(Inst{Op: MemAlloc, Dst: 0, Src: 1}, 3)
	if err != nil {
		t.Fatalf("asmInst(MemAlloc): %v", err)
	}
	closure, err := closureLines(Inst{Op: MakeEnv, Dst: 0}, 0, map[string]int{})
	if err != nil {
		t.Fatalf("closureLines(MakeEnv): %v", err)
	}
	for name, asm := range map[string]string{
		"MemAlloc": "\t" + memAlloc,
		"MakeEnv":  "\t" + strings.Join(closure, "\n\t"),
	} {
		for _, line := range unguardedBumps(asm) {
			t.Errorf("%s: cursor published without the guard: %s", name, line)
		}
	}
}

// `mov [rip + __ssa_heap_ptr], rN` — the cursor publish. RIP-relative, so unlike
// the arm64 renderer there is no address register to track: the store names the
// symbol directly and every publish is one line.
var cursorStoreRe = regexp.MustCompile(`^mov\s+\[rip \+ ` + heapPtrSym + `\],\s*\w+$`)

// unguardedBumps returns the cursor stores in asm that are not followed by a
// call to the guard within the next few instructions.
func unguardedBumps(asm string) []string {
	var instrs []string
	for _, line := range strings.Split(asm, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(line, "\t") && t != "" {
			instrs = append(instrs, t)
		}
	}
	var bad []string
	for i, in := range instrs {
		if !cursorStoreRe.MatchString(in) {
			continue
		}
		guarded := false
		for j := i + 1; j < len(instrs) && j <= i+3; j++ {
			if instrs[j] == heapGuardCall {
				guarded = true
			}
		}
		if !guarded {
			bad = append(bad, in)
		}
	}
	return bad
}
