package x86_64ssa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The arena is 16 GiB — the native backend's size — based high enough that
// every address it hands out has bits above 31 set, so any arithmetic that
// narrows a pointer to 32 bits is wrong for the very first allocation instead
// of only past 2 GiB (#7329).
func TestHeapReservationFitsTheAddressRange(t *testing.T) {
	if want := int64(16) << 30; heapBytes != want {
		t.Errorf("arena is %d bytes, want %d (the native backend's 16 GiB)", heapBytes, want)
	}
	if heapHint <= 0xffffffff {
		t.Errorf("arena base %#x is inside the low 4 GiB, so a truncated pointer "+
			"still addresses live memory and the suite stops detecting one", heapHint)
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
