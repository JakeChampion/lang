package arm64ssa

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

// Every bump site publishes its new cursor and then calls the guard, which is
// what turns an exhausted arena into a diagnostic instead of a store into
// unmapped memory. A site that skips the call allocates past the end silently,
// and nothing else in the suite notices until a program large enough to reach
// the end runs — so check the emitted text of every runtime helper directly.
//
// A store carrying heapRewindComment is a cursor LOWERED back over space
// nothing owns; it moves the arena end no closer, so it is exempt — and being
// marked is what keeps the exemption from covering a bump that lost its guard.
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
		body := b.String()
		for _, line := range unguardedBumps(body) {
			t.Errorf("%s: cursor published without the guard: %s", name, line)
		}
		// A helper whose body names the arena, the guard or the
		// alloc-preserve trampoline needs a heapUsingHelpers entry, or
		// `heap` stays false for a module whose only call is to it and
		// _start emits neither the reservation nor the guard. The helper
		// then references symbols the module never defines, and the link
		// gate refuses it — while the same program builds under the flat
		// emitter. That list was hand-maintained and drifted: environ,
		// getgroups and the free read_line all reached the arena without
		// being on it.
		for _, sym := range []string{heapPtrSym, heapGuardSym, allocPresSym} {
			if strings.Contains(body, sym) && !heapUsingHelpers[name] {
				t.Errorf("%s references %s but is not in heapUsingHelpers, so a module whose only call is to it emits no arena", name, sym)
				break
			}
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
		if strings.HasSuffix(in, heapRewindComment) {
			store := plainStoreRe.FindStringSubmatch(strings.TrimSuffix(in, heapRewindComment))
			if store == nil || !cursorReg[store[1]] {
				bad = append(bad, in+" (marked a rewind but does not store through &"+heapPtrSym+")")
			}
			continue
		}
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
