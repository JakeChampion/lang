package arm64ssa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The arena is 16 GiB — the native backend's size — based high enough that
// every address it hands out has bits above 31 set, so any arithmetic that
// narrows a pointer to 32 bits is wrong for the very first allocation instead
// of only past 2 GiB (#7329). It also has to load as movz + lsl, since _start
// materialises its base and size without a literal pool.
func TestHeapReservationFitsTheAddressRange(t *testing.T) {
	if got := int64(heapUnits) << heapShift; got != heapBytes {
		t.Fatalf("heapUnits<<heapShift = %d, want heapBytes = %d", got, heapBytes)
	}
	if heapUnits > 0xffff {
		t.Errorf("heapUnits = %d does not fit a movz immediate", heapUnits)
	}
	if want := int64(16) << 30; heapBytes != want {
		t.Errorf("arena is %d bytes, want %d (the native backend's 16 GiB)", heapBytes, want)
	}
	if base := int64(1) << heapBaseShift; base <= 0xffffffff {
		t.Errorf("arena base %#x is inside the low 4 GiB, so a truncated pointer "+
			"still addresses live memory and the suite stops detecting one", base)
	}
	if heapSlackBytes%4096 != 0 || heapSlackBytes == 0 {
		t.Errorf("heapSlackBytes = %d, want a non-zero whole number of pages",
			heapSlackBytes)
	}
}

// ssa.ResolveWidths decides whether a call result keeps the i32 sign-extend
// mask by looking the callee up by name, so a helper it has never heard of gets
// the wrong answer silently — a truncated pointer if the helper returns one, a
// zero-extended negative i32 if it does not. Neither shows up under a low
// arena. Every helper this backend emits therefore has to be classified.
func TestEveryRuntimeHelperResultIsClassified(t *testing.T) {
	names := make([]string, 0, len(runtimeHelperEmitters))
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ssa.RuntimeHelperResultClassified(name) {
			t.Errorf("%s: unclassified — add it to runtimeHelperWideResult (pointer / "+
				"f64 / i64 result) or narrowRuntimeHelpers (void / i32) in internal/ssa/width.go",
				name)
		}
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

var (
	// `add xN, xN, #:lo12:__ssa_heap_ptr` — xN now holds &cursor.
	cursorAddrRe = regexp.MustCompile(`^add\s+(x\d+),\s*x\d+,\s*#:lo12:` + heapPtrSym + `$`)
	// `str xN, [xM]` — a store through a register, no offset.
	plainStoreRe = regexp.MustCompile(`^str\s+x\d+,\s*\[(x\d+)\]$`)
	// The destination register of an instruction that writes one.
	writesRe = regexp.MustCompile(`^(?:mov|add|sub|and|orr|eor|lsl|lsr|asr|ldr|ldur|adrp|mul|neg|csel|sxtw|uxtw)\s+(x\d+)`)
)

// unguardedBumps returns the cursor stores in asm that are not followed by a
// call to the guard within the next few instructions.
func unguardedBumps(asm string) []string {
	cursorReg := map[string]bool{}
	var instrs []string
	for _, line := range strings.Split(asm, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(line, "\t") && t != "" {
			instrs = append(instrs, t)
		}
	}
	var bad []string
	for i, in := range instrs {
		if m := cursorAddrRe.FindStringSubmatch(in); m != nil {
			cursorReg[m[1]] = true
			continue
		}
		if m := plainStoreRe.FindStringSubmatch(in); m != nil && cursorReg[m[1]] {
			guarded := false
			for j := i + 1; j < len(instrs) && j <= i+3; j++ {
				if instrs[j] == "bl "+heapGuardSym {
					guarded = true
				}
			}
			if !guarded {
				bad = append(bad, in)
			}
			continue
		}
		if m := writesRe.FindStringSubmatch(in); m != nil {
			delete(cursorReg, m[1])
		}
	}
	return bad
}
